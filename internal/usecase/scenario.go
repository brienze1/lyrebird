package usecase

import "github.com/brienze1/lyrebird/internal/domain"

// ResolveScenarioResponse picks which of sc.Responses a call that just
// consumed consumedIdx (the value ScenarioStateRepo.AdvanceScenario
// returned) should be answered with.
//
// wrap cycles back to Responses[0] once exhausted (modulo). repeat_last
// clamps to the final response once exhausted. fallthrough never reaches
// here already-exhausted — MatchRequest.Execute skips a fallthrough
// candidate at match time once its peeked index is out of range (see
// scenarioExhausted in match_request.go), so by the time a scenario mock
// commits to answering, fallthrough behaves identically to repeat_last
// for any consumedIdx it could actually receive.
func ResolveScenarioResponse(sc domain.Scenario, consumedIdx int) domain.RespondAction {
	n := len(sc.Responses)
	if n == 0 {
		// MockCRUD.validate rejects an empty Responses at write time; this
		// is defense-in-depth only, never expected in practice.
		return domain.RespondAction{}
	}
	if sc.OnExhaust == domain.OnExhaustWrap {
		return sc.Responses[consumedIdx%n]
	}
	if consumedIdx >= n {
		consumedIdx = n - 1
	}
	return sc.Responses[consumedIdx]
}
