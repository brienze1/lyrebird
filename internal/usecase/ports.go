// Package usecase defines the application's use cases and the repository
// ports they depend on. At M0 only the port interfaces existed; concrete
// use-case implementations land starting at M1 (see specs/001-lyrebird/tasks.md).
package usecase

import (
	"context"
	"fmt"
	"time"

	"github.com/brienze1/lyrebird/internal/domain"
)

// TrafficFilter narrows a traffic listing by the plaintext, indexed columns.
type TrafficFilter struct {
	Method     string
	Host       string
	PathPrefix string
	Status     *int
	Since      *time.Time
	Until      *time.Time
	// Limit bounds the number of rows returned (0 = unbounded). Applied at
	// the SQL layer (a genuine LIMIT, not fetch-all-then-slice) since traffic
	// volume, unlike mock count, can be large. Applied before the per-row
	// decrypt-or-skip check (FR-029), so a caller asking for Limit=20 can
	// legitimately get fewer than 20 rows back — not an off-by-one bug.
	Limit int
}

// MockRepo persists ephemeral mocks. Seeded mocks never pass through it —
// they live only in memory (constitution Principle III).
//
// Method names are entity-qualified (CreateMock, not Create) rather than
// bare, because a single concrete adapter (*store.Store) implements every
// port interface in this file, and Go has no method overloading: two ports
// both declaring a bare List/Get/Delete with different signatures cannot
// both be satisfied by one receiver type. This was discovered as a real
// compile-blocker while implementing M1's TrafficRepo/UpstreamRepo
// alongside the pre-existing MockRepo/PartitionRepo, and fixed uniformly
// across all four interfaces at once rather than one collision at a time.
type MockRepo interface {
	CreateMock(ctx context.Context, m domain.Mock) error
	GetMock(ctx context.Context, partition, id string) (domain.Mock, error)
	ListMocks(ctx context.Context, partition string) ([]domain.Mock, error)
	UpdateMock(ctx context.Context, m domain.Mock) error
	DeleteMock(ctx context.Context, partition, id string) error
	DeleteMocksByPartition(ctx context.Context, partition string) error
	// PruneExpiredEphemeralMocks removes ephemeral mocks whose TTL has
	// elapsed as of now. Seeded mocks are never touched. Named to match
	// *store.Store's existing M0 method exactly (gc.Pruner already depends
	// on that name) — zero rename needed on the store side.
	PruneExpiredEphemeralMocks(ctx context.Context, now time.Time) (int, error)
}

// TrafficRepo persists the spy traffic log (FR-002), bounded by retention
// (FR-027).
type TrafficRepo interface {
	AppendTraffic(ctx context.Context, t domain.TrafficRecord) error
	GetTraffic(ctx context.Context, partition, id string) (domain.TrafficRecord, error)
	ListTraffic(ctx context.Context, partition string, filter TrafficFilter) ([]domain.TrafficRecord, error)
	PruneTraffic(ctx context.Context, olderThan time.Time) (int, error)
	ClearTraffic(ctx context.Context, partition string) error
}

// PartitionRepo manages spaces/partitions (FR-023).
type PartitionRepo interface {
	CreatePartition(ctx context.Context, p domain.Partition) error
	GetPartition(ctx context.Context, id string) (domain.Partition, error)
	ListPartitions(ctx context.Context) ([]domain.Partition, error)
	// DeletePartition cascades the partition's mocks/traffic/upstreams.
	// Callers MUST reject domain.DefaultPartitionID before calling
	// (FR-024); the repo itself does not special-case it.
	DeletePartition(ctx context.Context, id string) error
}

// UpstreamRepo manages the real targets spy passthrough forwards to (FR-003).
type UpstreamRepo interface {
	SetUpstream(ctx context.Context, u domain.Upstream) error
	ListUpstreams(ctx context.Context, partition string) ([]domain.Upstream, error)
	DeleteUpstreamsByPartition(ctx context.Context, partition string) error
}

// ScenarioStateRepo tracks each mock's position through its Scenario
// sequence, reset by a reset operation.
type ScenarioStateRepo interface {
	ScenarioIndex(ctx context.Context, partition, mockID string) (int, error)
	AdvanceScenario(ctx context.Context, partition, mockID string) (int, error)
	ResetScenario(ctx context.Context, partition, mockID string) error
	ResetAllScenarios(ctx context.Context, partition string) error
}

