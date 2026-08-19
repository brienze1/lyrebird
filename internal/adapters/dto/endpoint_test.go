package dto

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/brienze1/lyrebird/internal/domain"
)

func TestEndpointDTORoundTripsEveryFramingVariant(t *testing.T) {
	tests := []struct {
		name    string
		framing domain.Framing
	}{
		{"printable delimiter", domain.Framing{Kind: domain.FramingDelimiter, Delimiter: []byte("\r\n")}},
		{"binary delimiter", domain.Framing{Kind: domain.FramingDelimiter, Delimiter: []byte{0x00, 0xFF}}},
		{"fixed length", domain.Framing{Kind: domain.FramingLength, Length: 16}},
		{"big-endian prefix", domain.Framing{Kind: domain.FramingPrefix, PrefixWidth: 2, PrefixEndian: domain.EndianBig}},
		{"little-endian prefix", domain.Framing{Kind: domain.FramingPrefix, PrefixWidth: 4, PrefixEndian: domain.EndianLittle}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := FramingFromDTO(FramingToDTO(tt.framing))
			if err != nil {
				t.Fatalf("FramingFromDTO(FramingToDTO(%+v)): %v", tt.framing, err)
			}
			if got.Kind != tt.framing.Kind || string(got.Delimiter) != string(tt.framing.Delimiter) ||
				got.Length != tt.framing.Length || got.PrefixWidth != tt.framing.PrefixWidth ||
				got.PrefixEndian != tt.framing.PrefixEndian {
				t.Errorf("round-trip = %+v, want %+v", got, tt.framing)
			}
		})
	}
}

// A binary delimiter must not round-trip through the readable `delimiter`
// field: JSON would mangle it. It goes out as hex and comes back byte-exact.
func TestFramingToDTOUsesHexForNonPrintableDelimiters(t *testing.T) {
	d := FramingToDTO(domain.Framing{Kind: domain.FramingDelimiter, Delimiter: []byte{0x00, 0x81}})
	if d.Delimiter != "" || d.DelimiterHex != "0081" {
		t.Errorf("FramingToDTO() = %+v, want delimiter_hex 0081 and no plain delimiter", d)
	}
}

func TestFramingFromDTORejectsAmbiguousAndEmptyDeclarations(t *testing.T) {
	tests := []struct {
		name string
		in   FramingDTO
	}{
		{"nothing set", FramingDTO{}},
		{"delimiter and length", FramingDTO{Delimiter: "\n", Length: 4}},
		{"length and prefix", FramingDTO{Length: 4, Prefix: &PrefixDTO{Width: 2}}},
		{"invalid hex", FramingDTO{DelimiterHex: "zz"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := FramingFromDTO(tt.in); err == nil {
				t.Errorf("FramingFromDTO(%+v) succeeded, want it rejected", tt.in)
			}
		})
	}
}

func TestProjectionRoundTripsAndStaysNilSafe(t *testing.T) {
	if ProjectionToDTO(nil) != nil {
		t.Error("ProjectionToDTO(nil) is non-nil, want nil so `inherit` stays distinct from `override with nothing`")
	}
	if ProjectionFromDTO(nil) != nil {
		t.Error("ProjectionFromDTO(nil) is non-nil, want nil")
	}

	p := &domain.Projection{Split: ",", At: []domain.ProjectionField{
		{Name: "kind", Offset: 0, Length: 1, As: domain.ProjectAsText},
		{Name: "count", Offset: 2, Length: 2, As: domain.ProjectAsInt},
	}}
	got := ProjectionFromDTO(ProjectionToDTO(p))
	if got.Split != p.Split || len(got.At) != len(p.At) || got.At[1].As != domain.ProjectAsInt {
		t.Errorf("round-trip = %+v, want %+v", got, p)
	}
}

func TestCadenceRoundTripsThroughBothRepresentations(t *testing.T) {
	text := "TICK"
	c := &domain.Cadence{
		Interval:  150 * time.Millisecond,
		OnExhaust: domain.OnExhaustionLoop,
		Frames:    [][]domain.FramePart{{{Text: &text}}},
	}

	viaDTO, err := CadenceFromDTO(CadenceToDTO(c))
	if err != nil {
		t.Fatalf("CadenceFromDTO(CadenceToDTO()): %v", err)
	}
	if viaDTO.Interval != c.Interval || viaDTO.OnExhaust != c.OnExhaust ||
		len(viaDTO.Frames) != 1 || *viaDTO.Frames[0][0].Text != text {
		t.Errorf("structured round-trip = %+v, want %+v", viaDTO, c)
	}

	// The MCP surface carries frames as spec strings, because FramePartDTO is
	// self-referential and the SDK's schema generator cannot express that.
	interval, onExhaustion, frames, err := CadenceToSpecs(c)
	if err != nil {
		t.Fatalf("CadenceToSpecs(): %v", err)
	}
	viaSpecs, err := CadenceFromSpecs(interval, onExhaustion, frames)
	if err != nil {
		t.Fatalf("CadenceFromSpecs(): %v", err)
	}
	if viaSpecs.Interval != c.Interval || len(viaSpecs.Frames) != 1 || *viaSpecs.Frames[0][0].Text != text {
		t.Errorf("spec-string round-trip = %+v, want %+v", viaSpecs, c)
	}
}

