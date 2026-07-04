package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
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
