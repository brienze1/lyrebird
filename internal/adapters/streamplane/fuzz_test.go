package streamplane

import (
	"bytes"
	"testing"

	"github.com/brienze1/lyrebird/internal/domain"
)

// FuzzProjectFrame asserts FR-027 at the layer that reads attacker-shaped
// input: whatever bytes arrive, and whatever projection an agent declared,
// projection must not panic and must produce parseable JSON — because a panic
// here would take down an endpoint for every other connection on it.
//
// Matches how matcher, dto and seeds are already fuzzed in this codebase.
func FuzzProjectFrame(f *testing.F) {
	f.Add([]byte("A,12,OK\r\n"), ",", 0, 1)
	f.Add([]byte{}, "", 0, 0)
	f.Add([]byte{0x00, 0xFF, 0x80}, ",", -1, -1)
	f.Add([]byte("12345"), "|", 3, 99)

	f.Fuzz(func(t *testing.T, frame []byte, split string, offset, length int) {
		p := &domain.Projection{
			Split: split,
			At: []domain.ProjectionField{
				{Name: "text", Offset: offset, Length: length, As: domain.ProjectAsText},
				{Name: "int", Offset: offset, Length: length, As: domain.ProjectAsInt},
				{Name: "hex", Offset: offset, Length: length, As: domain.ProjectAsHex},
			},
		}
		raw, err := projectFrame(frame, p)
		if err != nil {
			t.Fatalf("projectFrame() returned an error for %q: %v — an unreadable field must be absent, not fatal", frame, err)
		}
		if !bytes.HasPrefix(bytes.TrimSpace(raw), []byte("{")) {
			t.Fatalf("projectFrame() produced %q, want a JSON object", raw)
		}
	})
}

// FuzzParseAndBuildFrame asserts the outbound half of the same property: an
// arbitrary frame spec, however malformed, must fail cleanly rather than
// panic or hang.
func FuzzParseAndBuildFrame(f *testing.F) {
	f.Add(`[{"text":"OK"}]`)
	f.Add(`{"parts":[{"hex":"0d0a"}],"raw":true}`)
	f.Add(`[{"repeat":{"text":"x"},"times":3}]`)
	f.Add(``)
	f.Add(`[{`)

	f.Fuzz(func(_ *testing.T, spec string) {
		fs, err := parseFrameSpec([]byte(spec))
		if err != nil {
			return // rejecting a malformed spec is the correct outcome
		}
		// Errors are fine here; panics and hangs are not, which is what the
		// fuzzer is actually checking for.
		_, _ = buildFrame(fs, []byte(`{"text":"x","fields":["a","b"]}`), delimiterFraming())
	})
}
