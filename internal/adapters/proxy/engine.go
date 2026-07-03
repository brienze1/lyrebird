// Package proxy implements Lyrebird's data-plane spy/passthrough engine:
// resolving the configured Upstream for an incoming request and forwarding
// to it verbatim via net/http/httputil.ReverseProxy, per
// specs/001-lyrebird/contracts/data-plane.md.
package proxy

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	"github.com/brienze1/lyrebird/internal/domain"
)

type proxyCtxKey struct{}

// proxyState is the single per-request accumulator threaded through
// Rewrite/ModifyResponse/ErrorHandler via the outbound request's context —
// the standard idiom for using one long-lived *httputil.ReverseProxy against
// many dynamic backends. It is written by exactly one goroutine (the one
// handling this request) for the duration of one ServeHTTP call, so no
// synchronization is needed.
type proxyState struct {
	upstream domain.Upstream
	capBytes int64

	status  int
	headers map[string][]string

	// respCapture is set by modifyResponse once a real upstream response is
	// received. Its Result() isn't valid until the client's copy loop has
	// finished draining resp.Body, which happens inside ReverseProxy after
	// modifyResponse returns — so Forward reads it only after ServeHTTP
	// itself returns.
	respCapture *cappedCapture

	// Set directly by errorHandler instead (no real response body exists to
	// capture when the backend was never reached).
	respBody  []byte
	respTrunc bool
	respTotal int64
	errorKind string // "" | "timeout" | "unreachable"
}

// Recording is what Forward reports back once the exchange is complete.
type Recording struct {
	Status               int
	Headers              map[string][]string
	Body                 []byte
	BodyTruncated        bool
	BodyTotalSize        int64
	SynthesizedErrorKind string
}

// Engine forwards requests to a resolved Upstream and reports what happened
// for recording. One Engine is reused across all requests.
type Engine struct {
	rp *httputil.ReverseProxy
}

// NewEngine builds an Engine. upstreamTimeout bounds dial + response-header
// wait per upstream call (contract: unreachable/timeout -> synthesize
// 502/504) without limiting how long a legitimately slow body download may
// take once headers have arrived.
func NewEngine(upstreamTimeout time.Duration) *Engine {
	dialer := &net.Dialer{Timeout: upstreamTimeout}
	verify := &http.Transport{
		DialContext:           dialer.DialContext,
		ResponseHeaderTimeout: upstreamTimeout,
		ForceAttemptHTTP2:     true,
	}
	skipVerify := &http.Transport{
		DialContext:           dialer.DialContext,
		ResponseHeaderTimeout: upstreamTimeout,
		ForceAttemptHTTP2:     true,
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // operator opt-in per Upstream.TLSSkipVerify
	}

	e := &Engine{}
	e.rp = &httputil.ReverseProxy{
		Transport:      &dualTransport{verify: verify, skipVerify: skipVerify},
		Rewrite:        e.rewrite,
		ModifyResponse: e.modifyResponse,
		ErrorHandler:   e.errorHandler,
	}
	return e
}

// Forward forwards r to upstream, writes the (possibly synthesized)
// response to w, and returns a Recording describing what was sent.
//
// If the client disconnects mid-response, ReverseProxy's internal body-copy
// loop panics with http.ErrAbortHandler (stdlib behavior, not ours) and
// net/http's server recovers it silently — Forward never returns for that
// request, so the caller never gets to record it at all, not even a partial
// entry. Acceptable under Principle III (disposable traffic log), but worth
// knowing before assuming every request that reaches Forward gets recorded.
func (e *Engine) Forward(w http.ResponseWriter, r *http.Request, upstream domain.Upstream, capBytes int64) Recording {
	st := &proxyState{upstream: upstream, capBytes: capBytes}
	e.rp.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), proxyCtxKey{}, st)))

	rec := Recording{
		Status: st.status, Headers: st.headers,
		Body: st.respBody, BodyTruncated: st.respTrunc, BodyTotalSize: st.respTotal,
		SynthesizedErrorKind: st.errorKind,
	}
	if st.respCapture != nil {
		rec.Body, rec.BodyTruncated, rec.BodyTotalSize = st.respCapture.Result()
	}
	return rec
}

func stateFromCtx(ctx context.Context) *proxyState {
	st, _ := ctx.Value(proxyCtxKey{}).(*proxyState)
	return st
}

func (e *Engine) rewrite(pr *httputil.ProxyRequest) {
	st := stateFromCtx(pr.Out.Context())
	target, err := url.Parse(st.upstream.TargetURL)
	if err != nil {
		// SetUpstream already validates URLs at write time; an invalid URL
		// reaching here is defense-in-depth only. Point at an address that
		// will reliably fail to dial, routing this request through the
		// normal ErrorHandler "unreachable" path rather than panicking.
		target = &url.URL{Scheme: "http", Host: "lyrebird-invalid-upstream.invalid"}
	}
	pr.SetURL(target)
}

func (e *Engine) modifyResponse(resp *http.Response) error {
	st := stateFromCtx(resp.Request.Context())
	st.status = resp.StatusCode
	st.headers = map[string][]string(resp.Header.Clone())

	tee, capture := newCappedTee(resp.Body, st.capBytes)
	resp.Body = tee
	st.respCapture = capture
	return nil
}

func (e *Engine) errorHandler(w http.ResponseWriter, r *http.Request, err error) {
	st := stateFromCtx(r.Context())
	status, kind := classifyUpstreamError(err)

	// Marshaling a map[string]string literal cannot fail; the error is
	// deliberately discarded rather than handled.
	body, _ := json.Marshal(map[string]string{"error": kind, "message": err.Error()})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)

	st.status = status
	st.headers = map[string][]string{"Content-Type": {"application/json"}}
	st.respBody = body
	st.respTotal = int64(len(body))
	st.errorKind = kind
}

func classifyUpstreamError(err error) (status int, kind string) {
	if errors.Is(err, context.DeadlineExceeded) {
		return http.StatusGatewayTimeout, "timeout"
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return http.StatusGatewayTimeout, "timeout"
	}
	return http.StatusBadGateway, "unreachable"
}

// dualTransport picks between a TLS-verifying and TLS-skip-verifying
// *http.Transport per request, based on the resolved Upstream.TLSSkipVerify
// — kept as two long-lived transports (not one built per request) so
// connection pooling still works.
type dualTransport struct {
	verify, skipVerify http.RoundTripper
}

func (t *dualTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	st := stateFromCtx(r.Context())
	if st != nil && st.upstream.TLSSkipVerify {
		return t.skipVerify.RoundTrip(r)
	}
	return t.verify.RoundTrip(r)
}
