package usecase

import (
	"context"
	"testing"

	"github.com/brienze1/lyrebird/internal/domain"
)

func TestResetRemovesEphemeralMocksAndReportsCount(t *testing.T) {
	repo := newFakeMockRepo()
	if err := repo.CreateMock(context.Background(), domain.Mock{ID: "a", Partition: "default", Name: "a"}); err != nil {
		t.Fatalf("CreateMock(): %v", err)
	}
	if err := repo.CreateMock(context.Background(), domain.Mock{ID: "b", Partition: "default", Name: "b"}); err != nil {
		t.Fatalf("CreateMock(): %v", err)
	}
	traffic := &fakeTrafficRepo{}
	uc := NewReset(repo, traffic, &fakeScenarioStateRepo{})

	out, err := uc.Execute(context.Background(), ResetInput{Partition: "default"})
	if err != nil {
		t.Fatalf("Execute(): %v", err)
	}
	if out.MocksRemoved != 2 {
		t.Errorf("MocksRemoved = %d, want 2", out.MocksRemoved)
	}
	if out.TrafficCleared {
		t.Error("TrafficCleared = true, want false (ClearTraffic wasn't requested)")
	}
	if list, _ := repo.ListMocks(context.Background(), "default"); len(list) != 0 {
		t.Errorf("mocks remain after Reset: %+v", list)
	}
	if len(traffic.cleared) != 0 {
		t.Errorf("traffic.cleared = %v, want empty (ClearTraffic wasn't requested)", traffic.cleared)
	}
}

func TestResetOptionallyClearsTraffic(t *testing.T) {
	repo := newFakeMockRepo()
	traffic := &fakeTrafficRepo{}
	uc := NewReset(repo, traffic, &fakeScenarioStateRepo{})

	out, err := uc.Execute(context.Background(), ResetInput{Partition: "default", ClearTraffic: true})
	if err != nil {
		t.Fatalf("Execute(): %v", err)
	}
	if !out.TrafficCleared {
		t.Error("TrafficCleared = false, want true")
	}
	if len(traffic.cleared) != 1 || traffic.cleared[0] != "default" {
		t.Errorf("traffic.cleared = %v, want [\"default\"]", traffic.cleared)
	}
}

func TestResetRestartsScenarioSequences(t *testing.T) {
	repo := newFakeMockRepo()
	traffic := &fakeTrafficRepo{}
	scenario := &fakeScenarioStateRepo{indexes: map[string]int{"default/seq": 3, "other/seq": 1}}
	uc := NewReset(repo, traffic, scenario)

	if _, err := uc.Execute(context.Background(), ResetInput{Partition: "default"}); err != nil {
		t.Fatalf("Execute(): %v", err)
	}
	if idx, _ := scenario.ScenarioIndex(context.Background(), "default", "seq"); idx != 0 {
		t.Errorf("default/seq index after Reset = %d, want 0", idx)
	}
	if idx, _ := scenario.ScenarioIndex(context.Background(), "other", "seq"); idx != 1 {
		t.Errorf("other/seq index after resetting a different partition = %d, want untouched 1", idx)
	}
}
