// Package dto holds the wire-shape DTOs and domain<->DTO conversions shared
// by every adapter that exposes Lyrebird's management API — Admin REST
// (internal/adapters/httpadmin) and MCP (internal/adapters/mcp). Defining
// these once here, rather than in httpadmin with mcp importing it (or vice
// versa), keeps both adapters as peers over the same use-case layer per
// constitution Principle II: MCP and REST must not duplicate business
// logic, and the nontrivial matcher/action conversion logic here counts as
// exactly that.
package dto

import (
	"fmt"

	"github.com/brienze1/lyrebird/internal/domain"
	"github.com/brienze1/lyrebird/internal/usecase"
)

// Note: MatcherDTO, BodyMatcherDTO, MatchDTO, and ActionDTO (below) mirror
// contracts/seed-config.md's schema exactly (flattened Matcher fields, no
// separate "matcher" wrapper key; ActionKind inferred from which of
// respond/proxy/fault is present, not a separate "kind" field) — the same
// shape used by internal/infra/seeds, since import/export round-trips
// through this schema too.

// MatcherDTO is the wire shape of domain.Matcher.
type MatcherDTO struct {
	Equals   *string `json:"equals,omitempty"`
	Contains *string `json:"contains,omitempty"`
	Regex    *string `json:"regex,omitempty"`
	Exists   *bool   `json:"exists,omitempty"`
}

// BodyMatcherDTO is one body condition: MatcherDTO's fields flattened
// alongside the JSONPath they apply to.
type BodyMatcherDTO struct {
	JSONPath string  `json:"jsonpath"`
	Equals   *string `json:"equals,omitempty"`
	Contains *string `json:"contains,omitempty"`
	Regex    *string `json:"regex,omitempty"`
	Exists   *bool   `json:"exists,omitempty"`
}

// MatchDTO is the wire shape of domain.Match.
type MatchDTO struct {
	Method  string                `json:"method,omitempty"`
	Path    string                `json:"path,omitempty"`
	Headers map[string]MatcherDTO `json:"headers,omitempty"`
	Query   map[string]MatcherDTO `json:"query,omitempty"`
	Body    []BodyMatcherDTO      `json:"body,omitempty"`
}

// RespondDTO is the wire shape of domain.RespondAction.
type RespondDTO struct {
	Status    int               `json:"status"`
	Headers   map[string]string `json:"headers,omitempty"`
	Body      string            `json:"body"`
	Template  bool              `json:"template,omitempty"`
	LatencyMS *int              `json:"latency_ms,omitempty"`
}

// ProxyDTO is the wire shape of domain.ProxyAction.
type ProxyDTO struct {
	RewriteRequestScript    *string `json:"rewrite_request,omitempty"`
	TransformResponseScript *string `json:"transform_response,omitempty"`
	LatencyMS               *int    `json:"latency_ms,omitempty"`
}

// FaultDTO is the wire shape of domain.FaultAction.
type FaultDTO struct {
	Kind    string `json:"kind"`
	DelayMS *int   `json:"delay_ms,omitempty"`
}

// ActionDTO is the wire shape of domain.Action: exactly one of
// Respond/Proxy/Fault is set, and that presence selects the ActionKind.
type ActionDTO struct {
	Respond *RespondDTO `json:"respond,omitempty"`
	Proxy   *ProxyDTO   `json:"proxy,omitempty"`
	Fault   *FaultDTO   `json:"fault,omitempty"`
}

// MockDTO is the wire shape of domain.Mock.
type MockDTO struct {
	ID         string    `json:"id,omitempty"`
	Name       string    `json:"name"`
	Priority   int       `json:"priority,omitempty"`
	Group      string    `json:"group,omitempty"`
	Lifetime   string    `json:"lifetime,omitempty"`
	TTLSeconds *int      `json:"ttl_seconds,omitempty"`
	Match      MatchDTO  `json:"match"`
	Action     ActionDTO `json:"action"`
}

// MatcherFromDTO converts a MatcherDTO to its domain equivalent.
func MatcherFromDTO(d MatcherDTO) domain.Matcher {
	return domain.Matcher{Equals: d.Equals, Contains: d.Contains, Regex: d.Regex, Exists: d.Exists}
}

// MatcherToDTO converts a domain.Matcher to its wire equivalent.
func MatcherToDTO(m domain.Matcher) MatcherDTO {
	return MatcherDTO{Equals: m.Equals, Contains: m.Contains, Regex: m.Regex, Exists: m.Exists}
}

// MatchFromDTO converts a MatchDTO to its domain equivalent.
func MatchFromDTO(d MatchDTO) domain.Match {
	out := domain.Match{Method: d.Method, Path: d.Path}
	if len(d.Headers) > 0 {
		out.Headers = make(map[string]domain.Matcher, len(d.Headers))
		for k, v := range d.Headers {
			out.Headers[k] = MatcherFromDTO(v)
		}
	}
	if len(d.Query) > 0 {
		out.Query = make(map[string]domain.Matcher, len(d.Query))
		for k, v := range d.Query {
			out.Query[k] = MatcherFromDTO(v)
		}
	}
	for _, b := range d.Body {
		out.Body = append(out.Body, domain.BodyMatcher{
			Path:    b.JSONPath,
			Matcher: domain.Matcher{Equals: b.Equals, Contains: b.Contains, Regex: b.Regex, Exists: b.Exists},
		})
	}
	return out
}

