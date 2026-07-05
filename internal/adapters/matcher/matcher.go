// Package matcher implements usecase.MatchEval: declarative matching of an
// inbound request against a domain.Match (FR-008).
package matcher

import (
	"fmt"
	"net/textproto"
	stdpath "path"
	"regexp"
	"strings"
	"sync"

	"github.com/tidwall/gjson"

	"github.com/brienze1/lyrebird/internal/domain"
	"github.com/brienze1/lyrebird/internal/usecase"
)

// Engine implements usecase.MatchEval. It holds no per-request state and is
// safe for concurrent use — regex compilation is cached in a package-level
// sync.Map keyed by pattern text, shared across every Engine instance
// (there is normally only one, but a shared cache is harmless either way).
type Engine struct{}

// New builds a matcher Engine.
func New() *Engine { return &Engine{} }

// regexCache holds pattern string -> *regexp.Regexp and is intentionally
// unbounded and unevicted: it is keyed only by patterns from write-time
// validated mock Match/Matcher configuration (ValidateMatch, at mock
// create/update time), so it grows with the number of distinct configured
// patterns, not with request volume or attacker-controlled input. Combined
// with this project's disposability principle (short-lived deployments,
// see usecase.Reset), an LRU/TTL is deliberately not used. This has been
// re-evaluated across multiple refactor passes and is a closed decision,
// not an oversight.
var regexCache sync.Map

func compileCached(pattern string) (*regexp.Regexp, error) {
	if v, ok := regexCache.Load(pattern); ok {
		return v.(*regexp.Regexp), nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	regexCache.Store(pattern, re)
	return re, nil
}

// ValidateMatch checks m is well-formed: every regex (path, and any
// header/query/body Matcher.Regex) must compile, and a non-regex path must
// be a syntactically valid glob. Called at mock create/update time so a bad
// pattern is rejected at write time, not at first-match time — without
// this, a malformed glob (e.g. an unterminated "[" class) would silently
// never match anything, live or in a match-test dry-run, with no
// indication it's a syntax error rather than a legitimate mismatch.
func (e *Engine) ValidateMatch(m domain.Match) error {
	if rx, isRegex := strings.CutPrefix(m.Path, "~"); isRegex {
		if _, err := compileCached(rx); err != nil {
			return fmt.Errorf("%w: path regex %q: %w", domain.ErrInvalidMock, rx, err)
		}
	} else if m.Path != "" {
		if _, err := stdpath.Match(m.Path, ""); err != nil {
			return fmt.Errorf("%w: path glob %q: %w", domain.ErrInvalidMock, m.Path, err)
		}
	}
	for name, matcher := range m.Headers {
		if err := validateMatcher(matcher); err != nil {
			return fmt.Errorf("%w: header %q: %w", domain.ErrInvalidMock, name, err)
		}
	}
	for name, matcher := range m.Query {
		if err := validateMatcher(matcher); err != nil {
			return fmt.Errorf("%w: query %q: %w", domain.ErrInvalidMock, name, err)
		}
	}
	for _, bm := range m.Body {
		if err := validateMatcher(bm.Matcher); err != nil {
			return fmt.Errorf("%w: body path %q: %w", domain.ErrInvalidMock, bm.Path, err)
		}
	}
	return nil
}

func validateMatcher(m domain.Matcher) error {
	if m.Regex == nil {
		return nil
	}
	_, err := compileCached(*m.Regex)
	return err
}

// Matches reports whether every condition in m holds against in, plus the
// per-condition detail (method, path, each header/query/body condition).
func (e *Engine) Matches(m domain.Match, in usecase.MatchInput) (bool, []usecase.ConditionResult) {
	var results []usecase.ConditionResult
	overall := true

	if m.Method != "" {
		passed := strings.EqualFold(m.Method, in.Method)
		results = append(results, usecase.ConditionResult{Field: "method", Expected: m.Method, Actual: in.Method, Passed: passed})
		overall = overall && passed
	}

	if m.Path != "" {
		passed, actual := matchPath(m.Path, in.Path)
		results = append(results, usecase.ConditionResult{Field: "path", Expected: m.Path, Actual: actual, Passed: passed})
		overall = overall && passed
	}

	for name, matcher := range m.Headers {
		actual, present := headerValue(in.Header, name)
		passed := evalMatcher(matcher, actual, present)
		results = append(results, usecase.ConditionResult{Field: "header." + name, Expected: describeMatcher(matcher), Actual: actual, Passed: passed})
		overall = overall && passed
	}

	for name, matcher := range m.Query {
		actual, present := firstValue(in.Query, name)
		passed := evalMatcher(matcher, actual, present)
		results = append(results, usecase.ConditionResult{Field: "query." + name, Expected: describeMatcher(matcher), Actual: actual, Passed: passed})
		overall = overall && passed
	}

	for _, bm := range m.Body {
		// gjson never panics on malformed/truncated JSON — it fails closed,
		// reporting the path as not present, which evalMatcher then treats
		// like any other absent field. Worth remembering if a JSONPath
		// condition near the tail of a very large body ever "mysteriously"
		// misses: it may be peekBody's cap truncating the body mid-token,
		// not a matcher bug.
		result := gjson.GetBytes(in.Body, bm.Path)
		actual, present := result.String(), result.Exists()
		passed := evalMatcher(bm.Matcher, actual, present)
		results = append(results, usecase.ConditionResult{Field: "body." + bm.Path, Expected: describeMatcher(bm.Matcher), Actual: actual, Passed: passed})
		overall = overall && passed
	}

	return overall, results
}

// matchPath interprets pattern per FR-008 (exact/glob/regex): a "~" prefix
// selects regex on the remainder; otherwise path.Match is used, which
// already degrades to an exact match for a pattern with no glob metachars.
func matchPath(pattern, actual string) (bool, string) {
	if rx, isRegex := strings.CutPrefix(pattern, "~"); isRegex {
		re, err := compileCached(rx)
		if err != nil {
			return false, actual
		}
		return re.MatchString(actual), actual
	}
	ok, err := stdpath.Match(pattern, actual)
	return err == nil && ok, actual
}

// headerValue looks up name in header case-insensitively, canonicalizing
// via textproto (the same normalization http.Header.Clone/net/http already
// apply), and returns its first value.
func headerValue(header map[string][]string, name string) (string, bool) {
	return firstValue(header, textproto.CanonicalMIMEHeaderKey(name))
}

func firstValue(m map[string][]string, key string) (string, bool) {
	if vs, ok := m[key]; ok && len(vs) > 0 {
		return vs[0], true
	}
	return "", false
}

// evalMatcher applies m's set fields (AND) against actual/present.
// Equals/Contains/Regex all implicitly require presence; Exists checks
// presence only.
func evalMatcher(m domain.Matcher, actual string, present bool) bool {
	if m.Exists != nil && *m.Exists != present {
		return false
	}
	if m.Equals != nil && (!present || actual != *m.Equals) {
		return false
	}
	if m.Contains != nil && (!present || !strings.Contains(actual, *m.Contains)) {
		return false
	}
	if m.Regex != nil {
		if !present {
			return false
		}
		re, err := compileCached(*m.Regex)
		if err != nil || !re.MatchString(actual) {
			return false
		}
	}
	return true
}

func describeMatcher(m domain.Matcher) string {
	switch {
	case m.Equals != nil:
		return "equals " + *m.Equals
	case m.Contains != nil:
		return "contains " + *m.Contains
	case m.Regex != nil:
		return "regex " + *m.Regex
	case m.Exists != nil:
		return fmt.Sprintf("exists=%v", *m.Exists)
	default:
		return ""
	}
}
