package httpadmin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/brienze1/lyrebird/internal/adapters/httpmw"
	"github.com/brienze1/lyrebird/internal/domain"
	"github.com/brienze1/lyrebird/internal/usecase"
)

// matcherDTO/bodyMatcherDTO/matchDTO/actionDTO mirror
// contracts/seed-config.md's schema exactly (flattened Matcher fields, no
// separate "matcher" wrapper key; ActionKind inferred from which of
// respond/proxy/fault is present, not a separate "kind" field) — the same
// shape used by internal/infra/seeds, since import/export round-trips
// through this schema too.
type matcherDTO struct {
	Equals   *string `json:"equals,omitempty"`
	Contains *string `json:"contains,omitempty"`
	Regex    *string `json:"regex,omitempty"`
	Exists   *bool   `json:"exists,omitempty"`
}

type bodyMatcherDTO struct {
	JSONPath string  `json:"jsonpath"`
	Equals   *string `json:"equals,omitempty"`
	Contains *string `json:"contains,omitempty"`
	Regex    *string `json:"regex,omitempty"`
	Exists   *bool   `json:"exists,omitempty"`
}

type matchDTO struct {
	Method  string                `json:"method,omitempty"`
	Path    string                `json:"path,omitempty"`
	Headers map[string]matcherDTO `json:"headers,omitempty"`
	Query   map[string]matcherDTO `json:"query,omitempty"`
	Body    []bodyMatcherDTO      `json:"body,omitempty"`
}

type respondDTO struct {
	Status    int               `json:"status"`
	Headers   map[string]string `json:"headers,omitempty"`
	Body      string            `json:"body"`
	Template  bool              `json:"template,omitempty"`
	LatencyMS *int              `json:"latency_ms,omitempty"`
}

type proxyDTO struct {
	RewriteRequestScript    *string `json:"rewrite_request,omitempty"`
	TransformResponseScript *string `json:"transform_response,omitempty"`
	LatencyMS               *int    `json:"latency_ms,omitempty"`
}

type faultDTO struct {
	Kind    string `json:"kind"`
	DelayMS *int   `json:"delay_ms,omitempty"`
}

type actionDTO struct {
	Respond *respondDTO `json:"respond,omitempty"`
	Proxy   *proxyDTO   `json:"proxy,omitempty"`
	Fault   *faultDTO   `json:"fault,omitempty"`
}

type mockDTO struct {
	ID         string    `json:"id,omitempty"`
	Name       string    `json:"name"`
	Priority   int       `json:"priority,omitempty"`
	Group      string    `json:"group,omitempty"`
	Lifetime   string    `json:"lifetime,omitempty"`
	TTLSeconds *int      `json:"ttl_seconds,omitempty"`
	Match      matchDTO  `json:"match"`
	Action     actionDTO `json:"action"`
}

func matcherFromDTO(d matcherDTO) domain.Matcher {
	return domain.Matcher{Equals: d.Equals, Contains: d.Contains, Regex: d.Regex, Exists: d.Exists}
}

func matcherToDTO(m domain.Matcher) matcherDTO {
	return matcherDTO{Equals: m.Equals, Contains: m.Contains, Regex: m.Regex, Exists: m.Exists}
}

