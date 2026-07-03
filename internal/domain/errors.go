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
)
