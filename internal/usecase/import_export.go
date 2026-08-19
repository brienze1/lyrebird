package usecase

import (
	"context"
	"fmt"

	"github.com/brienze1/lyrebird/internal/domain"
)

// ExportBundle is a partition's exportable runtime state: every upstream
// plus every ephemeral mock (never seeded ones — see ExportSeeds.Execute).
type ExportBundle struct {
	Space     string
	Upstreams []domain.Upstream
	Mocks     []domain.Mock
	// Endpoints are the space's ephemeral byte-stream boundaries, so an
	// export reproduces a boundary alongside the rules that target it
	// (003's FR-023). Empty on a Lyrebird with no byte-stream endpoints,
	// which is every Lyrebird that has not declared one.
	Endpoints []domain.Endpoint
}

// ExportSeeds exports a partition's upstreams, ephemeral mocks and
// byte-stream endpoints as a seed bundle.
type ExportSeeds struct {
	upstreams *ListUpstreams
	mocks     *MockCRUD
	endpoints *Endpoints
}

// NewExportSeeds builds an ExportSeeds use case. endpoints may be nil on a
// build with no byte-stream plane wired, in which case an export simply
// carries no endpoints.
func NewExportSeeds(upstreams *ListUpstreams, mocks *MockCRUD, endpoints *Endpoints) *ExportSeeds {
	return &ExportSeeds{upstreams: upstreams, mocks: mocks, endpoints: endpoints}
}

// Execute excludes seeded mocks and seeded upstreams, since they already
// round-trip through mounted seed config.
func (uc *ExportSeeds) Execute(ctx context.Context, partition string) (ExportBundle, error) {
	upstreams, err := uc.upstreams.ExecuteRuntime(ctx, partition)
	if err != nil {
		return ExportBundle{}, fmt.Errorf("export: list upstreams: %w", err)
	}
	all, err := uc.mocks.List(ctx, partition, "")
	if err != nil {
		return ExportBundle{}, fmt.Errorf("export: list mocks: %w", err)
	}
	ephemeral := make([]domain.Mock, 0, len(all))
	for _, m := range all {
		if m.Lifetime == domain.LifetimeEphemeral {
			ephemeral = append(ephemeral, m)
		}
	}
	bundle := ExportBundle{Space: partition, Upstreams: upstreams, Mocks: ephemeral}
	if uc.endpoints != nil {
		views, err := uc.endpoints.List(ctx, partition)
		if err != nil {
			return ExportBundle{}, fmt.Errorf("export: list endpoints: %w", err)
		}
		for _, v := range views {
			// Seeded endpoints are excluded for the same reason seeded mocks
			// are: they already round-trip through mounted seed config, and
			// exporting them would duplicate them on the next import.
			if v.Endpoint.Lifetime == domain.LifetimeEphemeral {
				bundle.Endpoints = append(bundle.Endpoints, v.Endpoint)
			}
		}
	}
	return bundle, nil
}

// ImportResult reports how many of each kind ImportSeeds.Execute created.
type ImportResult struct {
	UpstreamsImported int
	MocksImported     int
	EndpointsImported int
}

// ImportSeeds imports a seed bundle's upstreams, byte-stream endpoints and
// mocks into a partition.
type ImportSeeds struct {
	upstreams *SetUpstream
	mocks     *MockCRUD
	endpoints *Endpoints
}

// NewImportSeeds builds an ImportSeeds use case. endpoints may be nil, in
// which case a bundle carrying endpoints is rejected rather than silently
// importing only half of itself.
func NewImportSeeds(upstreams *SetUpstream, mocks *MockCRUD, endpoints *Endpoints) *ImportSeeds {
	return &ImportSeeds{upstreams: upstreams, mocks: mocks, endpoints: endpoints}
}

// Execute is fail-fast: it stops and returns an error on the first item that
// fails to apply.
//
// Endpoints are applied BEFORE mocks so that a bundle exported from a working
// space imports into a working space: a rule targeting an endpoint that does
// not exist yet is accepted, but the endpoint listing would report it
// unreachable until the boundary caught up.
func (uc *ImportSeeds) Execute(
	ctx context.Context, partition string,
	upstreams []domain.Upstream, mocks []MockInput, endpoints []EndpointInput,
) (ImportResult, error) {
	var result ImportResult
	for _, u := range upstreams {
		u.Partition = partition
		if err := uc.upstreams.Execute(ctx, u); err != nil {
			return result, fmt.Errorf("import: upstream %q: %w", u.MatchHost, err)
		}
		result.UpstreamsImported++
	}
	for _, in := range endpoints {
		if uc.endpoints == nil {
			return result, fmt.Errorf("import: endpoint %q: the byte-stream data plane is not enabled", in.Name)
		}
		in.Partition = partition
		if _, err := uc.endpoints.Create(ctx, in); err != nil {
			return result, fmt.Errorf("import: endpoint %q: %w", in.Name, err)
		}
		result.EndpointsImported++
	}
	for _, in := range mocks {
		in.Partition = partition
		if _, err := uc.mocks.Create(ctx, in); err != nil {
			return result, fmt.Errorf("import: mock %q: %w", in.Name, err)
		}
		result.MocksImported++
	}
	return result, nil
}
