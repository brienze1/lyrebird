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
