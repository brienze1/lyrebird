// Package usecase defines the application's use cases and the repository
// ports they depend on. At M0 only the port interfaces exist; concrete
// use-case implementations land starting at M1 (see specs/001-lyrebird/tasks.md).
package usecase

import (
	"context"
	"time"

	"github.com/brienze1/lyrebird/internal/domain"
)

// TrafficFilter narrows a traffic listing by the plaintext, indexed columns.
type TrafficFilter struct {
	Method     string
	Host       string
	PathPrefix string
	Status     *int
	Since      *time.Time
	Until      *time.Time
}

// MockRepo persists ephemeral mocks. Seeded mocks never pass through it —
// they live only in memory (constitution Principle III).
type MockRepo interface {
	Create(ctx context.Context, m domain.Mock) error
	Get(ctx context.Context, partition, id string) (domain.Mock, error)
	List(ctx context.Context, partition string) ([]domain.Mock, error)
	Update(ctx context.Context, m domain.Mock) error
	Delete(ctx context.Context, partition, id string) error
	DeleteByPartition(ctx context.Context, partition string) error
	// PruneExpired removes ephemeral mocks whose TTL has elapsed as of now.
	// Seeded mocks are never touched.
	PruneExpired(ctx context.Context, now time.Time) (int, error)
}

// TrafficRepo persists the spy traffic log (FR-002), bounded by retention
// (FR-027).
type TrafficRepo interface {
	Append(ctx context.Context, t domain.TrafficRecord) error
	Get(ctx context.Context, partition, id string) (domain.TrafficRecord, error)
	List(ctx context.Context, partition string, filter TrafficFilter) ([]domain.TrafficRecord, error)
	Purge(ctx context.Context, olderThan time.Time) (int, error)
	Clear(ctx context.Context, partition string) error
}

// PartitionRepo manages spaces/partitions (FR-023).
type PartitionRepo interface {
	Create(ctx context.Context, p domain.Partition) error
	Get(ctx context.Context, id string) (domain.Partition, error)
	List(ctx context.Context) ([]domain.Partition, error)
	// Delete cascades the partition's mocks/traffic/upstreams. Callers MUST
	// reject domain.DefaultPartitionID before calling (FR-024); the repo
	// itself does not special-case it.
	Delete(ctx context.Context, id string) error
}

// UpstreamRepo manages the real targets spy passthrough forwards to (FR-003).
type UpstreamRepo interface {
	Set(ctx context.Context, u domain.Upstream) error
	List(ctx context.Context, partition string) ([]domain.Upstream, error)
	DeleteByPartition(ctx context.Context, partition string) error
}

// ScenarioStateRepo tracks each mock's position through its Scenario
// sequence, reset by a reset operation.
type ScenarioStateRepo interface {
	Index(ctx context.Context, partition, mockID string) (int, error)
	Advance(ctx context.Context, partition, mockID string) (int, error)
	Reset(ctx context.Context, partition, mockID string) error
	ResetAll(ctx context.Context, partition string) error
}

// Clock abstracts time.Now so tests can control it.
type Clock interface{ Now() time.Time }

// IDGen abstracts id generation so tests can control it.
type IDGen interface{ NewID() string }
