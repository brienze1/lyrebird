package dto

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/brienze1/lyrebird/internal/domain"
	"github.com/brienze1/lyrebird/internal/usecase"
)

// FramingDTO is the wire shape of domain.Framing. Like ActionDTO, the variant
// is selected by WHICH FIELD IS PRESENT rather than by a separate "kind" key,
// so `{"delimiter": "\r\n"}` means what it obviously means and there is no way
// to declare a kind that disagrees with the fields beside it.
//
// Delimiter is a string rather than []byte because encoding/json renders a
// []byte as base64: an author writing a seed file would have to base64 a
// carriage return. A delimiter that is not valid UTF-8 is expressible through
// DelimiterHex instead.
type FramingDTO struct {
	Delimiter    string     `json:"delimiter,omitempty" yaml:"delimiter,omitempty"`
	DelimiterHex string     `json:"delimiter_hex,omitempty" yaml:"delimiter_hex,omitempty"`
	Length       int        `json:"length,omitempty" yaml:"length,omitempty"`
	Prefix       *PrefixDTO `json:"prefix,omitempty" yaml:"prefix,omitempty"`
}

// PrefixDTO is the wire shape of a length-prefix framing.
type PrefixDTO struct {
	Width  int    `json:"width" yaml:"width"`
	Endian string `json:"endian,omitempty" yaml:"endian,omitempty"`
}

// ProjectionFieldDTO is the wire shape of domain.ProjectionField.
type ProjectionFieldDTO struct {
	Name   string `json:"name" yaml:"name"`
	Offset int    `json:"offset" yaml:"offset"`
	Length int    `json:"length" yaml:"length"`
	As     string `json:"as,omitempty" yaml:"as,omitempty"`
}

// ProjectionDTO is the wire shape of domain.Projection. It appears in two
// places — on an endpoint as its default, and on a mock as that rule's
// override — which is one shape with two homes, per 003's FR-006.
type ProjectionDTO struct {
	Split string               `json:"split,omitempty" yaml:"split,omitempty"`
	At    []ProjectionFieldDTO `json:"at,omitempty" yaml:"at,omitempty"`
}

// FramePartDTO is the wire shape of domain.FramePart — one element of the
// declarative grammar that builds an outbound frame.
type FramePartDTO struct {
	Text     *string       `json:"text,omitempty" yaml:"text,omitempty"`
	Hex      *string       `json:"hex,omitempty" yaml:"hex,omitempty"`
	Int      *int64        `json:"int,omitempty" yaml:"int,omitempty"`
	CopyFrom *string       `json:"copyFrom,omitempty" yaml:"copyFrom,omitempty"`
	Width    int           `json:"width,omitempty" yaml:"width,omitempty"`
	Pad      string        `json:"pad,omitempty" yaml:"pad,omitempty"`
	Repeat   *FramePartDTO `json:"repeat,omitempty" yaml:"repeat,omitempty"`
	Times    int           `json:"times,omitempty" yaml:"times,omitempty"`
}

// CadenceDTO is the wire shape of domain.Cadence. Interval is a duration
// STRING ("100ms"), not a number: a bare number would be nanoseconds through
// both encoding/json and yaml.v3, so `interval: 100` would silently mean
// 100ns — a mistake nobody catches by reading the file.
type CadenceDTO struct {
	Interval     string           `json:"interval" yaml:"interval"`
	OnExhaustion string           `json:"on_exhaustion,omitempty" yaml:"on_exhaustion,omitempty"`
	Frames       [][]FramePartDTO `json:"frames" yaml:"frames"`
}

// EndpointDTO is the wire shape of domain.Endpoint. Occupied is read-only
// runtime state, present on a listing and ignored on the way in.
type EndpointDTO struct {
	Name       string         `json:"name" yaml:"name"`
	Framing    FramingDTO     `json:"framing" yaml:"framing"`
	Projection *ProjectionDTO `json:"projection,omitempty" yaml:"projection,omitempty"`
	Cadence    *CadenceDTO    `json:"cadence,omitempty" yaml:"cadence,omitempty"`
	Lifetime   string         `json:"lifetime,omitempty" yaml:"-"`
	Occupied   bool           `json:"occupied,omitempty" yaml:"-"`
}

