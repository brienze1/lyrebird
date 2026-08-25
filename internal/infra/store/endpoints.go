package store

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/brienze1/lyrebird/internal/domain"
)

// endpointFraming is the persisted shape of domain.Framing. It exists rather
// than marshalling the domain type directly because Framing.Delimiter is a
// []byte, which encoding/json renders as base64 — correct but unreadable in a
// stored blob, and a needless coupling of the on-disk format to a Go encoding
// detail. Hex is explicit and byte-exact for any delimiter, printable or not.
type endpointFraming struct {
	Kind         string `json:"kind"`
	DelimiterHex string `json:"delimiter_hex,omitempty"`
	Length       int    `json:"length,omitempty"`
	PrefixWidth  int    `json:"prefix_width,omitempty"`
	PrefixEndian string `json:"prefix_endian,omitempty"`
}

// CreateEndpoint persists a new ephemeral byte-stream endpoint. Seeded
// endpoints never reach this method — like seeded mocks they live only in
// memory (constitution Principle III).
//
// framing_blob is sealed alongside projection and cadence: a cadence's frames
// are fixture content in exactly the sense a mock's respond body is, and
// splitting the three across different protection levels would be a
// distinction with no rationale behind it.
func (s *Store) CreateEndpoint(ctx context.Context, e domain.Endpoint) error {
	framingBlob, projectionBlob, cadenceBlob, err := encodeEndpoint(s, e)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO ephemeral_endpoints ("partition", name, created_at, framing_blob, projection_blob, cadence_blob)
		VALUES (?, ?, ?, ?, ?, ?)`,
		e.Partition, e.Name, e.CreatedAt.UnixNano(), framingBlob, projectionBlob, cadenceBlob,
	)
	if err != nil {
		return fmt.Errorf("store: create endpoint: %w", err)
	}
	return nil
}

// GetEndpoint returns the ephemeral endpoint (partition, name), or
// domain.ErrNotFound if it does not exist or its framing_blob fails to
// decrypt under the active at-rest key — undecryptable is treated as absent,
// the same contract GetMock has.
func (s *Store) GetEndpoint(ctx context.Context, partition, name string) (domain.Endpoint, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT "partition", name, created_at, framing_blob, projection_blob, cadence_blob
		FROM ephemeral_endpoints WHERE "partition" = ? AND name = ?`, partition, name)

	e, err := scanEndpointRow(s, row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, domain.ErrNotFound) {
			return domain.Endpoint{}, domain.ErrNotFound
		}
		return domain.Endpoint{}, fmt.Errorf("store: get endpoint: %w", err)
	}
	return e, nil
}

