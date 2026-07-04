package store

import (
	"context"
	"sync"
	"testing"
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
