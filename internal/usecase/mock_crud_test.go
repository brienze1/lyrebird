package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/brienze1/lyrebird/internal/domain"
)

type fakeMockRepo struct {
	mocks map[string]domain.Mock // key: partition+"/"+id
}

func newFakeMockRepo() *fakeMockRepo { return &fakeMockRepo{mocks: map[string]domain.Mock{}} }

func (f *fakeMockRepo) key(partition, id string) string { return partition + "/" + id }

func (f *fakeMockRepo) CreateMock(_ context.Context, m domain.Mock) error {
	f.mocks[f.key(m.Partition, m.ID)] = m
	return nil
}
func (f *fakeMockRepo) GetMock(_ context.Context, partition, id string) (domain.Mock, error) {
	m, ok := f.mocks[f.key(partition, id)]
	if !ok {
		return domain.Mock{}, domain.ErrNotFound
	}
	return m, nil
}
func (f *fakeMockRepo) ListMocks(_ context.Context, partition string) ([]domain.Mock, error) {
	var out []domain.Mock
	for _, m := range f.mocks {
		if m.Partition == partition {
			out = append(out, m)
		}
	}
	return out, nil
}
func (f *fakeMockRepo) UpdateMock(_ context.Context, m domain.Mock) error {
	if _, ok := f.mocks[f.key(m.Partition, m.ID)]; !ok {
		return domain.ErrNotFound
	}
	f.mocks[f.key(m.Partition, m.ID)] = m
	return nil
}
func (f *fakeMockRepo) DeleteMock(_ context.Context, partition, id string) error {
	k := f.key(partition, id)
	if _, ok := f.mocks[k]; !ok {
		return domain.ErrNotFound
	}
	delete(f.mocks, k)
	return nil
}
func (f *fakeMockRepo) DeleteMocksByPartition(_ context.Context, partition string) error {
	for k, m := range f.mocks {
		if m.Partition == partition {
			delete(f.mocks, k)
		}
	}
	return nil
}
func (f *fakeMockRepo) PruneExpiredEphemeralMocks(_ context.Context, _ time.Time) (int, error) {
	return 0, nil
}

type fakeSeededSource struct{ mocks []domain.Mock }

func (f *fakeSeededSource) SeededMocks(partition string) []domain.Mock {
	var out []domain.Mock
	for _, m := range f.mocks {
		if m.Partition == partition {
			out = append(out, m)
		}
	}
	return out
}

type fakeMatchEval struct{ invalid bool }

func (f *fakeMatchEval) Matches(_ domain.Match, _ MatchInput) (bool, []ConditionResult) {
	return true, nil
}
func (f *fakeMatchEval) ValidateMatch(_ domain.Match) error {
	if f.invalid {
		return domain.ErrInvalidMock
	}
	return nil
}

type fakeIDGen struct{ n int }

func (f *fakeIDGen) NewID() string { f.n++; return "id-" + string(rune('a'+f.n-1)) }

type fakeClock struct{ t time.Time }

func (f *fakeClock) Now() time.Time { return f.t }

func newCRUD() (*MockCRUD, *fakeSeededSource) {
	seeds := &fakeSeededSource{}
	uc := NewMockCRUD(newFakeMockRepo(), seeds, &fakeMatchEval{}, &fakeIDGen{}, &fakeClock{t: time.Unix(1000, 0)})
	return uc, seeds
}

func respondAction(status int) domain.Action {
	return domain.Action{Kind: domain.ActionRespond, Respond: &domain.RespondAction{Status: status}}
}

func TestMockCRUDCreateAlwaysSetsEphemeralLifetime(t *testing.T) {
	uc, _ := newCRUD()
	m, err := uc.Create(context.Background(), MockInput{Partition: "default", Name: "x", Action: respondAction(200)})
	if err != nil {
		t.Fatalf("Create(): %v", err)
	}
	if m.Lifetime != domain.LifetimeEphemeral {
		t.Errorf("Lifetime = %q, want %q", m.Lifetime, domain.LifetimeEphemeral)
	}
}

func TestMockCRUDCreateRejectsEmptyName(t *testing.T) {
	uc, _ := newCRUD()
	_, err := uc.Create(context.Background(), MockInput{Partition: "default", Action: respondAction(200)})
	if !errors.Is(err, domain.ErrInvalidMock) {
		t.Fatalf("Create() = %v, want ErrInvalidMock", err)
	}
}

func TestMockCRUDCreateRejectsMismatchedActionKind(t *testing.T) {
	uc, _ := newCRUD()
	_, err := uc.Create(context.Background(), MockInput{Partition: "default", Name: "x", Action: domain.Action{Kind: domain.ActionRespond}})
	if !errors.Is(err, domain.ErrInvalidMock) {
		t.Fatalf("Create() with Kind=respond but nil Respond = %v, want ErrInvalidMock", err)
	}
}

