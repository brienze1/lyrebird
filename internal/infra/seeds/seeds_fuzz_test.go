package seeds

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"gopkg.in/yaml.v3"
)

// FuzzDecodeSeedFile exercises the exact decode path Load uses per seed
// file — yaml.NewDecoder with KnownFields(true) into the file struct — plus
// every toDomain conversion Load calls afterward, on arbitrary bytes. Seed
// YAML files are operator-authored but explicitly documented (seeds.go's
// package doc, contracts/seed-config.md) as less trusted than the rest of
// the config, so a panic here (malformed YAML, adversarial anchors/aliases,
// deeply nested structures) would be a real startup-time DoS.
func FuzzDecodeSeedFile(f *testing.F) {
	seeds := []string{
		"",
		"space: payments-team\n",
		"space: p\nupstreams:\n  - match_host: \"api.stripe.com\"\n    target_url: \"https://api.stripe.com\"\n",
		"mocks:\n  - name: charge-declined\n    priority: 100\n    action:\n      respond:\n        status: 200\n        body: \"ok\"\n",
		"mocks:\n  - name: bad\n    action:\n      fault:\n        kind: bogus\n",
		"mocks:\n  - name: bad/name\n    action:\n      respond:\n        status: 200\n",
		"unknown_field: 1\n",
		"mocks:\n  - name: a\n    match:\n      headers:\n        X-Test:\n          regex: \"[\"\n    action:\n      respond:\n        status: 200\n",
		"&a [*a]\n", // self-referential alias, classic YAML billion-laughs style trigger
		"a: &a\n  b: *a\n",
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}

	f.Fuzz(func(_ *testing.T, data []byte) {
		var fl file
		dec := yaml.NewDecoder(bytes.NewReader(data))
		dec.KnownFields(true)
		if err := dec.Decode(&fl); err != nil && !errors.Is(err, io.EOF) {
			return
		}
		for _, m := range fl.Mocks {
			_, _ = m.Action.toDomain("fuzz")
			_ = m.Match.toDomain()
			if m.Script != nil {
				_ = m.Script.toDomain()
			}
			if m.Scenario != nil {
				_ = m.Scenario.toDomain()
			}
		}
	})
}
