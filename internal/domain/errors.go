package domain

import "errors"

var (
	// ErrNotFound is returned when a lookup by id/partition finds nothing —
	// including when a row exists but fails to decrypt under the active
	// at-rest key (FR-029: treated as absent, not as corruption).
	ErrNotFound = errors.New("lyrebird: not found")

	// ErrDuplicateID is returned when a seed file declares a mock/partition
	// id that collides with one already loaded.
	ErrDuplicateID = errors.New("lyrebird: duplicate id")

	// ErrDefaultPartitionProtected is returned when deletion of the default
	// partition is attempted (FR-024: default cannot be deleted).
	ErrDefaultPartitionProtected = errors.New("lyrebird: default partition cannot be deleted")

	// ErrInvalidUpstream is returned when an Upstream fails basic validation
	// (missing partition/match_host, or a target_url that isn't a valid
	// absolute http(s) URL).
	ErrInvalidUpstream = errors.New("lyrebird: invalid upstream")

	// ErrInvalidMock is returned when a Mock fails basic validation (missing
	// name, an action whose Kind doesn't match its populated variant, or a
	// Match condition that fails MatchEval.ValidateMatch, e.g. a bad regex).
	ErrInvalidMock = errors.New("lyrebird: invalid mock")

	// ErrInvalidPartition is returned when a Partition fails basic
	// validation (missing id).
	ErrInvalidPartition = errors.New("lyrebird: invalid partition")

	// ErrSeededMockImmutable is returned when an update or delete targets a
	// seeded mock. Seeded mocks are protected config, not runtime state
	// (constitution Principle III, FR-025) — the rejection is explicit
	// rather than a silent no-op or a bare ErrNotFound.
	ErrSeededMockImmutable = errors.New("lyrebird: seeded mock cannot be modified or deleted")

	// ErrInvalidTrafficFilter is returned when a TrafficFilter fails basic
	// validation (e.g. a negative Limit). Limit == 0 is a legitimate,
	// documented sentinel meaning "unbounded" and is never rejected — only
	// values with no legitimate meaning are.
	ErrInvalidTrafficFilter = errors.New("lyrebird: invalid traffic filter")
)
