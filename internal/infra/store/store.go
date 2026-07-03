// Package store owns Lyrebird's disposable SQLite persistence: the spy
// traffic log and ephemeral mocks. State here is disposable by design
// (constitution Principle III) — losing it on restart must be acceptable
// and must never be treated as corruption (FR-029).
package store

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver

	"github.com/brienze1/lyrebird/internal/infra/crypto"
)

//go:embed schema.sql
var schemaSQL string

// Store owns the SQLite connection and the sealer used to encrypt/decrypt
// sensitive blob columns at the storage boundary.
type Store struct {
	db     *sql.DB
	sealer crypto.Sealer
	log    *slog.Logger
}

// Open opens (or creates) the SQLite database at path and applies the
// schema. It never fails on a missing, empty, or genuinely corrupt file
// (FR-029): if migration fails against an existing file — for any reason,
// including a transient infra blip — that file is unconditionally
// quarantined (renamed aside, never deleted) and a fresh database is started
// in its place. Open does NOT distinguish "genuinely corrupt" from
// "temporarily unreadable"; it deliberately treats every migrate failure the
// same way, because Principle III already makes losing this state acceptable
// — the alternative (trying to classify failures) would add complexity this
// disposable, restart-tolerant store doesn't need. An error is returned only
// when even the fresh, quarantined-aside attempt fails (e.g. the quarantine
// rename itself failing, typically a permissions/disk problem) — that is a
// genuine infrastructure failure, not a disposability case.
func Open(ctx context.Context, path string, sealer crypto.Sealer, log *slog.Logger) (*Store, error) {
	if log == nil {
		log = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}

	// A fully ephemeral deployment (no mounted volume) still needs its
	// parent directory to exist before SQLite can create the file. This is
	// a one-time infra step, not a disposability case: if it fails (e.g.
	// permissions), that's a genuine error, not something to quarantine.
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("store: create db directory %s: %w", dir, err)
		}
	}

	db, err := openAndMigrate(ctx, path)
	if err != nil {
		log.Warn("existing database file failed to open, quarantining and starting fresh",
			"path", path, "err", err)

		quarantinePath, qErr := quarantine(path)
		if qErr != nil {
			return nil, fmt.Errorf("store: cannot quarantine unreadable db %s: %w", path, qErr)
		}
		log.Warn("quarantined unreadable database file", "from", path, "to", quarantinePath)

		db, err = openAndMigrate(ctx, path)
		if err != nil {
			// A fresh file failing migration is a genuine infra problem
			// (disk full, permissions), not a disposability case.
			return nil, fmt.Errorf("store: migrate fresh database: %w", err)
		}
	}

	return &Store{db: db, sealer: sealer, log: log}, nil
}

// openAndMigrate opens path and applies the schema. CREATE TABLE IF NOT
// EXISTS is idempotent, so this already succeeds unconditionally against a
// missing, empty, or well-formed-but-incomplete file; it only fails against
// a file that isn't a valid SQLite database at all.
func openAndMigrate(ctx context.Context, path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	// SQLite is single-writer; serialize all access through one connection
	// so concurrent GC + request-handling writes don't race on SQLITE_BUSY.
	db.SetMaxOpenConns(1)

	if _, err := db.ExecContext(ctx, "PRAGMA busy_timeout=5000;"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("pragma busy_timeout: %w", err)
	}
	if _, err := db.ExecContext(ctx, "PRAGMA journal_mode=WAL;"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("pragma journal_mode: %w", err)
	}
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return db, nil
}

// quarantine renames path (and any WAL/SHM sidecar files) aside — never
// deletes them, so a human can inspect them later — and returns the main
// file's new path. If path does not exist, quarantine is a no-op.
func quarantine(path string) (string, error) {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	suffix := fmt.Sprintf(".corrupt-%d", time.Now().UnixNano())

	for _, sidecar := range []string{path + "-wal", path + "-shm"} {
		if _, err := os.Stat(sidecar); err == nil {
			if err := os.Rename(sidecar, sidecar+suffix); err != nil {
				return "", fmt.Errorf("rename sidecar %s: %w", sidecar, err)
			}
		}
	}

	dest := path + suffix
	if err := os.Rename(path, dest); err != nil {
		return "", fmt.Errorf("rename %s to %s: %w", path, dest, err)
	}
	return dest, nil
}

