package usecase

import (
	"context"
	"errors"
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
		ok, cerr := uc.candidateMatches(ctx, partition, m, in)
		if cerr != nil {
			return zeroUnlessScriptError(m, cerr), false, cerr
		}
		if ok {
			return m, true, nil
		}
	}
	return domain.Mock{}, false, nil
}

// ExecuteProjected is Execute for a data plane whose request body is not one
// fixed document but is DERIVED per candidate — today, the byte-stream plane,
// where a rule may declare its own projection of the frame's bytes and so
// must be evaluated against a different MatchInput.Body than its neighbours
// (003's FR-006).
//
// It exists rather than the adapter looping candidates itself because that
// loop is where mock precedence, scenario exhaustion and script gating live:
// re-implementing it in an adapter would both duplicate FR-009a's ordering
// and put business logic outside the use-case layer. The adapter supplies
// only the one thing it alone knows — how to project the bytes for a given
// mock — through the BodyProjector port.
//
// It returns the MatchInput the winning candidate was actually evaluated
// against, so the caller builds its response (and resolves any copyFrom
// path) against the same projection the match used, not a different one.
//
// A projector error for one candidate SKIPS that candidate rather than
// failing the frame: a rule whose projection cannot read these particular
// bytes has simply not matched them, and a lower-priority rule that can read
// them should still get its turn.
func (uc *MatchRequest) ExecuteProjected(
	ctx context.Context, partition string, base MatchInput, p BodyProjector,
) (domain.Mock, MatchInput, bool, error) {
	candidates, err := loadSortedCandidates(ctx, uc.repo, uc.seeds, partition)
	if err != nil {
		return domain.Mock{}, base, false, err
	}
	for _, m := range candidates {
		in := base
		if p != nil {
			body, perr := p.ProjectFor(m)
			if perr != nil {
				continue
			}
			in.Body = body
		}
		ok, cerr := uc.candidateMatches(ctx, partition, m, in)
		if cerr != nil {
			return zeroUnlessScriptError(m, cerr), in, false, cerr
		}
		if ok {
			return m, in, true, nil
		}
	}
	return domain.Mock{}, base, false, nil
}

// candidateMatches evaluates one candidate: declarative conditions first
// (cheapest), then scenario exhaustion, then the AND-composed script gate —
// so a candidate whose declarative Match never passes is never sandboxed at
// all. Shared by Execute and ExecuteProjected so the two can never drift on
// precedence, exhaustion or fail-safe script semantics.
func (uc *MatchRequest) candidateMatches(
	ctx context.Context, partition string, m domain.Mock, in MatchInput,
) (bool, error) {
	if ok, _ := uc.match.Matches(m.Match, in); !ok {
		return false, nil
	}
	exhausted, err := scenarioExhausted(ctx, uc.scenario, partition, m)
	if err != nil {
		return false, fmt.Errorf("usecase: match request: peek scenario: %w", err)
	}
	if exhausted {
		return false, nil
	}
	if m.Script != nil && m.Script.MatchSrc != "" {
		ok, err := uc.script.EvalMatch(m.Script.MatchSrc, in)
		if err != nil {
			return false, &ScriptError{MockID: m.ID, Phase: "match", Err: err}
		}
		return ok, nil
	}
	return true, nil
}

// zeroUnlessScriptError preserves the mock-returning contract callers depend
// on: a *ScriptError comes back with the offending mock attached (the proxy
// Handler needs its id to record DecisionScriptFailed), while any other
// failure returns the zero Mock so a caller cannot mistake a half-evaluated
// candidate for a match.
func zeroUnlessScriptError(m domain.Mock, err error) domain.Mock {
	var se *ScriptError
	if errors.As(err, &se) {
		return m
	}
	return domain.Mock{}
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
