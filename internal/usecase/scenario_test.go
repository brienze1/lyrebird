package usecase

import (
	"testing"

	"github.com/brienze1/lyrebird/internal/domain"
)

func TestResolveScenarioResponseRepeatLastClampsToFinal(t *testing.T) {
	sc := domain.Scenario{
		Responses: []domain.RespondAction{{Body: []byte("one")}, {Body: []byte("two")}},
		OnExhaust: domain.OnExhaustRepeatLast,
	}
	cases := map[string]struct {
		idx  int
		want string
	}{
		"first":     {0, "one"},
		"second":    {1, "two"},
		"exhausted": {2, "two"},
		"way past":  {50, "two"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got := ResolveScenarioResponse(sc, c.idx)
			if string(got.Body) != c.want {
				t.Errorf("ResolveScenarioResponse(idx=%d) = %q, want %q", c.idx, got.Body, c.want)
			}
		})
	}
}

func TestResolveScenarioResponseWrapCycles(t *testing.T) {
	sc := domain.Scenario{
		Responses: []domain.RespondAction{{Body: []byte("one")}, {Body: []byte("two")}},
		OnExhaust: domain.OnExhaustWrap,
	}
	cases := map[int]string{0: "one", 1: "two", 2: "one", 3: "two", 4: "one"}
	for idx, want := range cases {
		got := ResolveScenarioResponse(sc, idx)
		if string(got.Body) != want {
			t.Errorf("ResolveScenarioResponse(idx=%d) = %q, want %q", idx, got.Body, want)
		}
	}
}

func TestResolveScenarioResponseFallthroughBehavesLikeRepeatLastWithinRange(t *testing.T) {
	// MatchRequest.Execute never lets a fallthrough scenario reach here
	// already-exhausted, so only in-range indexes are ever passed in practice.
	sc := domain.Scenario{
		Responses: []domain.RespondAction{{Body: []byte("one")}},
		OnExhaust: domain.OnExhaustFallthrough,
	}
	got := ResolveScenarioResponse(sc, 0)
	if string(got.Body) != "one" {
		t.Errorf("ResolveScenarioResponse(idx=0) = %q, want %q", got.Body, "one")
	}
}
