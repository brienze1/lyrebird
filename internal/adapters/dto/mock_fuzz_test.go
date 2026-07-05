package dto

import (
	"encoding/json"
	"testing"
)

// FuzzMockInputFromDTO exercises the exact path an inbound Admin REST/MCP
// mock create/update request takes: json.Unmarshal of the raw request body
// into MockDTO (httpadmin decodes with json.Decode; this mirrors it), then
// MockInputFromDTO's own conversion/validation. Every byte here is
// attacker/caller controlled, so a panic (e.g. from indexing into a nil
// slice/map, or from a malformed nested struct) would be reachable from an
// untrusted HTTP request body.
func FuzzMockInputFromDTO(f *testing.F) {
	seeds := []string{
		`{"name":"ping","match":{"method":"GET","path":"/ping"},"action":{"respond":{"status":200,"body":"pong"}}}`,
		`{"name":"ping","lifetime":"seeded","action":{}}`,
		`{"name":"a","action":{"respond":{},"proxy":{}}}`,
		`{"action":{}}`,
		`{}`,
		`{"match":{"headers":{"X":{"regex":"["}}},"action":{"fault":{"kind":"bogus"}}}`,
		`{"match":{"body":[{"jsonpath":"a.b","equals":"x"}]},"action":{"proxy":{}}}`,
		`null`,
		`{"scenario":{"responses":[{"status":200,"body":"x"}],"on_exhaust":"repeat_last"}}`,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}

	f.Fuzz(func(_ *testing.T, data []byte) {
		var d MockDTO
		if err := json.Unmarshal(data, &d); err != nil {
			return
		}
		_, _ = MockInputFromDTO("default", d)
	})
}
