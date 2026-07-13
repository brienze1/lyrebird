package store

import (
	"context"
	"fmt"

	"github.com/brienze1/lyrebird/internal/domain"
)

// upstreamID derives a deterministic id from (partition, match_host,
// match_path) so SetUpstream is naturally idempotent — no UUID generation
// needed, and calling it twice for the same partition+host+path updates
// rather than duplicates. match_path is part of the key so two upstreams can
// share a host and route by path.
func upstreamID(partition, matchHost, matchPath string) string {
	return partition + "\x00" + matchHost + "\x00" + matchPath
}

// SetUpstream creates or updates the real target spy passthrough forwards
// to for (partition, match_host) — FR-003.
func (s *Store) SetUpstream(ctx context.Context, u domain.Upstream) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO upstreams (id, "partition", match_host, match_path, target_url, tls_skip_verify)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			target_url = excluded.target_url,
			tls_skip_verify = excluded.tls_skip_verify`,
		upstreamID(u.Partition, u.MatchHost, u.MatchPath), u.Partition, u.MatchHost, u.MatchPath, u.TargetURL, boolToInt(u.TLSSkipVerify),
	)
	if err != nil {
		return fmt.Errorf("store: set upstream: %w", err)
	}
	return nil
}

// ListUpstreams returns every upstream configured in partition, ordered by
// match_host for deterministic output.
func (s *Store) ListUpstreams(ctx context.Context, partition string) ([]domain.Upstream, error) {
	rows, err := s.readDB.QueryContext(ctx,
		`SELECT match_host, match_path, target_url, tls_skip_verify FROM upstreams WHERE "partition" = ? ORDER BY match_host, match_path`,
		partition,
	)
	if err != nil {
		return nil, fmt.Errorf("store: list upstreams: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []domain.Upstream
	for rows.Next() {
		var matchHost, matchPath, targetURL string
		var skip int
		if err := rows.Scan(&matchHost, &matchPath, &targetURL, &skip); err != nil {
			return nil, fmt.Errorf("store: scan upstream row: %w", err)
		}
		out = append(out, domain.Upstream{
			Partition: partition, MatchHost: matchHost, MatchPath: matchPath, TargetURL: targetURL, TLSSkipVerify: skip != 0,
		})
	}
	return out, rows.Err()
}

// DeleteUpstreamsByPartition removes every upstream configured in partition.
// Called when a partition is deleted (FR-024).
func (s *Store) DeleteUpstreamsByPartition(ctx context.Context, partition string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM upstreams WHERE "partition" = ?`, partition)
	if err != nil {
		return fmt.Errorf("store: delete upstreams by partition: %w", err)
	}
	return nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
