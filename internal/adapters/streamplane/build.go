package streamplane

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/brienze1/lyrebird/internal/adapters/jsonpath"
	"github.com/brienze1/lyrebird/internal/domain"
)

// partSpec is the wire shape of one domain.FramePart (data-model.md §5).
// Exactly one of Text/Hex/Int/CopyFrom/Repeat is set.
type partSpec struct {
	Text     *string   `json:"text,omitempty"`
	Hex      *string   `json:"hex,omitempty"`
	Int      *int64    `json:"int,omitempty"`
	CopyFrom *string   `json:"copyFrom,omitempty"`
	Width    int       `json:"width,omitempty"`
	Pad      string    `json:"pad,omitempty"`
	Repeat   *partSpec `json:"repeat,omitempty"`
	Times    int       `json:"times,omitempty"`
}

// toDomain converts one decoded part into the domain vocabulary, which is the
// single representation everything downstream builds from. Decoding converts
// once, here; nothing converts back.
func (p partSpec) toDomain() domain.FramePart {
	out := domain.FramePart{
		Text: p.Text, Hex: p.Hex, Int: p.Int, CopyFrom: p.CopyFrom,
		Width: p.Width, Pad: p.Pad, Times: p.Times,
	}
	if p.Repeat != nil {
		inner := p.Repeat.toDomain()
		out.Repeat = &inner
	}
	return out
}

// wireFrameSpec is the JSON shape of a complete outbound frame declaration.
// A bare JSON array is accepted as shorthand for {"parts": [...]}, because the
// overwhelmingly common spec is a list of parts with no raw flag and making
// authors wrap it would be friction for nothing.
type wireFrameSpec struct {
	Parts []partSpec `json:"parts"`
	Raw   bool       `json:"raw,omitempty"`
}

// frameSpec is a decoded outbound frame declaration. Its parts are domain
// values, so a cadence — whose frames are already domain.FrameParts — feeds
// buildFrame with no conversion at all.
type frameSpec struct {
	Parts []domain.FramePart
	Raw   bool
}

// maxRepeatTimes bounds a single repeat part. Without it an agent-authored
// {"repeat":{"hex":"00"},"times":2000000000} would allocate gigabytes inside
// a request-handling goroutine — a foot-gun the equivalent gRPC grammar does
// not have, because protobuf field specs cannot express repetition counts.
const maxRepeatTimes = 1 << 16

// parseFrameSpec decodes a mock's respond body (or a cadence/injection
// payload) into a frame declaration. An empty body is a valid empty frame:
// on a byte stream "answer with nothing but the terminator" is a real thing
// a peripheral does, unlike HTTP where an empty body still carries a status.
func parseFrameSpec(raw []byte) (frameSpec, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return frameSpec{}, nil
	}
	if strings.HasPrefix(trimmed, "[") {
		var parts []partSpec
		if err := json.Unmarshal([]byte(trimmed), &parts); err != nil {
			return frameSpec{}, fmt.Errorf("streamplane: frame spec is not a valid part list: %w", err)
		}
		return frameSpec{Parts: toDomainParts(parts)}, nil
	}
	var wire wireFrameSpec
	if err := json.Unmarshal([]byte(trimmed), &wire); err != nil {
		return frameSpec{}, fmt.Errorf("streamplane: frame spec is not a valid object or part list: %w", err)
	}
	return frameSpec{Parts: toDomainParts(wire.Parts), Raw: wire.Raw}, nil
}

func toDomainParts(parts []partSpec) []domain.FramePart {
	out := make([]domain.FramePart, 0, len(parts))
	for _, p := range parts {
		out = append(out, p.toDomain())
	}
	return out
}

// buildFrame assembles the outbound bytes from a parsed spec. envelope is the
// triggering frame's projection, which copyFrom paths resolve against; it may
// be nil for an unprompted emission, where a copyFrom simply contributes
// nothing because there is no triggering frame to copy from.
//
// The endpoint's framing terminator is appended unless the spec declares
// itself raw — which is how a `malformed` fault produces a frame whose
// terminator is missing, leaving the consumer's own validation to reject it.
func buildFrame(fs frameSpec, envelope []byte, framing domain.Framing) ([]byte, error) {
	var out []byte
	for i, p := range fs.Parts {
		b, err := appendPart(p, envelope, 0)
		if err != nil {
			return nil, fmt.Errorf("streamplane: frame spec part %d: %w", i, err)
		}
		out = append(out, b...)
	}
	if !fs.Raw {
		out = append(out, terminator(framing)...)
	}
	return out, nil
}

// appendPart renders one part. depth guards against a spec that nests repeat
// inside repeat inside repeat: the grammar is declarative and agent-authored,
// so a pathological nesting must fail cleanly rather than exhaust the stack
// (FR-027).
func appendPart(p domain.FramePart, envelope []byte, depth int) ([]byte, error) {
	const maxDepth = 8
	if depth > maxDepth {
		return nil, fmt.Errorf("repeat nested more than %d deep", maxDepth)
	}

	switch {
	case p.Text != nil:
		return latin1Bytes(*p.Text), nil

	case p.Hex != nil:
		b, err := hex.DecodeString(strings.TrimSpace(*p.Hex))
		if err != nil {
			return nil, fmt.Errorf("hex is not valid: %w", err)
		}
		return b, nil

	case p.Int != nil:
		return []byte(padNumber(strconv.FormatInt(*p.Int, 10), p.Width, p.Pad)), nil

	case p.CopyFrom != nil:
		if len(envelope) == 0 {
			// An unprompted emission has no triggering frame. Contributing
			// nothing is the honest answer; erroring would make a cadence
			// that happens to contain a copyFrom fail forever at runtime.
			return nil, nil
		}
		res := jsonpath.GetBytes(envelope, *p.CopyFrom)
		if !res.Exists() {
			return nil, nil
		}
		return latin1Bytes(res.String()), nil

	case p.Repeat != nil:
		if p.Times <= 0 {
			return nil, nil
		}
		if p.Times > maxRepeatTimes {
			return nil, fmt.Errorf("times %d exceeds the maximum of %d", p.Times, maxRepeatTimes)
		}
		unit, err := appendPart(*p.Repeat, envelope, depth+1)
		if err != nil {
			return nil, err
		}
		out := make([]byte, 0, len(unit)*p.Times)
		for i := 0; i < p.Times; i++ {
			out = append(out, unit...)
		}
		return out, nil

	default:
		return nil, fmt.Errorf("part must set exactly one of text/hex/int/copyFrom/repeat")
	}
}

// padNumber left-pads s to width runes with pad (default "0"). Only the first
// rune of pad is used; a width at or below the current length leaves s alone,
// so padding can never truncate a number into a different number.
func padNumber(s string, width int, pad string) string {
	if width <= len(s) {
		return s
	}
	padRune := "0"
	if pad != "" {
		padRune = string([]rune(pad)[0])
	}
	return strings.Repeat(padRune, width-len(s)) + s
}
