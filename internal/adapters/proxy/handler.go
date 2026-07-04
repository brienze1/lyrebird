package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/brienze1/lyrebird/internal/adapters/httpmw"
	"github.com/brienze1/lyrebird/internal/domain"
	"github.com/brienze1/lyrebird/internal/usecase"
)

// upstreamLister is the subset of *usecase.ListUpstreams's behavior Handler
// depends on, named at the point of use per Go convention.
type upstreamLister interface {
	Execute(ctx context.Context, partition string) ([]domain.Upstream, error)
}

// trafficRecorder is the subset of *usecase.RecordTraffic's behavior Handler
// depends on.
type trafficRecorder interface {
	Execute(ctx context.Context, in usecase.RecordTrafficInput) (domain.TrafficRecord, error)
}

// mockMatcher is the subset of *usecase.MatchRequest's behavior Handler
// depends on.
type mockMatcher interface {
	Execute(ctx context.Context, partition string, in usecase.MatchInput) (domain.Mock, bool, error)
}

// scriptEvaluator is the subset of usecase.ScriptEval Handler needs
// directly (respond-phase evaluation only — match-phase script failures
// already surface through mockMatcher.Execute's *usecase.ScriptError
// return, since that evaluation happens inside MatchRequest).
type scriptEvaluator interface {
	EvalRespond(src string, in usecase.MatchInput) ([]byte, error)
}

// scenarioAdvancer is the subset of usecase.ScenarioStateRepo Handler needs
// directly — consuming the next response slot once serveMocked has
// committed to answering with a scenario mock (MatchRequest's own
// read-only ScenarioPeeker already handled the "is this candidate
// exhausted" check before matching got this far).
type scenarioAdvancer interface {
	AdvanceScenario(ctx context.Context, partition, mockID string) (int, error)
}

// Handler is Lyrebird's data-plane entry point (T029): a mock-match check
// (US2) runs ahead of spy passthrough (US1). A matched action=respond mock
// is built and written directly — h.upstreams.Execute and h.engine.Forward
// are never called for that request, which is what makes SC-003 (zero
// upstream calls on a mock hit) true structurally, not just by test
// observation. A matched action=fault mock injects a chaos failure. A mock
// whose script (match or respond phase) errors or times out fails safe
// (US4, FR-016) — a synthesized 500 is written and recorded, never a hang
// or a fallthrough to a real upstream. Every other case (action=proxy, or
// no match at all) falls through to the unmodified M1 spy passthrough path
// below.
type Handler struct {
	upstreams    upstreamLister
	record       trafficRecorder
	matchReq     mockMatcher
	tpl          usecase.Templater
	script       scriptEvaluator
	scenario     scenarioAdvancer
	engine       *Engine
	bodyCapBytes int64
	clock        usecase.Clock
	log          *slog.Logger
	allowHosts   []string
	serverCtx    context.Context
}

