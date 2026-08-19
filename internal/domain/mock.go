package domain

import "time"

// Lifetime distinguishes mocks loaded from mounted seed config (protected
// from reset/GC) from mocks created at runtime (ephemeral, GC/TTL-eligible).
type Lifetime string

// The Lifetime values a Mock may have.
const (
	LifetimeSeeded    Lifetime = "seeded"
	LifetimeEphemeral Lifetime = "ephemeral"
)

// Mock is a named rule that may intercept matching requests. Many mocks may
// share a route: resolution is by Priority descending, then CreatedAt
// descending (most-recently-created wins ties), then ID.
type Mock struct {
	ID         string
	Partition  string
	Name       string
	Lifetime   Lifetime
	TTLSeconds *int
	Priority   int
	Group      string
	Match      Match
	Script     *Script
	Action     Action
	Scenario   *Scenario
	// Projection overrides the byte-stream endpoint's own field projection
	// for this rule alone (FR-006). Nil on every mock that is not a
	// byte-stream rule, and nil on a stream rule content to inherit its
	// endpoint's default — which is the common case.
	Projection *Projection
	// FromCapture marks a mock created by PromoteTraffic, i.e. one whose
	// match and response were derived from bytes that actually crossed a
	// data plane rather than authored by hand. It exists so export_config
	// can warn that a file may carry real captured content (FR-035), and is
	// deliberately plane-agnostic: a promoted HTTP mock carries captured
	// bytes just as a promoted stream mock does.
	FromCapture bool
	CreatedAt   time.Time
}
