package usecase

import (
	"context"
	"sort"

	"github.com/brienze1/lyrebird/internal/domain"
)

// loadSortedCandidates merges partition's ephemeral mocks (from repo) with
// its seeded mocks (from seeds, held only in memory) and sorts them by
// priority descending, then CreatedAt descending, then ID ascending — a
// genuine total order (FR-009a). Seeded mocks get a synthetic
// CreatedAt = time.Unix(0,0) at load time (internal/infra/seeds), so any
// ephemeral mock of equal priority always outranks a seeded one: API
// overrides beat static config by design.
//
// This does a fresh list+sort on every call (no cross-request caching),
// matching the precedent already set by M1's upstreams.Execute — fine at
// M2's expected mock-count scale, a documented future optimization if
// SC-009 latency ever demands otherwise.
func loadSortedCandidates(ctx context.Context, repo MockRepo, seeds SeededMockSource, partition string) ([]domain.Mock, error) {
	ephemeral, err := repo.ListMocks(ctx, partition)
	if err != nil {
		return nil, err
	}
	seeded := seeds.SeededMocks(partition)

	all := make([]domain.Mock, 0, len(ephemeral)+len(seeded))
	all = append(all, ephemeral...)
	all = append(all, seeded...)

	sort.SliceStable(all, func(i, j int) bool {
		if all[i].Priority != all[j].Priority {
			return all[i].Priority > all[j].Priority
		}
		if !all[i].CreatedAt.Equal(all[j].CreatedAt) {
			return all[i].CreatedAt.After(all[j].CreatedAt)
		}
		return all[i].ID < all[j].ID
	})
	return all, nil
}
