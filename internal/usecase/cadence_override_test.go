package usecase

import (
	"context"
	"testing"
	"time"

	"github.com/brienze1/lyrebird/internal/domain"
)

func cadenceActionMock(id, name string, priority int, createdAt time.Time) domain.Mock {
	return domain.Mock{
		ID: id, Partition: "default", Name: name, Priority: priority, CreatedAt: createdAt,
		Match: domain.Match{Method: domain.StreamMethod, Path: "/cb5/gps"},
		Action: domain.Action{Kind: domain.ActionCadence, Cadence: &domain.CadenceAction{
			Frames: [][]domain.FramePart{{{Text: ptrString(name)}}},
		}},
	}
}

// TestCadenceOverride_Resolve covers WI-02's Test Plan item 1's priority
// dimension ("higher-priority override wins", "delete mid-stream → declared
// again"), reusing loadSortedCandidates rather than a bespoke ordering
// (FR-009a) — the same guarantee MatchRequest.Execute gives every other
// mock kind.
func TestCadenceOverride_Resolve(t *testing.T) {
	t.Run("no cadence-action mock present resolves to nothing", func(t *testing.T) {
		repo := newFakeMockRepo()
		seeds := &fakeSeededSource{}
		uc := NewCadenceOverride(repo, seeds, &fakeMatchEval{})

		_, ok, err := uc.Resolve(context.Background(), "default", "cb5/gps")
		if err != nil {
			t.Fatalf("Resolve(): %v", err)
		}
		if ok {
			t.Fatal("Resolve() reported an active override, want none")
		}
	})

	t.Run("a single cadence-action mock wins", func(t *testing.T) {
		repo := newFakeMockRepo()
		m := cadenceActionMock("m1", "override", 100, time.Unix(1, 0))
		_ = repo.CreateMock(context.Background(), m)
		seeds := &fakeSeededSource{}
		uc := NewCadenceOverride(repo, seeds, &fakeMatchEval{})

		got, ok, err := uc.Resolve(context.Background(), "default", "cb5/gps")
		if err != nil {
			t.Fatalf("Resolve(): %v", err)
		}
		if !ok || got.ID != "m1" {
			t.Fatalf("Resolve() = %+v, %v, want m1, true", got, ok)
		}
	})

	t.Run("higher priority override wins", func(t *testing.T) {
		repo := newFakeMockRepo()
		low := cadenceActionMock("low", "low-priority", 50, time.Unix(1, 0))
		high := cadenceActionMock("high", "high-priority", 100, time.Unix(1, 0))
		_ = repo.CreateMock(context.Background(), low)
		_ = repo.CreateMock(context.Background(), high)
		seeds := &fakeSeededSource{}
		uc := NewCadenceOverride(repo, seeds, &fakeMatchEval{})

		got, ok, err := uc.Resolve(context.Background(), "default", "cb5/gps")
		if err != nil {
			t.Fatalf("Resolve(): %v", err)
		}
		if !ok || got.ID != "high" {
			t.Fatalf("Resolve() = %+v, %v, want high, true", got, ok)
		}
	})

	t.Run("a runtime ephemeral outranks a seed of equal priority", func(t *testing.T) {
		repo := newFakeMockRepo()
		ephemeral := cadenceActionMock("eph", "ephemeral", 100, time.Unix(0, 0))
		_ = repo.CreateMock(context.Background(), ephemeral)
		seeded := cadenceActionMock("seed", "seeded", 100, time.Unix(0, 0)) // CreatedAt equal at load time
		seeds := &fakeSeededSource{mocks: []domain.Mock{seeded}}
		uc := NewCadenceOverride(repo, seeds, &fakeMatchEval{})

		got, ok, err := uc.Resolve(context.Background(), "default", "cb5/gps")
		if err != nil {
			t.Fatalf("Resolve(): %v", err)
		}
		if !ok || got.ID != "eph" {
			t.Fatalf("Resolve() = %+v, %v, want eph (ephemeral outranks seed at equal priority), true", got, ok)
		}
	})

	t.Run("deleted mid-stream reverts to no active override", func(t *testing.T) {
		repo := newFakeMockRepo()
		m := cadenceActionMock("m1", "override", 100, time.Unix(1, 0))
		_ = repo.CreateMock(context.Background(), m)
		seeds := &fakeSeededSource{}
		uc := NewCadenceOverride(repo, seeds, &fakeMatchEval{})

		if _, ok, err := uc.Resolve(context.Background(), "default", "cb5/gps"); err != nil || !ok {
			t.Fatalf("Resolve() before delete = %v, %v, want ok", ok, err)
		}

		if err := repo.DeleteMock(context.Background(), "default", "m1"); err != nil {
			t.Fatalf("DeleteMock(): %v", err)
		}

		_, ok, err := uc.Resolve(context.Background(), "default", "cb5/gps")
		if err != nil {
			t.Fatalf("Resolve() after delete: %v", err)
		}
		if ok {
			t.Fatal("Resolve() after the override mock was deleted still reports one active, want none")
		}
	})

	t.Run("a respond mock on the same endpoint never wins the cadence resolution", func(t *testing.T) {
		repo := newFakeMockRepo()
		respond := domain.Mock{
			ID: "r1", Partition: "default", Name: "respond", Priority: 1000, CreatedAt: time.Unix(1, 0),
			Match:  domain.Match{Method: domain.StreamMethod, Path: "/cb5/gps"},
			Action: domain.Action{Kind: domain.ActionRespond, Respond: &domain.RespondAction{Body: []byte("x")}},
		}
		_ = repo.CreateMock(context.Background(), respond)
		seeds := &fakeSeededSource{}
		uc := NewCadenceOverride(repo, seeds, &fakeMatchEval{})

		_, ok, err := uc.Resolve(context.Background(), "default", "cb5/gps")
		if err != nil {
			t.Fatalf("Resolve(): %v", err)
		}
		if ok {
			t.Fatal("Resolve() picked a respond-action mock as a cadence override, want none")
		}
	})
}
