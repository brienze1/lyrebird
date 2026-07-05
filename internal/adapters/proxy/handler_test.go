package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/brienze1/lyrebird/internal/domain"
	"github.com/brienze1/lyrebird/internal/usecase"
)

// fakeUpstreamLister returns upstreams normally, or err if set — letting a
// test simulate an upstream-list lookup failure (site 3 of the
// internal-error traffic-log fix) without a bespoke fake.
type fakeUpstreamLister struct {
	upstreams []domain.Upstream
	err       error
}

func (f fakeUpstreamLister) Execute(_ context.Context, _ string) ([]domain.Upstream, error) {
	return f.upstreams, f.err
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

// fakeScenarioAdvancer returns a fixed (possibly out-of-range) index and nil
// error by default, simulating "a concurrent request already consumed the
// slot MatchRequest's earlier read-only peek saw as available." Setting err
// simulates a scenario-state-store failure (site 4 of the internal-error
// traffic-log fix). AdvanceEphemeralScenario delegates to the same
// idx/err fields by default — a test that needs to simulate the two
// methods diverging (e.g. a not-found from the ephemeral-only guard) can set
// ephemeralIdx/ephemeralErr instead. advanceScenarioCalls/
// advanceEphemeralScenarioCalls are optional *int counters a test can wire
// up to assert which of the two methods serveMocked actually invoked.
type fakeScenarioAdvancer struct {
	idx int
	err error

	ephemeralIdx      int
	ephemeralErr      error
	useEphemeralField bool

	advanceScenarioCalls          *int
	advanceEphemeralScenarioCalls *int
}

func (f fakeScenarioAdvancer) AdvanceScenario(_ context.Context, _, _ string) (int, error) {
	if f.advanceScenarioCalls != nil {
		*f.advanceScenarioCalls++
	}
	return f.idx, f.err
}

func (f fakeScenarioAdvancer) AdvanceEphemeralScenario(_ context.Context, _, _ string) (int, error) {
	if f.advanceEphemeralScenarioCalls != nil {
		*f.advanceEphemeralScenarioCalls++
	}
	if f.useEphemeralField {
		return f.ephemeralIdx, f.ephemeralErr
	}
	return f.idx, f.err
}

// fakeMockMatcher lets a test control mockMatcher.Execute's return value
// directly, including returning a generic (non-*usecase.ScriptError) error —
// the case ServeHTTP must route to serveInternalError's "match_failed" site
// rather than serveScriptFailed.
type fakeMockMatcher struct {
	mock    domain.Mock
	matched bool
	err     error
}

func (f fakeMockMatcher) Execute(_ context.Context, _ string, _ usecase.MatchInput) (domain.Mock, bool, error) {
	return f.mock, f.matched, f.err
}

// errReadCloser is an io.ReadCloser whose Read always fails, used to force
// peekBody's io.ReadAll to error out — the one internal-error site reached
// before mock matching is ever attempted.
type errReadCloser struct{ err error }

func (e errReadCloser) Read(_ []byte) (int, error) { return 0, e.err }
func (e errReadCloser) Close() error               { return nil }

// decodeErrorBody unmarshals a serveInternalError (or similar) JSON error
// body of the form {"error": ..., "message": ...}.
func decodeErrorBody(t *testing.T, body []byte) map[string]string {
	t.Helper()
	var m map[string]string
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("failed to unmarshal error body %q: %v", body, err)
	}
	return m
}

