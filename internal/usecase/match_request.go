package usecase

import (
	"context"
	"fmt"

	"github.com/brienze1/lyrebird/internal/domain"
)

// RespondScriptEval is the subset of ScriptEval BuildRespondOutputWithScript
// needs — named at the point of use so a caller (the proxy Handler) can
// depend on just this, not the full ScriptEval surface.
type RespondScriptEval interface {
	EvalRespond(src string, in MatchInput) ([]byte, error)
}

// MatchRequest resolves which mock, if any, applies to an inbound request
// (FR-009/009a). It only decides WHICH mock wins — interpreting the winning
// mock's Action.Kind (respond directly, inject a fault, or fall through to
// spy passthrough for proxy/no-match) is the caller's job (the proxy
// Handler), since that interpretation involves writing an HTTP response,
// which usecase deliberately stays free of.
type MatchRequest struct {
	repo     MockRepo
	seeds    SeededMockSource
	match    MatchEval
	script   ScriptEval
	scenario ScenarioPeeker
}

// NewMatchRequest builds a MatchRequest use case.
func NewMatchRequest(repo MockRepo, seeds SeededMockSource, match MatchEval, script ScriptEval, scenario ScenarioPeeker) *MatchRequest {
	return &MatchRequest{repo: repo, seeds: seeds, match: match, script: script, scenario: scenario}
}

// scenarioExhausted reports whether m's Scenario is a fallthrough scenario
// whose responses are already used up, per the peeked (not consumed) index
// — used by MatchRequest.Execute (and MatchTest, for accurate dry-run
// previews) to skip such a candidate before ever committing to it. repeat_last
// and wrap scenarios are never "exhausted" in this sense: they always have
// some valid response to serve, they just pick a different one.
func scenarioExhausted(ctx context.Context, peek ScenarioPeeker, partition string, m domain.Mock) (bool, error) {
	if m.Scenario == nil || m.Scenario.OnExhaust != domain.OnExhaustFallthrough {
		return false, nil
	}
	idx, err := peek.ScenarioIndex(ctx, partition, m.ID)
	if err != nil {
		return false, err
	}
	return idx >= len(m.Scenario.Responses), nil
}

// Execute returns the first candidate (by priority desc, created_at desc,
// id asc — FR-009a) whose Match conditions all hold against in, and true.
// If a candidate's declarative Match passes but it also carries a
// Script.MatchSrc, that script is evaluated as an additional AND-composed
// gate (FR-014's "and/or": a script narrows, it doesn't replace, static
// matching) — cheaper-first ordering, since a candidate whose declarative
// Match never passes is never sandboxed at all. A script error stops the
// search immediately (fails safe) rather than falling through to a
// lower-priority candidate — the caller (proxy Handler) is expected to
// synthesize a safe error response for a returned *ScriptError, never
// silently continue as if this mock hadn't matched.
// If no mock matches, it returns the zero Mock and false.
func (uc *MatchRequest) Execute(ctx context.Context, partition string, in MatchInput) (domain.Mock, bool, error) {
	candidates, err := loadSortedCandidates(ctx, uc.repo, uc.seeds, partition)
	if err != nil {
		return domain.Mock{}, false, err
	}
	for _, m := range candidates {
		ok, _ := uc.match.Matches(m.Match, in)
		if !ok {
			continue
		}
		exhausted, serr := scenarioExhausted(ctx, uc.scenario, partition, m)
		if serr != nil {
			return domain.Mock{}, false, fmt.Errorf("usecase: match request: peek scenario: %w", serr)
		}
		if exhausted {
			continue
		}
		if m.Script != nil && m.Script.MatchSrc != "" {
			sok, serr := uc.script.EvalMatch(m.Script.MatchSrc, in)
			if serr != nil {
				return m, false, &ScriptError{MockID: m.ID, Phase: "match", Err: serr}
			}
			if !sok {
				continue
			}
		}
		return m, true, nil
	}
	return domain.Mock{}, false, nil
}

// BuildRespondOutput resolves a matched mock's RespondAction into concrete
// status/headers/body, applying Templater rendering only when the action
// opts in (Template == true) — otherwise body/headers are used verbatim,
// exactly as authored. Used by MatchTest, which deliberately never
// evaluates a mock's Script (running a potentially-hanging agent-authored
// script as a side effect of a "safe dry-run preview" would defeat the
// point of match_test) — the live data-plane path uses
// BuildRespondOutputWithScript instead.
func BuildRespondOutput(action domain.RespondAction, in MatchInput, tpl Templater) (status int, headers map[string]string, body []byte) {
	status = action.Status
	if status == 0 {
		status = 200
	}
	headers, body = action.Headers, action.Body
	if action.Template {
		body = tpl.Render(body, in)
		headers = tpl.RenderHeaders(headers, in)
	}
	return status, headers, body
}

// BuildRespondOutputWithScript is BuildRespondOutput's script-aware sibling,
// used only by the live data-plane path. When script.RespondSrc is set it
// takes over Body only — Status/Headers/LatencyMS still come from action,
// per data-model.md's own wording that a script may build the body "or"
// templating may, not both — a mock combining Template:true and a
// non-empty RespondSrc is not a supported/tested configuration; RespondSrc
// silently wins. A non-nil error return means script evaluation failed and
// the caller MUST fail safe (synthesize an error response, record
// DecisionScriptFailed) rather than serve a partial/zero-value response.
func BuildRespondOutputWithScript(
	action domain.RespondAction, script *domain.Script, in MatchInput, tpl Templater, se RespondScriptEval,
) (status int, headers map[string]string, body []byte, err error) {
	if script != nil && script.RespondSrc != "" {
		status = action.Status
		if status == 0 {
			status = 200
		}
		body, err = se.EvalRespond(script.RespondSrc, in)
		if err != nil {
			return 0, nil, nil, err
		}
		return status, action.Headers, body, nil
	}
	status, headers, body = BuildRespondOutput(action, in, tpl)
	return status, headers, body, nil
}