// A bare number would be nanoseconds through both encoders, so `interval:
// 100` would silently mean 100ns. It must be rejected, not guessed at.
func TestCadenceFromDTORejectsANonDurationInterval(t *testing.T) {
	if _, err := CadenceFromDTO(&CadenceDTO{Interval: "100"}); err == nil {
		t.Error("CadenceFromDTO() with a bare number succeeded, want a duration required")
	}
	if _, err := CadenceFromSpecs("banana", "", []string{`[{"text":"x"}]`}); err == nil {
		t.Error("CadenceFromSpecs() with a malformed interval succeeded, want it rejected")
	}
}

func TestCadenceHelpersAreNilSafe(t *testing.T) {
	if got, err := CadenceFromDTO(nil); err != nil || got != nil {
		t.Errorf("CadenceFromDTO(nil) = (%v, %v), want (nil, nil)", got, err)
	}
	if CadenceToDTO(nil) != nil {
		t.Error("CadenceToDTO(nil) is non-nil, want nil")
	}
	if _, _, frames, err := CadenceToSpecs(nil); err != nil || frames != nil {
		t.Errorf("CadenceToSpecs(nil) = (%v, %v), want (nil, nil)", frames, err)
	}
}

func TestFramePartsFromJSONRejectsMalformedSpecs(t *testing.T) {
	if got, err := FramePartsFromJSON("  "); err != nil || got != nil {
		t.Errorf("FramePartsFromJSON(blank) = (%v, %v), want (nil, nil)", got, err)
	}
	if _, err := FramePartsFromJSON(`{"text":"not a list"}`); err == nil {
		t.Error("FramePartsFromJSON() with an object succeeded, want a list required")
	}
}

func TestEndpointInputFromDTOCarriesEveryField(t *testing.T) {
	in, err := EndpointInputFromDTO("space-a", EndpointDTO{
		Name:       "widget",
		Framing:    FramingDTO{Delimiter: "\n"},
		Projection: &ProjectionDTO{Split: ","},
		Cadence:    &CadenceDTO{Interval: "1s", Frames: [][]FramePartDTO{{{Hex: strPtr("00")}}}},
	})
	if err != nil {
		t.Fatalf("EndpointInputFromDTO(): %v", err)
	}
	if in.Partition != "space-a" || in.Name != "widget" ||
		in.Framing.Kind != domain.FramingDelimiter || in.Projection == nil || in.Cadence == nil {
		t.Errorf("EndpointInputFromDTO() = %+v, want every field carried through", in)
	}
}

// FR-035: provenance is something Lyrebird observes, never something a caller
// asserts — otherwise a hand-written mock could claim to be captured traffic,
// or a captured one could be laundered into looking authored.
func TestMockInputFromDTOIgnoresFromCapture(t *testing.T) {
	in, err := MockInputFromDTO("default", MockDTO{
		Name:        "claims-to-be-captured",
		Action:      ActionDTO{Respond: &RespondDTO{Status: 200}},
		FromCapture: true,
	})
	if err != nil {
		t.Fatalf("MockInputFromDTO(): %v", err)
	}
	if in.FromCapture {
		t.Error("FromCapture was accepted from the wire, want it ignored")
	}
}

// FuzzEndpointDTO asserts no arbitrary endpoint JSON can panic the conversion
// — the same property the other DTOs are already fuzzed for.
func FuzzEndpointDTO(f *testing.F) {
	f.Add(`{"name":"widget","framing":{"delimiter":"\r\n"}}`)
	f.Add(`{"name":"t","framing":{"prefix":{"width":2}},"cadence":{"interval":"1s","frames":[[{"text":"x"}]]}}`)
	f.Add(`{"framing":{"length":-1}}`)
	f.Add(`{}`)

	f.Fuzz(func(_ *testing.T, raw string) {
		var d EndpointDTO
		if err := json.Unmarshal([]byte(raw), &d); err != nil {
			return
		}
		// Errors are the expected outcome for most inputs; a panic is not.
		if in, err := EndpointInputFromDTO("default", d); err == nil {
			_ = FramingToDTO(in.Framing)
			_ = ProjectionToDTO(in.Projection)
			_ = CadenceToDTO(in.Cadence)
		}
	})
}

func strPtr(s string) *string { return &s }
