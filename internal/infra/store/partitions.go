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

// DeletePartition cascades id's ephemeral mocks, recorded traffic, and
// upstream configuration in one operation (FR-024), then removes the
// partition's own registration row. Callers MUST reject
// domain.DefaultPartitionID before calling — this method does not
// special-case it. Steps run sequentially without an explicit transaction,
// matching Reset's own accepted trade-off: SQLite's single-connection
// serialization (Open already sets db.SetMaxOpenConns(1)) plus Principle
// III's disposability discipline make that acceptable here too.
func (s *Store) DeletePartition(ctx context.Context, id string) error {
	if err := s.DeleteMocksByPartition(ctx, id); err != nil {
		return err
	}
	if err := s.ClearTraffic(ctx, id); err != nil {
		return err
	}
	if err := s.DeleteUpstreamsByPartition(ctx, id); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM partitions WHERE id = ?`, id); err != nil {
		return fmt.Errorf("store: delete partition: %w", err)
	}
	return nil
}
