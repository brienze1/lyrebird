package domain

import "time"

// ActionKind selects which of Action's variants is active.
type ActionKind string

// The ActionKind values, one per Action variant.
const (
	ActionRespond ActionKind = "respond"
	ActionProxy   ActionKind = "proxy"
	ActionFault   ActionKind = "fault"
	// ActionCadence overrides what a stream endpoint's already-running
	// cadence emits, per tick, for as long as this mock remains the
	// highest-priority match for that endpoint (FR-009a's ordering).
	// Runtime, reversible, content-only by default — the motivating case is
	// the CB5 position source: a scenario-scripted GPRMC speed must become
	// the SUSTAINED content of a seeded, already-ticking cadence, not a
	// one-shot injection an already-running cadence drowns out.
	ActionCadence ActionKind = "cadence"
)

// Action is what a Mock does once it matches a request. Exactly one of
// Respond, Proxy, Fault, or Cadence is populated, selected by Kind.
type Action struct {
	Kind    ActionKind
	Respond *RespondAction
	Proxy   *ProxyAction
	Fault   *FaultAction
	Cadence *CadenceAction
}

// RespondAction builds a mock response. Body is encrypted at rest
// (data-model.md); the domain struct always holds plaintext — encryption is
// applied only at the store boundary.
type RespondAction struct {
	Status    int
	Headers   map[string]string
	Body      []byte
	Template  bool
	LatencyMS *int
}

// ProxyAction forwards to the resolved Upstream. RewriteRequestScript and
// TransformResponseScript hold JS source executed by goja at proxy-forward
// time (internal/adapters/proxy/engine.go), letting the mock rewrite the
// outgoing request and the real upstream response respectively. Unlike
// Mock.Script's match/respond scripts, which fail closed, a failure in
// either of these two scripts fails open: the engine logs a warning and
// forwards/returns the request/response unmodified rather than erroring out
// an otherwise-working proxy call.
type ProxyAction struct {
	RewriteRequestScript    *string
	TransformResponseScript *string
	LatencyMS               *int
}

// FaultKind selects the kind of injected failure.
type FaultKind string

// The FaultKind values a FaultAction may inject.
const (
	FaultDelay     FaultKind = "delay"
	FaultReset     FaultKind = "reset"
	FaultTimeout   FaultKind = "timeout"
	FaultMalformed FaultKind = "malformed"
)

// FaultAction injects a chaos-testing failure instead of a normal response.
type FaultAction struct {
	Kind    FaultKind
	DelayMS *int
}

// CadenceAction overrides what a stream endpoint's running cadence emits, for
// as long as this mock is the winning candidate for that endpoint (resolved
// fresh each tick, the same priority order every other mock kind uses —
// FR-009a). It requires an endpoint that ALREADY declares a cadence: the
// override replaces what that cadence sustains, it does not start one from
// nothing.
//
// Content-only by default: Interval and OnExhaust are optional and inherit
// the endpoint's declared cadence's values when left unset (nil / ""), so
// the common case — swap the sustained frame, change nothing about its
// pacing — needs no more than Frames.
type CadenceAction struct {
	// Interval, when set, retunes the emission pacing while this override is
	// active. Nil inherits the endpoint's declared interval. A negative value
	// is rejected at write time.
	Interval *time.Duration
	// Frames is the override's content, in the same declarative grammar as
	// domain.Cadence.Frames — the one field an override always sets, since
	// replacing content is the whole point.
	Frames [][]FramePart
	// OnExhaust, when set, overrides the endpoint's declared exhaustion
	// behaviour. Empty inherits it.
	OnExhaust OnExhaustion
}
