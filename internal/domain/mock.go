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
	CreatedAt  time.Time
}