func matchFromDTO(d matchDTO) domain.Match {
	out := domain.Match{Method: d.Method, Path: d.Path}
	if len(d.Headers) > 0 {
		out.Headers = make(map[string]domain.Matcher, len(d.Headers))
		for k, v := range d.Headers {
			out.Headers[k] = matcherFromDTO(v)
		}
	}
	if len(d.Query) > 0 {
		out.Query = make(map[string]domain.Matcher, len(d.Query))
		for k, v := range d.Query {
			out.Query[k] = matcherFromDTO(v)
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

func matchToDTO(m domain.Match) matchDTO {
	out := matchDTO{Method: m.Method, Path: m.Path}
	if len(m.Headers) > 0 {
		out.Headers = make(map[string]matcherDTO, len(m.Headers))
		for k, v := range m.Headers {
			out.Headers[k] = matcherToDTO(v)
		}
	}
	if len(m.Query) > 0 {
		out.Query = make(map[string]matcherDTO, len(m.Query))
		for k, v := range m.Query {
			out.Query[k] = matcherToDTO(v)
		}
	}
	for _, b := range m.Body {
		out.Body = append(out.Body, bodyMatcherDTO{
			JSONPath: b.Path, Equals: b.Matcher.Equals, Contains: b.Matcher.Contains,
			Regex: b.Matcher.Regex, Exists: b.Matcher.Exists,
		})
	}
	return out
}

func actionFromDTO(d actionDTO) (domain.Action, error) {
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

func actionToDTO(a domain.Action) actionDTO {
	switch a.Kind {
	case domain.ActionRespond:
		if a.Respond == nil {
			return actionDTO{}
		}
		return actionDTO{Respond: &respondDTO{
			Status: a.Respond.Status, Headers: a.Respond.Headers, Body: string(a.Respond.Body),
			Template: a.Respond.Template, LatencyMS: a.Respond.LatencyMS,
		}}
	case domain.ActionProxy:
		if a.Proxy == nil {
			return actionDTO{}
		}
		return actionDTO{Proxy: &proxyDTO{
			RewriteRequestScript: a.Proxy.RewriteRequestScript, TransformResponseScript: a.Proxy.TransformResponseScript,
			LatencyMS: a.Proxy.LatencyMS,
		}}
	case domain.ActionFault:
		if a.Fault == nil {
			return actionDTO{}
		}
		return actionDTO{Fault: &faultDTO{Kind: string(a.Fault.Kind), DelayMS: a.Fault.DelayMS}}
	default:
		return actionDTO{}
	}
}

func mockToDTO(m domain.Mock) mockDTO {
	return mockDTO{
		ID: m.ID, Name: m.Name, Priority: m.Priority, Group: m.Group,
		Lifetime: string(m.Lifetime), TTLSeconds: m.TTLSeconds,
		Match: matchToDTO(m.Match), Action: actionToDTO(m.Action),
	}
}

func mockInputFromDTO(partition string, d mockDTO) (usecase.MockInput, error) {
	action, err := actionFromDTO(d.Action)
	if err != nil {
		return usecase.MockInput{}, err
	}
	return usecase.MockInput{
		Partition: partition, Name: d.Name, Priority: d.Priority, Group: d.Group,
		Match: matchFromDTO(d.Match), Action: action, TTLSeconds: d.TTLSeconds,
	}, nil
}

type mockCreator interface {
	Create(ctx context.Context, in usecase.MockInput) (domain.Mock, error)
}

type mockGetter interface {
	Get(ctx context.Context, partition, id string) (domain.Mock, error)
}

type mockLister interface {
	List(ctx context.Context, partition string) ([]domain.Mock, error)
}

type mockUpdater interface {
	Update(ctx context.Context, partition, id string, in usecase.MockInput) (domain.Mock, error)
}

type mockDeleter interface {
	Delete(ctx context.Context, partition, id string) error
}

// ListMocks handles GET /__lyrebird/mocks (contracts/admin-rest.md).
func ListMocks(uc mockLister) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		partition := httpmw.PartitionFromContext(r.Context())
		list, err := uc.List(r.Context(), partition)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err)
			return
		}
		out := make([]mockDTO, len(list))
		for i, m := range list {
			out[i] = mockToDTO(m)
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// CreateMock handles POST /__lyrebird/mocks (contracts/admin-rest.md).
func CreateMock(uc mockCreator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var dto mockDTO
		if err := json.NewDecoder(r.Body).Decode(&dto); err != nil {
			writeJSONError(w, http.StatusBadRequest, err)
			return
		}
		in, err := mockInputFromDTO(httpmw.PartitionFromContext(r.Context()), dto)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err)
			return
		}
		m, err := uc.Create(r.Context(), in)
		if err != nil {
			writeMockError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, mockToDTO(m))
	}
}

// GetMock handles GET /__lyrebird/mocks/{id} (contracts/admin-rest.md).
func GetMock(uc mockGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		partition := httpmw.PartitionFromContext(r.Context())
		m, err := uc.Get(r.Context(), partition, r.PathValue("id"))
		if err != nil {
			writeMockError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, mockToDTO(m))
	}
}

// UpdateMock handles PUT /__lyrebird/mocks/{id} (contracts/admin-rest.md).
func UpdateMock(uc mockUpdater) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var dto mockDTO
		if err := json.NewDecoder(r.Body).Decode(&dto); err != nil {
			writeJSONError(w, http.StatusBadRequest, err)
			return
		}
		partition := httpmw.PartitionFromContext(r.Context())
		in, err := mockInputFromDTO(partition, dto)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err)
			return
		}
		m, err := uc.Update(r.Context(), partition, r.PathValue("id"), in)
		if err != nil {
			writeMockError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, mockToDTO(m))
	}
}

// DeleteMock handles DELETE /__lyrebird/mocks/{id} (contracts/admin-rest.md).
func DeleteMock(uc mockDeleter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		partition := httpmw.PartitionFromContext(r.Context())
		if err := uc.Delete(r.Context(), partition, r.PathValue("id")); err != nil {
			writeMockError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func writeMockError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrInvalidMock):
		writeJSONError(w, http.StatusBadRequest, err)
	case errors.Is(err, domain.ErrSeededMockImmutable):
		writeJSONError(w, http.StatusConflict, err)
	case errors.Is(err, domain.ErrNotFound):
		writeJSONError(w, http.StatusNotFound, err)
	default:
		writeJSONError(w, http.StatusInternalServerError, err)
	}
}
