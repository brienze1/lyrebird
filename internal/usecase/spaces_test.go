package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/brienze1/lyrebird/internal/domain"
)

// fakePartitionRepo is a small in-memory PartitionRepo that actually tracks
// existence (unlike an always-succeeds stub), so it can catch a use case
// that skips checking GetPartition before acting on an id.
type fakePartitionRepo struct {
	byID    map[string]domain.Partition
	deleted []string
}

func newFakePartitionRepo() *fakePartitionRepo {
	return &fakePartitionRepo{byID: map[string]domain.Partition{}}
}

func (f *fakePartitionRepo) CreatePartition(_ context.Context, p domain.Partition) error {
	f.byID[p.ID] = p
	return nil
}
func (f *fakePartitionRepo) GetPartition(_ context.Context, id string) (domain.Partition, error) {
	p, ok := f.byID[id]
	if !ok {
		return domain.Partition{}, domain.ErrNotFound
	}
	return p, nil
}
func (f *fakePartitionRepo) ListPartitions(_ context.Context) ([]domain.Partition, error) {
	out := make([]domain.Partition, 0, len(f.byID))
	for _, p := range f.byID {
		out = append(out, p)
	}
	return out, nil
}
func (f *fakePartitionRepo) DeletePartition(_ context.Context, id string) error {
	f.deleted = append(f.deleted, id)
	delete(f.byID, id)
	return nil
}

func TestCreateSpaceStampsCreatedAtOnFirstCreation(t *testing.T) {
	repo := newFakePartitionRepo()
	now := time.Unix(1_700_000_000, 0)
	uc := NewCreateSpace(repo, fixedClock{t: now})

	got, err := uc.Execute(context.Background(), domain.Partition{ID: "agent-a", Description: "d"})
	if err != nil {
		t.Fatalf("Execute(): %v", err)
	}
	if got.ID != "agent-a" || got.Description != "d" || !got.CreatedAt.Equal(now) {
		t.Errorf("Execute() = %+v, want ID=agent-a Description=d CreatedAt=%v", got, now)
	}
	if stored := repo.byID["agent-a"]; stored.ID != "agent-a" {
		t.Errorf("repo.byID[agent-a] = %+v, want it stored", stored)
	}
}

func TestCreateSpaceRecreatePreservesOriginalCreatedAt(t *testing.T) {
	repo := newFakePartitionRepo()
	first := time.Unix(1_700_000_000, 0)
	uc := NewCreateSpace(repo, fixedClock{t: first})
	if _, err := uc.Execute(context.Background(), domain.Partition{ID: "agent-a", Description: "v1"}); err != nil {
		t.Fatalf("Execute() first: %v", err)
	}

	second := time.Unix(1_800_000_000, 0)
	uc2 := NewCreateSpace(repo, fixedClock{t: second})
	got, err := uc2.Execute(context.Background(), domain.Partition{ID: "agent-a", Description: "v2"})
	if err != nil {
		t.Fatalf("Execute() second: %v", err)
	}
	if got.Description != "v2" {
		t.Errorf("Execute() second Description = %q, want v2", got.Description)
	}
	if !got.CreatedAt.Equal(first) {
		t.Errorf("Execute() second CreatedAt = %v, want the original %v (not the second clock's Now())", got.CreatedAt, first)
	}
}

func TestCreateSpaceRejectsEmptyID(t *testing.T) {
	repo := newFakePartitionRepo()
	uc := NewCreateSpace(repo, fixedClock{t: time.Now()})

	_, err := uc.Execute(context.Background(), domain.Partition{})
	if !errors.Is(err, domain.ErrInvalidPartition) {
		t.Fatalf("Execute() = %v, want ErrInvalidPartition", err)
	}
	if len(repo.byID) != 0 {
		t.Errorf("invalid partition was still passed to the repo: %+v", repo.byID)
	}
}

func TestListSpacesDelegatesToRepo(t *testing.T) {
	repo := newFakePartitionRepo()
	repo.byID["agent-a"] = domain.Partition{ID: "agent-a"}
	got, err := NewListSpaces(repo).Execute(context.Background())
	if err != nil {
		t.Fatalf("Execute(): %v", err)
	}
	if len(got) != 1 || got[0].ID != "agent-a" {
		t.Errorf("Execute() = %+v, want [agent-a]", got)
	}
}

func TestDeleteSpaceRejectsDefaultWithoutCallingRepo(t *testing.T) {
	repo := newFakePartitionRepo()
	err := NewDeleteSpace(repo).Execute(context.Background(), domain.DefaultPartitionID)
	if !errors.Is(err, domain.ErrDefaultPartitionProtected) {
		t.Fatalf("Execute(default) = %v, want ErrDefaultPartitionProtected", err)
	}
	if len(repo.deleted) != 0 {
		t.Errorf("repo.DeletePartition was called for the default space: %+v", repo.deleted)
	}
}

func TestDeleteSpaceDelegatesForExistingNonDefault(t *testing.T) {
	repo := newFakePartitionRepo()
	repo.byID["agent-a"] = domain.Partition{ID: "agent-a"}
	if err := NewDeleteSpace(repo).Execute(context.Background(), "agent-a"); err != nil {
		t.Fatalf("Execute(): %v", err)
	}
	if len(repo.deleted) != 1 || repo.deleted[0] != "agent-a" {
		t.Errorf("repo.deleted = %+v, want [agent-a]", repo.deleted)
	}
}

// TestDeleteSpaceIsIdempotentForAnUnregisteredID mirrors data-model.md's
// documented ad-hoc space usage (a space's mocks/traffic/upstreams can
// exist without ever calling create_space) — deleting an id never
// registered through create_space must still succeed rather than error,
// matching ordinary REST DELETE idempotency.
func TestDeleteSpaceIsIdempotentForAnUnregisteredID(t *testing.T) {
	repo := newFakePartitionRepo()
	if err := NewDeleteSpace(repo).Execute(context.Background(), "never-created"); err != nil {
		t.Fatalf("Execute(never-created) = %v, want nil (idempotent no-op)", err)
	}
	if len(repo.deleted) != 1 || repo.deleted[0] != "never-created" {
		t.Errorf("repo.deleted = %+v, want [never-created]", repo.deleted)
	}
}
