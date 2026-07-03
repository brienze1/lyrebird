package proxy

import (
	"context"
	"encoding/json"
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

// Handler is Lyrebird's data-plane entry point — DecideMockOrProxy at M1:
// no MatchRequest use-case exists yet (M2, T028), so every request falls
// through to spy passthrough unconditionally. M2 will insert a mock-match
// check ahead of the ResolveUpstream call below, short-circuiting to a
// mocked response when a mock matches.
type Handler struct {
	upstreams    upstreamLister
	record       trafficRecorder
	engine       *Engine
	bodyCapBytes int64
	clock        usecase.Clock
	log          *slog.Logger
}

// NewHandler builds the data-plane Handler.
func NewHandler(upstreams upstreamLister, record trafficRecorder, engine *Engine, bodyCapBytes int64, clock usecase.Clock, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	return &Handler{upstreams: upstreams, record: record, engine: engine, bodyCapBytes: bodyCapBytes, clock: clock, log: log}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := h.clock.Now()
	partition := httpmw.PartitionFromContext(r.Context())

	reqBody, reqCapture := newCappedTee(r.Body, h.bodyCapBytes)
	r.Body = reqBody
	reqHeaders := map[string][]string(r.Header.Clone())

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

	rec := h.engine.Forward(w, r, upstream, h.bodyCapBytes)
	reqStoredBody, reqTrunc, reqTotal := reqCapture.Result()

	h.recordAsync(r.Context(), partition, r, start,
		reqHeaders, reqStoredBody, reqTrunc, reqTotal,
		domain.DecisionProxied, rec.Status, rec.Headers, rec.Body, rec.BodyTruncated, rec.BodyTotalSize)
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
		reqHeaders, reqStoredBody, reqTrunc, reqTotal,
		domain.DecisionNotConfigured, http.StatusNotFound,
		map[string][]string{"Content-Type": {"application/json"}}, respBody, false, int64(len(respBody)))
}

func (h *Handler) recordAsync(
	ctx context.Context, partition string, r *http.Request, start time.Time,
	reqHeaders map[string][]string, reqBody []byte, reqTrunc bool, reqTotal int64,
	decision domain.Decision, status int, respHeaders map[string][]string, respBody []byte, respTrunc bool, respTotal int64,
) {
	_, err := h.record.Execute(ctx, usecase.RecordTrafficInput{
		Partition: partition, Method: r.Method, Host: r.Host, Path: r.URL.Path,
		RequestHeaders: reqHeaders, RequestBody: reqBody, RequestBodyTruncated: reqTrunc, RequestBodyTotalSize: reqTotal,
		Decision:        decision,
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
