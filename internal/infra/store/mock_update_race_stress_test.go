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
	"github.com/brienze1/lyrebird/internal/infra/clock"
	"github.com/brienze1/lyrebird/internal/infra/idgen"
	"github.com/brienze1/lyrebird/internal/usecase"
)

// permissiveMatchEval is a minimal usecase.MatchEval stub for this test:
// every Match is considered well-formed (ValidateMatch never rejects it) and
// Matches/its per-condition detail are never exercised by
// usecase.MockCRUD.Update's own code path, so they're unreachable here.
type permissiveMatchEval struct{}

func (permissiveMatchEval) Matches(domain.Match, usecase.MatchInput) (bool, []usecase.ConditionResult) {
	return true, nil
}
func (permissiveMatchEval) ValidateMatch(domain.Match) error { return nil }

// permissiveScriptEval is a minimal usecase.ScriptEval stub — this test never
// sets MockInput.Script, so only ValidateScript's "" (no-op) path is ever
// reached; the rest are unreachable and only exist to satisfy the interface.
type permissiveScriptEval struct{}

func (permissiveScriptEval) ValidateScript(string) error { return nil }
func (permissiveScriptEval) EvalMatch(string, usecase.MatchInput) (bool, error) {
	return true, nil
}
func (permissiveScriptEval) EvalRespond(string, usecase.MatchInput) ([]byte, error) {
	return nil, nil
}
func (permissiveScriptEval) EvalRewriteRequest(string, usecase.MatchInput) (usecase.RewrittenRequest, error) {
	return usecase.RewrittenRequest{}, nil
}
func (permissiveScriptEval) EvalTransformResponse(string, usecase.TransformInput) (usecase.TransformedResponse, error) {
	return usecase.TransformedResponse{}, nil
}

// noSeeds is a usecase.SeededMockSource with no seeded mocks — this test only
// races ephemeral mocks against each other.
type noSeeds struct{}

func (noSeeds) SeededMocks(string) []domain.Mock { return nil }

