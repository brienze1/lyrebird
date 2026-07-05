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
)

// TestDeletePartitionAgainstConcurrentWritesStress is a stress test for a
// logical race between DeletePartition's own 5-step cascade (see
// partitions.go) and a concurrent CreateMock/AppendTraffic/SetUpstream/
// AdvanceScenario call targeting the EXACT SAME partition id, mid-deletion.
//
// Unlike gc_scenario_race_stress_test.go's design (one long-lived race
// hammered for a fixed wall-clock duration against a single fixed id), this
// test deliberately runs many independent, single-shot rounds, each against
// a FRESH partition id that is registered once, raced against exactly once,
// and never touched again afterward. This structure matters: partitions.go's
// per-table deletes (e.g. `DELETE FROM ephemeral_mocks WHERE "partition" =
// ?`) are blanket, unconditional deletes, not row-specific ones — so a write
// that lands mid-cascade during one DeletePartition call would actually be
// self-healed by any LATER DeletePartition call for that same id. In real
// production usage, DeletePartition is called exactly once per id (a space
// is deleted once, not repeatedly), so the only race that can produce a
// PERMANENT orphan is one that survives past the one-and-only cascade call
// for that id. Racing many fresh, single-use ids — rather than one id hit
// by a long-running loop — reproduces that exact single-call shape directly,
// with no ambiguity about whether a later cascade call happened to clean up
// after the fact.
//
// Race A (mocks/traffic/upstreams): a writer's INSERT lands after
// DeleteMocksByPartition/ClearTraffic/DeleteUpstreamsByPartition's own step
// has already run for that table, but before DeletePartition's final `DELETE
// FROM partitions` commits — resurrecting a row for a partition that
// GetPartition will report as gone, with nothing ever scheduled to clean it
// up again.
//
// Race B (scenario_state): the same shape against ResetAllScenarios's step
// and a concurrent AdvanceScenario call — AdvanceScenario's INSERT ... ON
// CONFLICT ... RETURNING is an unconditional upsert with no existence guard
// (unlike AdvanceEphemeralScenario's mock-existence guard, which guards
// against a DIFFERENT race — GC pruning — not this one), so it can recreate
// a scenario_state row after ResetAllScenarios already ran.
func TestDeletePartitionAgainstConcurrentWritesStress(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	const rounds = 300
	const writersPerOp = 2

	var (
		racedRounds                                           int64
		orphanMockRows, orphanTrafficRows, orphanUpstreamRows int64
		orphanScenarioRows                                    int64
		firstErr                                              atomic.Value // stores error
	)
	recordErr := func(err error) {
		if err == nil {
			return
		}
		firstErr.CompareAndSwap(nil, err)
	}

	for round := 0; round < rounds; round++ {
		partitionID := fmt.Sprintf("racepartition-%d", round)
		if err := st.CreatePartition(ctx, domain.Partition{ID: partitionID, CreatedAt: time.Now()}); err != nil {
			t.Fatalf("round %d: CreatePartition(): %v", round, err)
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
			launch(func(attempt int) {
				err := st.CreateMock(ctx, domain.Mock{
					ID:        fmt.Sprintf("m-%d-%d-%d", round, w, attempt),
					Partition: partitionID,
					Name:      "race",
					CreatedAt: time.Now(),
					Action:    domain.Action{Kind: domain.ActionRespond, Respond: &domain.RespondAction{Status: 200}},
				})
				recordErr(err)
			})
			launch(func(attempt int) {
				err := st.AppendTraffic(ctx, domain.TrafficRecord{
					ID:        fmt.Sprintf("t-%d-%d-%d", round, w, attempt),
					Partition: partitionID,
					Timestamp: time.Now(),
					Method:    "GET",
					Host:      "api.example.com",
					Path:      "/x",
					Status:    200,
				})
				recordErr(err)
			})
			launch(func(attempt int) {
				// A unique match_host per attempt, not a fixed one: SetUpstream
				// is an idempotent upsert keyed on (partition, match_host), so
				// reusing one fixed host across attempts would let a single
				// benign post-cascade straggler collapse onto the same row an
				// earlier, genuinely mid-cascade write also targeted — masking
				// the count-based signal this test relies on to tell "many
				// writes landed in the cascade's gap" apart from "one write
				// happened to be queued on the connection when the
				// transaction committed and ran right after."
				err := st.SetUpstream(ctx, domain.Upstream{
					Partition: partitionID,
					MatchHost: fmt.Sprintf("host-%d-%d-%d.example.com", round, w, attempt),
					TargetURL: "https://api.example.com",
				})
				recordErr(err)
			})
			launch(func(attempt int) {
				// Same reasoning as SetUpstream above: a unique mock_id per
				// attempt, since AdvanceScenario upserts on (partition, mock_id).
				_, err := st.AdvanceScenario(ctx, partitionID, fmt.Sprintf("scenario-mock-%d-%d-%d", round, w, attempt))
				recordErr(err)
			})
		}

		if err := st.DeletePartition(ctx, partitionID); err != nil {
			t.Fatalf("round %d: DeletePartition(): %v", round, err)
		}
		// Signal writers to stop immediately after the one-and-only
		// DeletePartition call for this round's id returns. Any writer
		// attempt already in flight at this instant may still complete
		// after the signal.
		//
		// IMPORTANT: this overshoot is NOT itself a bug, even with a fully
		// correct (transactional) DeletePartition. Each writer goroutine
		// loops tightly with at most one outstanding ExecContext call at a
		// time; if that call happened to be blocked waiting for
		// db.SetMaxOpenConns(1)'s sole connection (held by DeletePartition's
		// transaction) at the moment of commit, it will unblock and succeed
		// right after — which is exactly "recreating a row in a since-
		// deleted ad hoc space id after the deletion has fully completed,"
		// explicitly legitimate per usecase.DeleteSpace's own doc comment.
		// With `writersPerOp` goroutines per operation type, AT MOST
		// `writersPerOp` such legitimate stragglers can land per table per
		// round. The real bug (the non-transactional cascade) instead
		// exposes 4 separate gaps — one between each of its 5 sequential,
		// independently-checked-out-and-released statements — giving
		// writers many more repeated opportunities across the whole
		// cascade's duration to land a surviving write, not just one
		// release-triggered opportunity at the very end. So the two cases
		// are told apart by MAGNITUDE (a handful of stragglers per round vs.
		// many), not by "zero orphans" — see the threshold below, which is
		// calibrated with a generous margin above the legitimate-straggler
		// baseline and was empirically confirmed to cleanly separate the
		// fixed and unfixed implementations (see this file's revert-proof
		// run in the refactor report).
		atomic.StoreInt32(&stopped, 1)
		wg.Wait()

		if v := firstErr.Load(); v != nil {
			t.Fatalf("round %d: unexpected writer error during stress run: %v", round, v)
		}

		// This round's partition id is never touched again after this
		// point — no future DeletePartition call will ever run for it, in
		// this test or in real production usage (a space is deleted once).
		if _, err := st.GetPartition(ctx, partitionID); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("round %d: GetPartition() = %v, want ErrNotFound", round, err)
		}

		count := func(table string) int64 {
			var n int64
			q := fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE "partition" = ?`, table)
			if err := st.db.QueryRowContext(ctx, q, partitionID).Scan(&n); err != nil {
				t.Fatalf("round %d: count %s: %v", round, table, err)
			}
			return n
		}

		nMocks := count("ephemeral_mocks")
		nTraffic := count("traffic")
		nUpstreams := count("upstreams")
		nScenarios := count("scenario_state")

		if nMocks+nTraffic+nUpstreams+nScenarios > 0 {
			atomic.AddInt64(&racedRounds, 1)
			atomic.AddInt64(&orphanMockRows, nMocks)
			atomic.AddInt64(&orphanTrafficRows, nTraffic)
			atomic.AddInt64(&orphanUpstreamRows, nUpstreams)
			atomic.AddInt64(&orphanScenarioRows, nScenarios)
		}
	}

	t.Logf("rounds=%d racedRounds=%d orphanMockRows=%d orphanTrafficRows=%d orphanUpstreamRows=%d orphanScenarioRows=%d",
		rounds, racedRounds, orphanMockRows, orphanTrafficRows, orphanUpstreamRows, orphanScenarioRows)

	// Threshold: a generous 4x the theoretical legitimate-straggler ceiling
	// per table (writersPerOp stragglers per round, one per goroutine that
	// could be blocked on the sole connection at the instant DeletePartition
	// commits). Empirically, the transactional (fixed) implementation lands
	// close to the 1x baseline (~writersPerOp per round per table); the
	// non-transactional (buggy) implementation blows past this 4x threshold
	// by a wide margin on the non-upsert tables (ephemeral_mocks, traffic),
	// which is where this test's per-attempt-unique keys make the true
	// mid-cascade race directly visible as accumulating extra rows rather
	// than being masked by an upsert collapsing many racing writes into one
	// surviving row.
	perTableThreshold := int64(writersPerOp) * int64(rounds) * 4
	checkTable := func(name string, n int64) {
		if n > perTableThreshold {
			t.Errorf("%s: %d orphaned row(s) across %d rounds exceeds the generous legitimate-straggler threshold "+
				"of %d (writersPerOp=%d) — DeletePartition is leaving far more permanent orphans than a handful of "+
				"benign post-commit stragglers can explain, consistent with a real race in its own multi-step "+
				"cascade rather than expected ad-hoc-space-reuse noise",
				name, n, rounds, perTableThreshold, writersPerOp)
		}
	}
	checkTable("ephemeral_mocks", orphanMockRows)
	checkTable("traffic", orphanTrafficRows)
	checkTable("upstreams", orphanUpstreamRows)
	checkTable("scenario_state", orphanScenarioRows)
}