func TestMockCRUDCreateRejectsInvalidMatch(t *testing.T) {
	repo := newFakeMockRepo()
	uc := NewMockCRUD(repo, &fakeSeededSource{}, &fakeMatchEval{invalid: true}, &fakeIDGen{}, &fakeClock{t: time.Unix(0, 0)})
	_, err := uc.Create(context.Background(), MockInput{Partition: "default", Name: "x", Action: respondAction(200)})
	if !errors.Is(err, domain.ErrInvalidMock) {
		t.Fatalf("Create() with an invalid Match = %v, want ErrInvalidMock", err)
	}
}

func TestMockCRUDUpdateRejectsSeededMock(t *testing.T) {
	uc, seeds := newCRUD()
	seeds.mocks = []domain.Mock{{ID: "seed:default/preset", Partition: "default", Name: "preset", Lifetime: domain.LifetimeSeeded, Action: respondAction(200)}}

	_, err := uc.Update(context.Background(), "default", "seed:default/preset", MockInput{Name: "preset", Action: respondAction(201)})
	if !errors.Is(err, domain.ErrSeededMockImmutable) {
		t.Fatalf("Update() on a seeded mock = %v, want ErrSeededMockImmutable", err)
	}
}

func TestMockCRUDDeleteRejectsSeededMock(t *testing.T) {
	uc, seeds := newCRUD()
	seeds.mocks = []domain.Mock{{ID: "seed:default/preset", Partition: "default", Name: "preset", Lifetime: domain.LifetimeSeeded, Action: respondAction(200)}}

	err := uc.Delete(context.Background(), "default", "seed:default/preset")
	if !errors.Is(err, domain.ErrSeededMockImmutable) {
		t.Fatalf("Delete() on a seeded mock = %v, want ErrSeededMockImmutable", err)
	}
}

func TestMockCRUDUpdateAndDeleteWorkOnEphemeralMocks(t *testing.T) {
	uc, _ := newCRUD()
	created, err := uc.Create(context.Background(), MockInput{Partition: "default", Name: "x", Priority: 1, Action: respondAction(200)})
	if err != nil {
		t.Fatalf("Create(): %v", err)
	}

	updated, err := uc.Update(context.Background(), "default", created.ID, MockInput{Name: "x", Priority: 5, Action: respondAction(201)})
	if err != nil {
		t.Fatalf("Update(): %v", err)
	}
	if updated.Priority != 5 || updated.CreatedAt != created.CreatedAt {
		t.Errorf("Update() = %+v, want priority=5 and preserved CreatedAt=%v", updated, created.CreatedAt)
	}

	if err := uc.Delete(context.Background(), "default", created.ID); err != nil {
		t.Fatalf("Delete(): %v", err)
	}
	if _, err := uc.Get(context.Background(), "default", created.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Get() after Delete() = %v, want ErrNotFound", err)
	}
}

func TestMockCRUDGetAndListMergeEphemeralAndSeeded(t *testing.T) {
	uc, seeds := newCRUD()
	seeds.mocks = []domain.Mock{{ID: "seed:default/preset", Partition: "default", Name: "preset", Lifetime: domain.LifetimeSeeded, Action: respondAction(200)}}
	if _, err := uc.Create(context.Background(), MockInput{Partition: "default", Name: "ephemeral", Action: respondAction(200)}); err != nil {
		t.Fatalf("Create(): %v", err)
	}

	list, err := uc.List(context.Background(), "default", "")
	if err != nil {
		t.Fatalf("List(): %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("List() = %d mocks, want 2 (1 ephemeral + 1 seeded)", len(list))
	}

	got, err := uc.Get(context.Background(), "default", "seed:default/preset")
	if err != nil {
		t.Fatalf("Get() seeded mock: %v", err)
	}
	if got.Lifetime != domain.LifetimeSeeded {
		t.Errorf("Get() seeded mock Lifetime = %q, want %q", got.Lifetime, domain.LifetimeSeeded)
	}
}

func TestMockCRUDListFiltersByGroup(t *testing.T) {
	uc, _ := newCRUD()
	if _, err := uc.Create(context.Background(), MockInput{Partition: "default", Name: "in-group", Group: "checkout", Action: respondAction(200)}); err != nil {
		t.Fatalf("Create(): %v", err)
	}
	if _, err := uc.Create(context.Background(), MockInput{Partition: "default", Name: "other-group", Group: "billing", Action: respondAction(200)}); err != nil {
		t.Fatalf("Create(): %v", err)
	}

	list, err := uc.List(context.Background(), "default", "checkout")
	if err != nil {
		t.Fatalf("List(): %v", err)
	}
	if len(list) != 1 || list[0].Name != "in-group" {
		t.Fatalf("List(group=checkout) = %+v, want just [in-group]", list)
	}
}
