package matcher

import (
	"testing"

	"github.com/brienze1/lyrebird/internal/domain"
	"github.com/brienze1/lyrebird/internal/usecase"
)

func strp(s string) *string { return &s }
func boolp(b bool) *bool    { return &b }

func TestMatchesMethodCaseInsensitive(t *testing.T) {
	e := New()
	ok, _ := e.Matches(domain.Match{Method: "get"}, usecase.MatchInput{Method: "GET"})
	if !ok {
		t.Fatal("expected method match to be case-insensitive")
	}
}

func TestMatchesPathExactGlobRegex(t *testing.T) {
	e := New()
	cases := []struct {
		name    string
		pattern string
		path    string
		want    bool
	}{
		{"exact match", "/ping", "/ping", true},
		{"exact mismatch", "/ping", "/pong", false},
		{"glob match", "/users/*", "/users/42", true},
		{"glob mismatch", "/users/*", "/orders/42", false},
		{"regex match", "~^/v[0-9]+/charges$", "/v1/charges", true},
		{"regex mismatch", "~^/v[0-9]+/charges$", "/v1/refunds", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ok, _ := e.Matches(domain.Match{Path: tc.pattern}, usecase.MatchInput{Path: tc.path})
			if ok != tc.want {
				t.Errorf("Matches(path=%q, pattern=%q) = %v, want %v", tc.path, tc.pattern, ok, tc.want)
			}
		})
	}
}

func TestMatchesHeaderConditionsAndCaseInsensitiveLookup(t *testing.T) {
	e := New()
	m := domain.Match{Headers: map[string]domain.Matcher{"X-VIP": {Equals: strp("1")}}}

	ok, _ := e.Matches(m, usecase.MatchInput{Header: map[string][]string{"X-Vip": {"1"}}})
	if !ok {
		t.Error("expected header match with differing case to pass (canonicalized lookup)")
	}

	ok, _ = e.Matches(m, usecase.MatchInput{Header: map[string][]string{"X-Vip": {"0"}}})
	if ok {
		t.Error("expected header mismatch to fail")
	}

	ok, _ = e.Matches(m, usecase.MatchInput{})
	if ok {
		t.Error("expected absent header with Equals set to fail (implicit presence requirement)")
	}
}

func TestMatchesExistsChecksPresenceOnly(t *testing.T) {
	e := New()
	mustExist := domain.Match{Headers: map[string]domain.Matcher{"X-Trace": {Exists: boolp(true)}}}
	mustNotExist := domain.Match{Headers: map[string]domain.Matcher{"X-Trace": {Exists: boolp(false)}}}

	ok, _ := e.Matches(mustExist, usecase.MatchInput{Header: map[string][]string{"X-Trace": {"anything"}}})
	if !ok {
		t.Error("Exists:true should pass when the header is present, regardless of value")
	}
	ok, _ = e.Matches(mustExist, usecase.MatchInput{})
	if ok {
		t.Error("Exists:true should fail when the header is absent")
	}
	ok, _ = e.Matches(mustNotExist, usecase.MatchInput{})
	if !ok {
		t.Error("Exists:false should pass when the header is absent")
	}
}

func TestMatchesBodyJSONPathViaGjson(t *testing.T) {
	e := New()
	m := domain.Match{Body: []domain.BodyMatcher{{Path: "tier", Matcher: domain.Matcher{Equals: strp("gold")}}}}

	ok, _ := e.Matches(m, usecase.MatchInput{Body: []byte(`{"tier":"gold"}`)})
	if !ok {
		t.Error("expected matching JSON body to pass")
	}
	ok, _ = e.Matches(m, usecase.MatchInput{Body: []byte(`{"tier":"basic"}`)})
	if ok {
		t.Error("expected non-matching JSON body to fail")
	}
}

func TestMatchesBodyNeverPanicsOnMalformedJSON(t *testing.T) {
	e := New()
	m := domain.Match{Body: []domain.BodyMatcher{{Path: "tier", Matcher: domain.Matcher{Exists: boolp(true)}}}}

	ok, _ := e.Matches(m, usecase.MatchInput{Body: []byte(`{not valid json`)})
	if ok {
		t.Error("expected a JSONPath condition against malformed JSON to fail closed, not panic or pass")
	}
}

func TestMatchesEmptyMatchAlwaysPasses(t *testing.T) {
	e := New()
	ok, results := e.Matches(domain.Match{}, usecase.MatchInput{Method: "GET", Path: "/anything"})
	if !ok {
		t.Error("expected an empty Match to match every request")
	}
	if len(results) != 0 {
		t.Errorf("expected no conditions evaluated for an empty Match, got %+v", results)
	}
}

func TestValidateMatchRejectsBadRegex(t *testing.T) {
	e := New()
	cases := map[string]domain.Match{
		"bad path regex":   {Path: "~("},
		"bad header regex": {Headers: map[string]domain.Matcher{"X": {Regex: strp("(")}}},
		"bad query regex":  {Query: map[string]domain.Matcher{"q": {Regex: strp("(")}}},
		"bad body regex":   {Body: []domain.BodyMatcher{{Path: "x", Matcher: domain.Matcher{Regex: strp("(")}}}},
	}
	for name, m := range cases {
		t.Run(name, func(t *testing.T) {
			if err := e.ValidateMatch(m); err == nil {
				t.Errorf("ValidateMatch(%+v) = nil, want an error", m)
			}
		})
	}
}

func TestValidateMatchAcceptsWellFormedMatch(t *testing.T) {
	e := New()
	m := domain.Match{Path: "~^/v[0-9]+/charges$", Headers: map[string]domain.Matcher{"X": {Regex: strp("^ok$")}}}
	if err := e.ValidateMatch(m); err != nil {
		t.Errorf("ValidateMatch(%+v) = %v, want nil", m, err)
	}
}

func TestValidateMatchRejectsBadGlobPath(t *testing.T) {
	e := New()
	m := domain.Match{Path: "/foo["} // unterminated character class
	if err := e.ValidateMatch(m); err == nil {
		t.Errorf("ValidateMatch(%+v) = nil, want an error for a malformed glob", m)
	}
}

func TestValidateMatchAcceptsWellFormedGlobPath(t *testing.T) {
	e := New()
	m := domain.Match{Path: "/users/*"}
	if err := e.ValidateMatch(m); err != nil {
		t.Errorf("ValidateMatch(%+v) = %v, want nil", m, err)
	}
}