// EndpointToDTO converts a domain.Endpoint to its wire equivalent.
func EndpointToDTO(e domain.Endpoint, occupied bool) EndpointDTO {
	return EndpointDTO{
		Name:       e.Name,
		Framing:    FramingToDTO(e.Framing),
		Projection: ProjectionToDTO(e.Projection),
		Cadence:    CadenceToDTO(e.Cadence),
		Lifetime:   string(e.Lifetime),
		Occupied:   occupied,
	}
}

// EndpointInputFromDTO converts a wire endpoint into the use case's input.
func EndpointInputFromDTO(partition string, d EndpointDTO) (usecase.EndpointInput, error) {
	framing, err := FramingFromDTO(d.Framing)
	if err != nil {
		return usecase.EndpointInput{}, err
	}
	cadence, err := CadenceFromDTO(d.Cadence)
	if err != nil {
		return usecase.EndpointInput{}, err
	}
	return usecase.EndpointInput{
		Partition:  partition,
		Name:       d.Name,
		Framing:    framing,
		Projection: ProjectionFromDTO(d.Projection),
		Cadence:    cadence,
	}, nil
}

// FramingToDTO renders a framing, preferring the readable `delimiter` form
// and falling back to hex only when the bytes are not printable text — so a
// round-tripped export stays legible in the common case.
func FramingToDTO(f domain.Framing) FramingDTO {
	switch f.Kind {
	case domain.FramingLength:
		return FramingDTO{Length: f.Length}
	case domain.FramingPrefix:
		return FramingDTO{Prefix: &PrefixDTO{Width: f.PrefixWidth, Endian: string(f.PrefixEndian)}}
	default:
		if isPrintableDelimiter(f.Delimiter) {
			return FramingDTO{Delimiter: string(f.Delimiter)}
		}
		return FramingDTO{DelimiterHex: hex.EncodeToString(f.Delimiter)}
	}
}

// FramingFromDTO resolves the variant from which field the author set,
// rejecting a block that sets more than one: two variants at once has no
// single correct reading, and guessing would make the same bytes mean
// different things on different boots.
func FramingFromDTO(d FramingDTO) (domain.Framing, error) {
	set := 0
	if d.Delimiter != "" || d.DelimiterHex != "" {
		set++
	}
	if d.Length > 0 {
		set++
	}
	if d.Prefix != nil {
		set++
	}
	switch {
	case set == 0:
		return domain.Framing{}, fmt.Errorf("framing must set exactly one of delimiter/delimiter_hex/length/prefix")
	case set > 1:
		return domain.Framing{}, fmt.Errorf("framing sets more than one of delimiter/delimiter_hex/length/prefix")
	case d.Length > 0:
		return domain.Framing{Kind: domain.FramingLength, Length: d.Length}, nil
	case d.Prefix != nil:
		endian := domain.Endianness(d.Prefix.Endian)
		if endian == "" {
			endian = domain.EndianBig
		}
		return domain.Framing{Kind: domain.FramingPrefix, PrefixWidth: d.Prefix.Width, PrefixEndian: endian}, nil
	case d.DelimiterHex != "":
		raw, err := hex.DecodeString(strings.TrimSpace(d.DelimiterHex))
		if err != nil {
			return domain.Framing{}, fmt.Errorf("framing.delimiter_hex is not valid hex: %w", err)
		}
		return domain.Framing{Kind: domain.FramingDelimiter, Delimiter: raw}, nil
	default:
		return domain.Framing{Kind: domain.FramingDelimiter, Delimiter: []byte(d.Delimiter)}, nil
	}
}

// ProjectionToDTO is nil-safe: a nil projection stays nil so that "inherit
// the endpoint's default" and "override it with nothing" remain distinct.
func ProjectionToDTO(p *domain.Projection) *ProjectionDTO {
	if p == nil {
		return nil
	}
	out := &ProjectionDTO{Split: p.Split}
	for _, f := range p.At {
		out.At = append(out.At, ProjectionFieldDTO{
			Name: f.Name, Offset: f.Offset, Length: f.Length, As: string(f.As),
		})
	}
	return out
}

