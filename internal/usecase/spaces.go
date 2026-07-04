package usecase

import (
	"context"
	"errors"

	"github.com/brienze1/lyrebird/internal/domain"
)

// CreateSpace registers a space, or refreshes its description if it already
// exists (FR-023).
type CreateSpace struct {
	repo  PartitionRepo
	clock Clock
}

// NewCreateSpace builds a CreateSpace use case.
func NewCreateSpace(repo PartitionRepo, clock Clock) *CreateSpace {
	return &CreateSpace{repo: repo, clock: clock}
}

// Execute validates and persists p. CreatedAt is stamped from the injected
// Clock only on first creation; a re-create looks up the existing row and
// preserves its real CreatedAt (Store.CreatePartition itself already never
// overwrites created_at on conflict — this keeps the value Execute returns
// consistent with what's actually persisted, rather than reporting a
// fabricated timestamp on every re-create).
func (uc *CreateSpace) Execute(ctx context.Context, p domain.Partition) (domain.Partition, error) {
	if p.ID == "" {
		return domain.Partition{}, domain.ErrInvalidPartition
	}
	existing, err := uc.repo.GetPartition(ctx, p.ID)
	switch {
	case err == nil:
		p.CreatedAt = existing.CreatedAt
	case errors.Is(err, domain.ErrNotFound):
		p.CreatedAt = uc.clock.Now()
	default:
		return domain.Partition{}, err
	}
	if err := uc.repo.CreatePartition(ctx, p); err != nil {
		return domain.Partition{}, err
	}
	return p, nil
}

// ListSpaces returns every registered space.
type ListSpaces struct{ repo PartitionRepo }

// NewListSpaces builds a ListSpaces use case.
func NewListSpaces(repo PartitionRepo) *ListSpaces { return &ListSpaces{repo: repo} }

// Execute returns every registered space.
func (uc *ListSpaces) Execute(ctx context.Context) ([]domain.Partition, error) {
	return uc.repo.ListPartitions(ctx)
}

// DeleteSpace removes a space, cascading its ephemeral mocks, recorded
// traffic, and upstream configuration (FR-024). The default space can never
// be deleted.
type DeleteSpace struct{ repo PartitionRepo }

// NewDeleteSpace builds a DeleteSpace use case.
func NewDeleteSpace(repo PartitionRepo) *DeleteSpace { return &DeleteSpace{repo: repo} }

// Execute deletes id, rejecting domain.DefaultPartitionID before ever
// calling the repo (which does not special-case it itself).
//
// Deliberately no existence check against the partitions registry first:
// spaces are usable ad hoc via a mock/upstream's space argument or the
// X-Lyrebird-Space header without ever being registered through
// create_space (data-model.md's Partition section, quickstart.md Scenario
// E — neither requires create_space before a space's mocks/traffic/
// upstreams exist or before deleting it). Requiring prior registration
// here would make cleanup of an ad-hoc space impossible unless the caller
// had separately called create_space first, contradicting that design.
// DeletePartition's cascade is a no-op for a space with no state, so this
// is idempotent-by-construction — matching ordinary REST DELETE semantics
// (deleting something already absent is a harmless success, not an error).
func (uc *DeleteSpace) Execute(ctx context.Context, id string) error {
	if id == domain.DefaultPartitionID {
		return domain.ErrDefaultPartitionProtected
	}
	return uc.repo.DeletePartition(ctx, id)
}
