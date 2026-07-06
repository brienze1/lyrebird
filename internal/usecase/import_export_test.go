package usecase

import (
	"context"
	"testing"

	"github.com/brienze1/lyrebird/internal/domain"
)

func TestExportSeeds_ReturnsUpstreamsAndOnlyEphemeralMocks(t *testing.T) {
	mockCRUD, seeds := newCRUD()
	seeds.mocks = []domain.Mock{{ID: "seeded-1", Partition: "default", Name: "seeded", Lifetime: domain.LifetimeSeeded, Action: respondAction(200)}}
	if _, err := mockCRUD.Create(context.Background(), MockInput{Partition: "default", Name: "ephemeral", Action: respondAction(200)}); err != nil {
		t.Fatalf("Create(): %v", err)
	}

	upstreamRepo := &fakeUpstreamRepo{set: []domain.Upstream{{Partition: "default", MatchHost: "example.local", TargetURL: "https://example.local"}}}
	uc := NewExportSeeds(NewListUpstreams(upstreamRepo), mockCRUD)

	bundle, err := uc.Execute(context.Background(), "default")
	if err != nil {
		t.Fatalf("Execute(): %v", err)
	}
	if bundle.Space != "default" {
		t.Errorf("Space = %q, want default", bundle.Space)
	}
	if len(bundle.Upstreams) != 1 || bundle.Upstreams[0].MatchHost != "example.local" {
		t.Errorf("Upstreams = %+v, want one upstream for example.local", bundle.Upstreams)
	}
	if len(bundle.Mocks) != 1 || bundle.Mocks[0].Name != "ephemeral" {
		t.Errorf("Mocks = %+v, want only the ephemeral mock (not the seeded one)", bundle.Mocks)
	}
}

func TestImportSeeds_CreatesEachUpstreamAndMock(t *testing.T) {
	mockCRUD, _ := newCRUD()
	upstreamRepo := &fakeUpstreamRepo{}
	uc := NewImportSeeds(NewSetUpstream(upstreamRepo), mockCRUD)

	result, err := uc.Execute(context.Background(), "default",
		[]domain.Upstream{{MatchHost: "example.local", TargetURL: "https://example.local"}},
		[]MockInput{{Name: "imported", Action: respondAction(200)}},
	)
	if err != nil {
		t.Fatalf("Execute(): %v", err)
	}
	if result.UpstreamsImported != 1 || result.MocksImported != 1 {
		t.Errorf("result = %+v, want 1 upstream and 1 mock imported", result)
	}
	if len(upstreamRepo.set) != 1 || upstreamRepo.set[0].Partition != "default" {
		t.Errorf("upstream repo = %+v, want one upstream scoped to the default partition", upstreamRepo.set)
	}
	list, err := mockCRUD.List(context.Background(), "default", "")
	if err != nil {
		t.Fatalf("List(): %v", err)
	}
	if len(list) != 1 || list[0].Name != "imported" {
		t.Errorf("mocks = %+v, want one mock named imported", list)
	}
}

func TestImportSeeds_IsAdditiveNotDestructive(t *testing.T) {
	mockCRUD, _ := newCRUD()
	if _, err := mockCRUD.Create(context.Background(), MockInput{Partition: "default", Name: "pre-existing", Action: respondAction(200)}); err != nil {
		t.Fatalf("Create(): %v", err)
	}
	uc := NewImportSeeds(NewSetUpstream(&fakeUpstreamRepo{}), mockCRUD)

	if _, err := uc.Execute(context.Background(), "default", nil, []MockInput{{Name: "imported", Action: respondAction(200)}}); err != nil {
		t.Fatalf("Execute(): %v", err)
	}
	list, err := mockCRUD.List(context.Background(), "default", "")
	if err != nil {
		t.Fatalf("List(): %v", err)
	}
	if len(list) != 2 {
		t.Errorf("mocks = %+v, want both the pre-existing and imported mock", list)
	}
}

func TestImportSeeds_StopsAtTheFirstFailingMock(t *testing.T) {
	mockCRUD, _ := newCRUD()
	uc := NewImportSeeds(NewSetUpstream(&fakeUpstreamRepo{}), mockCRUD)
	invalidMock := MockInput{Name: "", Action: respondAction(200)}

	result, err := uc.Execute(context.Background(), "default", nil, []MockInput{
		{Name: "valid", Action: respondAction(200)},
		invalidMock,
		{Name: "unreachable", Action: respondAction(200)},
	})
	if err == nil {
		t.Fatal("Execute() with an invalid mock in the bundle succeeded, want an error")
	}
	if result.MocksImported != 1 {
		t.Errorf("result = %+v, want 1 mock imported before the failure", result)
	}
	list, err := mockCRUD.List(context.Background(), "default", "")
	if err != nil {
		t.Fatalf("List(): %v", err)
	}
	if len(list) != 1 || list[0].Name != "valid" {
		t.Errorf("mocks = %+v, want only the valid mock created, not unreachable", list)
	}
}