// Close releases the underlying database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// InsertRawEphemeralMock is a low-level fixture helper for tests: it seals
// actionJSON with the given sealer (which may differ from the Store's own,
// to simulate data written under a previous at-rest key) and inserts a row
// directly. Production code never calls this — it exists solely so BDD
// scenarios can fabricate pre-existing on-disk state without a MockRepo,
// which doesn't land until M1.
func (s *Store) InsertRawEphemeralMock(ctx context.Context, sealer crypto.Sealer, partition, id, name string, actionJSON []byte) error {
	sealed, err := sealer.Seal(actionJSON)
	if err != nil {
		return fmt.Errorf("store: seal fixture row: %w", err)
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO ephemeral_mocks (id, "partition", name, priority, created_at, match_blob, action_blob)
		 VALUES (?, ?, ?, 0, ?, ?, ?)`,
		id, partition, name, time.Now().Unix(), []byte("{}"), sealed,
	)
	if err != nil {
		return fmt.Errorf("store: insert fixture row: %w", err)
	}
	return nil
}

// decryptOrAbsent is the single place that opens a sealed blob read back
// from storage. Every row-reading method routes through it. An AEAD failure
// — wrong key or a corrupt blob — is logged at Debug and the row is treated
// as absent; it is never propagated as a query error (FR-029, constitution
// Principle III). Any other, non-AEAD error is treated the same way and
// logged at Warn instead, so a read path can never crash the caller over a
// single bad row.
func (s *Store) decryptOrAbsent(blob []byte, rowDesc string) ([]byte, bool) {
	if blob == nil {
		return nil, false
	}
	pt, err := s.sealer.Open(blob)
	if err == nil {
		return pt, true
	}
	if errors.Is(err, crypto.ErrAuthFailed) {
		s.log.Debug("row undecryptable under active key, treated as absent", "row", rowDesc)
	} else {
		s.log.Warn("unexpected decrypt error, treated as absent", "row", rowDesc, "err", err)
	}
	return nil, false
}

// ListEphemeralMockIDs returns the ids of ephemeral mocks in partition whose
// action_blob decrypts successfully under the Store's own sealer. A row that
// fails to decrypt (wrong key, corrupt blob) is silently skipped, not
// treated as an error (FR-029).
func (s *Store) ListEphemeralMockIDs(ctx context.Context, partition string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, action_blob FROM ephemeral_mocks WHERE "partition" = ?`, partition)
	if err != nil {
		return nil, fmt.Errorf("store: list ephemeral mocks: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var ids []string
	for rows.Next() {
		var id string
		var blob []byte
		if err := rows.Scan(&id, &blob); err != nil {
			return nil, fmt.Errorf("store: scan ephemeral mock row: %w", err)
		}
		if _, ok := s.decryptOrAbsent(blob, "ephemeral_mocks id="+id); !ok {
			continue
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// PruneTraffic deletes traffic rows older than olderThan, returning the
// number removed. Used by the GC loop to bound storage (FR-027).
func (s *Store) PruneTraffic(ctx context.Context, olderThan time.Time) (int, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM traffic WHERE "timestamp" < ?`, olderThan.UnixMilli())
	if err != nil {
		return 0, fmt.Errorf("store: prune traffic: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: prune traffic rows affected: %w", err)
	}
	return int(n), nil
}

// PruneExpiredEphemeralMocks deletes ephemeral mocks whose TTL has elapsed as
// of now, returning the number removed. Seeded mocks are never stored in
// this table, so they are never touched.
func (s *Store) PruneExpiredEphemeralMocks(ctx context.Context, now time.Time) (int, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM ephemeral_mocks WHERE expires_at IS NOT NULL AND expires_at < ?`, now.Unix())
	if err != nil {
		return 0, fmt.Errorf("store: prune expired ephemeral mocks: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: prune expired ephemeral mocks rows affected: %w", err)
	}
	return int(n), nil
}
