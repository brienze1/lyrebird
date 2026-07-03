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

// ListUpstreams returns every upstream configured in a partition.
type ListUpstreams struct{ repo UpstreamRepo }

// NewListUpstreams builds a ListUpstreams use case.
func NewListUpstreams(repo UpstreamRepo) *ListUpstreams { return &ListUpstreams{repo: repo} }

// Execute returns every upstream configured in partition.
func (uc *ListUpstreams) Execute(ctx context.Context, partition string) ([]domain.Upstream, error) {
	return uc.repo.ListUpstreams(ctx, partition)
}
