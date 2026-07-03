package usecase

import (
	"context"

	"github.com/brienze1/lyrebird/internal/domain"
)

// MatchRequest resolves which mock, if any, applies to an inbound request
// (FR-009/009a). It only decides WHICH mock wins — interpreting the winning
// mock's Action.Kind (respond directly, inject a fault, or fall through to
// spy passthrough for proxy/no-match) is the caller's job (the proxy
// Handler), since that interpretation involves writing an HTTP response,
// which usecase deliberately stays free of.
type MatchRequest struct {
	repo  MockRepo
	seeds SeededMockSource
	match MatchEval
}

// NewMatchRequest builds a MatchRequest use case.
func NewMatchRequest(repo MockRepo, seeds SeededMockSource, match MatchEval) *MatchRequest {
	return &MatchRequest{repo: repo, seeds: seeds, match: match}
}

// Execute returns the first candidate (by priority desc, created_at desc,
// id asc — FR-009a) whose Match conditions all hold against in, and true.
// If none match, it returns the zero Mock and false.
func (uc *MatchRequest) Execute(ctx context.Context, partition string, in MatchInput) (domain.Mock, bool, error) {
	candidates, err := loadSortedCandidates(ctx, uc.repo, uc.seeds, partition)
	if err != nil {
		return domain.Mock{}, false, err
	}
	for _, m := range candidates {
		if ok, _ := uc.match.Matches(m.Match, in); ok {
			return m, true, nil
		}
	}
	return domain.Mock{}, false, nil
}

// BuildRespondOutput resolves a matched mock's RespondAction into concrete
// status/headers/body, applying Templater rendering only when the action
// opts in (Template == true) — otherwise body/headers are used verbatim,
// exactly as authored.
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