// newTestHandler always wires a fakeUpstreamLister with no configured
// upstreams: every caller in this file exercises the mock-match/scenario
// path, where spy passthrough falling through to "not_configured" (rather
// than a real upstream match) is the correct, intended behavior being
// tested — so upstreams is not parameterized here (unparam).
func newTestHandler(rec *fakeTrafficRecorder, scenario scenarioAdvancer) *Handler {
	return NewHandler(
		context.Background(),
		fakeUpstreamLister{}, rec, nil, noopTemplaterProxy{}, noopScriptEvaluator{}, scenario,
		NewEngine(time.Second, nil, nil), 1<<20, systemClockProxy{}, nil, nil, nil,
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
	h := newTestHandler(rec, fakeScenarioAdvancer{idx: 5})

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
	h.serveMocked(w, req, "default", time.Now(), map[string][]string{}, reqBody, reqCapture, mock, in, nil)

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
	h := newTestHandler(rec, fakeScenarioAdvancer{idx: 0})

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
	h.serveMocked(w, req, "default", time.Now(), map[string][]string{}, reqBody, reqCapture, mock, in, nil)

	if w.Code != http.StatusOK || w.Body.String() != "one" {
		t.Fatalf("status/body = %d/%q, want 200/%q", w.Code, w.Body.String(), "one")
	}
}

// TestServeMockedCallsAdvanceEphemeralScenarioForEphemeralMocks is the
// regression test proving serveMocked's Lifetime branch actually routes
// ephemeral mocks through the TOCTOU-guarded AdvanceEphemeralScenario, not
// the original unguarded AdvanceScenario — the whole point of the fix for
// the GC/AdvanceScenario race (an ephemeral mock deleted by a concurrent GC
// sweep between match time and this call must never resurrect a
// scenario_state row).
func TestServeMockedCallsAdvanceEphemeralScenarioForEphemeralMocks(t *testing.T) {
	rec := &fakeTrafficRecorder{}
	var advanceCalls, advanceEphemeralCalls int
	h := newTestHandler(rec, fakeScenarioAdvancer{
		idx: 0, advanceScenarioCalls: &advanceCalls, advanceEphemeralScenarioCalls: &advanceEphemeralCalls,
	})

	mock := domain.Mock{
		ID: "seq-ephemeral", Lifetime: domain.LifetimeEphemeral,
		Action: domain.Action{Kind: domain.ActionRespond, Respond: &domain.RespondAction{Status: 200}},
		Scenario: &domain.Scenario{
			Responses: []domain.RespondAction{{Status: 200, Body: []byte("one")}},
			OnExhaust: domain.OnExhaustFallthrough,
		},
	}

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.local/seq-ephemeral", nil)
	req.Host = "example.local"
	w := httptest.NewRecorder()

	reqBody, reqCapture := newCappedTee(req.Body, 1<<20)
	in := usecase.MatchInput{Method: req.Method, Path: req.URL.Path}
	h.serveMocked(w, req, "default", time.Now(), map[string][]string{}, reqBody, reqCapture, mock, in, nil)

	if advanceEphemeralCalls != 1 {
		t.Errorf("AdvanceEphemeralScenario calls = %d, want 1", advanceEphemeralCalls)
	}
	if advanceCalls != 0 {
		t.Errorf("AdvanceScenario calls = %d, want 0 — an ephemeral mock must never use the unguarded path", advanceCalls)
	}
	if w.Code != http.StatusOK || w.Body.String() != "one" {
		t.Fatalf("status/body = %d/%q, want 200/%q", w.Code, w.Body.String(), "one")
	}
}

// TestServeMockedCallsAdvanceScenarioForSeededMocks is the companion check:
// a seeded mock (never stored in ephemeral_mocks, so the ephemeral-only
// existence guard would incorrectly reject it) must keep using the original
// AdvanceScenario.
func TestServeMockedCallsAdvanceScenarioForSeededMocks(t *testing.T) {
	rec := &fakeTrafficRecorder{}
	var advanceCalls, advanceEphemeralCalls int
	h := newTestHandler(rec, fakeScenarioAdvancer{
		idx: 0, advanceScenarioCalls: &advanceCalls, advanceEphemeralScenarioCalls: &advanceEphemeralCalls,
	})

	mock := domain.Mock{
		ID: "seq-seeded", Lifetime: domain.LifetimeSeeded,
		Action: domain.Action{Kind: domain.ActionRespond, Respond: &domain.RespondAction{Status: 200}},
		Scenario: &domain.Scenario{
			Responses: []domain.RespondAction{{Status: 200, Body: []byte("one")}},
			OnExhaust: domain.OnExhaustFallthrough,
		},
	}

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.local/seq-seeded", nil)
	req.Host = "example.local"
	w := httptest.NewRecorder()

	reqBody, reqCapture := newCappedTee(req.Body, 1<<20)
	in := usecase.MatchInput{Method: req.Method, Path: req.URL.Path}
	h.serveMocked(w, req, "default", time.Now(), map[string][]string{}, reqBody, reqCapture, mock, in, nil)

	if advanceCalls != 1 {
		t.Errorf("AdvanceScenario calls = %d, want 1", advanceCalls)
	}
	if advanceEphemeralCalls != 0 {
		t.Errorf("AdvanceEphemeralScenario calls = %d, want 0 — a seeded mock must never use the ephemeral-only guarded path", advanceEphemeralCalls)
	}
	if w.Code != http.StatusOK || w.Body.String() != "one" {
		t.Fatalf("status/body = %d/%q, want 200/%q", w.Code, w.Body.String(), "one")
	}
}

// TestServeHTTPRecordsInternalErrorWhenBodyPeekFails is the regression test
// for internal-error site 1: a body-read failure inside peekBody, before
// mock matching is even attempted, must still produce a 500 response and a
// DecisionInternalError traffic record rather than failing silently.
func TestServeHTTPRecordsInternalErrorWhenBodyPeekFails(t *testing.T) {
	rec := &fakeTrafficRecorder{}
	h := NewHandler(
		context.Background(),
		fakeUpstreamLister{}, rec, fakeMockMatcher{}, noopTemplaterProxy{}, noopScriptEvaluator{}, fakeScenarioAdvancer{},
		NewEngine(time.Second, nil, nil), 1<<20, systemClockProxy{}, nil, nil, nil,
	)

	readErr := errors.New("boom: connection reset")
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "http://example.local/x", errReadCloser{err: readErr})
	req.Host = "example.local"
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	respBody := decodeErrorBody(t, w.Body.Bytes())
	if respBody["error"] != "peek_body_failed" {
		t.Errorf("error code = %q, want %q", respBody["error"], "peek_body_failed")
	}
	if !strings.Contains(respBody["message"], "failed to read request body") {
		t.Errorf("message = %q, want it to mention the body read failure", respBody["message"])
	}

	if len(rec.recorded) != 1 {
		t.Fatalf("recorded %d traffic entries, want 1", len(rec.recorded))
	}
	got := rec.recorded[0]
	if got.Decision != domain.DecisionInternalError {
		t.Errorf("recorded decision = %q, want %q", got.Decision, domain.DecisionInternalError)
	}
	if got.Status != http.StatusInternalServerError {
		t.Errorf("recorded status = %d, want 500", got.Status)
	}
	if got.MatchedMockID != nil {
		t.Errorf("recorded MatchedMockID = %v, want nil", got.MatchedMockID)
	}
	if string(got.ResponseBody) != w.Body.String() {
		t.Errorf("recorded response body = %q, want it to match the body written to the client %q", got.ResponseBody, w.Body.String())
	}
}