// Clock abstracts time.Now so tests can control it.
type Clock interface{ Now() time.Time }

// IDGen abstracts id generation so tests can control it.
type IDGen interface{ NewID() string }

// MatchInput is the plain-data view of an inbound request that MatchEval and
// Templater operate on. Deliberately not net/http (map[string][]string
// instead of http.Header) so usecase stays free of adapter/stdlib-net
// dependencies, matching the RecordedMessage convention already established
// in M1.
type MatchInput struct {
	Method string
	Path   string
	Header map[string][]string
	Query  map[string][]string
	Body   []byte
}

// ConditionResult reports one evaluated match condition's outcome. Used by
// both the live request-matching hot path (only the aggregate bool is
// consulted) and MatchTest's full per-condition dry-run detail (FR-011).
type ConditionResult struct {
	Field    string
	Expected string
	Actual   string
	Passed   bool
}

// MatchEval evaluates a domain.Match against a MatchInput. It is a port
// (rather than being called directly from an adapter) because
// internal/usecase cannot import internal/adapters/* (Clean Architecture's
// inward-only dependency rule) while still needing declarative matching
// logic; the concrete implementation lives in internal/adapters/matcher.
type MatchEval interface {
	// Matches reports whether every condition in m holds against in, plus
	// the per-condition detail.
	Matches(m domain.Match, in MatchInput) (bool, []ConditionResult)
	// ValidateMatch checks m is well-formed (e.g. every regex compiles)
	// without evaluating it against a request. Called at mock create/update
	// time so a bad pattern is rejected at write time, not at first-match
	// time.
	ValidateMatch(m domain.Match) error
}

// Templater renders {{...}} placeholders in a RespondAction's body/headers
// against a MatchInput, when RespondAction.Template is true. A port for the
// same reason as MatchEval; implemented by internal/adapters/template.
type Templater interface {
	Render(body []byte, in MatchInput) []byte
	RenderHeaders(headers map[string]string, in MatchInput) map[string]string
}

// SeededMockSource returns the seeded (in-memory, TTL/reset-immune) mocks
// for a partition. Implemented directly by seeds.Seeds — never by MockRepo,
// since seeded content never touches the disposable store (constitution
// Principle III).
type SeededMockSource interface {
	SeededMocks(partition string) []domain.Mock
}

// ScriptEval evaluates a domain.Script's match_src/respond_src hooks inside
// a sandboxed JS VM against a MatchInput. A port for the same reason as
// MatchEval/Templater: usecase cannot import internal/adapters/scripting
// (Clean Architecture's inward-only dependency rule); the concrete
// implementation lives there.
type ScriptEval interface {
	// ValidateScript reports whether src compiles as well-formed JS, without
	// executing it — mirrors MatchEval.ValidateMatch's write-time-not-
	// first-match-time contract. src == "" is always valid (no-op).
	ValidateScript(src string) error
	// EvalMatch runs match_src and reports whether the mock should be
	// considered matched (JS truthiness of the last-evaluated expression).
	// A non-nil error means the script itself misbehaved (threw, timed out,
	// or exceeded the call-stack bound) — callers MUST treat this as a
	// fail-safe condition (FR-016/SC-010), never as "didn't match".
	EvalMatch(src string, in MatchInput) (bool, error)
	// EvalRespond runs respond_src and returns the response body it
	// produced: a returned JS string is used as raw bytes verbatim;
	// anything else JSON-encodable is JSON-marshaled. Same fail-safe error
	// contract as EvalMatch.
	EvalRespond(src string, in MatchInput) ([]byte, error)
}

// ScriptError wraps a ScriptEval failure (match or respond phase) with the
// mock it belongs to, so callers can record it as traffic (FR-016/SC-010)
// instead of only logging and returning a generic internal error.
type ScriptError struct {
	MockID string
	Phase  string // "match" | "respond"
	Err    error
}

func (e *ScriptError) Error() string {
	return fmt.Sprintf("usecase: mock %q script (%s) failed: %v", e.MockID, e.Phase, e.Err)
}

func (e *ScriptError) Unwrap() error { return e.Err }
