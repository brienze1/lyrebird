// Package dto holds the wire-shape DTOs and domain<->DTO conversions shared
// by Lyrebird's management API adapters.
package dto

import (
	"fmt"

	"github.com/brienze1/lyrebird/internal/domain"
	"github.com/brienze1/lyrebird/internal/usecase"
)

// MatcherDTO is the wire shape of domain.Matcher.
type MatcherDTO struct {
	Equals   *string `json:"equals,omitempty" yaml:"equals,omitempty"`
	Contains *string `json:"contains,omitempty" yaml:"contains,omitempty"`
	Regex    *string `json:"regex,omitempty" yaml:"regex,omitempty"`
	Exists   *bool   `json:"exists,omitempty" yaml:"exists,omitempty"`
}

// BodyMatcherDTO is one body condition: MatcherDTO's fields flattened
// alongside the JSONPath they apply to.
type BodyMatcherDTO struct {
	JSONPath string  `json:"jsonpath" yaml:"jsonpath"`
	Equals   *string `json:"equals,omitempty" yaml:"equals,omitempty"`
	Contains *string `json:"contains,omitempty" yaml:"contains,omitempty"`
	Regex    *string `json:"regex,omitempty" yaml:"regex,omitempty"`
	Exists   *bool   `json:"exists,omitempty" yaml:"exists,omitempty"`
}

// MatchDTO is the wire shape of domain.Match.
type MatchDTO struct {
	Method  string                `json:"method,omitempty" yaml:"method,omitempty"`
	Path    string                `json:"path,omitempty" yaml:"path,omitempty"`
	Headers map[string]MatcherDTO `json:"headers,omitempty" yaml:"headers,omitempty"`
	Query   map[string]MatcherDTO `json:"query,omitempty" yaml:"query,omitempty"`
	Body    []BodyMatcherDTO      `json:"body,omitempty" yaml:"body,omitempty"`
}

// RespondDTO is the wire shape of domain.RespondAction.
type RespondDTO struct {
	Status    int               `json:"status" yaml:"status"`
	Headers   map[string]string `json:"headers,omitempty" yaml:"headers,omitempty"`
	Body      string            `json:"body" yaml:"body"`
	Template  bool              `json:"template,omitempty" yaml:"template,omitempty"`
	LatencyMS *int              `json:"latency_ms,omitempty" yaml:"latency_ms,omitempty"`
}

// ProxyDTO is the wire shape of domain.ProxyAction.
type ProxyDTO struct {
	RewriteRequestScript    *string `json:"rewrite_request,omitempty" yaml:"rewrite_request,omitempty"`
	TransformResponseScript *string `json:"transform_response,omitempty" yaml:"transform_response,omitempty"`
	LatencyMS               *int    `json:"latency_ms,omitempty" yaml:"latency_ms,omitempty"`
}

// FaultDTO is the wire shape of domain.FaultAction.
type FaultDTO struct {
	Kind    string `json:"kind" yaml:"kind"`
	DelayMS *int   `json:"delay_ms,omitempty" yaml:"delay_ms,omitempty"`
}

// ActionDTO is the wire shape of domain.Action: exactly one of
// Respond/Proxy/Fault is set, and that presence selects the ActionKind.
type ActionDTO struct {
	Respond *RespondDTO `json:"respond,omitempty" yaml:"respond,omitempty"`
	Proxy   *ProxyDTO   `json:"proxy,omitempty" yaml:"proxy,omitempty"`
	Fault   *FaultDTO   `json:"fault,omitempty" yaml:"fault,omitempty"`
}

// ScriptDTO is the wire shape of domain.Script.
type ScriptDTO struct {
	MatchSrc   string `json:"match_src,omitempty" yaml:"match_src,omitempty"`
	RespondSrc string `json:"respond_src,omitempty" yaml:"respond_src,omitempty"`
}

// ScriptFromDTO converts a ScriptDTO to its domain equivalent (nil-safe).
func ScriptFromDTO(d *ScriptDTO) *domain.Script {
	if d == nil {
		return nil
	}
	return &domain.Script{MatchSrc: d.MatchSrc, RespondSrc: d.RespondSrc}
}

