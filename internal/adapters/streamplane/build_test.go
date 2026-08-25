package streamplane

import (
	"strings"
	"testing"

	"github.com/brienze1/lyrebird/internal/domain"
)

func build(t *testing.T, spec, envelope string, framing domain.Framing) string {
	t.Helper()
	fs, err := parseFrameSpec([]byte(spec))
	if err != nil {
		t.Fatalf("parseFrameSpec(%q): %v", spec, err)
	}
	out, err := buildFrame(fs, []byte(envelope), framing)
	if err != nil {
		t.Fatalf("buildFrame(): %v", err)
	}
	return string(out)
}

func TestBuildFrameRendersEachPartKind(t *testing.T) {
	tests := []struct {
		name string
		spec string
		want string
	}{
		{"text", `[{"text":"OK"}]`, "OK\r\n"},
		{"hex", `[{"hex":"4f4b"}]`, "OK\r\n"},
		{"int", `[{"int":7}]`, "7\r\n"},
		{"int padded", `[{"int":7,"width":3}]`, "007\r\n"},
		{"int padded with a custom rune", `[{"int":7,"width":3,"pad":" "}]`, "  7\r\n"},
		{"repeat", `[{"repeat":{"text":"ab"},"times":3}]`, "ababab\r\n"},
		{"concatenation", `[{"text":"A"},{"text":"B"}]`, "AB\r\n"},
		{"object form", `{"parts":[{"text":"OK"}]}`, "OK\r\n"},
		{"raw suppresses the terminator", `{"parts":[{"text":"OK"}],"raw":true}`, "OK"},
		{"empty spec is an empty frame", ``, "\r\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := build(t, tt.spec, "", delimiterFraming()); got != tt.want {
				t.Errorf("built %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildFrameCopyFromResolvesAgainstTheEnvelope(t *testing.T) {
	envelope := `{"text":"READ,42","fields":["READ","42"],"at":{"kind":"R"}}`

	if got := build(t, `[{"text":"VALUE,"},{"copyFrom":"$.fields.1"}]`, envelope, delimiterFraming()); got != "VALUE,42\r\n" {
		t.Errorf("built %q, want %q", got, "VALUE,42\r\n")
	}
	if got := build(t, `[{"copyFrom":"$.at.kind"}]`, envelope, delimiterFraming()); got != "R\r\n" {
		t.Errorf("built %q, want %q", got, "R\r\n")
	}
}

// A copyFrom with nothing to copy from contributes nothing rather than
// failing: an unprompted emission has no triggering frame, and a cadence that
// happens to contain a copyFrom must not fail forever at runtime.
func TestBuildFrameCopyFromWithNoEnvelopeContributesNothing(t *testing.T) {
	if got := build(t, `[{"text":"X"},{"copyFrom":"$.at.kind"}]`, "", delimiterFraming()); got != "X\r\n" {
		t.Errorf("built %q, want %q", got, "X\r\n")
	}
	if got := build(t, `[{"text":"X"},{"copyFrom":"$.absent"}]`, `{"text":"Y"}`, delimiterFraming()); got != "X\r\n" {
		t.Errorf("built %q with an absent path, want %q", got, "X\r\n")
	}
}

func TestBuildFrameRejectsMalformedSpecs(t *testing.T) {
	tests := []struct {
		name string
		spec string
	}{
		{"not JSON", `not json`},
		{"a part setting nothing", `[{}]`},
		{"invalid hex", `[{"hex":"zz"}]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs, err := parseFrameSpec([]byte(tt.spec))
			if err != nil {
				return // rejected at parse time, which is just as good
			}
			if _, err := buildFrame(fs, nil, delimiterFraming()); err == nil {
				t.Errorf("buildFrame(%q) succeeded, want an error", tt.spec)
			}
		})
	}
}

// The grammar is agent-authored, so a pathological spec must fail cleanly
// rather than exhaust memory or the stack (FR-027).
func TestBuildFrameBoundsPathologicalSpecs(t *testing.T) {
	huge := `[{"repeat":{"text":"x"},"times":999999999}]`
	fs, err := parseFrameSpec([]byte(huge))
	if err != nil {
		t.Fatalf("parseFrameSpec(): %v", err)
	}
	if _, err := buildFrame(fs, nil, delimiterFraming()); err == nil {
		t.Error("buildFrame() with a huge repeat count succeeded, want it refused")
	}

	nested := strings.Repeat(`{"repeat":`, 20) + `{"text":"x"}` + strings.Repeat(`,"times":2}`, 20)
	fs, err = parseFrameSpec([]byte("[" + nested + "]"))
	if err != nil {
		t.Fatalf("parseFrameSpec(): %v", err)
	}
	if _, err := buildFrame(fs, nil, delimiterFraming()); err == nil {
		t.Error("buildFrame() with deeply nested repeats succeeded, want it refused")
	}
}

func TestPadNumberNeverTruncates(t *testing.T) {
	if got := padNumber("1234", 2, "0"); got != "1234" {
		t.Errorf("padNumber(1234, width 2) = %q, want the number unchanged", got)
	}
}
