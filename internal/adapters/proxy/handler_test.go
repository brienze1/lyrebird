package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/brienze1/lyrebird/internal/domain"
	"github.com/brienze1/lyrebird/internal/usecase"
)

type fakeUpstreamLister struct{ upstreams []domain.Upstream }

func (f fakeUpstreamLister) Execute(_ context.Context, _ string) ([]domain.Upstream, error) {
	return f.upstreams, nil
}

type fakeTrafficRecorder struct{ recorded []usecase.RecordTrafficInput }

func (f *fakeTrafficRecorder) Execute(_ context.Context, in usecase.RecordTrafficInput) (domain.TrafficRecord, error) {
	f.recorded = append(f.recorded, in)
	return domain.TrafficRecord{}, nil
}

type noopScriptEvaluator struct{}

func (noopScriptEvaluator) EvalRespond(_ string, _ usecase.MatchInput) ([]byte, error) {
	return nil, nil
}

type noopTemplaterProxy struct{}

func (noopTemplaterProxy) Render(body []byte, _ usecase.MatchInput) []byte { return body }
func (noopTemplaterProxy) RenderHeaders(h map[string]string, _ usecase.MatchInput) map[string]string {
	return h
}

// fakeScenarioAdvancer always returns a fixed (possibly out-of-range) index,
// simulating "a concurrent request already consumed the slot MatchRequest's
// earlier read-only peek saw as available."
type fakeScenarioAdvancer struct{ idx int }

func (f fakeScenarioAdvancer) AdvanceScenario(_ context.Context, _, _ string) (int, error) {
	return f.idx, nil
}

func newTestHandler(upstreams []domain.Upstream, rec *fakeTrafficRecorder, scenario scenarioAdvancer) *Handler {
	return NewHandler(
		context.Background(),
		fakeUpstreamLister{upstreams: upstreams}, rec, nil, noopTemplaterProxy{}, noopScriptEvaluator{}, scenario,
		NewEngine(time.Second, nil, nil), 1<<20, systemClockProxy{}, nil, nil,
	)
}

type systemClockProxy struct{}

func (systemClockProxy) Now() time.Time { return time.Now() }

// TestServeMockedFallsThroughWhenScenarioExhaustedConcurrently is the
// regression test for a real TOCTOU race: usecase.MatchRequest.Execute's
// read-only ScenarioPeeker check happens before this call, so a concurrent
// request can consume the scenario's last response slot in between —
// meaning by the time Handler.serveMocked's own AdvanceScenario call lands,
// a fallthrough-mode scenario may already be exhausted even though matching
// judged it not-yet-exhausted moments earlier. serveMocked must detect this
// itself and fall through to spy passthrough, not silently serve a stale
// repeated response — the entire point of "fallthrough" is that the mock
// stops intercepting once exhausted, and that guarantee must hold under
// concurrency, not just for a single isolated request.
func TestServeMockedFallsThroughWhenScenarioExhaustedConcurrently(t *testing.T) {
	rec := &fakeTrafficRecorder{}
	// No upstream configured for this host — so falling through correctly
	// resolves to "not_configured" (404), never re-serving the mock.
	h := newTestHandler(nil, rec, fakeScenarioAdvancer{idx: 5})

	mock := domain.Mock{
		ID: "seq", Action: domain.Action{Kind: domain.ActionRespond, Respond: &domain.RespondAction{Status: 200, Body: []byte("stale")}},
		Scenario: &domain.Scenario{
			Responses: []domain.RespondAction{{Status: 200, Body: []byte("one")}},
			OnExhaust: domain.OnExhaustFallthrough,
		},
	}

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.local/seq", nil)
	req.Host = "example.local"
	w := httptest.NewRecorder()

	reqBody, reqCapture := newCappedTee(req.Body, 1<<20)
	in := usecase.MatchInput{Method: req.Method, Path: req.URL.Path}
	h.serveMocked(w, req, "default", time.Now(), map[string][]string{}, reqBody, reqCapture, mock, in)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (not_configured) — a stale scenario response must never be served once exhausted", w.Code)
	}
	if len(rec.recorded) != 1 {
		t.Fatalf("recorded %d traffic entries, want 1", len(rec.recorded))
	}
	if rec.recorded[0].Decision != domain.DecisionNotConfigured {
		t.Errorf("recorded decision = %q, want %q", rec.recorded[0].Decision, domain.DecisionNotConfigured)
	}
	if rec.recorded[0].MatchedMockID != nil {
		t.Errorf("recorded MatchedMockID = %v, want nil (this mock did not actually serve the response)", rec.recorded[0].MatchedMockID)
	}
}

// TestServeMockedServesScenarioResponseWhenNotExhausted is the sibling
// happy-path check: an in-range index still serves the mock's scenario
// response normally.
func TestServeMockedServesScenarioResponseWhenNotExhausted(t *testing.T) {
	rec := &fakeTrafficRecorder{}
	h := newTestHandler(nil, rec, fakeScenarioAdvancer{idx: 0})

	mock := domain.Mock{
		ID: "seq", Action: domain.Action{Kind: domain.ActionRespond, Respond: &domain.RespondAction{Status: 200}},
		Scenario: &domain.Scenario{
			Responses: []domain.RespondAction{{Status: 200, Body: []byte("one")}},
			OnExhaust: domain.OnExhaustFallthrough,
		},
	}

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.local/seq", nil)
	req.Host = "example.local"
	w := httptest.NewRecorder()

	reqBody, reqCapture := newCappedTee(req.Body, 1<<20)
	in := usecase.MatchInput{Method: req.Method, Path: req.URL.Path}
	h.serveMocked(w, req, "default", time.Now(), map[string][]string{}, reqBody, reqCapture, mock, in)

	if w.Code != http.StatusOK || w.Body.String() != "one" {
		t.Fatalf("status/body = %d/%q, want 200/%q", w.Code, w.Body.String(), "one")
	}
}
