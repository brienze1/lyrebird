package usecase

import (
	"context"
	"net/url"

	"github.com/brienze1/lyrebird/internal/domain"
)

// SetUpstream creates or updates the real target spy passthrough forwards
// to for a (partition, match_host) pair (FR-003).
type SetUpstream struct{ repo UpstreamRepo }

// NewSetUpstream builds a SetUpstream use case.
func NewSetUpstream(repo UpstreamRepo) *SetUpstream { return &SetUpstream{repo: repo} }

// Execute validates and persists u, rejecting a missing partition/match_host
// or a target_url that isn't a valid absolute http(s) URL.
func (uc *SetUpstream) Execute(ctx context.Context, u domain.Upstream) error {
	if u.Partition == "" || u.MatchHost == "" {
		return domain.ErrInvalidUpstream
	}
	parsed, err := url.Parse(u.TargetURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return domain.ErrInvalidUpstream
	}
	return uc.repo.SetUpstream(ctx, u)
}

// ListUpstreams returns every upstream configured in a partition — runtime
// (store-backed) and seeded (in-memory, from /config) together.
type ListUpstreams struct {
	repo  UpstreamRepo
	seeds SeededUpstreamSource
}

// NewListUpstreams builds a ListUpstreams use case.
func NewListUpstreams(repo UpstreamRepo, seeds SeededUpstreamSource) *ListUpstreams {
	return &ListUpstreams{repo: repo, seeds: seeds}
}

// Execute returns every upstream configured in partition, runtime upstreams
// first followed by seeded ones — the same ordering MockCRUD.List uses for
// mocks.
func (uc *ListUpstreams) Execute(ctx context.Context, partition string) ([]domain.Upstream, error) {
	runtime, err := uc.repo.ListUpstreams(ctx, partition)
	if err != nil {
		return nil, err
	}
	return append(append([]domain.Upstream{}, runtime...), uc.seeds.SeededUpstreams(partition)...), nil
}

// ExecuteRuntime returns only partition's runtime (store-backed) upstreams,
// excluding seeded ones — used by ExportSeeds, which (like mock export)
// never re-exports seeded content that already round-trips through mounted
// seed config.
func (uc *ListUpstreams) ExecuteRuntime(ctx context.Context, partition string) ([]domain.Upstream, error) {
	return uc.repo.ListUpstreams(ctx, partition)
}
