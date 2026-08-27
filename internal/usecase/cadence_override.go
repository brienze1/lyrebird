package usecase

import (
	"context"

	"github.com/brienze1/lyrebird/internal/domain"
)

// CadenceOverride resolves, for one stream endpoint, which mock — if any —
// currently overrides what its running cadence emits (WI-02's cadence-
// override capability). It is the cadence-tick counterpart of MatchRequest:
// the same candidate loading and priority order (FR-009a), narrowed to
// mocks carrying an active `cadence` action whose Match names this
// endpoint, called once per tick from the byte-stream plane rather than
// once per inbound frame.
type CadenceOverride struct {
	repo  MockRepo
	seeds SeededMockSource
	match MatchEval
}

// NewCadenceOverride builds a CadenceOverride use case.
func NewCadenceOverride(repo MockRepo, seeds SeededMockSource, match MatchEval) *CadenceOverride {
	return &CadenceOverride{repo: repo, seeds: seeds, match: match}
}

// Resolve returns the highest-priority mock in partition carrying an active
// cadence action whose Match names endpointName — loadSortedCandidates'
// ordering (priority desc, createdAt desc, id), so a runtime override always
// outranks a seed of equal priority, mirroring MatchRequest.Execute rather
// than inventing a second ordering (FR-009a) — and true. It returns the zero
// Mock and false when none does, which the caller reads as "the endpoint's
// own declared cadence still applies."
//
// A cadence tick has no triggering frame, so — unlike an ordinary stream
// rule, matched against the frame's own projected content — an override mock
// is addressed purely by method/path/headers/query (an empty Body in the
// MatchInput below): content never selects which cadence override applies,
// only which frames it emits once selected. Candidates whose action is not
// `cadence` are skipped outright, so a respond/fault rule sharing the same
// path never wins this resolution by accident.
func (uc *CadenceOverride) Resolve(ctx context.Context, partition, endpointName string) (domain.Mock, bool, error) {
	candidates, err := loadSortedCandidates(ctx, uc.repo, uc.seeds, partition)
	if err != nil {
		return domain.Mock{}, false, err
	}
	in := MatchInput{
		Method: domain.StreamMethod,
		Path:   "/" + endpointName,
		Host:   domain.StreamHost,
	}
	for _, m := range candidates {
		if m.Action.Kind != domain.ActionCadence || m.Action.Cadence == nil {
			continue
		}
		if ok, _ := uc.match.Matches(m.Match, in); ok {
			return m, true, nil
		}
	}
	return domain.Mock{}, false, nil
}