// NewHandler builds the data-plane Handler. serverCtx is the
// process/server-lifetime context (from bootstrap.Run), threaded through to
// a FaultTimeout hang so it can outlive this specific request's own
// ServeHTTP call — see fault.go's serveFault doc comment for why
// r.Context() alone can't be used for that. allowHosts is
// cfg.AllowProxyHosts; empty means every host may be proxied (today's
// behavior, preserved — Principle V: a security feature activates only
// when explicitly configured).
func NewHandler(
	serverCtx context.Context,
	upstreams upstreamLister, record trafficRecorder, matchReq mockMatcher, tpl usecase.Templater,
	script scriptEvaluator, scenario scenarioAdvancer, engine *Engine, bodyCapBytes int64,
	clock usecase.Clock, log *slog.Logger, allowHosts []string,
) *Handler {
	if log == nil {
		log = slog.Default()
	}
	if serverCtx == nil {
		serverCtx = context.Background()
	}
	return &Handler{
		upstreams: upstreams, record: record, matchReq: matchReq, tpl: tpl, script: script, scenario: scenario,
		engine: engine, bodyCapBytes: bodyCapBytes, clock: clock, log: log, allowHosts: allowHosts, serverCtx: serverCtx,
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := h.clock.Now()
	partition := httpmw.PartitionFromContext(r.Context())

	peeked, body, err := peekBody(r.Body, h.bodyCapBytes)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	r.Body = body

	reqBody, reqCapture := newCappedTee(r.Body, h.bodyCapBytes)
	r.Body = reqBody
	reqHeaders := map[string][]string(r.Header.Clone())

	in := usecase.MatchInput{
		Method: r.Method, Path: r.URL.Path,
		Header: map[string][]string(r.Header), Query: map[string][]string(r.URL.Query()),
		Body: peeked,
	}

	mock, matched, err := h.matchReq.Execute(r.Context(), partition, in)
	if err != nil {
		var serr *usecase.ScriptError
		if errors.As(err, &serr) {
			h.serveScriptFailed(w, r, partition, start, reqHeaders, reqBody, reqCapture, serr)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if matched && mock.Action.Kind == domain.ActionRespond && mock.Action.Respond != nil {
		h.serveMocked(w, r, partition, start, reqHeaders, reqBody, reqCapture, mock, in)
		return
	}
	if matched && mock.Action.Kind == domain.ActionFault && mock.Action.Fault != nil {
		h.serveFaulted(w, r, partition, start, reqHeaders, reqBody, reqCapture, mock)
		return
	}
	if matched && mock.Action.Kind == domain.ActionProxy && mock.Action.Proxy != nil {
		mockID := mock.ID
		h.serveProxied(w, r, partition, start, reqHeaders, reqBody, reqCapture, in, mock.Action.Proxy, &mockID)
		return
	}

	h.serveProxied(w, r, partition, start, reqHeaders, reqBody, reqCapture, in, nil, nil)
}

// serveProxied is the real-upstream path — reached both by a bare unmatched
// request (action, matchedMockID nil) and by an explicit action=proxy mock
// match (action carries that mock's rewrite/transform/latency config,
// matchedMockID its id). Unifying both into one method is what lets the
// allow/deny host check (FR-006) and Engine.Forward's rewrite/transform
// hooks apply identically regardless of which path led here.
func (h *Handler) serveProxied(
	w http.ResponseWriter, r *http.Request, partition string, start time.Time,
	reqHeaders map[string][]string, reqBody io.Reader, reqCapture *cappedCapture,
	in usecase.MatchInput, action *domain.ProxyAction, matchedMockID *string,
) {
	if !HostAllowed(h.allowHosts, r.Host) {
		h.serveBlocked(w, r, partition, start, reqHeaders, reqBody, reqCapture)
		return
	}

	upstreams, err := h.upstreams.Execute(r.Context(), partition)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	upstream, found := ResolveUpstream(upstreams, r.Host)
	if !found {
		h.serveNotConfigured(w, r, partition, start, reqHeaders, reqBody, reqCapture)
		return
	}

	rec := h.engine.Forward(w, r, upstream, h.bodyCapBytes, action, in)
	reqStoredBody, reqTrunc, reqTotal := reqCapture.Result()

	h.recordAsync(r.Context(), partition, r, start,
		reqHeaders, reqStoredBody, reqTrunc, reqTotal, matchedMockID,
		domain.DecisionProxied, rec.Status, rec.Headers, rec.Body, rec.BodyTruncated, rec.BodyTotalSize)
}

// serveMocked writes a matched action=respond mock's response directly and
// records it — no upstream is ever contacted for this request. If the mock
// carries a respond_src script and it fails, this delegates to
// serveScriptFailedBody instead of writing a partial/zero-value response.
// If the mock carries a Scenario, the response it's built from is whichever
// one usecase.ResolveScenarioResponse picks for the slot this call consumes
// (via AdvanceScenario) — mock.Action.Respond itself is then just a
// placeholder that satisfied MockCRUD's write-time validation.
func (h *Handler) serveMocked(
	w http.ResponseWriter, r *http.Request, partition string, start time.Time,
	reqHeaders map[string][]string, reqBody io.Reader, reqCapture *cappedCapture,
	mock domain.Mock, in usecase.MatchInput,
) {
	respondAction := *mock.Action.Respond
	if mock.Scenario != nil {
		idx, err := h.scenario.AdvanceScenario(r.Context(), partition, mock.ID)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if mock.Scenario.OnExhaust == domain.OnExhaustFallthrough && idx >= len(mock.Scenario.Responses) {
			// Lost a race against a concurrent request for this scenario's
			// last response slot: MatchRequest.Execute's read-only peek (via
			// ScenarioPeeker) saw this mock as not-yet-exhausted at match
			// time, but a concurrent request's own AdvanceScenario call
			// consumed the final slot first, so by the time THIS request's
			// AdvanceScenario call landed, the mock genuinely is exhausted.
			// Falling through to spy passthrough here — before the request
			// body has been drained, so it's still fully forwardable — is
			// exactly what a live match-time check would have done had it
			// observed this state, and is what makes fallthrough's
			// "stop matching once exhausted" guarantee hold under
			// concurrency, not just for a single request in isolation.
			h.serveProxied(w, r, partition, start, reqHeaders, reqBody, reqCapture, in, nil, nil)
			return
		}
		respondAction = usecase.ResolveScenarioResponse(*mock.Scenario, idx)
	}

	// Nothing downstream will read the rest of the body — drain only up to
	// one byte past the cap, same discipline as serveNotConfigured.
	_, _ = io.CopyN(io.Discard, reqBody, h.bodyCapBytes+1)
	reqStoredBody, reqTrunc, reqTotal := reqCapture.Result()

	status, headers, respBody, err := usecase.BuildRespondOutputWithScript(respondAction, mock.Script, in, h.tpl, h.script)
	if err != nil {
		h.serveScriptFailedBody(w, r, partition, start, reqHeaders, reqStoredBody, reqTrunc, reqTotal, mock.ID, "respond", err)
		return
	}
	for k, v := range headers {
		w.Header().Set(k, v)
	}
	w.WriteHeader(status)
	_, _ = w.Write(respBody)

	respHeaders := make(map[string][]string, len(headers))
	for k, v := range headers {
		respHeaders[k] = []string{v}
	}

	mockID := mock.ID
	h.recordAsync(r.Context(), partition, r, start,
		reqHeaders, reqStoredBody, reqTrunc, reqTotal, &mockID,
		domain.DecisionMocked, status, respHeaders, respBody, false, int64(len(respBody)))
}

// serveFaulted injects a matched action=fault mock's chaos failure and
// records it.
func (h *Handler) serveFaulted(
	w http.ResponseWriter, r *http.Request, partition string, start time.Time,
	reqHeaders map[string][]string, reqBody io.Reader, reqCapture *cappedCapture,
	mock domain.Mock,
) {
	_, _ = io.CopyN(io.Discard, reqBody, h.bodyCapBytes+1)
	reqStoredBody, reqTrunc, reqTotal := reqCapture.Result()

	status := serveFault(h.serverCtx, w, r, *mock.Action.Fault)

	mockID := mock.ID
	h.recordAsync(r.Context(), partition, r, start,
		reqHeaders, reqStoredBody, reqTrunc, reqTotal, &mockID,
		domain.DecisionFaulted, status, nil, nil, false, 0)
}

// serveScriptFailed handles a match-phase script failure (the *usecase.ScriptError
// returned by mockMatcher.Execute) — the body hasn't been drained yet at
// this point, unlike the respond-phase case which reaches serveScriptFailedBody
// from inside serveMocked after that draining already happened.
func (h *Handler) serveScriptFailed(
	w http.ResponseWriter, r *http.Request, partition string, start time.Time,
	reqHeaders map[string][]string, reqBody io.Reader, reqCapture *cappedCapture,
	serr *usecase.ScriptError,
) {
	_, _ = io.CopyN(io.Discard, reqBody, h.bodyCapBytes+1)
	reqStoredBody, reqTrunc, reqTotal := reqCapture.Result()
	h.serveScriptFailedBody(w, r, partition, start, reqHeaders, reqStoredBody, reqTrunc, reqTotal, serr.MockID, serr.Phase, serr.Err)
}

// serveScriptFailedBody writes a synthesized 500 and records it with
// DecisionScriptFailed — the fail-safe outcome for both match-phase and
// respond-phase script errors (FR-016/SC-010): never a hang, a panic, or a
// silent fallthrough to a real upstream, and the goroutine for this request
// completes exactly like any other, so the server itself is never at risk.
func (h *Handler) serveScriptFailedBody(
	w http.ResponseWriter, r *http.Request, partition string, start time.Time,
	reqHeaders map[string][]string, reqStoredBody []byte, reqTrunc bool, reqTotal int64,
	mockID, phase string, cause error,
) {
	// Marshaling this map literal cannot fail; the error is deliberately
	// discarded rather than handled.
	respBody, _ := json.Marshal(map[string]string{
		"error":   "script_failed",
		"message": fmt.Sprintf("mock %q script (%s) failed: %v", mockID, phase, cause),
	})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusInternalServerError)
	_, _ = w.Write(respBody)

	mid := mockID
	h.recordAsync(r.Context(), partition, r, start,
		reqHeaders, reqStoredBody, reqTrunc, reqTotal, &mid,
		domain.DecisionScriptFailed, http.StatusInternalServerError,
		map[string][]string{"Content-Type": {"application/json"}}, respBody, false, int64(len(respBody)))
}

func (h *Handler) serveNotConfigured(
	w http.ResponseWriter, r *http.Request, partition string, start time.Time,
	reqHeaders map[string][]string, reqBody io.Reader, reqCapture *cappedCapture,
) {
	// Nothing will forward this body, so drain only up to one byte past the
	// cap — enough to know the true size exceeds it, never unbounded. Unlike
	// the proxied path (which always streams the full body to upstream, so
	// its recorded BodyTotalSize is exact), a body larger than cap+1 here
	// has its true size under-reported: reqTotal reflects only what was
	// actually read, not the client's real content length.
	_, _ = io.CopyN(io.Discard, reqBody, h.bodyCapBytes+1)
	reqStoredBody, reqTrunc, reqTotal := reqCapture.Result()

	// Marshaling a map[string]string literal cannot fail; the error is
	// deliberately discarded rather than handled.
	respBody, _ := json.Marshal(map[string]string{
		"error":   "not_configured",
		"message": fmt.Sprintf("no upstream configured for host %q in partition %q", r.Host, partition),
	})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write(respBody)

	h.recordAsync(r.Context(), partition, r, start,
		reqHeaders, reqStoredBody, reqTrunc, reqTotal, nil,
		domain.DecisionNotConfigured, http.StatusNotFound,
		map[string][]string{"Content-Type": {"application/json"}}, respBody, false, int64(len(respBody)))
}

// serveBlocked writes a 403 for a request whose host isn't in the proxy
// allow/deny policy (FR-006) — distinct from serveNotConfigured's 404
// ("nothing is configured for this host"): this is an explicit policy
// denial, refused before ever attempting to resolve an upstream.
func (h *Handler) serveBlocked(
	w http.ResponseWriter, r *http.Request, partition string, start time.Time,
	reqHeaders map[string][]string, reqBody io.Reader, reqCapture *cappedCapture,
) {
	_, _ = io.CopyN(io.Discard, reqBody, h.bodyCapBytes+1)
	reqStoredBody, reqTrunc, reqTotal := reqCapture.Result()

	// Marshaling a map[string]string literal cannot fail; the error is
	// deliberately discarded rather than handled.
	respBody, _ := json.Marshal(map[string]string{
		"error":   "host_not_allowed",
		"message": fmt.Sprintf("host %q is not in the proxy allow list for partition %q", r.Host, partition),
	})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_, _ = w.Write(respBody)

	h.recordAsync(r.Context(), partition, r, start,
		reqHeaders, reqStoredBody, reqTrunc, reqTotal, nil,
		domain.DecisionBlocked, http.StatusForbidden,
		map[string][]string{"Content-Type": {"application/json"}}, respBody, false, int64(len(respBody)))
}

func (h *Handler) recordAsync(
	ctx context.Context, partition string, r *http.Request, start time.Time,
	reqHeaders map[string][]string, reqBody []byte, reqTrunc bool, reqTotal int64, matchedMockID *string,
	decision domain.Decision, status int, respHeaders map[string][]string, respBody []byte, respTrunc bool, respTotal int64,
) {
	_, err := h.record.Execute(ctx, usecase.RecordTrafficInput{
		Partition: partition, Method: r.Method, Host: r.Host, Path: r.URL.Path,
		RequestHeaders: reqHeaders, RequestBody: reqBody, RequestBodyTruncated: reqTrunc, RequestBodyTotalSize: reqTotal,
		Decision: decision, MatchedMockID: matchedMockID,
		ResponseHeaders: respHeaders, ResponseBody: respBody, ResponseBodyTruncated: respTrunc, ResponseBodyTotalSize: respTotal,
		Status: status, LatencyMS: int(h.clock.Now().Sub(start).Milliseconds()),
	})
	if err != nil {
		// Recording must never fail an already-completed HTTP response —
		// losing traffic-log data is acceptable (constitution Principle
		// III); corrupting a live response is not.
		h.log.Warn("proxy: record traffic failed", "err", err)
	}
}

// peekBody reads up to capBytes of body into memory for mock-matching, then
// returns a ReadCloser that replays those bytes followed by the rest of the
// original stream — so every downstream consumer (the capped-tee, then
// either a mocked response or ReverseProxy) sees an untouched, unbounded
// stream. This resolves the tension between mock-matching (needs bytes now)
// and proxy passthrough (needs to stream arbitrarily large bodies without
// buffering them in full).
func peekBody(body io.ReadCloser, capBytes int64) ([]byte, io.ReadCloser, error) {
	peeked, err := io.ReadAll(io.LimitReader(body, capBytes))
	if err != nil {
		return nil, nil, fmt.Errorf("proxy: peek body: %w", err)
	}
	return peeked, readCloser{Reader: io.MultiReader(bytes.NewReader(peeked), body), Closer: body}, nil
}

type readCloser struct {
	io.Reader
	io.Closer
}
