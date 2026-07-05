package matcher

import (
	"fmt"
	"sync"
	"testing"

	"github.com/brienze1/lyrebird/internal/domain"
	"github.com/brienze1/lyrebird/internal/usecase"
)

// TestCompileCachedConcurrentSharedPattern exercises concurrent access to a
// single regexCache entry: every goroutine calls ValidateMatch and Matches
// against a Match whose path regex is byte-identical across all of them, so
// they race to either store the same freshly compiled *regexp.Regexp or load
// one another's already-stored value for the same key. Run with -race: the
// package comment claims regexCache is "safe for concurrent use", and this
// is what actually exercises that claim against the same key instead of just
// asserting correctness from a single goroutine.
func TestCompileCachedConcurrentSharedPattern(t *testing.T) {
	e := New()
	m := domain.Match{Path: "~^/v[0-9]+/charges$"}
	const matchPath = "/v1/charges"
	const nonMatchPath = "/v1/refunds"

	const n = 100
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()

			if err := e.ValidateMatch(m); err != nil {
				t.Errorf("goroutine %d: ValidateMatch(%+v) = %v, want nil", i, m, err)
			}

			ok, _ := e.Matches(m, usecase.MatchInput{Path: matchPath})
			if !ok {
				t.Errorf("goroutine %d: Matches(path=%q) = false, want true", i, matchPath)
			}

			ok, _ = e.Matches(m, usecase.MatchInput{Path: nonMatchPath})
			if ok {
				t.Errorf("goroutine %d: Matches(path=%q) = true, want false", i, nonMatchPath)
			}
		}(i)
	}
	wg.Wait()
}

// TestCompileCachedConcurrentDistinctPatterns exercises concurrent inserts of
// many distinct regexCache keys: each goroutine builds a Match with a regex
// pattern unique to its own index (both as a header Matcher.Regex, going
// through evalMatcher, and as a path regex, going through matchPath), so no
// two goroutines ever contend for the same cache entry — this stresses
// concurrent sync.Map.Store calls across disjoint keys rather than repeated
// access to one key. Run with -race alongside the shared-pattern test above.
func TestCompileCachedConcurrentDistinctPatterns(t *testing.T) {
	e := New()

	const n = 100
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()

			headerPattern := fmt.Sprintf("^item-%d$", i)
			pathPattern := fmt.Sprintf("~^/goods/item-%d$", i)
			m := domain.Match{
				Path:    pathPattern,
				Headers: map[string]domain.Matcher{"X-Item": {Regex: strp(headerPattern)}},
			}

			if err := e.ValidateMatch(m); err != nil {
				t.Errorf("goroutine %d: ValidateMatch(%+v) = %v, want nil", i, m, err)
			}

			matchValue := fmt.Sprintf("item-%d", i)
			nonMatchValue := fmt.Sprintf("item-%d-nope", i)
			matchPath := fmt.Sprintf("/goods/item-%d", i)
			nonMatchPath := fmt.Sprintf("/goods/item-%d-nope", i)

			ok, _ := e.Matches(m, usecase.MatchInput{
				Path:   matchPath,
				Header: map[string][]string{"X-Item": {matchValue}},
			})
			if !ok {
				t.Errorf("goroutine %d: Matches(path=%q, header=%q) = false, want true", i, matchPath, matchValue)
			}

			ok, _ = e.Matches(m, usecase.MatchInput{
				Path:   nonMatchPath,
				Header: map[string][]string{"X-Item": {nonMatchValue}},
			})
			if ok {
				t.Errorf("goroutine %d: Matches(path=%q, header=%q) = true, want false (pattern is unique to goroutine %d)", i, nonMatchPath, nonMatchValue, i)
			}
		}(i)
	}
	wg.Wait()
}
