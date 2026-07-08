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
	"net/url"
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
	// AdvanceEphemeralScenario is AdvanceScenario's TOCTOU-safe sibling for
	// ephemeral (domain.LifetimeEphemeral) mocks — see
	// store.AdvanceEphemeralScenario's doc comment for the race it closes
	// against gc.go's sweep, and why seeded mocks must keep using
	// AdvanceScenario instead.
	AdvanceEphemeralScenario(ctx context.Context, partition, mockID string) (int, error)
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
	mitmCA       mitmCA
}

// NewHandler builds the data-plane Handler. serverCtx is the
// process/server-lifetime context (from bootstrap.Run), threaded through to
// a FaultTimeout hang so it can outlive this specific request's own
// ServeHTTP call — see fault.go's serveFault doc comment for why
// r.Context() alone can't be used for that. allowHosts is
// cfg.AllowProxyHosts; empty means every host may be proxied (today's
// behavior, preserved — Principle V: a security feature activates only
// when explicitly configured). mitmCA is nil unless MITM is enabled
// (cfg.MITMEnabled) — ServeHTTP rejects CONNECT with 501 whenever it is nil,
// so the flag being off leaves every other code path provably unchanged.
func NewHandler(
	serverCtx context.Context,
	upstreams upstreamLister, record trafficRecorder, matchReq mockMatcher, tpl usecase.Templater,
	script scriptEvaluator, scenario scenarioAdvancer, engine *Engine, bodyCapBytes int64,
	clock usecase.Clock, log *slog.Logger, allowHosts []string, mitmCA mitmCA,
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
		mitmCA: mitmCA,
	}
}

// ServeHTTP dispatches a CONNECT request to serveConnect (the MITM tunnel
// path) and everything else to serveOne with forwardTo nil (the unmodified
// M1 spy/reverse-proxy path).
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	partition := httpmw.PartitionFromContext(r.Context())
	if r.Method == http.MethodConnect {
		h.serveConnect(w, r, partition)
		return
	}
	h.serveOne(w, r, partition, nil)
}

// serveOne is ServeHTTP's real body — reused as-is for a plain reverse-proxy
// request (forwardTo nil) and for one plaintext HTTP request read off an
// MITM tunnel after TLS termination (forwardTo set to the CONNECT target),
// so the entire match/mock/fault/proxy/record pipeline is identical in both
// modes.
func (h *Handler) serveOne(w http.ResponseWriter, r *http.Request, partition string, forwardTo *url.URL) {
	start := h.clock.Now()

	peeked, body, err := peekBody(r.Body, h.bodyCapBytes)
	if err != nil {
		reqHeaders := map[string][]string(r.Header.Clone())
		h.serveInternalError(w, r, partition, start, reqHeaders, nil, false, 0, nil,
			"peek_body_failed", fmt.Sprintf("failed to read request body: %v", err))
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
		_, _ = io.CopyN(io.Discard, reqBody, h.bodyCapBytes+1)
		reqStoredBody, reqTrunc, reqTotal := reqCapture.Result()
		h.serveInternalError(w, r, partition, start, reqHeaders, reqStoredBody, reqTrunc, reqTotal, nil,
			"match_failed", fmt.Sprintf("request matching failed: %v", err))
		return
	}
	if matched && mock.Action.Kind == domain.ActionRespond && mock.Action.Respond != nil {
		h.serveMocked(w, r, partition, start, reqHeaders, reqBody, reqCapture, mock, in, forwardTo)
		return
	}
	if matched && mock.Action.Kind == domain.ActionFault && mock.Action.Fault != nil {
		h.serveFaulted(w, r, partition, start, reqHeaders, reqBody, reqCapture, mock)
		return
	}
	if matched && mock.Action.Kind == domain.ActionProxy && mock.Action.Proxy != nil {
		mockID := mock.ID
		h.serveProxied(w, r, partition, start, reqHeaders, reqBody, reqCapture, in, mock.Action.Proxy, &mockID, forwardTo)
		return
	}

	h.serveProxied(w, r, partition, start, reqHeaders, reqBody, reqCapture, in, nil, nil, forwardTo)
}