// ScriptToDTO converts a domain.Script to its wire equivalent (nil-safe).
func ScriptToDTO(s *domain.Script) *ScriptDTO {
	if s == nil {
		return nil
	}
	return &ScriptDTO{MatchSrc: s.MatchSrc, RespondSrc: s.RespondSrc}
}

// ScenarioDTO is the wire shape of domain.Scenario.
type ScenarioDTO struct {
	Responses []RespondDTO `json:"responses" yaml:"responses"`
	OnExhaust string       `json:"on_exhaust,omitempty" yaml:"on_exhaust,omitempty"`
}

// ScenarioFromDTO converts a ScenarioDTO to its domain equivalent (nil-safe).
func ScenarioFromDTO(d *ScenarioDTO) *domain.Scenario {
	if d == nil {
		return nil
	}
	sc := &domain.Scenario{OnExhaust: domain.OnExhaust(d.OnExhaust)}
	for _, r := range d.Responses {
		sc.Responses = append(sc.Responses, domain.RespondAction{
			Status: r.Status, Headers: r.Headers, Body: []byte(r.Body), Template: r.Template, LatencyMS: r.LatencyMS,
		})
	}
	return sc
}

// ScenarioToDTO converts a domain.Scenario to its wire equivalent (nil-safe).
func ScenarioToDTO(s *domain.Scenario) *ScenarioDTO {
	if s == nil {
		return nil
	}
	d := &ScenarioDTO{OnExhaust: string(s.OnExhaust)}
	for _, r := range s.Responses {
		d.Responses = append(d.Responses, RespondDTO{
			Status: r.Status, Headers: r.Headers, Body: string(r.Body), Template: r.Template, LatencyMS: r.LatencyMS,
		})
	}
	return d
}

// MockDTO is the wire shape of domain.Mock.
type MockDTO struct {
	ID         string       `json:"id,omitempty" yaml:"id,omitempty"`
	Name       string       `json:"name" yaml:"name"`
	Priority   int          `json:"priority,omitempty" yaml:"priority,omitempty"`
	Group      string       `json:"group,omitempty" yaml:"group,omitempty"`
	Lifetime   string       `json:"lifetime,omitempty" yaml:"lifetime,omitempty"`
	TTLSeconds *int         `json:"ttl_seconds,omitempty" yaml:"ttl_seconds,omitempty"`
	Match      MatchDTO     `json:"match" yaml:"match"`
	Script     *ScriptDTO   `json:"script,omitempty" yaml:"script,omitempty"`
	Action     ActionDTO    `json:"action" yaml:"action"`
	Scenario   *ScenarioDTO `json:"scenario,omitempty" yaml:"scenario,omitempty"`
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
// domain.ErrInvalidMock if zero or more than one of Respond/Proxy/Fault is
// set.
func ActionFromDTO(d ActionDTO) (domain.Action, error) {
	set := 0
	for _, isSet := range []bool{d.Respond != nil, d.Proxy != nil, d.Fault != nil} {
		if isSet {
			set++
		}
	}
	if set > 1 {
		return domain.Action{}, fmt.Errorf("%w: exactly one of action.respond/action.proxy/action.fault may be set", domain.ErrInvalidMock)
	}

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
		Match: MatchToDTO(m.Match), Script: ScriptToDTO(m.Script), Action: ActionToDTO(m.Action),
		Scenario: ScenarioToDTO(m.Scenario),
	}
}

// NewMockDTOFromFields builds a MockDTO from a mock's settable fields (ID is server-assigned, so excluded).
func NewMockDTOFromFields(name string, match MatchDTO, script *ScriptDTO, action ActionDTO, scenario *ScenarioDTO, priority int, group string, ttlSeconds *int, lifetime string) MockDTO {
	return MockDTO{
		Name: name, Priority: priority, Group: group, Lifetime: lifetime, TTLSeconds: ttlSeconds,
		Match: match, Script: script, Action: action, Scenario: scenario,
	}
}

// MockInputFromDTO builds a usecase.MockInput from a MockDTO for partition; rejects a non-empty, non-"ephemeral" Lifetime.
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
		Match: MatchFromDTO(d.Match), Script: ScriptFromDTO(d.Script), Action: action, TTLSeconds: d.TTLSeconds,
		Scenario: ScenarioFromDTO(d.Scenario),
	}, nil
}