// TestServeHTTPRecordsInternalErrorWhenMatchFails is the regression test for
// internal-error site 2: a generic (non-*usecase.ScriptError) error from
// mockMatcher.Execute must route to serveInternalError's "match_failed" site
// — not serveScriptFailed, which is reserved for *usecase.ScriptError — and
// still be recorded.
func TestServeHTTPRecordsInternalErrorWhenMatchFails(t *testing.T) {
	rec := &fakeTrafficRecorder{}
	matchErr := errors.New("match store unavailable")
	h := NewHandler(
		context.Background(),
		fakeUpstreamLister{}, rec, fakeMockMatcher{err: matchErr}, noopTemplaterProxy{}, noopScriptEvaluator{}, fakeScenarioAdvancer{},
		NewEngine(time.Second, nil, nil), 1<<20, systemClockProxy{}, nil, nil, nil,
	)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.local/x", nil)
	req.Host = "example.local"
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	respBody := decodeErrorBody(t, w.Body.Bytes())
	if respBody["error"] != "match_failed" {
		t.Errorf("error code = %q, want %q", respBody["error"], "match_failed")
	}
	if !strings.Contains(respBody["message"], "request matching failed") {
		t.Errorf("message = %q, want it to mention the match failure", respBody["message"])
	}

	if len(rec.recorded) != 1 {
		t.Fatalf("recorded %d traffic entries, want 1", len(rec.recorded))
	}
	got := rec.recorded[0]
	if got.Decision != domain.DecisionInternalError {
		t.Errorf("recorded decision = %q, want %q", got.Decision, domain.DecisionInternalError)
	}
	if got.Status != http.StatusInternalServerError {
		t.Errorf("recorded status = %d, want 500", got.Status)
	}
	if got.MatchedMockID != nil {
		t.Errorf("recorded MatchedMockID = %v, want nil", got.MatchedMockID)
	}
}

