package store

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/brienze1/lyrebird/internal/domain"
	"github.com/brienze1/lyrebird/internal/usecase"
)

// TestResetAgainstConcurrentInFlightWritesStress is a stress test for
// usecase.Reset.Execute (internal/usecase/reset.go) racing against
// concurrent in-flight requests on the SAME partition, hunting for a
// permanent scenario_state orphan/resurrection — NOT the list-then-delete
// mock-sweep ambiguity that reset.go's own doc comment already documents
// and accepts as a trade-off (a mock created between ListMocks and
// DeleteMocksByPartition getting swept up, or surviving because it landed
// after the delete). That race predates this test and is out of scope here.
//
// The question this test asks instead: Reset.Execute runs, in order,
// DeleteMocksByPartition (deletes ephemeral_mocks rows) then
// ResetAllScenarios (an unconditional `DELETE FROM scenario_state WHERE
// partition = ?`, not conditioned on which mock ids existed). Concurrently,
// a goroutine simulating an in-flight request that matched one of this
// partition's ephemeral scenario mocks moments ago calls
// AdvanceEphemeralScenario against that mock's KNOWN id, oblivious to
// whether Reset has since deleted it. AdvanceEphemeralScenario guards its
// INSERT on the mock still existing in ephemeral_mocks (see scenario.go's
// doc comment on that method), so:
//
//   - If the call lands before DeleteMocksByPartition commits: the mock
//     still exists, the call succeeds and may insert a fresh scenario_state
//     row — but ResetAllScenarios runs next, in the same Execute call, and
//     unconditionally wipes every scenario_state row for the partition,
//     including this one.
//   - If the call lands after DeleteMocksByPartition commits (whether
//     before or after ResetAllScenarios, or even after Execute returns
//     entirely): the mock no longer exists in ephemeral_mocks, so the
//     guarded INSERT's WHERE EXISTS finds nothing and the call correctly
//     returns domain.ErrNotFound instead of resurrecting a row — exactly
//     like gc_scenario_race_stress_test.go's reasoning for GC pruning,
//     applied here to Reset instead.
//
// SQLite's single connection (db.SetMaxOpenConns(1)) serializes every
// individual statement, so there is no gap in which a mock could be "half
// deleted." The only question is ordering, and both orderings above end
// with zero surviving scenario_state rows for a Reset'd mock id — so this
// test asserts exactly that invariant holds under sustained concurrent
// pressure, rather than assuming it from reading the code.
//
// Like partitions_race_stress_test.go (and unlike
// gc_scenario_race_stress_test.go's single long-lived id), this uses many
// independent single-shot rounds, each against fresh mock ids suffixed by
// round number. That structure matters here too: DeleteMocksByPartition,
// ResetAllScenarios, and ClearTraffic are all blanket, unconditional
// per-partition deletes — so reusing the same mock id across rounds would
// let a later round's Reset call silently clean up an earlier round's
// orphan, hiding the very bug this test is hunting for. A fresh id per
// round, checked immediately after that round's one-and-only Reset call,
// removes that self-healing ambiguity entirely.
//
// AppendTraffic is also hammered concurrently (racing ClearTraffic), but
// deliberately not asserted on: whether a given traffic row survives the
// clear window is inherently ambiguous (same reasoning
// TestDeletePartitionAgainstConcurrentWritesStress applies to non-key-
// invariant tables) and is not a correctness violation either way. Only
// aggregate counts are logged for information.
func TestResetAgainstConcurrentInFlightWritesStress(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	uc := usecase.NewReset(st, st, st)

	const partition = "default"
	const rounds = 200
	const mocksPerRound = 4
	const writersPerOp = 2

	var (
		advanceCalls, notFoundCalls, trafficAppends int64
		orphanScenarioRows                          int64
		firstErr                                    atomic.Value // stores error
	)
	recordErr := func(err error) {
		if err == nil {
			return
		}
		firstErr.CompareAndSwap(nil, err)
	}

	for round := 0; round < rounds; round++ {
		mockIDs := make([]string, mocksPerRound)
		for i := range mockIDs {
			mockIDs[i] = fmt.Sprintf("reset-mock-%d-%d", round, i)
			m := domain.Mock{
				ID:        mockIDs[i],
				Partition: partition,
				Name:      "reset-race",
				CreatedAt: time.Now(),
				Action:    domain.Action{Kind: domain.ActionRespond, Respond: &domain.RespondAction{Status: 200}},
			}
			if err := st.CreateMock(ctx, m); err != nil {
				t.Fatalf("round %d: CreateMock(%s): %v", round, mockIDs[i], err)
			}
		}

		var stopped int32
		var wg sync.WaitGroup

		launch := func(fn func(attempt int)) {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for attempt := 0; atomic.LoadInt32(&stopped) == 0; attempt++ {
					fn(attempt)
				}
			}()
		}

		for w := 0; w < writersPerOp; w++ {
			w := w
			// Simulates an in-flight request that matched one of this
			// round's scenario mocks moments ago and is now advancing it,
			// oblivious to whether Reset has since deleted that mock —
			// cycling through the round's known mock ids.
			launch(func(attempt int) {
				mockID := mockIDs[attempt%len(mockIDs)]
				if _, err := st.AdvanceEphemeralScenario(ctx, partition, mockID); err != nil {
					if errors.Is(err, domain.ErrNotFound) {
						atomic.AddInt64(&notFoundCalls, 1)
						return
					}
					recordErr(fmt.Errorf("round %d writer %d: AdvanceEphemeralScenario(%s): %w", round, w, mockID, err))
					return
				}
				atomic.AddInt64(&advanceCalls, 1)
			})
			// Races ClearTraffic. Not asserted on (see doc comment above) —
			// only logged in aggregate.
			launch(func(attempt int) {
				err := st.AppendTraffic(ctx, domain.TrafficRecord{
					ID:        fmt.Sprintf("reset-traffic-%d-%d-%d", round, w, attempt),
					Partition: partition,
					Timestamp: time.Now(),
					Method:    "GET",
					Host:      "api.example.com",
					Path:      "/x",
					Status:    200,
				})
				if err != nil {
					recordErr(fmt.Errorf("round %d writer %d: AppendTraffic: %w", round, w, err))
					return
				}
				atomic.AddInt64(&trafficAppends, 1)
			})
		}

		if _, err := uc.Execute(ctx, usecase.ResetInput{Partition: partition, ClearTraffic: true}); err != nil {
			t.Fatalf("round %d: Reset.Execute(): %v", round, err)
		}

		// Stop writers immediately after this round's one-and-only
		// Reset.Execute call returns. An attempt already in flight at this
		// instant may still complete after the signal (a benign straggler,
		// same reasoning as partitions_race_stress_test.go) — but since
		// this round's mock ids are never recreated by anything else in
		// this test, a straggler AdvanceEphemeralScenario call still
		// correctly observes the mock as permanently gone and returns
		// domain.ErrNotFound rather than resurrecting anything.
		atomic.StoreInt32(&stopped, 1)
		wg.Wait()

		if v := firstErr.Load(); v != nil {
			t.Fatalf("round %d: unexpected error during stress run: %v", round, v)
		}

		for _, mockID := range mockIDs {
			if _, err := st.GetMock(ctx, partition, mockID); !errors.Is(err, domain.ErrNotFound) {
				t.Fatalf("round %d: GetMock(%s) after Reset = %v, want domain.ErrNotFound", round, mockID, err)
			}

			var n int
			if err := st.db.QueryRowContext(ctx,
				`SELECT COUNT(*) FROM scenario_state WHERE "partition" = ? AND mock_id = ?`, partition, mockID,
			).Scan(&n); err != nil {
				t.Fatalf("round %d: query scenario_state for %s: %v", round, mockID, err)
			}
			if n != 0 {
				atomic.AddInt64(&orphanScenarioRows, int64(n))
				t.Errorf("round %d: scenario_state has %d orphaned row(s) for mock %q after Reset.Execute — "+
					"Reset deleted this mock and wiped scenario_state for the whole partition, and this id is "+
					"never reused by anything else in this test, so a surviving row is a genuine resurrection bug",
					round, n, mockID)
			}
		}
	}

	t.Logf("rounds=%d advanceCalls=%d notFoundCalls=%d trafficAppends=%d orphanScenarioRows=%d",
		rounds, advanceCalls, notFoundCalls, trafficAppends, orphanScenarioRows)
}
