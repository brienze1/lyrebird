package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/brienze1/lyrebird/internal/domain"
)

// CreatePartition creates or updates a space's registration (FR-023/024).
// Upserting by id is idempotent, matching SetUpstream's natural-key
// convention: re-creating an existing space only refreshes its description,
// never its created_at.
func (s *Store) CreatePartition(ctx context.Context, p domain.Partition) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO partitions (id, created_at, description)
		VALUES (?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET description = excluded.description`,
		p.ID, p.CreatedAt.UnixNano(), p.Description,
	)
	if err != nil {
		return fmt.Errorf("store: create partition: %w", err)
	}
	return nil
}

// GetPartition returns the space registered under id, or domain.ErrNotFound
// if none exists.
func (s *Store) GetPartition(ctx context.Context, id string) (domain.Partition, error) {
	var createdAt int64
	var desc sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT created_at, description FROM partitions WHERE id = ?`, id,
	).Scan(&createdAt, &desc)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Partition{}, fmt.Errorf("store: get partition %q: %w", id, domain.ErrNotFound)
	}
	if err != nil {
		return domain.Partition{}, fmt.Errorf("store: get partition: %w", err)
	}
	return domain.Partition{ID: id, CreatedAt: time.Unix(0, createdAt), Description: desc.String}, nil
}

// ListPartitions returns every registered space, ordered by id for
// deterministic output.
func (s *Store) ListPartitions(ctx context.Context) ([]domain.Partition, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, created_at, description FROM partitions ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("store: list partitions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []domain.Partition
	for rows.Next() {
		var id string
		var createdAt int64
		var desc sql.NullString
		if err := rows.Scan(&id, &createdAt, &desc); err != nil {
			return nil, fmt.Errorf("store: scan partition row: %w", err)
		}
		out = append(out, domain.Partition{ID: id, CreatedAt: time.Unix(0, createdAt), Description: desc.String})
	}
	return out, rows.Err()
}

// DeletePartition cascades id's ephemeral mocks, recorded traffic, upstream
// configuration, and scenario state in one operation (FR-024), then removes
// the partition's own registration row. Callers MUST reject
// domain.DefaultPartitionID before calling — this method does not
// special-case it.
//
// All 5 steps run inside a single transaction (BeginTx/Commit, the same
// convention PruneExpiredEphemeralMocks already established in store.go)
// rather than as 5 separate statements each independently checking out and
// releasing db.SetMaxOpenConns(1)'s sole connection. The previous sequential
// (no-tx) design was deliberately accepted as a trade-off for the crash-
// mid-cascade case (matching Reset's own accepted trade-off — a crash
// leaves at most a partial cleanup, and Principle III's disposability
// discipline makes that fine) but was never stress-tested against a
// different hazard: a concurrent CreateMock/AppendTraffic/SetUpstream/
// AdvanceScenario call targeting this exact partition id, landing in the
// gap between an earlier step (e.g. the ephemeral_mocks delete) and a later
// one (e.g. the final `DELETE FROM partitions`). A stress test
// (partitions_race_stress_test.go) confirmed that gap is real and
// reproducible: such a write permanently resurrects a row for a partition
// id that GetPartition will report as gone, with nothing ever scheduled to
// clean it up again — a genuine registry/data desync, not just an inert
// leaked row. Holding the sole connection for the whole cascade closes this:
// any concurrent writer's own ExecContext simply blocks (waiting for a
// connection from the pool) until this transaction commits, then runs
// cleanly against a partition that is either not-yet-deleted
// (indistinguishable from calling it before DeletePartition started) or
// fully-deleted (recreating a mock/traffic/upstream/scenario row in a
// since-deleted ad hoc space id — explicitly legitimate per
// usecase.DeleteSpace's own doc comment) — never a half-deleted state.
//
// The 4 per-table deletes are inlined here directly against the *sql.Tx
// rather than calling DeleteMocksByPartition/ResetAllScenarios/
// ClearTraffic/DeleteUpstreamsByPartition: those methods run against s.db
// (not a caller-supplied transaction) and are independently used standalone
// elsewhere (internal/usecase/reset.go, via the ScenarioStateRepo/
// MockRepo/TrafficRepo/UpstreamRepo port interfaces) — inlining here keeps
// their existing signatures and standalone behavior completely untouched.
func (s *Store) DeletePartition(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: delete partition: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `DELETE FROM ephemeral_mocks WHERE "partition" = ?`, id); err != nil {
		return fmt.Errorf("store: delete partition: delete mocks: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM scenario_state WHERE "partition" = ?`, id); err != nil {
		return fmt.Errorf("store: delete partition: reset scenarios: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM traffic WHERE "partition" = ?`, id); err != nil {
		return fmt.Errorf("store: delete partition: clear traffic: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM upstreams WHERE "partition" = ?`, id); err != nil {
		return fmt.Errorf("store: delete partition: delete upstreams: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM partitions WHERE id = ?`, id); err != nil {
		return fmt.Errorf("store: delete partition: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: delete partition: commit tx: %w", err)
	}
	return nil
}
