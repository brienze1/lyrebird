package proxy

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/brienze1/lyrebird/internal/domain"
	"github.com/brienze1/lyrebird/internal/usecase"
)

// forwardTo drives a full Engine.Forward call against a real *http.Request
// and a ResponseRecorder, mirroring how Handler actually calls Forward —
// exercising dualTransport and applyTransformResponse through the public
// entry point rather than poking either unexported piece directly.
func forwardTo(t *testing.T, e *Engine, upstream domain.Upstream, action *domain.ProxyAction, capBytes int64) Recording {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://placeholder.local/", nil)
	w := httptest.NewRecorder()
	return e.Forward(w, req, upstream, capBytes, action, usecase.MatchInput{Method: req.Method, Path: req.URL.Path})
}

func newTLSDispatchServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("upstream-ok"))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestDualTransportRejectsUntrustedCertWhenTLSSkipVerifyIsFalse is the
// regression test for the security-critical half of dualTransport.RoundTrip:
// inverting its boolean check, or breaking the stateFromCtx lookup, would
// silently disable TLS verification for every upstream call. A request
// against httptest.NewTLSServer's self-signed cert must fail here, since
// that cert is trusted by no root store.
func TestDualTransportRejectsUntrustedCertWhenTLSSkipVerifyIsFalse(t *testing.T) {
	srv := newTLSDispatchServer(t)
	e := NewEngine(time.Second, nil, nil)

	rec := forwardTo(t, e, domain.Upstream{TargetURL: srv.URL, TLSSkipVerify: false}, nil, 1<<20)

	if rec.Status != http.StatusBadGateway {
		t.Errorf("Status = %d, want %d (untrusted cert must be rejected)", rec.Status, http.StatusBadGateway)
	}
	if rec.SynthesizedErrorKind != "unreachable" {
		t.Errorf("SynthesizedErrorKind = %q, want %q", rec.SynthesizedErrorKind, "unreachable")
	}
}

// TestDualTransportAcceptsUntrustedCertWhenTLSSkipVerifyIsTrue is the sibling
// happy-path check: the same untrusted cert must succeed once the operator
// has explicitly opted in via Upstream.TLSSkipVerify.
func TestDualTransportAcceptsUntrustedCertWhenTLSSkipVerifyIsTrue(t *testing.T) {
	srv := newTLSDispatchServer(t)
	e := NewEngine(time.Second, nil, nil)

	rec := forwardTo(t, e, domain.Upstream{TargetURL: srv.URL, TLSSkipVerify: true}, nil, 1<<20)

	if rec.Status != http.StatusOK {
		t.Errorf("Status = %d, want %d", rec.Status, http.StatusOK)
	}
	if string(rec.Body) != "upstream-ok" {
		t.Errorf("Body = %q, want %q", rec.Body, "upstream-ok")
	}
}

// fakeScriptRewriter is a local scriptRewriter fake letting tests control
// EvalTransformResponse's outcome and observe whether it was even called.
// EvalRewriteRequest is a fixed no-op since these tests only exercise the
// response side.
type fakeScriptRewriter struct {
	transformCalls int
	transformErr   error
	transformResp  usecase.TransformedResponse
}

func (f *fakeScriptRewriter) EvalRewriteRequest(_ string, _ usecase.MatchInput) (usecase.RewrittenRequest, error) {
	return usecase.RewrittenRequest{}, nil
}

func (f *fakeScriptRewriter) EvalTransformResponse(_ string, _ usecase.TransformInput) (usecase.TransformedResponse, error) {
	f.transformCalls++
	return f.transformResp, f.transformErr
}

// TestApplyTransformResponseFallsBackToRealBodyWhenScriptErrors covers the
// script-error fallback branch documented on applyTransformResponse: when
// EvalTransformResponse itself errors, the real upstream response must be
// recorded untouched rather than corrupted or truncated.
func TestApplyTransformResponseFallsBackToRealBodyWhenScriptErrors(t *testing.T) {
	const body = "original response body untouched by failing script"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	script := &fakeScriptRewriter{transformErr: errors.New("script exploded")}
	e := NewEngine(time.Second, script, nil)
	action := &domain.ProxyAction{TransformResponseScript: strPtr("this script always errors")}

	rec := forwardTo(t, e, domain.Upstream{TargetURL: srv.URL}, action, 1<<20)

	if script.transformCalls != 1 {
		t.Fatalf("EvalTransformResponse calls = %d, want 1", script.transformCalls)
	}
	if rec.Status != http.StatusCreated {
		t.Errorf("Status = %d, want %d (script error must not affect the real status)", rec.Status, http.StatusCreated)
	}
	if string(rec.Body) != body {
		t.Errorf("Body = %q, want the untouched upstream body %q", rec.Body, body)
	}
	if rec.BodyTruncated {
		t.Error("BodyTruncated = true, want false")
	}
}

// TestApplyTransformResponseOverCapBehavesLikeNoTransformScript covers the
// over-cap fallback branch: when the response body exceeds capBytes, the
// transform script must never even be evaluated, and the recorded outcome
// must be identical to a request with no transform script configured at
// all — proving the skip path reuses the ordinary over-cap behavior rather
// than doing something different or broken.
func TestApplyTransformResponseOverCapBehavesLikeNoTransformScript(t *testing.T) {
	const body = "this response body is deliberately longer than the tiny cap used below"
	const capBytes = int64(5)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	script := &fakeScriptRewriter{transformResp: usecase.TransformedResponse{Status: intPtr(999)}}
	e := NewEngine(time.Second, script, nil)
	upstream := domain.Upstream{TargetURL: srv.URL}

	withTransform := forwardTo(t, e, upstream, &domain.ProxyAction{TransformResponseScript: strPtr("would replace status with 999 if it ran")}, capBytes)
	withoutTransform := forwardTo(t, e, upstream, nil, capBytes)

	if script.transformCalls != 0 {
		t.Fatalf("EvalTransformResponse calls = %d, want 0 (over-cap must skip evaluating the script entirely)", script.transformCalls)
	}
	if withTransform.Status != withoutTransform.Status ||
		withTransform.BodyTruncated != withoutTransform.BodyTruncated ||
		withTransform.BodyTotalSize != withoutTransform.BodyTotalSize ||
		string(withTransform.Body) != string(withoutTransform.Body) {
		t.Errorf("Recording with a (skipped) transform script = %+v, want identical to without one = %+v", withTransform, withoutTransform)
	}
	if !withTransform.BodyTruncated {
		t.Error("BodyTruncated = false, want true (ordinary over-cap behavior)")
	}
}
