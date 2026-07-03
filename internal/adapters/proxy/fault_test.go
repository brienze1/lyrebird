package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/brienze1/lyrebird/internal/domain"
)

// newFaultServer starts a real TCP server (httptest.NewServer, not
// httptest.ResponseRecorder) since serveFault's reset/timeout/malformed
// paths rely on http.Hijacker, which ResponseRecorder doesn't implement.
func newFaultServer(t *testing.T, fault domain.FaultAction) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveFault(w, r, fault)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func mustGet(t *testing.T, client *http.Client, url string) (*http.Response, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	return client.Do(req)
}

func TestServeFaultDelayRespondsAfterWaiting(t *testing.T) {
	delayMS := 30
	srv := newFaultServer(t, domain.FaultAction{Kind: domain.FaultDelay, DelayMS: &delayMS})

	start := time.Now()
	resp, err := mustGet(t, http.DefaultClient, srv.URL)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if elapsed < time.Duration(delayMS)*time.Millisecond {
		t.Errorf("elapsed = %v, want at least %dms", elapsed, delayMS)
	}
}

func TestServeFaultResetClosesConnectionWithoutResponse(t *testing.T) {
	srv := newFaultServer(t, domain.FaultAction{Kind: domain.FaultReset})

	resp, err := mustGet(t, http.DefaultClient, srv.URL)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err == nil {
		t.Fatal("expected an error from a reset connection, got nil")
	}
}

func TestServeFaultMalformedProducesAnInvalidHTTPResponse(t *testing.T) {
	srv := newFaultServer(t, domain.FaultAction{Kind: domain.FaultMalformed})

	resp, err := mustGet(t, http.DefaultClient, srv.URL)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err == nil {
		t.Fatal("expected the client to reject a malformed response, got nil error")
	}
}

func TestServeFaultUnknownKindDefaultsToOK(t *testing.T) {
	srv := newFaultServer(t, domain.FaultAction{Kind: domain.FaultKind("unknown")})

	resp, err := mustGet(t, http.DefaultClient, srv.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}
