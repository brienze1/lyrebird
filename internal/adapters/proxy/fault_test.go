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
// serverCtx is what bounds a FaultTimeout hang (see serveFault's doc
// comment for why r.Context() can't be used for that) — context.Background()
// is fine for every other fault kind, which never reads it.
func newFaultServer(serverCtx context.Context, t *testing.T, fault domain.FaultAction) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveFault(serverCtx, w, r, fault)
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
	srv := newFaultServer(context.Background(), t, domain.FaultAction{Kind: domain.FaultDelay, DelayMS: &delayMS})

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
	srv := newFaultServer(context.Background(), t, domain.FaultAction{Kind: domain.FaultReset})

	start := time.Now()
	resp, err := mustGet(t, http.DefaultClient, srv.URL)
	elapsed := time.Since(start)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err == nil {
		t.Fatal("expected an error from a reset connection, got nil")
	}
	// Reset must fail fast — it's a distinct wire-level outcome from
	// FaultTimeout's genuine, unbounded hang (TestServeFaultTimeoutHangsUntilTheClientGivesUp
	// below), not the same close-immediately behavior under a different name.
	if elapsed > time.Second {
		t.Errorf("reset took %v to fail, want well under 1s (a fast, immediate failure)", elapsed)
	}
}

func TestServeFaultMalformedProducesAnInvalidHTTPResponse(t *testing.T) {
	srv := newFaultServer(context.Background(), t, domain.FaultAction{Kind: domain.FaultMalformed})

	resp, err := mustGet(t, http.DefaultClient, srv.URL)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err == nil {
		t.Fatal("expected the client to reject a malformed response, got nil error")
	}
}

func TestServeFaultUnknownKindDefaultsToOK(t *testing.T) {
	srv := newFaultServer(context.Background(), t, domain.FaultAction{Kind: domain.FaultKind("unknown")})

	resp, err := mustGet(t, http.DefaultClient, srv.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

// TestServeFaultTimeoutHangsUntilTheClientGivesUp proves FaultTimeout is
// genuinely distinct from FaultReset: no bytes arrive within a bounded
// client-side deadline (a fast reset would fail almost immediately, well
// under that deadline), and the held-open connection is only ever released
// when serverCtx is canceled — never on its own, and never because this
// request's own handler returned (see serveFault's doc comment for why
// r.Context() couldn't be used for this instead).
func TestServeFaultTimeoutHangsUntilTheClientGivesUp(t *testing.T) {
	serverCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := newFaultServer(serverCtx, t, domain.FaultAction{Kind: domain.FaultTimeout})

	clientCtx, clientCancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer clientCancel()
	req, err := http.NewRequestWithContext(clientCtx, http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	elapsed := time.Since(start)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err == nil {
		t.Fatal("expected the client's own timeout to fire, got a response")
	}
	if elapsed < 150*time.Millisecond {
		t.Errorf("client failed after only %v (client timeout was 200ms) — too fast to be a genuine hang", elapsed)
	}
}
