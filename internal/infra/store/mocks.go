package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/brienze1/lyrebird/internal/domain"
)

// CreateMock persists a new ephemeral mock. match_blob is plaintext JSON
// (data-model.md: Match fields aren't marked encrypted); action_blob is
// sealed, since RespondAction.Body may carry sensitive fixture data.
// Seeded mocks never reach this method — they live only in memory
// (constitution Principle III). created_at/expires_at are stored in
// nanoseconds, not seconds — FR-009a's tie-break needs enough resolution to
// distinguish two mocks created in quick succession (routine for
// back-to-back Admin REST calls; even millisecond resolution, tried first,
// proved too coarse and let two real HTTP+SQLite round trips collide into
// the same bucket, observed as a flaky BDD scenario).
func (s *Store) CreateMock(ctx context.Context, m domain.Mock) error {
	matchJSON, actionBlob, err := encodeMock(s, m)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO ephemeral_mocks (id, "partition", name, priority, "group", created_at, expires_at, match_blob, action_blob)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.Partition, m.Name, m.Priority, m.Group, m.CreatedAt.UnixNano(), expiresAtColumn(m), matchJSON, actionBlob,
	)
	if err != nil {
		return fmt.Errorf("store: create mock: %w", err)
	}
	return nil
}

// GetMock returns the ephemeral mock (id, partition), or domain.ErrNotFound
// if it doesn't exist or its action_blob fails to decrypt under the active
// at-rest key (FR-029: undecryptable is treated as absent, not corruption).
func (s *Store) GetMock(ctx context.Context, partition, id string) (domain.Mock, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, "partition", name, priority, "group", created_at, expires_at, match_blob, action_blob
		FROM ephemeral_mocks WHERE id = ? AND "partition" = ?`, id, partition)

	m, actionBlob, err := scanMockRow(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Mock{}, domain.ErrNotFound
		}
		return domain.Mock{}, fmt.Errorf("store: get mock: %w", err)
	}
	if err := decodeMockAction(s, &m, actionBlob); err != nil {
		return domain.Mock{}, domain.ErrNotFound
	}
	return m, nil
}

// ListMocks returns every ephemeral mock in partition whose action_blob
// decrypts successfully under the Store's own sealer. A row that fails to
// decrypt is silently skipped, not treated as an error (FR-029).
func (s *Store) ListMocks(ctx context.Context, partition string) ([]domain.Mock, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, "partition", name, priority, "group", created_at, expires_at, match_blob, action_blob
		FROM ephemeral_mocks WHERE "partition" = ?`, partition)
	if err != nil {
		return nil, fmt.Errorf("store: list mocks: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []domain.Mock
	for rows.Next() {
		m, actionBlob, err := scanMockRow(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan mock row: %w", err)
		}
		if err := decodeMockAction(s, &m, actionBlob); err != nil {
			continue
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// UpdateMock overwrites an existing ephemeral mock's mutable fields. Callers
// (usecase.MockCRUD) are responsible for rejecting updates to seeded mocks
// before calling this — the store layer has no notion of seeded mocks at
// all, since they never reach it.
func (s *Store) UpdateMock(ctx context.Context, m domain.Mock) error {
	matchJSON, actionBlob, err := encodeMock(s, m)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE ephemeral_mocks
		SET name = ?, priority = ?, "group" = ?, expires_at = ?, match_blob = ?, action_blob = ?
		WHERE id = ? AND "partition" = ?`,
		m.Name, m.Priority, m.Group, expiresAtColumn(m), matchJSON, actionBlob, m.ID, m.Partition,
	)
	if err != nil {
		return fmt.Errorf("store: update mock: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: update mock rows affected: %w", err)
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// DeleteMock removes one ephemeral mock, returning domain.ErrNotFound if it
// doesn't exist.
func (s *Store) DeleteMock(ctx context.Context, partition, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM ephemeral_mocks WHERE id = ? AND "partition" = ?`, id, partition)
	if err != nil {
		return fmt.Errorf("store: delete mock: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: delete mock rows affected: %w", err)
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// DeleteMocksByPartition removes every ephemeral mock in partition. Called
// when a partition is deleted (FR-024).
func (s *Store) DeleteMocksByPartition(ctx context.Context, partition string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM ephemeral_mocks WHERE "partition" = ?`, partition)
	if err != nil {
		return fmt.Errorf("store: delete mocks by partition: %w", err)
	}
	return nil
}

func expiresAtColumn(m domain.Mock) any {
	if m.TTLSeconds == nil {
		return nil
	}
	return m.CreatedAt.UnixNano() + int64(*m.TTLSeconds)*int64(time.Second)
}

func encodeMock(s *Store, m domain.Mock) (matchJSON, sealedAction []byte, err error) {
	matchJSON, err = json.Marshal(m.Match)
	if err != nil {
		return nil, nil, fmt.Errorf("store: marshal match: %w", err)
	}
	actionJSON, err := json.Marshal(m.Action)
	if err != nil {
		return nil, nil, fmt.Errorf("store: marshal action: %w", err)
	}
	sealedAction, err = s.sealer.Seal(actionJSON)
	if err != nil {
		return nil, nil, fmt.Errorf("store: seal action: %w", err)
	}
	return matchJSON, sealedAction, nil
}

func decodeMockAction(s *Store, m *domain.Mock, actionBlob []byte) error {
	actionJSON, ok := s.decryptOrAbsent(actionBlob, "ephemeral_mocks id="+m.ID)
	if !ok {
		return domain.ErrNotFound
	}
	if err := json.Unmarshal(actionJSON, &m.Action); err != nil {
		s.log.Warn("ephemeral mock action unmarshal failed, treated as absent", "id", m.ID, "err", err)
		return domain.ErrNotFound
	}
	return nil
}

// scanMockRow uses the package's shared rowScanner (traffic.go) —
// satisfied by both *sql.Row (GetMock) and *sql.Rows (ListMocks).
func scanMockRow(row rowScanner) (domain.Mock, []byte, error) {
	var m domain.Mock
	var group sql.NullString
	var expiresAt sql.NullInt64
	var createdAtNanos int64
	var matchJSON, actionBlob []byte

	if err := row.Scan(&m.ID, &m.Partition, &m.Name, &m.Priority, &group, &createdAtNanos, &expiresAt, &matchJSON, &actionBlob); err != nil {
		return domain.Mock{}, nil, err
	}
	m.Group = group.String
	m.Lifetime = domain.LifetimeEphemeral
	m.CreatedAt = time.Unix(0, createdAtNanos).UTC()
	if expiresAt.Valid {
		ttl := int((expiresAt.Int64 - createdAtNanos) / int64(time.Second))
		m.TTLSeconds = &ttl
	}
	if err := json.Unmarshal(matchJSON, &m.Match); err != nil {
		return domain.Mock{}, nil, fmt.Errorf("unmarshal match: %w", err)
	}
	return m, actionBlob, nil
}
