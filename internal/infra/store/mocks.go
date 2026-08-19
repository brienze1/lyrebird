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
// (data-model.md: Match fields aren't marked encrypted); script_blob and
// action_blob are sealed, since RespondAction.Body/Script source may carry
// sensitive fixture data or logic. Seeded mocks never reach this method —
// they live only in memory (constitution Principle III). created_at/
// expires_at are stored in nanoseconds, not seconds — FR-009a's tie-break
// needs enough resolution to distinguish two mocks created in quick
// succession (routine for back-to-back Admin REST calls; even millisecond
// resolution, tried first, proved too coarse and let two real HTTP+SQLite
// round trips collide into the same bucket, observed as a flaky BDD
// scenario).
func (s *Store) CreateMock(ctx context.Context, m domain.Mock) error {
	b, err := encodeMock(s, m)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO ephemeral_mocks (id, "partition", name, priority, "group", created_at, expires_at, match_blob, script_blob, scenario_blob, action_blob, projection_blob, from_capture)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.Partition, m.Name, m.Priority, m.Group, m.CreatedAt.UnixNano(), expiresAtColumn(m),
		b.match, b.script, b.scenario, b.action, b.projection, m.FromCapture,
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
		SELECT id, "partition", name, priority, "group", created_at, expires_at, match_blob, script_blob, scenario_blob, action_blob, projection_blob, from_capture
		FROM ephemeral_mocks WHERE id = ? AND "partition" = ?`, id, partition)

	m, b, err := scanMockRow(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Mock{}, domain.ErrNotFound
		}
		return domain.Mock{}, fmt.Errorf("store: get mock: %w", err)
	}
	if err := decodeMockAction(s, &m, b.action); err != nil {
		return domain.Mock{}, domain.ErrNotFound
	}
	decodeMockScript(s, &m, b.script)
	decodeMockScenario(s, &m, b.scenario)
	decodeMockProjection(s, &m, b.projection)
	return m, nil
}

// ListMocks returns every ephemeral mock in partition whose action_blob
// decrypts successfully under the Store's own sealer. A row that fails to
// decrypt is silently skipped, not treated as an error (FR-029).
func (s *Store) ListMocks(ctx context.Context, partition string) ([]domain.Mock, error) {
	rows, err := s.readDB.QueryContext(ctx, `
		SELECT id, "partition", name, priority, "group", created_at, expires_at, match_blob, script_blob, scenario_blob, action_blob, projection_blob, from_capture
		FROM ephemeral_mocks WHERE "partition" = ?`, partition)
	if err != nil {
		return nil, fmt.Errorf("store: list mocks: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []domain.Mock
	for rows.Next() {
		m, b, err := scanMockRow(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan mock row: %w", err)
		}
		if err := decodeMockAction(s, &m, b.action); err != nil {
			continue
		}
		decodeMockScript(s, &m, b.script)
		decodeMockScenario(s, &m, b.scenario)
		decodeMockProjection(s, &m, b.projection)
		out = append(out, m)
	}
	return out, rows.Err()
}

// UpdateMock overwrites an existing ephemeral mock's mutable fields. Callers
// (usecase.MockCRUD) are responsible for rejecting updates to seeded mocks
// before calling this — the store layer has no notion of seeded mocks at
// all, since they never reach it.
func (s *Store) UpdateMock(ctx context.Context, m domain.Mock) error {
	b, err := encodeMock(s, m)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE ephemeral_mocks
		SET name = ?, priority = ?, "group" = ?, expires_at = ?, match_blob = ?, script_blob = ?, scenario_blob = ?, action_blob = ?, projection_blob = ?, from_capture = ?
		WHERE id = ? AND "partition" = ?`,
		m.Name, m.Priority, m.Group, expiresAtColumn(m),
		b.match, b.script, b.scenario, b.action, b.projection, m.FromCapture, m.ID, m.Partition,
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

// mockBlobs is one ephemeral mock's serialized columns. It exists as a
// struct rather than a run of positional return values because there are now
// five of them: a five-tuple of []byte is trivially mis-ordered at a call
// site and the compiler cannot catch it, since every element has the same
// type.
type mockBlobs struct {
	match      []byte // plaintext JSON — data-model.md does not mark Match encrypted
	script     []byte // sealed, nil when the mock has no Script
	scenario   []byte // sealed, nil when the mock has no Scenario
	action     []byte // sealed
	projection []byte // sealed, nil when the mock has no byte-stream projection override
}

// encodeMock serializes m's blob columns. match_blob stays plaintext (its
// fields aren't marked encrypted); script, scenario, action and projection
// are sealed, since respond bodies, script source and field layouts may all
// carry sensitive fixture data.
func encodeMock(s *Store, m domain.Mock) (mockBlobs, error) {
	var b mockBlobs
	var err error

	if b.match, err = json.Marshal(m.Match); err != nil {
		return mockBlobs{}, fmt.Errorf("store: marshal match: %w", err)
	}
	if b.action, err = sealJSON(s, m.Action); err != nil {
		return mockBlobs{}, fmt.Errorf("store: action: %w", err)
	}
	if m.Script != nil {
		if b.script, err = sealJSON(s, m.Script); err != nil {
			return mockBlobs{}, fmt.Errorf("store: script: %w", err)
		}
	}
	if m.Scenario != nil {
		if b.scenario, err = sealJSON(s, m.Scenario); err != nil {
			return mockBlobs{}, fmt.Errorf("store: scenario: %w", err)
		}
	}
	if m.Projection != nil {
		if b.projection, err = sealJSON(s, m.Projection); err != nil {
			return mockBlobs{}, fmt.Errorf("store: projection: %w", err)
		}
	}
	return b, nil
}

// sealJSON marshals v and seals the result under the store's at-rest key.
func sealJSON(s *Store, v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}
	sealed, err := s.sealer.Seal(raw)
	if err != nil {
		return nil, fmt.Errorf("seal: %w", err)
	}
	return sealed, nil
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

// decodeMockScript degrades gracefully to a nil Script on any failure
// (empty blob, wrong key, corrupt JSON) — unlike decodeMockAction, a script
// decode failure does NOT make the whole mock "not found": Action alone
// still fully describes the mock's behavior without its optional script.
func decodeMockScript(s *Store, m *domain.Mock, scriptBlob []byte) {
	scriptJSON, ok := s.decryptOrAbsent(scriptBlob, "ephemeral_mocks id="+m.ID+" script")
	if !ok {
		m.Script = nil
		return
	}
	var scr domain.Script
	if err := json.Unmarshal(scriptJSON, &scr); err != nil {
		s.log.Warn("ephemeral mock script unmarshal failed, treated as absent", "id", m.ID, "err", err)
		m.Script = nil
		return
	}
	m.Script = &scr
}

// decodeMockScenario mirrors decodeMockScript's graceful-degrade-to-nil
// behavior — a scenario decode failure does NOT make the whole mock "not
// found": Action alone still fully describes the mock's non-scenario
// behavior without it.
func decodeMockScenario(s *Store, m *domain.Mock, scenarioBlob []byte) {
	scenarioJSON, ok := s.decryptOrAbsent(scenarioBlob, "ephemeral_mocks id="+m.ID+" scenario")
	if !ok {
		m.Scenario = nil
		return
	}
	var sc domain.Scenario
	if err := json.Unmarshal(scenarioJSON, &sc); err != nil {
		s.log.Warn("ephemeral mock scenario unmarshal failed, treated as absent", "id", m.ID, "err", err)
		m.Scenario = nil
		return
	}
	m.Scenario = &sc
}

// decodeMockProjection mirrors decodeMockScript's graceful-degrade-to-nil
// behavior. A mock whose byte-stream projection override cannot be read
// falls back to its endpoint's default projection, which is strictly better
// than making the whole rule disappear — and every mock on the other two
// planes has no projection at all, so nil is the overwhelmingly common case.
func decodeMockProjection(s *Store, m *domain.Mock, projectionBlob []byte) {
	projectionJSON, ok := s.decryptOrAbsent(projectionBlob, "ephemeral_mocks id="+m.ID+" projection")
	if !ok {
		m.Projection = nil
		return
	}
	var p domain.Projection
	if err := json.Unmarshal(projectionJSON, &p); err != nil {
		s.log.Warn("ephemeral mock projection unmarshal failed, treated as absent", "id", m.ID, "err", err)
		m.Projection = nil
		return
	}
	m.Projection = &p
}

// scanMockRow uses the package's shared rowScanner (traffic.go) —
// satisfied by both *sql.Row (GetMock) and *sql.Rows (ListMocks).
func scanMockRow(row rowScanner) (domain.Mock, mockBlobs, error) {
	var m domain.Mock
	var b mockBlobs
	var group sql.NullString
	var expiresAt sql.NullInt64
	var createdAtNanos int64

	if err := row.Scan(
		&m.ID, &m.Partition, &m.Name, &m.Priority, &group, &createdAtNanos, &expiresAt,
		&b.match, &b.script, &b.scenario, &b.action, &b.projection, &m.FromCapture,
	); err != nil {
		return domain.Mock{}, mockBlobs{}, err
	}
	m.Group = group.String
	m.Lifetime = domain.LifetimeEphemeral
	m.CreatedAt = time.Unix(0, createdAtNanos).UTC()
	if expiresAt.Valid {
		ttl := int((expiresAt.Int64 - createdAtNanos) / int64(time.Second))
		m.TTLSeconds = &ttl
	}
	if err := json.Unmarshal(b.match, &m.Match); err != nil {
		return domain.Mock{}, mockBlobs{}, fmt.Errorf("unmarshal match: %w", err)
	}
	return m, b, nil
}
