package usecase

import (
	"context"
	"fmt"

	"github.com/brienze1/lyrebird/internal/adapters/jsonpath"
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

// Execute returns recorded traffic in partition matching filter. A negative
// filter.Limit is rejected (no legitimate meaning); filter.Limit == 0 is the
// documented "unbounded" default and passes through unchanged.
func (uc *ListTraffic) Execute(ctx context.Context, partition string, filter TrafficFilter) ([]domain.TrafficRecord, error) {
	if filter.Limit < 0 {
		return nil, fmt.Errorf("%w: limit must not be negative", domain.ErrInvalidTrafficFilter)
	}
	if (filter.RequestBodyPath == "") != (filter.RequestBodyEquals == "") {
		return nil, fmt.Errorf("%w: request body path and value must be given together",
			domain.ErrInvalidTrafficFilter)
	}

	if filter.RequestBodyPath == "" {
		return uc.repo.ListTraffic(ctx, partition, filter)
	}

	// The body filter runs here, after decode, because the body is sealed in an opaque
	// blob the store cannot read. A limit therefore cannot be left to the store: it
	// would take the newest N rows and this filter would then throw away whichever of
	// them belong to somebody else, reporting nothing rather than the caller's own
	// entry. Scan unbounded, filter, then keep the newest N of what actually matched.
	wanted := filter.Limit
	scan := filter
	scan.Limit = 0

	records, err := uc.repo.ListTraffic(ctx, partition, scan)
	if err != nil {
		return records, err
	}

	matching := keepMatchingRequestBody(records, filter.RequestBodyPath, filter.RequestBodyEquals)
	if wanted > 0 && len(matching) > wanted {
		matching = matching[:wanted]
	}
	return matching, nil
}

// keepMatchingRequestBody drops entries whose request body does not carry want
// at path, including any it cannot decode.
func keepMatchingRequestBody(records []domain.TrafficRecord, path, want string) []domain.TrafficRecord {
	kept := make([]domain.TrafficRecord, 0, len(records))
	for _, record := range records {
		message, err := DecodeRecordedMessage(record.Request)
		if err != nil {
			continue
		}
		if jsonpath.GetBytes(message.Body, path).String() == want {
			kept = append(kept, record)
		}
	}
	return kept
}

// GetTraffic returns one recorded interaction by id.
type GetTraffic struct{ repo TrafficRepo }

// NewGetTraffic builds a GetTraffic use case.
func NewGetTraffic(repo TrafficRepo) *GetTraffic { return &GetTraffic{repo: repo} }

// Execute returns the recorded interaction identified by (partition, id).
func (uc *GetTraffic) Execute(ctx context.Context, partition, id string) (domain.TrafficRecord, error) {
	return uc.repo.GetTraffic(ctx, partition, id)
}

// ClearTraffic deletes every recorded traffic entry in a partition (FR-027/28).
type ClearTraffic struct{ repo TrafficRepo }

// NewClearTraffic builds a ClearTraffic use case.
func NewClearTraffic(repo TrafficRepo) *ClearTraffic { return &ClearTraffic{repo: repo} }

// Execute deletes every recorded traffic entry in partition.
func (uc *ClearTraffic) Execute(ctx context.Context, partition string) error {
	return uc.repo.ClearTraffic(ctx, partition)
}
