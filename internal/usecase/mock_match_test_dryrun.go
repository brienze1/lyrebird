package usecase

import (
	"context"
	"fmt"

	"github.com/brienze1/lyrebird/internal/domain"
)

// MatchTest is FR-011's dry-run: given a sample request, report which mock
// would fire, which conditions passed/failed for every candidate (not just
// the winner), and the resolved response — without ever sending anything
// onward. It is structurally incapable of forwarding: it depends on no
// upstream lister and no proxy Engine at all, only the same ports
// MatchRequest uses plus Templater to resolve the winning response.
type MatchTest struct {
	repo     MockRepo
	seeds    SeededMockSource
	match    MatchEval
	tpl      Templater
	scenario ScenarioPeeker
}

// NewMatchTest builds a MatchTest use case.
func NewMatchTest(repo MockRepo, seeds SeededMockSource, match MatchEval, tpl Templater, scenario ScenarioPeeker) *MatchTest {
	return &MatchTest{repo: repo, seeds: seeds, match: match, tpl: tpl, scenario: scenario}
}

// CandidateResult reports one candidate mock's evaluation outcome.
type CandidateResult struct {
	Mock       domain.Mock
	Matched    bool
	Conditions []ConditionResult
}

// MatchTestOutput is MatchTest.Execute's full result: every candidate's
// evaluation, plus which one (if any) won and its resolved response.
type MatchTestOutput struct {
	Candidates []CandidateResult
	Winner     *domain.Mock
	Status     int
	Headers    map[string]string
	Body       []byte
}

// Execute evaluates every candidate mock in partition against in, in
// priority order, and reports full detail for all of them.
func (uc *MatchTest) Execute(ctx context.Context, partition string, in MatchInput) (MatchTestOutput, error) {
	candidates, err := loadSortedCandidates(ctx, uc.repo, uc.seeds, partition)
	if err != nil {
		return MatchTestOutput{}, err
	}

	out := MatchTestOutput{Candidates: make([]CandidateResult, 0, len(candidates))}
	for _, m := range candidates {
		matched, conditions := uc.match.Matches(m.Match, in)
		out.Candidates = append(out.Candidates, CandidateResult{Mock: m, Matched: matched, Conditions: conditions})

		if !matched || out.Winner != nil {
			continue
		}
		// A fallthrough scenario mock that's already exhausted would never
		// actually win a live request (MatchRequest.Execute skips it) — skip
		// it here too so the dry-run preview doesn't misreport an outcome a
		// real request wouldn't produce. Purely a read-only peek: MatchTest
		// still never executes a script or mutates any state.
		exhausted, err := scenarioExhausted(ctx, uc.scenario, partition, m)
		if err != nil {
			return MatchTestOutput{}, fmt.Errorf("usecase: match test: peek scenario: %w", err)
		}
		if exhausted {
			continue
		}
		winner := m
		out.Winner = &winner
		if m.Action.Kind == domain.ActionRespond && m.Action.Respond != nil {
			out.Status, out.Headers, out.Body = BuildRespondOutput(*m.Action.Respond, in, uc.tpl)
		}
	}
	return out, nil
}
