package matcher

import (
	"testing"

	"github.com/brienze1/lyrebird/internal/domain"
	"github.com/brienze1/lyrebird/internal/usecase"
)

// FuzzMatchPath exercises matchPath's two code paths — regexp.Compile via
// compileCached (pattern with a "~" prefix) and stdpath.Match (everything
// else) — plus ValidateMatch, with an arbitrary user-supplied pattern and
// actual request path. Both are attacker/operator controlled: Match.Path
// comes straight off Admin REST/MCP mock create/update requests, so a panic
// or hang here would be reachable from an untrusted caller.
func FuzzMatchPath(f *testing.F) {
	seeds := []string{
		"/ping", "/users/*", "/orders/42", "",
		"~^/v[0-9]+/charges$", "~(a+)+$", "~[", "~(", "~)", "~*", "~\\",
		"[", "[a-", "\\", "**", "?", "***",
	}
	for _, p := range seeds {
		for _, a := range seeds {
			f.Add(p, a)
		}
	}

	e := New()
	f.Fuzz(func(_ *testing.T, pattern, actual string) {
		m := domain.Match{Path: pattern}
		_ = e.ValidateMatch(m) // error is a valid outcome; must not panic/hang
		e.Matches(m, usecase.MatchInput{Path: actual})
	})
}

// FuzzMatcherRegex exercises evalMatcher/validateMatcher's Regex field
// (compileCached again, but reached via header/query/body Matcher.Regex —
// also fully user-supplied via Admin REST/MCP).
func FuzzMatcherRegex(f *testing.F) {
	seeds := []string{
		"^abc$", "(a+)+$", "[", "(", ")", "*", "\\", "a{2,1}", ".*.*.*.*.*.*!",
	}
	for _, p := range seeds {
		for _, a := range seeds {
			f.Add(p, a)
		}
	}

	e := New()
	f.Fuzz(func(_ *testing.T, pattern, actual string) {
		m := domain.Matcher{Regex: &pattern}
		_ = e.ValidateMatch(domain.Match{Headers: map[string]domain.Matcher{"X-Test": m}})
		evalMatcher(m, actual, true)
	})
}