// serveProxied is the real-upstream path — reached both by a bare unmatched
// request (action, matchedMockID nil) and by an explicit action=proxy mock
// match (action carries that mock's rewrite/transform/latency config,
// matchedMockID its id). Unifying both into one method is what lets the
// allow/deny host check (FR-006) and Engine.Forward's rewrite/transform
// hooks apply identically regardless of which path led here. forwardTo is
// non-nil only for a request read off an MITM tunnel (serveConnect) — it
// bypasses h.upstreams.Execute/ResolveUpstream entirely and synthesizes the
// Upstream directly from the CONNECT target, since forward/MITM mode has no
// Upstream record to resolve (data-model.md: "In forward-proxy/MITM mode the
// target is derived from the request").
func (h *Handler) serveProxied(
	w http.ResponseWriter, r *http.Request, partition string, start time.Time,
	reqHeaders map[string][]string, reqBody io.Reader, reqCapture *cappedCapture,
	in usecase.MatchInput, action *domain.ProxyAction, matchedMockID *string, forwardTo *url.URL,
) {
	if !HostAllowed(h.allowHosts, r.Host) {
		h.serveBlocked(w, r, partition, start, reqHeaders, reqBody, reqCapture)
		return
	}

	var upstream domain.Upstream
	if forwardTo != nil {
		upstream = domain.Upstream{TargetURL: forwardTo.Scheme + "://" + forwardTo.Host}
	} else {
		upstreams, err := h.upstreams.Execute(r.Context(), partition)
		if err != nil {
			_, _ = io.CopyN(io.Discard, reqBody, h.bodyCapBytes+1)
			reqStoredBody, reqTrunc, reqTotal := reqCapture.Result()
			h.serveInternalError(w, r, partition, start, reqHeaders, reqStoredBody, reqTrunc, reqTotal, matchedMockID,
				"upstream_lookup_failed", fmt.Sprintf("failed to list upstreams for partition %q: %v", partition, err))
			return
		}

		var found bool
		upstream, found = ResolveUpstream(upstreams, r.Host)
		if !found {
			h.serveNotConfigured(w, r, partition, start, reqHeaders, reqBody, reqCapture)
			return
		}
	}

	rec := h.engine.Forward(w, r, upstream, h.bodyCapBytes, action, in)
	reqStoredBody, reqTrunc, reqTotal := reqCapture.Result()
	flushResponse(w)

	h.recordTraffic(r.Context(), partition, r, start,
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
	mock domain.Mock, in usecase.MatchInput, forwardTo *url.URL,
) {
	respondAction := *mock.Action.Respond
	if mock.Scenario != nil {
		// Ephemeral mocks route through the TOCTOU-guarded sibling: mock is
		// only a snapshot taken at match time, with no re-check here that it
		// still exists, so a concurrent GC sweep (store.
		// PruneExpiredEphemeralMocks) could have deleted it in the meantime.
		// Seeded mocks are never stored in ephemeral_mocks at all, so they
		// keep using the original, unguarded AdvanceScenario.
		var idx int
		var err error
		if mock.Lifetime == domain.LifetimeEphemeral {
			idx, err = h.scenario.AdvanceEphemeralScenario(r.Context(), partition, mock.ID)
		} else {
			idx, err = h.scenario.AdvanceScenario(r.Context(), partition, mock.ID)
		}
		if err != nil {
			_, _ = io.CopyN(io.Discard, reqBody, h.bodyCapBytes+1)
			reqStoredBody, reqTrunc, reqTotal := reqCapture.Result()
			mockID := mock.ID
			h.serveInternalError(w, r, partition, start, reqHeaders, reqStoredBody, reqTrunc, reqTotal, &mockID,
				"scenario_advance_failed", fmt.Sprintf("scenario advance failed for mock %q: %v", mock.ID, err))
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
			h.serveProxied(w, r, partition, start, reqHeaders, reqBody, reqCapture, in, nil, nil, forwardTo)
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
	flushResponse(w)

	respHeaders := make(map[string][]string, len(headers))
	for k, v := range headers {
		respHeaders[k] = []string{v}
	}

	mockID := mock.ID
	h.recordTraffic(r.Context(), partition, r, start,
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
	if status != 0 {
		flushResponse(w)
	}

	mockID := mock.ID
	h.recordTraffic(r.Context(), partition, r, start,
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
	flushResponse(w)

	mid := mockID
	h.recordTraffic(r.Context(), partition, r, start,
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
	flushResponse(w)

	h.recordTraffic(r.Context(), partition, r, start,
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
	flushResponse(w)

	h.recordTraffic(r.Context(), partition, r, start,
		reqHeaders, reqStoredBody, reqTrunc, reqTotal, nil,
		domain.DecisionBlocked, http.StatusForbidden,
		map[string][]string{"Content-Type": {"application/json"}}, respBody, false, int64(len(respBody)))
}

// serveInternalError writes a synthesized 500 for a generic internal
// failure that isn't one of the other specific failure modes handled
// elsewhere in this file — a body-peek I/O failure, a non-script-related
// match failure, an upstream-list lookup failure, or a scenario-advance
// failure. A fail-safe outcome, recorded with DecisionInternalError so it's
// never invisible in the traffic log. errCode is a short, call-site-specific
// identifier (e.g. "peek_body_failed") included in the JSON body to aid
// debugging.
func (h *Handler) serveInternalError(
	w http.ResponseWriter, r *http.Request, partition string, start time.Time,
	reqHeaders map[string][]string, reqStoredBody []byte, reqTrunc bool, reqTotal int64, matchedMockID *string,
	errCode, message string,
) {
	// Marshaling a map[string]string literal cannot fail; the error is
	// deliberately discarded rather than handled.
	respBody, _ := json.Marshal(map[string]string{
		"error":   errCode,
		"message": message,
	})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusInternalServerError)
	_, _ = w.Write(respBody)
	flushResponse(w)

	h.recordTraffic(r.Context(), partition, r, start,
		reqHeaders, reqStoredBody, reqTrunc, reqTotal, matchedMockID,
		domain.DecisionInternalError, http.StatusInternalServerError,
		map[string][]string{"Content-Type": {"application/json"}}, respBody, false, int64(len(respBody)))
}

// flushResponse flushes a written response to the client immediately, if the writer supports it.
func flushResponse(w http.ResponseWriter) {
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func (h *Handler) recordTraffic(
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
