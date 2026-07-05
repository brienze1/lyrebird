package store

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/brienze1/lyrebird/internal/domain"
)

// TestAdvanceScenarioAgainstConcurrentGCPruneStress is a stress test for a
// logical race (not a data race — the single shared *sql.DB connection,
// db.SetMaxOpenConns(1), already makes every individual SQL statement here
// atomic and race-detector-clean) between two call sites that both operate
// on the same ephemeral mock without ever sharing a transaction:
//
//   - internal/adapters/proxy/handler.go's serveMocked calls
//     AdvanceEphemeralScenario(partition, mock.ID) using a domain.Mock
//     snapshot that was already matched moments earlier (usecase.MatchRequest,
//     via ListMocks/ScenarioPeeker) — it never re-checks that the mock still
//     exists before advancing.
//   - internal/infra/gc/gc.go's sweep calls PruneExpiredEphemeralMocks,
//     which deletes an expired ephemeral_mocks row AND its scenario_state
//     row together in one transaction specifically to avoid leaving
//     scenario_state orphaned (see the store.go docstring on that method).
//
// This test used to call the original AdvanceScenario here, and used to
// FAIL: that method's INSERT...ON CONFLICT...RETURNING is unconditional — it
// never checks whether mock_id still has a corresponding ephemeral_mocks
// row. So if GC pruned a mock (deleting both its ephemeral_mocks row and its
// scenario_state row) and THEN a request that had already matched that mock
// moments before called AdvanceScenario on it, AdvanceScenario would
// recreate a fresh scenario_state row for an id that no longer existed
// anywhere else — a permanent orphan, since PruneExpiredEphemeralMocks's
// cleanup query only matches scenario_state rows whose (partition, mock_id)
// is currently joinable to an *expired-but-still-present* ephemeral_mocks
// row. Now that serveMocked (and this test) route ephemeral mocks through
// AdvanceEphemeralScenario instead — which guards its INSERT on the mock
// still existing in ephemeral_mocks in the same statement — a call that
// loses the race against GC correctly returns domain.ErrNotFound instead of
// resurrecting the row, and the invariant below holds.
//
// This is deliberately checked against a specific, known-ephemeral mock id
// rather than "any scenario_state row with no matching ephemeral_mocks
// row" — the latter is not a valid general invariant in this schema, since
// seeded mocks (domain.LifetimeSeeded) may also carry a Scenario and
// legitimately have a scenario_state row with NO corresponding
// ephemeral_mocks row for their entire (TTL/reset-immune) lifetime by
// design.
func TestAdvanceScenarioAgainstConcurrentGCPruneStress(t *testing.T) {
	for _, goroutines := range []int{50, 100} {
		goroutines := goroutines
		t.Run(mapGoroutineName(goroutines), func(t *testing.T) {
			st := openTestStore(t)
			ctx := context.Background()

			const partition = "default"
			const mockID = "race-mock"

			// Create the mock already expired (CreatedAt far in the past,
			// short TTL): expires_at is already behind "now" from the
			// instant the row exists, so every GC sweep for the whole run
			// is eligible to prune it — maximizing overlap between "mock
			// exists" and "mock just got pruned" across the stress run,
			// instead of needing to wait out one real TTL window.
			ttl := 1
			m := domain.Mock{
				ID: mockID, Partition: partition, Name: "race", Lifetime: domain.LifetimeEphemeral,
				TTLSeconds: &ttl, CreatedAt: time.Now().Add(-time.Hour),
				Match:  domain.Match{},
				Action: domain.Action{Kind: domain.ActionRespond, Respond: &domain.RespondAction{Status: 200}},
			}
			if err := st.CreateMock(ctx, m); err != nil {
				t.Fatalf("CreateMock(): %v", err)
			}

			const duration = 3 * time.Second
			deadline := time.Now().Add(duration)

			var wg sync.WaitGroup
			var advanceCalls, notFoundCalls, gcSweeps int64
			var firstErr atomic.Value // stores error

			recordErr := func(err error) {
				firstErr.CompareAndSwap(nil, err)
			}

			// Simulate gc.Loop's sweep hammering this same mock's expiry
			// as fast as possible (much tighter than any real GC interval)
			// to maximize race exposure within the time box.
			wg.Add(1)
			go func() {
				defer wg.Done()
				for time.Now().Before(deadline) {
					if _, err := st.PruneExpiredEphemeralMocks(ctx, time.Now()); err != nil {
						recordErr(err)
						return
					}
					atomic.AddInt64(&gcSweeps, 1)
				}
			}()

			// Simulate N concurrent in-flight requests that already matched
			// this scenario mock and are now calling AdvanceEphemeralScenario
			// on it, exactly like serveMocked does for an ephemeral mock —
			// oblivious to whether GC has pruned the mock in the meantime.
			// A domain.ErrNotFound is the expected, correct outcome once GC's
			// sweep wins the race (the whole point of the guard) — it is not
			// a stress-run failure; only any other error is.
			for i := 0; i < goroutines; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for time.Now().Before(deadline) {
						if _, err := st.AdvanceEphemeralScenario(ctx, partition, mockID); err != nil {
							if errors.Is(err, domain.ErrNotFound) {
								atomic.AddInt64(&notFoundCalls, 1)
								continue
							}
							recordErr(err)
							return
						}
						atomic.AddInt64(&advanceCalls, 1)
					}
				}()
			}

			wg.Wait()

			if v := firstErr.Load(); v != nil {
				t.Fatalf("unexpected error during stress run: %v", v)
			}

			t.Logf("goroutines=%d advanceCalls=%d notFoundCalls=%d gcSweeps=%d", goroutines, advanceCalls, notFoundCalls, gcSweeps)

			// The mock itself must be gone by now — that part is GC's job
			// and is not in question here.
			if _, err := st.GetMock(ctx, partition, mockID); !errors.Is(err, domain.ErrNotFound) {
				t.Fatalf("GetMock() after stress run = %v, want domain.ErrNotFound", err)
			}

			// INVARIANT: a known-ephemeral mock id that no longer exists in
			// ephemeral_mocks must not have a leftover scenario_state row.
			// A nonzero count here means AdvanceScenario recreated /
			// incremented scenario_state for mockID *after* GC had already
			// deleted both the mock and its scenario_state row in the same
			// transaction — a permanently orphaned row.
			var orphanCount int
			if err := st.db.QueryRowContext(ctx,
				`SELECT COUNT(*) FROM scenario_state WHERE "partition" = ? AND mock_id = ?`, partition, mockID,
			).Scan(&orphanCount); err != nil {
				t.Fatalf("query scenario_state: %v", err)
			}
			if orphanCount != 0 {
				t.Fatalf("scenario_state has %d orphaned row(s) for deleted mock %q after %d AdvanceScenario calls / %d GC sweeps — "+
					"AdvanceScenario resurrected state for a mock GC already removed, and no future GC sweep can ever clean it up",
					orphanCount, mockID, advanceCalls, gcSweeps)
			}
		})
	}
}

func mapGoroutineName(n int) string {
	switch n {
	case 50:
		return "50_goroutines"
	case 100:
		return "100_goroutines"
	default:
		return "n_goroutines"
	}
}