// ProjectionFromDTO is nil-safe, mirroring ProjectionToDTO.
func ProjectionFromDTO(d *ProjectionDTO) *domain.Projection {
	if d == nil {
		return nil
	}
	out := &domain.Projection{Split: d.Split}
	for _, f := range d.At {
		out.At = append(out.At, domain.ProjectionField{
			Name: f.Name, Offset: f.Offset, Length: f.Length, As: domain.ProjectionAs(f.As),
		})
	}
	return out
}

// CadenceToDTO is nil-safe.
func CadenceToDTO(c *domain.Cadence) *CadenceDTO {
	if c == nil {
		return nil
	}
	out := &CadenceDTO{Interval: c.Interval.String(), OnExhaustion: string(c.OnExhaust)}
	for _, frame := range c.Frames {
		parts := make([]FramePartDTO, 0, len(frame))
		for _, p := range frame {
			parts = append(parts, framePartToDTO(p))
		}
		out.Frames = append(out.Frames, parts)
	}
	return out
}

// CadenceFromDTO is nil-safe and parses the interval explicitly.
func CadenceFromDTO(d *CadenceDTO) (*domain.Cadence, error) {
	if d == nil {
		return nil, nil
	}
	interval, err := time.ParseDuration(d.Interval)
	if err != nil {
		return nil, fmt.Errorf("cadence.interval %q is not a duration (e.g. 100ms): %w", d.Interval, err)
	}
	out := &domain.Cadence{Interval: interval, OnExhaust: domain.OnExhaustion(d.OnExhaustion)}
	for _, frame := range d.Frames {
		parts := make([]domain.FramePart, 0, len(frame))
		for _, p := range frame {
			parts = append(parts, framePartFromDTO(p))
		}
		out.Frames = append(out.Frames, parts)
	}
	return out, nil
}

// CadenceActionToDTO is nil-safe. Frames are rendered as spec STRINGS (see
// CadenceActionDTO's doc comment) via the same json.Marshal CadenceToSpecs
// uses; the error that call could theoretically return is discarded rather
// than threading a "cannot happen" error return through ActionToDTO/
// MockToDTO (which have none today) — FramePartDTO's fields are all
// string/int64/nested-FramePartDTO, none of which json.Marshal ever fails
// on.
func CadenceActionToDTO(c *domain.CadenceAction) *CadenceActionDTO {
	if c == nil {
		return nil
	}
	out := &CadenceActionDTO{OnExhaustion: string(c.OnExhaust)}
	if c.Interval != nil {
		s := c.Interval.String()
		out.Interval = &s
	}
	for _, frame := range c.Frames {
		parts := make([]FramePartDTO, 0, len(frame))
		for _, p := range frame {
			parts = append(parts, framePartToDTO(p))
		}
		raw, _ := json.Marshal(parts)
		out.Frames = append(out.Frames, string(raw))
	}
	return out
}

// CadenceActionFromDTO is nil-safe and parses Interval explicitly, mirroring
// CadenceFromDTO — but Interval is OPTIONAL here (nil means "inherit the
// endpoint's declared interval"), unlike a cadence declaration's Interval,
// which is always required. Frames are parsed with FramePartsFromJSON, the
// same spec-string grammar CadenceFromSpecs (MCP's create_endpoint) and
// emit_frame already use.
func CadenceActionFromDTO(d *CadenceActionDTO) (*domain.CadenceAction, error) {
	if d == nil {
		return nil, nil
	}
	out := &domain.CadenceAction{OnExhaust: domain.OnExhaustion(d.OnExhaustion)}
	if d.Interval != nil {
		parsed, err := time.ParseDuration(*d.Interval)
		if err != nil {
			return nil, fmt.Errorf("action.cadence.interval %q is not a duration (e.g. 100ms): %w", *d.Interval, err)
		}
		out.Interval = &parsed
	}
	for _, spec := range d.Frames {
		parts, err := FramePartsFromJSON(spec)
		if err != nil {
			return nil, err
		}
		out.Frames = append(out.Frames, parts)
	}
	return out, nil
}

