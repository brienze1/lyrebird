package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/brienze1/lyrebird/internal/domain"
)

func TestScenarioIndexIsZeroWithNoRow(t *testing.T) {
	st := openTestStore(t)
	idx, err := st.ScenarioIndex(context.Background(), "default", "never-touched")
	if err != nil {
		t.Fatalf("ScenarioIndex(): %v", err)
	}
	if idx != 0 {
		t.Errorf("ScenarioIndex() = %d, want 0", idx)
	}
}

func TestAdvanceScenarioConsumesConsecutiveIndexes(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	for want := 0; want < 5; want++ {
		got, err := st.AdvanceScenario(ctx, "default", "seq")
		if err != nil {
			t.Fatalf("AdvanceScenario() call %d: %v", want, err)
		}
		if got != want {
			t.Fatalf("AdvanceScenario() call %d = %d, want %d", want, got, want)
		}
	}
}

func TestAdvanceScenarioIsIsolatedByPartitionAndMockID(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	if _, err := st.AdvanceScenario(ctx, "default", "a"); err != nil {
		t.Fatalf("AdvanceScenario(default/a): %v", err)
	}
	if _, err := st.AdvanceScenario(ctx, "default", "a"); err != nil {
		t.Fatalf("AdvanceScenario(default/a) again: %v", err)
	}
	got, err := st.AdvanceScenario(ctx, "default", "b")
	if err != nil {
		t.Fatalf("AdvanceScenario(default/b): %v", err)
	}
	if got != 0 {
		t.Errorf("AdvanceScenario(default/b) first call = %d, want 0 (isolated from mock a)", got)
	}
	got, err = st.AdvanceScenario(ctx, "other", "a")
	if err != nil {
		t.Fatalf("AdvanceScenario(other/a): %v", err)
	}
	if got != 0 {
		t.Errorf("AdvanceScenario(other/a) first call = %d, want 0 (isolated from partition default)", got)
	}
}

// TestAdvanceScenarioConcurrentCallsConsumeDistinctIndexes exercises the
// exact race the single-statement RETURNING approach was chosen to avoid: a
// naive SELECT-then-INSERT/UPDATE could let two concurrent callers both read
// the same starting index before either writes back, both consuming the
// same slot. store.Open serializes all queries through one connection
// (db.SetMaxOpenConns(1)), so this doesn't prove true multi-connection
// atomicity, but it does prove AdvanceScenario itself never returns a
// duplicate or a gap across concurrent Go-level callers.
func TestAdvanceScenarioConcurrentCallsConsumeDistinctIndexes(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	const n = 50
	results := make([]int, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got, err := st.AdvanceScenario(ctx, "default", "concurrent")
			if err != nil {
				t.Errorf("AdvanceScenario() goroutine %d: %v", i, err)
				return
			}
			results[i] = got
		}(i)
	}
	wg.Wait()

	seen := make(map[int]bool, n)
	for _, idx := range results {
		if seen[idx] {
			t.Fatalf("index %d consumed more than once across %d concurrent calls: %v", idx, n, results)
		}
		seen[idx] = true
	}
	for i := 0; i < n; i++ {
		if !seen[i] {
			t.Fatalf("index %d never consumed across %d concurrent calls: %v", i, n, results)
		}
	}
}