// ListEndpoints returns every ephemeral endpoint in partition whose
// framing_blob decrypts successfully. A row that does not is skipped rather
// than failing the listing, mirroring ListMocks.
func (s *Store) ListEndpoints(ctx context.Context, partition string) ([]domain.Endpoint, error) {
	rows, err := s.readDB.QueryContext(ctx, `
		SELECT "partition", name, created_at, framing_blob, projection_blob, cadence_blob
		FROM ephemeral_endpoints WHERE "partition" = ?`, partition)
	if err != nil {
		return nil, fmt.Errorf("store: list endpoints: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []domain.Endpoint
	for rows.Next() {
		e, err := scanEndpointRow(s, rows)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				continue
			}
			return nil, fmt.Errorf("store: scan endpoint row: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// DeleteEndpoint removes one ephemeral endpoint, returning
// domain.ErrNotFound if it does not exist.
func (s *Store) DeleteEndpoint(ctx context.Context, partition, name string) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM ephemeral_endpoints WHERE "partition" = ? AND name = ?`, partition, name)
	if err != nil {
		return fmt.Errorf("store: delete endpoint: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: delete endpoint rows affected: %w", err)
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// DeleteEndpointsByPartition removes every ephemeral endpoint in partition.
// Called by reset and by partition deletion, alongside the mock equivalent.
func (s *Store) DeleteEndpointsByPartition(ctx context.Context, partition string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM ephemeral_endpoints WHERE "partition" = ?`, partition)
	if err != nil {
		return fmt.Errorf("store: delete endpoints by partition: %w", err)
	}
	return nil
}

func encodeEndpoint(s *Store, e domain.Endpoint) (framingBlob, projectionBlob, cadenceBlob []byte, err error) {
	if framingBlob, err = sealJSON(s, toStoredFraming(e.Framing)); err != nil {
		return nil, nil, nil, fmt.Errorf("store: endpoint framing: %w", err)
	}
	if e.Projection != nil {
		if projectionBlob, err = sealJSON(s, e.Projection); err != nil {
			return nil, nil, nil, fmt.Errorf("store: endpoint projection: %w", err)
		}
	}
	if e.Cadence != nil {
		if cadenceBlob, err = sealJSON(s, e.Cadence); err != nil {
			return nil, nil, nil, fmt.Errorf("store: endpoint cadence: %w", err)
		}
	}
	return framingBlob, projectionBlob, cadenceBlob, nil
}

// scanEndpointRow reads one row and decodes it. It returns
// domain.ErrNotFound when the framing cannot be read, since an endpoint
// without a framing cannot divide a stream into frames and is therefore not
// a usable endpoint — unlike the optional projection and cadence, which
// degrade to nil.
func scanEndpointRow(s *Store, row rowScanner) (domain.Endpoint, error) {
	var e domain.Endpoint
	var createdAtNanos int64
	var framingBlob, projectionBlob, cadenceBlob []byte

	if err := row.Scan(&e.Partition, &e.Name, &createdAtNanos, &framingBlob, &projectionBlob, &cadenceBlob); err != nil {
		return domain.Endpoint{}, err
	}
	e.Lifetime = domain.LifetimeEphemeral
	e.CreatedAt = time.Unix(0, createdAtNanos).UTC()

	framingJSON, ok := s.decryptOrAbsent(framingBlob, "ephemeral_endpoints name="+e.Name)
	if !ok {
		return domain.Endpoint{}, domain.ErrNotFound
	}
	var stored endpointFraming
	if err := json.Unmarshal(framingJSON, &stored); err != nil {
		s.log.Warn("endpoint framing unmarshal failed, treated as absent", "endpoint", e.Name, "err", err)
		return domain.Endpoint{}, domain.ErrNotFound
	}
	framing, err := fromStoredFraming(stored)
	if err != nil {
		s.log.Warn("endpoint framing decode failed, treated as absent", "endpoint", e.Name, "err", err)
		return domain.Endpoint{}, domain.ErrNotFound
	}
	e.Framing = framing

	if projectionJSON, ok := s.decryptOrAbsent(projectionBlob, "ephemeral_endpoints name="+e.Name+" projection"); ok {
		var p domain.Projection
		if err := json.Unmarshal(projectionJSON, &p); err == nil {
			e.Projection = &p
		} else {
			s.log.Warn("endpoint projection unmarshal failed, treated as absent", "endpoint", e.Name, "err", err)
		}
	}
	if cadenceJSON, ok := s.decryptOrAbsent(cadenceBlob, "ephemeral_endpoints name="+e.Name+" cadence"); ok {
		var c domain.Cadence
		if err := json.Unmarshal(cadenceJSON, &c); err == nil {
			e.Cadence = &c
		} else {
			s.log.Warn("endpoint cadence unmarshal failed, treated as absent", "endpoint", e.Name, "err", err)
		}
	}
	return e, nil
}

func toStoredFraming(f domain.Framing) endpointFraming {
	return endpointFraming{
		Kind:         string(f.Kind),
		DelimiterHex: hex.EncodeToString(f.Delimiter),
		Length:       f.Length,
		PrefixWidth:  f.PrefixWidth,
		PrefixEndian: string(f.PrefixEndian),
	}
}

func fromStoredFraming(s endpointFraming) (domain.Framing, error) {
	delim, err := hex.DecodeString(s.DelimiterHex)
	if err != nil {
		return domain.Framing{}, fmt.Errorf("delimiter_hex: %w", err)
	}
	return domain.Framing{
		Kind:         domain.FramingKind(s.Kind),
		Delimiter:    delim,
		Length:       s.Length,
		PrefixWidth:  s.PrefixWidth,
		PrefixEndian: domain.Endianness(s.PrefixEndian),
	}, nil
}
