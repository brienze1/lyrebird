package streamplane

import (
	"testing"

	"github.com/tidwall/gjson"

	"github.com/brienze1/lyrebird/internal/domain"
)

func project(t *testing.T, frame string, p *domain.Projection) gjson.Result {
	t.Helper()
	raw, err := projectFrame([]byte(frame), p)
	if err != nil {
		t.Fatalf("projectFrame(): %v", err)
	}
	return gjson.ParseBytes(raw)
}

func TestProjectFrameAlwaysCarriesLenHexAndText(t *testing.T) {
	got := project(t, "A,12", nil)

	if got.Get("len").Int() != 4 {
		t.Errorf("$.len = %v, want 4", got.Get("len"))
	}
	if got.Get("hex").String() != "412c3132" {
		t.Errorf("$.hex = %q, want 412c3132", got.Get("hex").String())
	}
	if got.Get("text").String() != "A,12" {
		t.Errorf("$.text = %q, want A,12", got.Get("text").String())
	}
	// A frame with no declared projection is still fully matchable by regex
	// over the three always-present keys, so an endpoint needs no projection
	// to be useful.
	if got.Get("fields").Exists() || got.Get("at").Exists() {
		t.Error("$.fields/$.at present with no projection declared, want absent")
	}
}

func TestProjectFrameSplitPopulatesFields(t *testing.T) {
	got := project(t, "A,12,OK", &domain.Projection{Split: ","})

	if got.Get("fields.0").String() != "A" || got.Get("fields.2").String() != "OK" {
		t.Errorf("$.fields = %v, want [A 12 OK]", got.Get("fields"))
	}
}

func TestProjectFrameAtRendersEachDeclaredForm(t *testing.T) {
	p := &domain.Projection{At: []domain.ProjectionField{
		{Name: "kind", Offset: 0, Length: 1, As: domain.ProjectAsText},
		{Name: "count", Offset: 2, Length: 2, As: domain.ProjectAsInt},
		{Name: "marker", Offset: 0, Length: 2, As: domain.ProjectAsHex},
		{Name: "defaulted", Offset: 0, Length: 1},
	}}
	got := project(t, "A,12", p)

	if got.Get("at.kind").String() != "A" {
		t.Errorf("$.at.kind = %q, want A", got.Get("at.kind").String())
	}
	if got.Get("at.count").Int() != 12 {
		t.Errorf("$.at.count = %v, want the number 12", got.Get("at.count"))
	}
	if got.Get("at.marker").String() != "412c" {
		t.Errorf("$.at.marker = %q, want 412c", got.Get("at.marker").String())
	}
	if got.Get("at.defaulted").String() != "A" {
		t.Errorf("$.at.defaulted = %q, want A — an unset `as` means text", got.Get("at.defaulted").String())
	}
}

// FR-027: a declaration the frame cannot satisfy yields an ABSENT value, not
// an error — so a short frame stays matchable with `exists: false` instead of
// making the whole frame unmatchable by every rule.
func TestProjectFrameOmitsUnsatisfiableFields(t *testing.T) {
	p := &domain.Projection{At: []domain.ProjectionField{
		{Name: "past_end", Offset: 10, Length: 4},
		{Name: "not_a_number", Offset: 0, Length: 1, As: domain.ProjectAsInt},
		{Name: "negative_offset", Offset: -1, Length: 1},
		{Name: "zero_length", Offset: 0, Length: 0},
		{Name: "", Offset: 0, Length: 1},
		{Name: "present", Offset: 0, Length: 1},
	}}
	got := project(t, "AB", p)

	for _, absent := range []string{"past_end", "not_a_number", "negative_offset", "zero_length"} {
		if got.Get("at." + absent).Exists() {
			t.Errorf("$.at.%s exists, want absent", absent)
		}
	}
	if got.Get("at.present").String() != "A" {
		t.Errorf("$.at.present = %q, want A — one bad field must not lose the good ones",
			got.Get("at.present").String())
	}
}

// $.text must round-trip every byte: UTF-8 decoding would collapse invalid
// sequences to U+FFFD and let a rule match bytes that never arrived.
func TestProjectFrameTextRoundTripsArbitraryBytes(t *testing.T) {
	frame := []byte{0x00, 0x80, 0xFF, 'A'}
	got := project(t, string(frame), nil)

	if roundTripped := latin1Bytes(got.Get("text").String()); string(roundTripped) != string(frame) {
		t.Errorf("$.text round-trip = %x, want %x", roundTripped, frame)
	}
	if got.Get("hex").String() != "0080ff41" {
		t.Errorf("$.hex = %q, want 0080ff41", got.Get("hex").String())
	}
}

func TestLatin1RoundTrip(t *testing.T) {
	for i := 0; i < 256; i++ {
		b := []byte{byte(i)}
		if got := latin1Bytes(latin1(b)); len(got) != 1 || got[0] != byte(i) {
			t.Fatalf("byte %#x did not round-trip: got %x", i, got)
		}
	}
}
