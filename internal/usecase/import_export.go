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
}

// ExportSeeds exports a partition's upstreams and ephemeral mocks as a seed bundle.
type ExportSeeds struct {
	upstreams *ListUpstreams
	mocks     *MockCRUD
}

// NewExportSeeds builds an ExportSeeds use case.
func NewExportSeeds(upstreams *ListUpstreams, mocks *MockCRUD) *ExportSeeds {
	return &ExportSeeds{upstreams: upstreams, mocks: mocks}
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
	return ExportBundle{Space: partition, Upstreams: upstreams, Mocks: ephemeral}, nil
}

// ImportResult reports how many of each kind ImportSeeds.Execute created.
type ImportResult struct {
	UpstreamsImported int
	MocksImported     int
}

// ImportSeeds imports a seed bundle's upstreams and mocks into a partition.
type ImportSeeds struct {
	upstreams *SetUpstream
	mocks     *MockCRUD
}

// NewImportSeeds builds an ImportSeeds use case.
func NewImportSeeds(upstreams *SetUpstream, mocks *MockCRUD) *ImportSeeds {
	return &ImportSeeds{upstreams: upstreams, mocks: mocks}
}

// Execute is fail-fast: it stops and returns an error on the first item that fails to apply.
func (uc *ImportSeeds) Execute(ctx context.Context, partition string, upstreams []domain.Upstream, mocks []MockInput) (ImportResult, error) {
	var result ImportResult
	for _, u := range upstreams {
		u.Partition = partition
		if err := uc.upstreams.Execute(ctx, u); err != nil {
			return result, fmt.Errorf("import: upstream %q: %w", u.MatchHost, err)
		}
		result.UpstreamsImported++
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
