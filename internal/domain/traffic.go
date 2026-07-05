package domain

import "time"

// Decision records how a request was handled.
type Decision string

// The Decision values recording how a request was handled.
const (
	DecisionMocked        Decision = "mocked"
	DecisionProxied       Decision = "proxied"
	DecisionFaulted       Decision = "faulted"
	DecisionNotConfigured Decision = "not_configured"
	// DecisionScriptFailed records that a mock's sandboxed script (match or
	// respond phase) errored or exceeded its execution timeout — a fail-safe
	// outcome (FR-016/SC-010), not a hang or a crash.
	DecisionScriptFailed Decision = "script_failed"
	// DecisionBlocked records that a request was refused by the proxy
	// allow/deny host policy (FR-006) before any upstream was contacted.
	DecisionBlocked Decision = "blocked"
	// DecisionInternalError records that a generic internal error occurred
	// outside any of the other specific failure modes above — a body-peek
	// I/O failure, an upstream-list lookup failure, a scenario-advance
	// failure, or a non-script-related match failure. A fail-safe outcome,
	// still recorded so it's never invisible in the traffic log.
	DecisionInternalError Decision = "internal_error"
)

// TrafficRecord is a recorded interaction. Request/Response are encrypted at
// rest; Method/Host/Path/Status/Timestamp/LatencyMS stay plaintext so they
// can be filtered and swept by the garbage collector (data-model.md).
type TrafficRecord struct {
	ID            string
	Partition     string
	Timestamp     time.Time
	Method        string
	Host          string
	Path          string
	Request       []byte
	MatchedMockID *string
	Decision      Decision
	Response      []byte
	Status        int
	LatencyMS     int
}
