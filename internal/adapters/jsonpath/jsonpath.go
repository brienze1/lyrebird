// Package jsonpath is the one place matcher, template, and scripting each
// route their "JSONPath" body-lookup through — see GetBytes's doc comment
// for why a shared normalization step exists at all.
package jsonpath

import (
	"strings"

	"github.com/tidwall/gjson"
)

// GetBytes looks up path in body. path is accepted in either of two
// dialects: gjson's own bare-path syntax (e.g. "amount", "user.name") or a
// JSONPath-style path with a leading root marker (e.g. "$.amount", or "$"
// alone for the whole document) — gjson has no built-in support for the
// latter (a leading "$" is otherwise treated as a literal object key, which
// silently never matches), and every place in this codebase advertised as
// "JSONPath" (contracts/seed-config.md's own example, the script sandbox's
// jsonpath() helper) is written assuming the leading "$." works. Bracket
// index syntax (JSONPath's "$[0]") is intentionally out of scope — only the
// root-marker prefix is normalized.
func GetBytes(body []byte, path string) gjson.Result {
	return gjson.GetBytes(body, normalize(path))
}

func normalize(path string) string {
	if path == "$" {
		return "@this"
	}
	return strings.TrimPrefix(path, "$.")
}