// TestServeProxiedRecordsInternalErrorWhenUpstreamLookupFails is the
// regression test for internal-error site 3, and specifically for a real
// bug fixed alongside it: matchedMockID used to be hardcoded nil at this
// call site. An explicit action=proxy mock match whose h.upstreams.Execute
// call then fails must still have its real mock ID recorded against
// DecisionInternalError, not a bare-passthrough nil.
func TestServeProxiedRecordsInternalErrorWhenUpstreamLookupFails(t *testing.T) {
	rec := &fakeTrafficRecorder{}
	lookupErr := errors.New("upstreams store unavailable")
	h := NewHandler(
		context.Background(),
		fakeUpstreamLister{err: lookupErr}, rec, fakeMockMatcher{}, noopTemplaterProxy{}, noopScriptEvaluator{}, fakeScenarioAdvancer{},
		NewEngine(time.Second, nil, nil), 1<<20, systemClockProxy{}, nil, nil, nil,
	)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.local/x", nil)
	req.Host = "example.local"
	w := httptest.NewRecorder()

	reqBody, reqCapture := newCappedTee(req.Body, 1<<20)
	in := usecase.MatchInput{Method: req.Method, Path: req.URL.Path}
	mockID := "proxy-mock-1"

	h.serveProxied(w, req, "default", time.Now(), map[string][]string{}, reqBody, reqCapture, in, &domain.ProxyAction{}, &mockID, nil)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	respBody := decodeErrorBody(t, w.Body.Bytes())
	if respBody["error"] != "upstream_lookup_failed" {
		t.Errorf("error code = %q, want %q", respBody["error"], "upstream_lookup_failed")
	}
	if !strings.Contains(respBody["message"], "failed to list upstreams") {
		t.Errorf("message = %q, want it to mention the upstream lookup failure", respBody["message"])
	}

	if len(rec.recorded) != 1 {
		t.Fatalf("recorded %d traffic entries, want 1", len(rec.recorded))
	}
	got := rec.recorded[0]
	if got.Decision != domain.DecisionInternalError {
		t.Errorf("recorded decision = %q, want %q", got.Decision, domain.DecisionInternalError)
	}
	if got.Status != http.StatusInternalServerError {
		t.Errorf("recorded status = %d, want 500", got.Status)
	}
	if got.MatchedMockID == nil || *got.MatchedMockID != mockID {
		t.Errorf("recorded MatchedMockID = %v, want %q — the real matched mock's id must survive an upstream-lookup failure", got.MatchedMockID, mockID)
	}
}

// TestServeMockedRecordsInternalErrorWhenScenarioAdvanceFails is the
// regression test for internal-error site 4, and specifically for a real
// bug fixed alongside it: the &mockID passed to serveInternalError here
// used to be missing/hardcoded nil. A scenario-bearing mock whose
// AdvanceScenario call fails must still record its own mock ID against
// DecisionInternalError.
func TestServeMockedRecordsInternalErrorWhenScenarioAdvanceFails(t *testing.T) {
	rec := &fakeTrafficRecorder{}
	advanceErr := errors.New("scenario state store unavailable")
	h := NewHandler(
		context.Background(),
		fakeUpstreamLister{}, rec, fakeMockMatcher{}, noopTemplaterProxy{}, noopScriptEvaluator{}, fakeScenarioAdvancer{err: advanceErr},
		NewEngine(time.Second, nil, nil), 1<<20, systemClockProxy{}, nil, nil, nil,
	)

	mock := domain.Mock{
		ID: "seq-broken", Action: domain.Action{Kind: domain.ActionRespond, Respond: &domain.RespondAction{Status: 200, Body: []byte("stale")}},
		Scenario: &domain.Scenario{
			Responses: []domain.RespondAction{{Status: 200, Body: []byte("one")}},
			OnExhaust: domain.OnExhaustFallthrough,
		},
	}

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.local/seq-broken", nil)
	req.Host = "example.local"
	w := httptest.NewRecorder()

	reqBody, reqCapture := newCappedTee(req.Body, 1<<20)
	in := usecase.MatchInput{Method: req.Method, Path: req.URL.Path}

	h.serveMocked(w, req, "default", time.Now(), map[string][]string{}, reqBody, reqCapture, mock, in, nil)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	respBody := decodeErrorBody(t, w.Body.Bytes())
	if respBody["error"] != "scenario_advance_failed" {
		t.Errorf("error code = %q, want %q", respBody["error"], "scenario_advance_failed")
	}
	if !strings.Contains(respBody["message"], "scenario advance failed for mock") {
		t.Errorf("message = %q, want it to mention the scenario advance failure", respBody["message"])
	}

	if len(rec.recorded) != 1 {
		t.Fatalf("recorded %d traffic entries, want 1", len(rec.recorded))
	}
	got := rec.recorded[0]
	if got.Decision != domain.DecisionInternalError {
		t.Errorf("recorded decision = %q, want %q", got.Decision, domain.DecisionInternalError)
	}
	if got.Status != http.StatusInternalServerError {
		t.Errorf("recorded status = %d, want 500", got.Status)
	}
	if got.MatchedMockID == nil || *got.MatchedMockID != mock.ID {
		t.Errorf("recorded MatchedMockID = %v, want %q", got.MatchedMockID, mock.ID)
	}
}
