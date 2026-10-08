package matcher

import (
	"testing"

	"github.com/brienze1/lyrebird/internal/adapters/dto"
	"github.com/brienze1/lyrebird/internal/domain"
	"github.com/brienze1/lyrebird/internal/usecase"
)

func formInput(body string) usecase.MatchInput {
	return usecase.MatchInput{
		Header: map[string][]string{"Content-Type": {"application/x-www-form-urlencoded"}},
		Body:   []byte(body),
	}
}

func bodyMatch(path string, m domain.Matcher) domain.Match {
	return domain.Match{Body: []domain.BodyMatcher{{Path: path, Matcher: m}}}
}

func TestMatchesFormBodyByFieldName(t *testing.T) {
	e := New()
	const body = "channel=C123&text=hello+world%21&text=ignored"
	cases := []struct {
		name string
		m    domain.Match
		want bool
	}{
		{"equals first value", bodyMatch("text", domain.Matcher{Equals: strp("hello world!")}), true},
		{"equals mismatch", bodyMatch("text", domain.Matcher{Equals: strp("ignored")}), false},
		{"contains", bodyMatch("text", domain.Matcher{Contains: strp("world")}), true},
		{"contains mismatch", bodyMatch("text", domain.Matcher{Contains: strp("bye")}), false},
		{"regex", bodyMatch("channel", domain.Matcher{Regex: strp("^C[0-9]+$")}), true},
		{"regex mismatch", bodyMatch("channel", domain.Matcher{Regex: strp("^D")}), false},
		{"root marker path", bodyMatch("$.channel", domain.Matcher{Equals: strp("C123")}), true},
		{"exists", bodyMatch("channel", domain.Matcher{Exists: boolp(true)}), true},
		{"absent field", bodyMatch("thread_ts", domain.Matcher{Exists: boolp(false)}), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ok, results := e.Matches(tc.m, formInput(body))
			if ok != tc.want {
				t.Errorf("Matches() = %v, want %v (detail %+v)", ok, tc.want, results)
			}
		})
	}
}

func TestMatchesFormBodyReportsDecodedActual(t *testing.T) {
	_, results := New().Matches(bodyMatch("text", domain.Matcher{Equals: strp("x")}), formInput("text=a+b"))
	if len(results) != 1 || results[0].Actual != "a b" || results[0].Field != "body.text" {
		t.Fatalf("results = %+v, want one body.text condition with actual %q", results, "a b")
	}
}

func TestMatchesJSONBodyUnchangedUnderFormContentType(t *testing.T) {
	m := bodyMatch("user.tier", domain.Matcher{Equals: strp("gold")})
	ok, _ := New().Matches(m, formInput(`{"user":{"tier":"gold"}}`))
	if !ok {
		t.Error("a JSON body must be evaluated as JSON whatever its declared content type")
	}
}

func TestMatchesUndeclaredFormLikeBodyFailsClosed(t *testing.T) {
	m := bodyMatch("text", domain.Matcher{Exists: boolp(true)})
	for name, header := range map[string]map[string][]string{
		"no content type": nil,
		"text/plain":      {"Content-Type": {"text/plain"}},
		"json":            {"Content-Type": {"application/json"}},
	} {
		t.Run(name, func(t *testing.T) {
			ok, _ := New().Matches(m, usecase.MatchInput{Header: header, Body: []byte("text=hello")})
			if ok {
				t.Error("a body not declared as a form must not be read as one")
			}
		})
	}
}

func TestMatchesUndecodableFormFailsClosed(t *testing.T) {
	ok, _ := New().Matches(bodyMatch("text", domain.Matcher{Exists: boolp(true)}), formInput("text=%zz"))
	if ok {
		t.Error("an undecodable form body must report every field absent")
	}
}

// The match_test dry-run builds its MatchInput from the submitted sample and
// shares Matches, so a form sample predicts the live outcome.
func TestMatchTestDryRunInputMatchesFormBody(t *testing.T) {
	in := dto.MatchTestInputFromDTO(dto.MatchTestRequestDTO{
		Method:  "POST",
		Path:    "/api/chat.postMessage",
		Headers: map[string][]string{"content-type": {"application/x-www-form-urlencoded"}},
		Body:    "channel=C1&text=hi",
	})
	ok, _ := New().Matches(bodyMatch("text", domain.Matcher{Equals: strp("hi")}), in)
	if !ok {
		t.Error("dry-run input with a form body should match on its field")
	}
}
