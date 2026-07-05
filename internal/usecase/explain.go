package usecase

import (
	"errors"

	"github.com/brienze1/lyrebird/internal/domain"
)

// ErrorKind classifies an Explained error for the adapter layer to map onto
// a transport-specific status (HTTP status code, MCP tool error), without
// the mapping logic itself needing to know about domain error sentinels.
type ErrorKind int

// The ErrorKind values Explain can produce.
const (
	KindInternal ErrorKind = iota
	KindValidation
	KindNotFound
	KindConflict
)

// Explained is Explain's result: a message stating what failed and how to
// fix it (FR-020), classified so callers can pick an appropriate status.
type Explained struct {
	Kind    ErrorKind
	Message string
}

// Explain maps a use-case error into a consistently-worded, actionable
// message. This is the single place a new domain-error → English mapping is
// added, so MCP and REST error wording can never independently drift
// (constitution Principle II).
func Explain(err error) Explained {
	switch {
	case err == nil:
		return Explained{}
	case errors.Is(err, domain.ErrInvalidMock), errors.Is(err, domain.ErrInvalidUpstream), errors.Is(err, domain.ErrInvalidPartition):
		return Explained{KindValidation, err.Error() + " — check the field(s) named above against this tool's example payload."}
	case errors.Is(err, domain.ErrInvalidTrafficFilter):
		return Explained{KindValidation, err.Error() + " — check the limit field against this tool's example payload."}
	case errors.Is(err, domain.ErrSeededMockImmutable):
		return Explained{KindConflict, err.Error() + " — seeded mocks come from mounted config; create a new ephemeral mock (optionally at a higher priority) instead of editing this one."}
	case errors.Is(err, domain.ErrDefaultPartitionProtected):
		return Explained{KindValidation, err.Error() + " — the default space cannot be deleted."}
	case errors.Is(err, domain.ErrNotFound):
		return Explained{KindNotFound, err.Error() + " — call the matching list tool (list_mocks/list_traffic/list_upstreams) to see valid ids for this space."}
	default:
		return Explained{KindInternal, "internal error: " + err.Error()}
	}
}
