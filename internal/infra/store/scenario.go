package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/brienze1/lyrebird/internal/domain"
)

// ScenarioIndex returns the stored position for (partition, mockID) — 0 if
// no row exists yet (a mock whose scenario has never been advanced). A
// read-only peek: never creates or mutates a row.
func (s *Store) ScenarioIndex(ctx context.Context, partition, mockID string) (int, error) {
	var idx int
	err := s.db.QueryRowContext(ctx,
		`SELECT idx FROM scenario_state WHERE "partition" = ? AND mock_id = ?`, partition, mockID,
	).Scan(&idx)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("store: scenario index: %w", err)
	}
	return idx, nil
}

// AdvanceScenario atomically consumes the current index and returns it (the
// slot this call consumes), persisting the incremented value for the next
// call. A single INSERT ... ON CONFLICT ... RETURNING statement rather than
// a separate SELECT-then-write: two round trips would let two concurrent
// requests hitting the same scenario mock both read the same starting
// index before either writes back, both consuming the same response slot
// instead of consecutive ones. The first call inserts idx=1 and returns
// 1-1=0; a later call updates to old+1 and returns (old+1)-1=old — in both
// cases exactly "the index this call consumes," in one round trip.
func (s *Store) AdvanceScenario(ctx context.Context, partition, mockID string) (int, error) {
	var consumed int
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO scenario_state ("partition", mock_id, idx, updated_at) VALUES (?, ?, 1, ?)
		ON CONFLICT("partition", mock_id) DO UPDATE SET
			idx = scenario_state.idx + 1, updated_at = excluded.updated_at
		RETURNING idx - 1`,
		partition, mockID, time.Now().UnixNano(),
	).Scan(&consumed)
	if err != nil {
		return 0, fmt.Errorf("store: advance scenario: %w", err)
	}
	return consumed, nil
}

// AdvanceEphemeralScenario is AdvanceScenario's TOCTOU-safe sibling, used
// ONLY for ephemeral mocks (domain.LifetimeEphemeral) — never for seeded
// ones. It closes a real race between this call and gc.go's sweep
// (store.PruneExpiredEphemeralMocks): serveMocked calls this using a
// domain.Mock snapshot obtained at match time, with no re-check that the
// mock still exists; if GC's sweep deletes the mock's ephemeral_mocks row
// (and its scenario_state row, atomically, in one transaction — see
// PruneExpiredEphemeralMocks's doc comment) in the gap between match time
// and this call, AdvanceScenario's unconditional upsert would silently
// recreate a scenario_state row for a mock id that no longer exists
// anywhere — and no future GC sweep can ever clean it up, since
// PruneExpiredEphemeralMocks's cleanup query only joins against still-
// present, expired ephemeral_mocks rows. A permanent orphan, once per
// occurrence.
//
// The fix: guard the INSERT itself on the mock still being present in
// ephemeral_mocks, in the same statement, so the check and the write can
// never observe different states of the world. SQLite's single connection
// (db.SetMaxOpenConns(1), set in openAndMigrate) means only one statement or
// transaction can hold the connection at any instant, so this is race-free
// against GC's own transaction the same way AdvanceScenario's single
// RETURNING round trip is race-free against concurrent callers of itself.
// `INSERT ... SELECT ... WHERE EXISTS (...)` produces zero source rows when
// the mock is gone, so nothing is inserted, ON CONFLICT never fires (there's
// no candidate row to conflict), and RETURNING yields nothing — Scan then
// reports sql.ErrNoRows, which is mapped to domain.ErrNotFound.
//
// Seeded mocks (domain.LifetimeSeeded) MUST keep calling AdvanceScenario
// instead: they are never stored in ephemeral_mocks at all (loaded from
// mounted seed config at boot, kept only in memory per constitution
// Principle III), so this method's "exists in ephemeral_mocks" guard would
// incorrectly reject every seeded mock's scenario advance.
func (s *Store) AdvanceEphemeralScenario(ctx context.Context, partition, mockID string) (int, error) {
	var consumed int
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO scenario_state ("partition", mock_id, idx, updated_at)
		SELECT ?, ?, 1, ? WHERE EXISTS (SELECT 1 FROM ephemeral_mocks WHERE "partition" = ? AND id = ?)
		ON CONFLICT("partition", mock_id) DO UPDATE SET
			idx = scenario_state.idx + 1, updated_at = excluded.updated_at
		RETURNING idx - 1`,
		partition, mockID, time.Now().UnixNano(), partition, mockID,
	).Scan(&consumed)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("store: advance ephemeral scenario: mock %q: %w", mockID, domain.ErrNotFound)
	}
	if err != nil {
		return 0, fmt.Errorf("store: advance ephemeral scenario: %w", err)
	}
	return consumed, nil
}

// ResetScenario removes (partition, mockID)'s stored position, restarting
// its sequence from the beginning next time it's consumed. A no-op if no
// row exists.
func (s *Store) ResetScenario(ctx context.Context, partition, mockID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM scenario_state WHERE "partition" = ? AND mock_id = ?`, partition, mockID)
	if err != nil {
		return fmt.Errorf("store: reset scenario: %w", err)
	}
	return nil
}

// ResetAllScenarios removes every stored scenario position in partition —
// called by Reset (FR-028).
func (s *Store) ResetAllScenarios(ctx context.Context, partition string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM scenario_state WHERE "partition" = ?`, partition)
	if err != nil {
		return fmt.Errorf("store: reset all scenarios: %w", err)
	}
	return nil
}