// TestAdvanceEphemeralScenarioRejectsAdvanceAfterMockPruned is the fast,
// deterministic (no goroutines) regression test for the exact bug proven by
// the slower TestAdvanceScenarioAgainstConcurrentGCPruneStress: once GC has
// pruned an ephemeral mock (deleting its ephemeral_mocks row and its
// scenario_state row together, in one transaction — mirroring
// PruneExpiredEphemeralMocks's real behavior), a later call that still
// thinks the mock exists (e.g. serveMocked, working off a match-time
// snapshot) must not be able to resurrect a scenario_state row for it.
func TestAdvanceEphemeralScenarioRejectsAdvanceAfterMockPruned(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	const partition = "default"
	const mockID = "ephemeral-race"

	ttl := 1 // seconds
	m := domain.Mock{
		ID: mockID, Partition: partition, Name: "race", Lifetime: domain.LifetimeEphemeral,
		TTLSeconds: &ttl, CreatedAt: time.Now().Add(-time.Hour), // already expired
		Match:  domain.Match{},
		Action: domain.Action{Kind: domain.ActionRespond, Respond: &domain.RespondAction{Status: 200}},
	}
	if err := st.CreateMock(ctx, m); err != nil {
		t.Fatalf("CreateMock(): %v", err)
	}

	// While the mock still exists, AdvanceEphemeralScenario must behave
	// exactly like AdvanceScenario — the guard must never reject a live mock.
	if got, err := st.AdvanceEphemeralScenario(ctx, partition, mockID); err != nil || got != 0 {
		t.Fatalf("AdvanceEphemeralScenario() before prune = (%d, %v), want (0, nil)", got, err)
	}

	// Simulate GC's sweep: the mock is already expired, so this deletes both
	// its ephemeral_mocks row and its scenario_state row in one transaction —
	// exactly gc.go's real path (via PruneExpiredEphemeralMocks).
	if n, err := st.PruneExpiredEphemeralMocks(ctx, time.Now()); err != nil || n != 1 {
		t.Fatalf("PruneExpiredEphemeralMocks() = (%d, %v), want (1, nil)", n, err)
	}

	// The bug this guards against: a request that already matched mockID
	// moments before GC's sweep ran now calls AdvanceEphemeralScenario on a
	// mock that no longer exists anywhere. Must fail with domain.ErrNotFound,
	// never silently resurrect scenario_state.
	if _, err := st.AdvanceEphemeralScenario(ctx, partition, mockID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("AdvanceEphemeralScenario() after prune = %v, want domain.ErrNotFound", err)
	}

	// No orphan: ScenarioIndex must read back as 0 (no row exists), not a
	// resurrected value.
	if idx, err := st.ScenarioIndex(ctx, partition, mockID); err != nil || idx != 0 {
		t.Fatalf("ScenarioIndex() after prune = (%d, %v), want (0, nil) — no orphaned scenario_state row", idx, err)
	}
}

func TestResetScenarioRemovesOnlyThatMock(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	if _, err := st.AdvanceScenario(ctx, "default", "a"); err != nil {
		t.Fatalf("AdvanceScenario(a): %v", err)
	}
	if _, err := st.AdvanceScenario(ctx, "default", "b"); err != nil {
		t.Fatalf("AdvanceScenario(b): %v", err)
	}

	if err := st.ResetScenario(ctx, "default", "a"); err != nil {
		t.Fatalf("ResetScenario(): %v", err)
	}

	if idx, _ := st.ScenarioIndex(ctx, "default", "a"); idx != 0 {
		t.Errorf("ScenarioIndex(a) after reset = %d, want 0", idx)
	}
	if idx, _ := st.ScenarioIndex(ctx, "default", "b"); idx != 1 {
		t.Errorf("ScenarioIndex(b) after resetting a = %d, want untouched 1", idx)
	}
}

func TestResetScenarioOnUnknownMockIsANoOp(t *testing.T) {
	st := openTestStore(t)
	if err := st.ResetScenario(context.Background(), "default", "never-existed"); err != nil {
		t.Fatalf("ResetScenario() on unknown mock: %v", err)
	}
}

func TestResetAllScenariosOnlyAffectsThatPartition(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	if _, err := st.AdvanceScenario(ctx, "default", "a"); err != nil {
		t.Fatalf("AdvanceScenario(default/a): %v", err)
	}
	if _, err := st.AdvanceScenario(ctx, "other", "a"); err != nil {
		t.Fatalf("AdvanceScenario(other/a): %v", err)
	}

	if err := st.ResetAllScenarios(ctx, "default"); err != nil {
		t.Fatalf("ResetAllScenarios(): %v", err)
	}

	if idx, _ := st.ScenarioIndex(ctx, "default", "a"); idx != 0 {
		t.Errorf("ScenarioIndex(default/a) after ResetAllScenarios(default) = %d, want 0", idx)
	}
	if idx, _ := st.ScenarioIndex(ctx, "other", "a"); idx != 1 {
		t.Errorf("ScenarioIndex(other/a) after ResetAllScenarios(default) = %d, want untouched 1", idx)
	}
}