// MatchToDTO converts a domain.Match to its wire equivalent.
func MatchToDTO(m domain.Match) MatchDTO {
	out := MatchDTO{Method: m.Method, Path: m.Path}
	if len(m.Headers) > 0 {
		out.Headers = make(map[string]MatcherDTO, len(m.Headers))
		for k, v := range m.Headers {
			out.Headers[k] = MatcherToDTO(v)
		}
	}
	if len(m.Query) > 0 {
		out.Query = make(map[string]MatcherDTO, len(m.Query))
		for k, v := range m.Query {
			out.Query[k] = MatcherToDTO(v)
		}
	}
	for _, b := range m.Body {
		out.Body = append(out.Body, BodyMatcherDTO{
			JSONPath: b.Path, Equals: b.Matcher.Equals, Contains: b.Matcher.Contains,
			Regex: b.Matcher.Regex, Exists: b.Matcher.Exists,
		})
	}
	return out
}

// ActionFromDTO converts an ActionDTO to its domain equivalent, or
// domain.ErrInvalidMock if none of Respond/Proxy/Fault is set.
func ActionFromDTO(d ActionDTO) (domain.Action, error) {
	switch {
	case d.Respond != nil:
		return domain.Action{Kind: domain.ActionRespond, Respond: &domain.RespondAction{
			Status: d.Respond.Status, Headers: d.Respond.Headers, Body: []byte(d.Respond.Body),
			Template: d.Respond.Template, LatencyMS: d.Respond.LatencyMS,
		}}, nil
	case d.Proxy != nil:
		return domain.Action{Kind: domain.ActionProxy, Proxy: &domain.ProxyAction{
			RewriteRequestScript: d.Proxy.RewriteRequestScript, TransformResponseScript: d.Proxy.TransformResponseScript,
			LatencyMS: d.Proxy.LatencyMS,
		}}, nil
	case d.Fault != nil:
		return domain.Action{Kind: domain.ActionFault, Fault: &domain.FaultAction{
			Kind: domain.FaultKind(d.Fault.Kind), DelayMS: d.Fault.DelayMS,
		}}, nil
	default:
		return domain.Action{}, domain.ErrInvalidMock
	}
}

// ActionToDTO converts a domain.Action to its wire equivalent.
func ActionToDTO(a domain.Action) ActionDTO {
	switch a.Kind {
	case domain.ActionRespond:
		if a.Respond == nil {
			return ActionDTO{}
		}
		return ActionDTO{Respond: &RespondDTO{
			Status: a.Respond.Status, Headers: a.Respond.Headers, Body: string(a.Respond.Body),
			Template: a.Respond.Template, LatencyMS: a.Respond.LatencyMS,
		}}
	case domain.ActionProxy:
		if a.Proxy == nil {
			return ActionDTO{}
		}
		return ActionDTO{Proxy: &ProxyDTO{
			RewriteRequestScript: a.Proxy.RewriteRequestScript, TransformResponseScript: a.Proxy.TransformResponseScript,
			LatencyMS: a.Proxy.LatencyMS,
		}}
	case domain.ActionFault:
		if a.Fault == nil {
			return ActionDTO{}
		}
		return ActionDTO{Fault: &FaultDTO{Kind: string(a.Fault.Kind), DelayMS: a.Fault.DelayMS}}
	default:
		return ActionDTO{}
	}
}

// MockToDTO converts a domain.Mock to its wire equivalent.
func MockToDTO(m domain.Mock) MockDTO {
	return MockDTO{
		ID: m.ID, Name: m.Name, Priority: m.Priority, Group: m.Group,
		Lifetime: string(m.Lifetime), TTLSeconds: m.TTLSeconds,
		Match: MatchToDTO(m.Match), Action: ActionToDTO(m.Action),
	}
}

// MockInputFromDTO builds a usecase.MockInput from a MockDTO for a given
// partition. ID is deliberately not read from the DTO: it's server-assigned.
// Lifetime, if set, is validated here — not read into MockInput itself
// (usecase.MockCRUD.Create always produces LifetimeEphemeral regardless) —
// so a caller-supplied "seeded" is rejected with an explanatory error
// instead of being silently accepted and discarded. This check lives here,
// not in either adapter, so REST and MCP enforce it identically
// (constitution Principle II: neither may diverge on validation behavior
// the other lacks).
func MockInputFromDTO(partition string, d MockDTO) (usecase.MockInput, error) {
	if d.Lifetime != "" && d.Lifetime != string(domain.LifetimeEphemeral) {
		return usecase.MockInput{}, fmt.Errorf(`%w: lifetime must be "ephemeral" or omitted — mocks can only be created as ephemeral through this API; seeded mocks come only from mounted seed config files`, domain.ErrInvalidMock)
	}
	action, err := ActionFromDTO(d.Action)
	if err != nil {
		return usecase.MockInput{}, err
	}
	return usecase.MockInput{
		Partition: partition, Name: d.Name, Priority: d.Priority, Group: d.Group,
		Match: MatchFromDTO(d.Match), Action: action, TTLSeconds: d.TTLSeconds,
	}, nil
}
