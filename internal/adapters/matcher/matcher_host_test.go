package matcher

import (
	"testing"

	"github.com/brienze1/lyrebird/internal/domain"
	"github.com/brienze1/lyrebird/internal/usecase"
)

// net/http promotes Host onto Request.Host and removes it from Header, so a
// Host condition evaluated against the header map alone can never pass. The
// failure is silent — the mock simply never fires and the request proxies on to
// the upstream — which is why this is worth pinning down.
func TestHostConditionMatchesRequestAuthority(t *testing.T) {
	e := New()
	m := domain.Match{
		Method:  "GET",
		Path:    "/probe.tif",
		Headers: map[string]domain.Matcher{"Host": {Equals: strp("cog-s2:8080")}},
	}
	in := usecase.MatchInput{
		Method: "GET",
		Path:   "/probe.tif",
		Host:   "cog-s2:8080",
		Header: map[string][]string{}, // exactly what net/http hands us
	}
	ok, conds := e.Matches(m, in)
	if !ok {
		t.Fatalf("Host condition did not match; conditions: %+v", conds)
	}
}

// The whole point is discrimination between providers sharing a path — one
// lyrebird instance fronts several upstreams by authority alone.
func TestHostConditionRejectsAnotherAuthority(t *testing.T) {
	e := New()
	m := domain.Match{
		Path:    "/v1/search",
		Headers: map[string]domain.Matcher{"Host": {Equals: strp("stac")}},
	}
	in := usecase.MatchInput{
		Path:   "/v1/search",
		Host:   "sentinel-hub",
		Header: map[string][]string{},
	}
	if ok, _ := e.Matches(m, in); ok {
		t.Fatal("a mock scoped to one authority matched a different one")
	}
}

// An explicit Host header still wins, so a non-HTTP caller that legitimately
// carries one — or gRPC's :authority passed through as an ordinary header —
// keeps its previous behaviour.
func TestExplicitHostHeaderTakesPrecedence(t *testing.T) {
	e := New()
	m := domain.Match{Headers: map[string]domain.Matcher{"Host": {Equals: strp("from-header")}}}
	in := usecase.MatchInput{
		Host:   "from-authority",
		Header: map[string][]string{"Host": {"from-header"}},
	}
	if ok, conds := e.Matches(m, in); !ok {
		t.Fatalf("explicit Host header should win; conditions: %+v", conds)
	}
}

// Absent everywhere, a Host condition must fail rather than match vacuously.
func TestHostConditionFailsWhenAbsent(t *testing.T) {
	e := New()
	m := domain.Match{Headers: map[string]domain.Matcher{"Host": {Equals: strp("cog-s2")}}}
	if ok, _ := e.Matches(m, usecase.MatchInput{Header: map[string][]string{}}); ok {
		t.Fatal("Host condition matched with no authority present")
	}
}

// Other headers are untouched by the Host special case.
func TestNonHostHeadersUnaffected(t *testing.T) {
	e := New()
	m := domain.Match{Headers: map[string]domain.Matcher{"X-Vip": {Equals: strp("yes")}}}
	in := usecase.MatchInput{Host: "irrelevant", Header: map[string][]string{"X-Vip": {"yes"}}}
	if ok, conds := e.Matches(m, in); !ok {
		t.Fatalf("ordinary header condition broke; conditions: %+v", conds)
	}
}
