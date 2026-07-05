package template

import (
	"testing"

	"github.com/brienze1/lyrebird/internal/usecase"
)

func TestRenderSubstitutesRecognizedPlaceholders(t *testing.T) {
	e := New()
	in := usecase.MatchInput{
		Method: "POST",
		Header: map[string][]string{"X-User": {"alice"}},
		Query:  map[string][]string{"q": {"widgets"}},
		Body:   []byte(`{"tier":"gold"}`),
	}
	got := string(e.Render([]byte(
		`{"method":"{{request.method}}","user":"{{request.header.X-User}}","q":"{{request.query.q}}","tier":"{{request.body.tier}}"}`,
	), in))
	want := `{"method":"POST","user":"alice","q":"widgets","tier":"gold"}`
	if got != want {
		t.Errorf("Render() = %q, want %q", got, want)
	}
}

func TestRenderUnresolvedPlaceholderBecomesEmptyString(t *testing.T) {
	e := New()
	got := string(e.Render([]byte(`missing: [{{request.header.X-Absent}}]`), usecase.MatchInput{}))
	want := "missing: []"
	if got != want {
		t.Errorf("Render() = %q, want %q", got, want)
	}
}

func TestRenderNeverPanicsOnMalformedBody(t *testing.T) {
	e := New()
	got := e.Render([]byte(`{{request.body.tier}}`), usecase.MatchInput{Body: []byte(`{not valid`)})
	if string(got) != "" {
		t.Errorf("Render() with malformed body = %q, want empty string, not a panic", got)
	}
}

func TestRenderHeadersAppliesToEveryValue(t *testing.T) {
	e := New()
	in := usecase.MatchInput{Method: "GET"}
	out := e.RenderHeaders(map[string]string{"X-Echo": "{{request.method}}"}, in)
	if out["X-Echo"] != "GET" {
		t.Errorf("RenderHeaders()[X-Echo] = %q, want %q", out["X-Echo"], "GET")
	}
}

func TestRenderHeaderCanonicalizesLookup(t *testing.T) {
	e := New()
	in := usecase.MatchInput{Header: map[string][]string{"X-Trace-Id": {"abc123"}}}
	got := string(e.Render([]byte("{{request.header.x-trace-id}}"), in))
	if got != "abc123" {
		t.Errorf("Render() = %q, want %q", got, "abc123")
	}
}

func TestRenderQueryNameWithDotResolves(t *testing.T) {
	e := New()
	in := usecase.MatchInput{Query: map[string][]string{"filter.status": {"active"}}}
	got := string(e.Render([]byte("{{request.query.filter.status}}"), in))
	if got != "active" {
		t.Errorf("Render() = %q, want %q", got, "active")
	}
}

func TestRenderHeaderNameWithDotResolves(t *testing.T) {
	e := New()
	in := usecase.MatchInput{Header: map[string][]string{"X.custom": {"value"}}}
	got := string(e.Render([]byte("{{request.header.X.Custom}}"), in))
	if got != "value" {
		t.Errorf("Render() = %q, want %q", got, "value")
	}
}

func TestRenderMissingQueryParamBecomesEmptyString(t *testing.T) {
	e := New()
	in := usecase.MatchInput{Query: map[string][]string{"q": {"widgets"}}}
	got := string(e.Render([]byte("[{{request.query.missing}}]"), in))
	if got != "[]" {
		t.Errorf("Render() = %q, want %q", got, "[]")
	}
}

func TestRenderMultiValueQueryPicksFirst(t *testing.T) {
	e := New()
	in := usecase.MatchInput{Query: map[string][]string{"tag": {"first", "second"}}}
	got := string(e.Render([]byte("{{request.query.tag}}"), in))
	if got != "first" {
		t.Errorf("Render() = %q, want %q", got, "first")
	}
}
