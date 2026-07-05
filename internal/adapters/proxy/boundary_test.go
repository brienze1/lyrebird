package proxy

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/brienze1/lyrebird/internal/domain"
	"github.com/brienze1/lyrebird/internal/usecase"
)

// TestRequestBodyCapExactBoundary drives a real POST body of size
// capBytes-1, capBytes, and capBytes+1 through the full Handler.ServeHTTP
// (proxied) path against a real upstream, and checks the recorded
// RequestBodyTruncated flag matches captee.go's own documented convention
// (cappedCapture.Result: "truncated = total > cap", not >=). Per
// engine.go's applyTransformResponse doc comment, "exactly capBytes" must
// never be misclassified as truncated.
func TestRequestBodyCapExactBoundary(t *testing.T) {
	const capBytes = int64(16)

	var gotBodyLen int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, 0, 64)
		buf := make([]byte, 64)
		for {
			n, err := r.Body.Read(buf)
			body = append(body, buf[:n]...)
			if err != nil {
				break
			}
		}
		gotBodyLen = len(body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(srv.Close)

	cases := []struct {
		name        string
		size        int64
		wantTrunc   bool
		wantTotal   int64
		description string
	}{
		{"CapMinusOne", capBytes - 1, false, capBytes - 1, "one byte under the cap must never be reported as truncated"},
		{"ExactlyCap", capBytes, false, capBytes, "exactly at the cap must NOT be reported as truncated (boundary is > cap, not >=)"},
		{"CapPlusOne", capBytes + 1, true, capBytes + 1, "one byte over the cap must be reported as truncated"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &fakeTrafficRecorder{}
			h := NewHandler(
				context.Background(),
				fakeUpstreamLister{upstreams: []domain.Upstream{{MatchHost: "example.local", TargetURL: srv.URL}}},
				rec, fakeMockMatcher{}, noopTemplaterProxy{}, noopScriptEvaluator{}, fakeScenarioAdvancer{},
				NewEngine(time.Second, nil, nil), capBytes, systemClockProxy{}, nil, nil, nil,
			)

			payload := bytes.Repeat([]byte("x"), int(tc.size))
			req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "http://example.local/echo", bytes.NewReader(payload))
			req.Host = "example.local"
			w := httptest.NewRecorder()

			h.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
			}
			if int64(gotBodyLen) != tc.size {
				t.Fatalf("upstream received %d bytes, want %d (body never truncated in flight to upstream)", gotBodyLen, tc.size)
			}
			if len(rec.recorded) != 1 {
				t.Fatalf("recorded %d traffic entries, want 1", len(rec.recorded))
			}
			got := rec.recorded[0]
			if got.RequestBodyTruncated != tc.wantTrunc {
				t.Errorf("RequestBodyTruncated = %v, want %v: %s", got.RequestBodyTruncated, tc.wantTrunc, tc.description)
			}
			if got.RequestBodyTotalSize != tc.wantTotal {
				t.Errorf("RequestBodyTotalSize = %d, want %d", got.RequestBodyTotalSize, tc.wantTotal)
			}
			wantStoredLen := tc.size
			if wantStoredLen > capBytes {
				wantStoredLen = capBytes
			}
			if int64(len(got.RequestBody)) != wantStoredLen {
				t.Errorf("len(RequestBody) = %d, want %d (never more than capBytes buffered)", len(got.RequestBody), wantStoredLen)
			}
		})
	}
}

// TestApplyTransformResponseAtExactCapRunsScript is the sibling boundary
// check to TestApplyTransformResponseOverCapBehavesLikeNoTransformScript: a
// response body of EXACTLY capBytes must still have its transform_response
// script evaluated (not skipped), and must not be reported as truncated —
// per the doc comment on applyTransformResponse ("len(peeked) > capBytes
// only when there truly was more data ... matches captee.go's own total >
// cap convention for 'truncated,' not >=").
func TestApplyTransformResponseAtExactCapRunsScript(t *testing.T) {
	const capBytes = int64(5)

	cases := []struct {
		name      string
		bodyLen   int
		wantCalls int
		wantTrunc bool
	}{
		{"CapMinusOne", int(capBytes) - 1, 1, false},
		{"ExactlyCap", int(capBytes), 1, false},
		{"CapPlusOne", int(capBytes) + 1, 0, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := bytes.Repeat([]byte("y"), tc.bodyLen)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(body)
			}))
			t.Cleanup(srv.Close)

			script := &fakeScriptRewriter{transformResp: usecase.TransformedResponse{Status: intPtr(999)}}
			e := NewEngine(time.Second, script, nil)
			upstream := domain.Upstream{TargetURL: srv.URL}

			rec := forwardTo(t, e, upstream, &domain.ProxyAction{TransformResponseScript: strPtr("would replace status with 999 if it ran")}, capBytes)

			if script.transformCalls != tc.wantCalls {
				t.Errorf("EvalTransformResponse calls = %d, want %d (bodyLen=%d, capBytes=%d)", script.transformCalls, tc.wantCalls, tc.bodyLen, capBytes)
			}
			if rec.BodyTruncated != tc.wantTrunc {
				t.Errorf("BodyTruncated = %v, want %v (bodyLen=%d, capBytes=%d)", rec.BodyTruncated, tc.wantTrunc, tc.bodyLen, capBytes)
			}
			if tc.wantCalls == 1 && rec.Status != 999 {
				t.Errorf("Status = %d, want 999 (script should have run and applied its status override)", rec.Status)
			}
		})
	}
}
