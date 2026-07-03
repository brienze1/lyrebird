package domain

// ActionKind selects which of Action's variants is active.
type ActionKind string

// The ActionKind values, one per Action variant.
const (
	ActionRespond ActionKind = "respond"
	ActionProxy   ActionKind = "proxy"
	ActionFault   ActionKind = "fault"
)

// Action is what a Mock does once it matches a request. Exactly one of
// Respond, Proxy, or Fault is populated, selected by Kind.
type Action struct {
	Kind    ActionKind
	Respond *RespondAction
	Proxy   *ProxyAction
	Fault   *FaultAction
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

// ProxyAction forwards to the resolved Upstream. The rewrite/transform
// scripts hold JS source executed by goja starting at M4; M0 only needs the
// field to exist and round-trip.
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
