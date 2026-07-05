package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/brienze1/lyrebird/internal/domain"
	"github.com/brienze1/lyrebird/internal/usecase"
)

// AppendTraffic persists one recorded interaction (FR-002). Request/Response
// are opaque, already-encoded blobs (see usecase.RecordedMessage) — the
// store only encrypts and stores them, it does not interpret their content.
func (s *Store) AppendTraffic(ctx context.Context, t domain.TrafficRecord) error {
	reqBlob, err := s.sealer.Seal(t.Request)
	if err != nil {
		return fmt.Errorf("store: seal traffic request: %w", err)
	}
	respBlob, err := s.sealer.Seal(t.Response)
	if err != nil {
		return fmt.Errorf("store: seal traffic response: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO traffic (id, "partition", "timestamp", method, host, path, status, latency_ms,
		                      decision, matched_mock_id, request_blob, response_blob)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Partition, t.Timestamp.UnixMilli(), t.Method, t.Host, t.Path, t.Status, t.LatencyMS,
		string(t.Decision), t.MatchedMockID, reqBlob, respBlob,
	)
	if err != nil {
		return fmt.Errorf("store: append traffic: %w", err)
	}
	return nil
}

// GetTraffic returns one traffic record by id. A row whose request or
// response blob fails to decrypt under the active key is treated as absent
// (domain.ErrNotFound), never as an error — FR-029, same discipline as
// decryptOrAbsent everywhere else in this package.
func (s *Store) GetTraffic(ctx context.Context, partition, id string) (domain.TrafficRecord, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, "partition", "timestamp", method, host, path, status, latency_ms, decision,
		       matched_mock_id, request_blob, response_blob
		FROM traffic WHERE "partition" = ? AND id = ?`, partition, id)

	rec, reqBlob, respBlob, err := scanTrafficRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.TrafficRecord{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.TrafficRecord{}, fmt.Errorf("store: get traffic: %w", err)
	}

	reqPT, ok := s.decryptOrAbsent(reqBlob, "traffic id="+id+" request")
	if !ok {
		return domain.TrafficRecord{}, domain.ErrNotFound
	}
	respPT, ok := s.decryptOrAbsent(respBlob, "traffic id="+id+" response")
	if !ok {
		return domain.TrafficRecord{}, domain.ErrNotFound
	}
	rec.Request, rec.Response = reqPT, respPT
	return rec, nil
}

// ListTraffic returns traffic records in partition matching filter, newest
// first. A row that fails to decrypt is silently skipped, never surfaced as
// a list error (FR-029). filter.Limit is assumed non-negative here: a
// negative Limit is already rejected by usecase.ListTraffic.Execute before
// this method is ever called, so the gate below only needs to distinguish
// "positive" (apply LIMIT) from "0/unbounded" (no LIMIT clause).
func (s *Store) ListTraffic(ctx context.Context, partition string, filter usecase.TrafficFilter) ([]domain.TrafficRecord, error) {
	q := `SELECT id, "partition", "timestamp", method, host, path, status, latency_ms, decision,
	             matched_mock_id, request_blob, response_blob
	      FROM traffic WHERE "partition" = ?`
	args := []any{partition}

	if filter.Method != "" {
		q += ` AND method = ?`
		args = append(args, filter.Method)
	}
	if filter.Host != "" {
		q += ` AND host = ?`
		args = append(args, filter.Host)
	}
	if filter.PathPrefix != "" {
		q += ` AND path LIKE ? ESCAPE '\'`
		args = append(args, escapeLike(filter.PathPrefix)+"%")
	}
	if filter.Status != nil {
		q += ` AND status = ?`
		args = append(args, *filter.Status)
	}
	if filter.Since != nil {
		q += ` AND "timestamp" >= ?`
		args = append(args, filter.Since.UnixMilli())
	}
	if filter.Until != nil {
		q += ` AND "timestamp" <= ?`
		args = append(args, filter.Until.UnixMilli())
	}
	q += ` ORDER BY "timestamp" DESC`
	if filter.Limit > 0 {
		q += ` LIMIT ?`
		args = append(args, filter.Limit)
	}

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list traffic: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []domain.TrafficRecord
	for rows.Next() {
		rec, reqBlob, respBlob, err := scanTrafficRow(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan traffic row: %w", err)
		}
		reqPT, ok := s.decryptOrAbsent(reqBlob, "traffic id="+rec.ID+" request")
		if !ok {
			continue
		}
		respPT, ok := s.decryptOrAbsent(respBlob, "traffic id="+rec.ID+" response")
		if !ok {
			continue
		}
		rec.Request, rec.Response = reqPT, respPT
		out = append(out, rec)
	}
	return out, rows.Err()
}

// ClearTraffic deletes every traffic record in partition.
func (s *Store) ClearTraffic(ctx context.Context, partition string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM traffic WHERE "partition" = ?`, partition)
	if err != nil {
		return fmt.Errorf("store: clear traffic: %w", err)
	}
	return nil
}

// rowScanner is satisfied by both *sql.Row (GetTraffic) and *sql.Rows
// (ListTraffic's iteration), letting one scan function serve both.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanTrafficRow(sc rowScanner) (domain.TrafficRecord, []byte, []byte, error) {
	var (
		rec               domain.TrafficRecord
		timestampMillis   int64
		decision          string
		matchedMockID     sql.NullString
		reqBlob, respBlob []byte
	)
	err := sc.Scan(
		&rec.ID, &rec.Partition, &timestampMillis, &rec.Method, &rec.Host, &rec.Path,
		&rec.Status, &rec.LatencyMS, &decision, &matchedMockID, &reqBlob, &respBlob,
	)
	if err != nil {
		return domain.TrafficRecord{}, nil, nil, err
	}
	rec.Timestamp = time.UnixMilli(timestampMillis)
	rec.Decision = domain.Decision(decision)
	if matchedMockID.Valid {
		id := matchedMockID.String
		rec.MatchedMockID = &id
	}
	return rec, reqBlob, respBlob, nil
}

// escapeLike escapes SQL LIKE metacharacters so a caller-supplied path
// prefix containing a literal '%' or '_' is matched literally, not as a
// wildcard.
func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}