func framePartToDTO(p domain.FramePart) FramePartDTO {
	d := FramePartDTO{
		Text: p.Text, Hex: p.Hex, Int: p.Int, CopyFrom: p.CopyFrom,
		Width: p.Width, Pad: p.Pad, Times: p.Times,
	}
	if p.Repeat != nil {
		inner := framePartToDTO(*p.Repeat)
		d.Repeat = &inner
	}
	return d
}

func framePartFromDTO(d FramePartDTO) domain.FramePart {
	p := domain.FramePart{
		Text: d.Text, Hex: d.Hex, Int: d.Int, CopyFrom: d.CopyFrom,
		Width: d.Width, Pad: d.Pad, Times: d.Times,
	}
	if d.Repeat != nil {
		inner := framePartFromDTO(*d.Repeat)
		p.Repeat = &inner
	}
	return p
}

// isPrintableDelimiter reports whether a delimiter round-trips legibly as a
// JSON/YAML string. Control characters below 0x20 are allowed because the
// most common delimiters of all — CR and LF — are exactly that, and both
// encoders escape them faithfully.
func isPrintableDelimiter(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	for _, c := range b {
		if c >= 0x80 {
			return false
		}
	}
	return true
}

// FramePartsFromJSON decodes one frame spec — the same JSON part-list grammar
// a mock's respond body and emit_frame's frame argument use — into domain
// parts.
//
// It exists because FramePartDTO is self-referential through Repeat, and the
// MCP SDK's JSON-schema generator cannot express a recursive type: declaring
// a cadence's frames as structured input crashes tool registration outright.
// Carrying each frame as its spec STRING sidesteps that and, more usefully,
// means an agent writes one grammar everywhere a frame is declared.
func FramePartsFromJSON(raw string) ([]domain.FramePart, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}
	var parts []FramePartDTO
	if err := json.Unmarshal([]byte(trimmed), &parts); err != nil {
		return nil, fmt.Errorf("frame spec %q is not a JSON list of parts: %w", raw, err)
	}
	out := make([]domain.FramePart, 0, len(parts))
	for _, p := range parts {
		out = append(out, framePartFromDTO(p))
	}
	return out, nil
}

// CadenceFromSpecs builds a cadence whose frames arrive as spec strings —
// the shape the MCP tools use. Nil frames means no cadence at all, so an
// endpoint declared without one stays without one.
func CadenceFromSpecs(interval, onExhaustion string, frames []string) (*domain.Cadence, error) {
	if interval == "" && len(frames) == 0 {
		return nil, nil
	}
	parsed, err := time.ParseDuration(interval)
	if err != nil {
		return nil, fmt.Errorf("cadence.interval %q is not a duration (e.g. 100ms): %w", interval, err)
	}
	out := &domain.Cadence{Interval: parsed, OnExhaust: domain.OnExhaustion(onExhaustion)}
	for _, f := range frames {
		parts, err := FramePartsFromJSON(f)
		if err != nil {
			return nil, err
		}
		out.Frames = append(out.Frames, parts)
	}
	return out, nil
}

// CadenceToSpecs is CadenceFromSpecs' inverse, for rendering a cadence back
// over the MCP surface.
func CadenceToSpecs(c *domain.Cadence) (interval, onExhaustion string, frames []string, err error) {
	if c == nil {
		return "", "", nil, nil
	}
	for _, frame := range c.Frames {
		parts := make([]FramePartDTO, 0, len(frame))
		for _, p := range frame {
			parts = append(parts, framePartToDTO(p))
		}
		raw, err := json.Marshal(parts)
		if err != nil {
			return "", "", nil, err
		}
		frames = append(frames, string(raw))
	}
	return c.Interval.String(), string(c.OnExhaust), frames, nil
}
