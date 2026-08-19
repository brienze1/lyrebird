package streamplane

import (
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/brienze1/lyrebird/internal/domain"
)

// projectFrame turns a frame's raw bytes into the JSON envelope a rule
// matches against (data-model.md §4), so the EXISTING JSONPath body matchers
// apply to a byte stream with no new matching vocabulary and — crucially — no
// protocol decoder anywhere in Lyrebird (FR-006, FR-008).
//
// This is the byte-stream counterpart of grpcplane's projectForMatch, which
// does the same job for protobuf field numbers.
//
//	{
//	  "len":  9,
//	  "hex":  "412c31322c4f4b0d0a",
//	  "text": "A,12,OK\r\n",
//	  "fields": ["A", "12", "OK\r\n"],
//	  "at":   {"kind": "A", "count": 12}
//	}
//
// $.len, $.hex and $.text are always present; $.fields and $.at appear only
// when the projection declares a split or named fields. A declaration
// addressing bytes the frame does not have yields an ABSENT key rather than
// an error, so an `exists: false` condition can match on it (FR-027).
//
// p may be nil: a frame on an endpoint with no declared projection, matched
// by a rule that declares none either, still gets the three always-present
// keys and is fully matchable by regex over $.text or $.hex.
func projectFrame(frame []byte, p *domain.Projection) ([]byte, error) {
	obj := map[string]any{
		"len":  len(frame),
		"hex":  hex.EncodeToString(frame),
		"text": latin1(frame),
	}
	if p != nil {
		if p.Split != "" {
			obj["fields"] = splitFrame(frame, p.Split)
		}
		if at := projectAt(frame, p.At); len(at) > 0 {
			obj["at"] = at
		}
	}
	return json.Marshal(obj)
}

// splitFrame splits the frame's latin-1 text on sep. The separator is applied
// to the text rather than the bytes so that a multi-byte separator behaves
// identically either way, and so the resulting elements are directly
// comparable with a rule's `equals` string.
func splitFrame(frame []byte, sep string) []string {
	return strings.Split(latin1(frame), sep)
}

// projectAt renders each declared field, silently omitting any that addresses
// bytes outside the frame or that cannot be read as declared. Omission, not
// error, is deliberate: a short frame is a legitimate thing to match on with
// `exists: false`, and failing the whole projection would make one malformed
// frame unmatchable by every rule rather than just the ones that care.
func projectAt(frame []byte, fields []domain.ProjectionField) map[string]any {
	if len(fields) == 0 {
		return nil
	}
	out := make(map[string]any, len(fields))
	for _, f := range fields {
		if f.Name == "" || f.Offset < 0 || f.Length <= 0 {
			continue
		}
		end := f.Offset + f.Length
		// Guard the addition itself: a hostile or mistyped Offset+Length can
		// overflow int on 32-bit builds and wrap to a value inside the
		// frame, which would silently return the wrong bytes.
		if end < f.Offset || end > len(frame) {
			continue
		}
		run := frame[f.Offset:end]
		switch f.As {
		case domain.ProjectAsInt:
			n, err := strconv.ParseInt(strings.TrimSpace(latin1(run)), 10, 64)
			if err != nil {
				continue
			}
			out[f.Name] = n
		case domain.ProjectAsHex:
			out[f.Name] = hex.EncodeToString(run)
		default: // domain.ProjectAsText and the unset zero value
			out[f.Name] = latin1(run)
		}
	}
	return out
}

// latin1 decodes bytes one-to-one into runes, so every byte 0x00–0xFF
// round-trips and an arbitrary binary frame is still a valid Go string.
//
// UTF-8 decoding is deliberately NOT used: it replaces invalid sequences with
// U+FFFD, which would silently destroy binary content and make two different
// frames project to the same $.text — so a rule could match bytes that never
// arrived.
func latin1(b []byte) string {
	ascii := true
	for _, c := range b {
		if c >= 0x80 {
			ascii = false
			break
		}
	}
	if ascii {
		return string(b)
	}
	var sb strings.Builder
	sb.Grow(len(b) * 2)
	for _, c := range b {
		sb.WriteRune(rune(c))
	}
	return sb.String()
}

// latin1Bytes is latin1's inverse: it re-encodes a latin-1-decoded string
// back to the exact bytes it came from. Runes above 0xFF cannot have come
// from latin1 and are encoded as UTF-8, so an author writing literal
// non-latin-1 text in a frame spec still gets sensible bytes rather than
// silent truncation.
func latin1Bytes(s string) []byte {
	out := make([]byte, 0, len(s))
	for _, r := range s {
		if r <= 0xFF {
			out = append(out, byte(r))
			continue
		}
		out = append(out, []byte(string(r))...)
	}
	return out
}