// TestMockCRUDUpdateAgainstConcurrentUpdatesStress stress-tests
// usecase.MockCRUD.Update (internal/usecase/mock_crud.go) racing itself: many
// goroutines calling Update for the SAME mock id, each with a distinct,
// internally self-consistent payload, hunting for a classic lost-update /
// torn-write bug — one goroutine's Name landing next to another goroutine's
// Body in the final stored row.
//
// Tracing Update's own code (mock_crud.go): it does read the current row
// first (GetMock, to preserve the immutable ID/Partition/CreatedAt and to
// distinguish "not found" from "seeded"), but it does NOT merge any mutable
// field from that read into the row it writes back — every mutable field
// (Name/TTLSeconds/Priority/Group/Match/Script/Action/Scenario) comes
// entirely from the caller's MockInput, and store.UpdateMock (mocks.go) then
// issues a single unconditional `UPDATE ... SET name=?, priority=?, ... WHERE
// id=? AND "partition"=?` statement. So the GetMock-then-UpdateMock gap can
// only ever affect the 3 immutable fields it reads (which don't change across
// concurrent Updates to the same still-existing row) or turn into a clean
// domain.ErrNotFound if the row is deleted in between (RowsAffected==0) —
// never a partial merge of two different callers' payloads. Combined with
// SQLite's single connection (db.SetMaxOpenConns(1)) serializing every
// individual UPDATE statement to completion, this reasons out as safe by
// construction rather than a classic read-modify-write race. This test turns
// that reasoning into an empirical, permanent regression check rather than
// resting on code inspection alone (mirroring pass 16's
// reset_race_stress_test.go, which did the same for usecase.Reset).
//
// Each round targets a fresh, round-unique mock id (not one long-lived id
// reused across rounds): Update's write is unconditional, so if a later
// round's winning payload happened to collide in shape with an earlier
// round's, reusing ids could mask a genuine cross-goroutine mix-up. A fresh
// id per round, asserted on immediately after that round's writers stop,
// removes that ambiguity.
func TestMockCRUDUpdateAgainstConcurrentUpdatesStress(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	uc := usecase.NewMockCRUD(st, noSeeds{}, permissiveMatchEval{}, permissiveScriptEval{}, idgen.UUID{}, clock.System{}, st)

	const partition = "default"
	const rounds = 300
	const writers = 4

	var (
		updateCalls, notFoundCalls int64
		inconsistentRows           int64
		firstErr                   atomic.Value // stores error
	)
	recordErr := func(err error) {
		if err == nil {
			return
		}
		firstErr.CompareAndSwap(nil, err)
	}

	for round := 0; round < rounds; round++ {
		mockID := fmt.Sprintf("update-race-mock-%d", round)
		seed := domain.Mock{
			ID:        mockID,
			Partition: partition,
			Name:      "seed",
			CreatedAt: time.Now(),
			Action:    domain.Action{Kind: domain.ActionRespond, Respond: &domain.RespondAction{Status: 200}},
		}
		if err := st.CreateMock(ctx, seed); err != nil {
			t.Fatalf("round %d: CreateMock(%s): %v", round, mockID, err)
		}

		var stopped int32
		var wg sync.WaitGroup

		for w := 0; w < writers; w++ {
			w := w
			wg.Add(1)
			go func() {
				defer wg.Done()
				for attempt := 0; atomic.LoadInt32(&stopped) == 0; attempt++ {
					// tag is embedded identically in Name, Group, and the
					// respond body — Update must write all mutable fields
					// from the SAME MockInput in one shot, so after the race
					// settles, the final row's Name/Group/Body tags must all
					// agree with each other (whichever generation actually
					// won last). Any disagreement between them can only mean
					// two different concurrent Update calls' fields got
					// interleaved into the same row — a genuine torn write.
					tag := fmt.Sprintf("gen-%d-%d-%d", round, w, attempt)
					in := usecase.MockInput{
						Partition: partition,
						Name:      tag,
						Priority:  w*1_000_000 + attempt,
						Group:     tag,
						Match:     domain.Match{Method: "GET", Path: "/" + tag},
						Action: domain.Action{Kind: domain.ActionRespond, Respond: &domain.RespondAction{
							Status: 200, Body: []byte(tag),
						}},
					}
					_, err := uc.Update(ctx, partition, mockID, in)
					if err != nil {
						if errors.Is(err, domain.ErrNotFound) {
							atomic.AddInt64(&notFoundCalls, 1)
							continue
						}
						recordErr(fmt.Errorf("round %d writer %d attempt %d: Update: %w", round, w, attempt, err))
						return
					}
					atomic.AddInt64(&updateCalls, 1)
				}
			}()
		}

		// Let the writers actually contend for a little while before
		// stopping them — a single immediate stop could let all of them
		// finish before ever overlapping.
		time.Sleep(2 * time.Millisecond)
		atomic.StoreInt32(&stopped, 1)
		wg.Wait()

		if v := firstErr.Load(); v != nil {
			t.Fatalf("round %d: unexpected error during stress run: %v", round, v)
		}

		final, err := st.GetMock(ctx, partition, mockID)
		if err != nil {
			t.Fatalf("round %d: GetMock(%s) after race: %v", round, mockID, err)
		}
		bodyTag := string(final.Action.Respond.Body)
		pathTag := final.Match.Path
		if final.Name != final.Group || final.Name != bodyTag || "/"+final.Name != pathTag {
			atomic.AddInt64(&inconsistentRows, 1)
			t.Errorf("round %d: mock %q has inconsistent fields after concurrent Update race — "+
				"Name=%q Group=%q Match.Path=%q RespondAction.Body=%q; all four must carry the SAME "+
				"generation tag (each concurrent Update call wrote all of them from one MockInput), so "+
				"any mismatch means two different calls' fields got torn/merged into one row",
				round, mockID, final.Name, final.Group, pathTag, bodyTag)
		}

		var n int
		if err := st.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM ephemeral_mocks WHERE id = ? AND "partition" = ?`, mockID, partition,
		).Scan(&n); err != nil {
			t.Fatalf("round %d: query ephemeral_mocks for %s: %v", round, mockID, err)
		}
		if n != 1 {
			t.Fatalf("round %d: ephemeral_mocks has %d row(s) for mock %q after race, want exactly 1", round, n, mockID)
		}
	}

	t.Logf("rounds=%d updateCalls=%d notFoundCalls=%d inconsistentRows=%d",
		rounds, updateCalls, notFoundCalls, inconsistentRows)
}
