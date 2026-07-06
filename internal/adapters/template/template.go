// Package template implements usecase.Templater: a small, closed
// placeholder grammar for injecting request values into a mock's response
// (FR-010). Deliberately not text/template — its {{if}}/{{range}}/Go-
// expression surface is unneeded complexity and a bigger validation burden
// for operator-authored mock bodies than this tool needs.
package template

import (
	"net/textproto"
	"regexp"
	"strings"

	"github.com/brienze1/lyrebird/internal/adapters/jsonpath"
	"github.com/brienze1/lyrebird/internal/usecase"
)

// Engine implements usecase.Templater.
type Engine struct{}

// New builds a template Engine.
func New() *Engine { return &Engine{} }

// placeholderRe recognizes {{request.method}}, {{request.header.X}},
// {{request.query.q}}, and {{request.body.<gjson-path>}}. Header/query names
// are looked up as a single literal map key (see resolve), not traversed as
// a path, but the character class must still allow '.' since real-world
// header/query names legitimately contain dots (e.g. request.query.filter.status);
// otherwise such placeholders fail to match at all and their literal
// "{{...}}" text leaks into the rendered output.
var placeholderRe = regexp.MustCompile(`\{\{\s*request\.(method|header\.[^}\s]+|query\.[^}\s]+|body\.[^}\s]+)\s*\}\}`)

// Render substitutes every recognized placeholder in body against in. An
// unresolved placeholder (unknown field, missing header/query, absent body
// path) renders as an empty string — it never panics and never leaks the
// raw placeholder text back to the caller.
func (e *Engine) Render(body []byte, in usecase.MatchInput) []byte {
	return placeholderRe.ReplaceAllFunc(body, func(match []byte) []byte {
		return []byte(resolve(placeholderRe.FindSubmatch(match)[1], in))
	})
}

// RenderHeaders applies Render to every header value.
func (e *Engine) RenderHeaders(headers map[string]string, in usecase.MatchInput) map[string]string {
	out := make(map[string]string, len(headers))
	for k, v := range headers {
		out[k] = string(e.Render([]byte(v), in))
	}
	return out
}

func resolve(ref []byte, in usecase.MatchInput) string {
	expr := string(ref)
	switch {
	case expr == "method":
		return in.Method
	case strings.HasPrefix(expr, "header."):
		name := textproto.CanonicalMIMEHeaderKey(strings.TrimPrefix(expr, "header."))
		return firstValue(in.Header, name)
	case strings.HasPrefix(expr, "query."):
		name := strings.TrimPrefix(expr, "query.")
		return firstValue(in.Query, name)
	case strings.HasPrefix(expr, "body."):
		path := strings.TrimPrefix(expr, "body.")
		return jsonpath.GetBytes(in.Body, path).String()
	default:
		return ""
	}
}

func firstValue(m map[string][]string, key string) string {
	if vs, ok := m[key]; ok && len(vs) > 0 {
		return vs[0]
	}
	return ""
}
