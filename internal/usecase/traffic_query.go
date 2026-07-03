package usecase

import (
	"context"

	"github.com/brienze1/lyrebird/internal/domain"
)

// ListTraffic returns recorded traffic in a partition matching filter. Thin
// on purpose: it exists as a use-case (not called directly against the repo
// from httpadmin) so a later MCP tool can share the exact same object
// (contracts/admin-rest.md: Admin REST is a thin twin of MCP over one
// use-case layer).
type ListTraffic struct{ repo TrafficRepo }

// NewListTraffic builds a ListTraffic use case.
func NewListTraffic(repo TrafficRepo) *ListTraffic { return &ListTraffic{repo: repo} }

// Execute returns recorded traffic in partition matching filter.
func (uc *ListTraffic) Execute(ctx context.Context, partition string, filter TrafficFilter) ([]domain.TrafficRecord, error) {
	return uc.repo.ListTraffic(ctx, partition, filter)
}

// GetTraffic returns one recorded interaction by id.
type GetTraffic struct{ repo TrafficRepo }

// NewGetTraffic builds a GetTraffic use case.
func NewGetTraffic(repo TrafficRepo) *GetTraffic { return &GetTraffic{repo: repo} }

// Execute returns the recorded interaction identified by (partition, id).
func (uc *GetTraffic) Execute(ctx context.Context, partition, id string) (domain.TrafficRecord, error) {
	return uc.repo.GetTraffic(ctx, partition, id)
}
