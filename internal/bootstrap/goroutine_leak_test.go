package bootstrap_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/brienze1/lyrebird/internal/bootstrap"
	"github.com/brienze1/lyrebird/internal/infra/config"
	"github.com/brienze1/lyrebird/test/support"
)

// TestNoGoroutineLeakAcrossFullTrafficLifecycle boots a real Lyrebird
// instance (mirroring test/support/steps_disposability.go's bootWithDataKey
// recipe), drives a mixed load of control-plane admin calls and data-plane
// proxy traffic (including a FaultTimeout hijack-and-hang mock and a
// sequenced scenario mock) through the real HTTP wire protocol, then shuts
// the app down and asserts runtime.NumGoroutine() settles back to
// (approximately) its pre-traffic baseline.
//
// This is a permanent regression guard: nothing before this test exercised
// goroutine accounting specifically, so a future change that spawns a
// per-request goroutine without a matching exit path (an unbounded fault
// hang, an un-drained channel reader, a leaked GC ticker, a client/transport
// left with open idle connections, ...) would otherwise only show up as a
// vague "the process grows over time" symptom in production, long after the
// change that caused it landed.
//
// It intentionally does NOT assert exact equality — a handful of stray
// goroutines (Go runtime housekeeping, the test binary's own machinery)
// commonly persist across any two runtime.NumGoroutine() snapshots and are
// not a Lyrebird bug. What it does assert is that the delta stays small and
// bounded regardless of how much traffic was driven — a real leak would
// instead scale with the number of requests/mocks exercised below and never
// come back down.
func TestNoGoroutineLeakAcrossFullTrafficLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("goroutine-settle polling makes this slower than a unit test; skipped in -short")
	}

	dir := t.TempDir()
	seedDir := filepath.Join(dir, "config")
	if err := os.MkdirAll(seedDir, 0o755); err != nil {
		t.Fatalf("mkdir seed dir: %v", err)
	}

	dataKeyB64 := base64.StdEncoding.EncodeToString(make([]byte, 32))

	cfg := config.Config{
		DataPlaneAddr:    "127.0.0.1:0",
		ControlPlaneAddr: "127.0.0.1:0",
		DefaultSpace:     "default",
		TrafficTTL:       100 * time.Millisecond, // short so the GC loop actually prunes something each sweep
		TokenTTL:         time.Hour,
		BodyCapBytes:     1 << 20,
		UpstreamTimeout:  2 * time.Second,
		DBPath:           filepath.Join(dir, "lyrebird.db"),
		SeedDir:          seedDir,
		GCInterval:       150 * time.Millisecond, // short so several real sweeps happen during the test
		DataKeyB64:       dataKeyB64,
		AllowProxyHosts:  nil,
		ScriptTimeout:    100 * time.Millisecond,
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.Background()

	app, err := bootstrap.Run(ctx, cfg, log)
	if err != nil {
		t.Fatalf("bootstrap.Run: %v", err)
	}
	shutDown := false
	t.Cleanup(func() {
		if !shutDown {
			_ = app.Shutdown(context.Background())
		}
	})

	// A dedicated client (keep-alives disabled) for admin + data-plane
	// calls, so this test's own HTTP client doesn't leave idle-connection
	// goroutines in the count we're about to measure.
	client := &http.Client{
		Timeout:   5 * time.Second,
		Transport: &http.Transport{DisableKeepAlives: true},
	}

	// Let the freshly booted server's own goroutines (accept loops, GC
	// loop's first tick, etc.) settle before taking the baseline.
	time.Sleep(150 * time.Millisecond)
	runtime.GC()
	baselineA := runtime.NumGoroutine()
	t.Logf("baseline A (booted, idle): %d goroutines", baselineA)

	// --- Drive a mixed load of traffic ---

	fakeUpstream := support.NewFakeUpstream()
	fakeUpstream.SetResponse(http.StatusOK, []byte("upstream-ok"), nil)

	mustPost := func(url string, body any) {
		t.Helper()
		raw, mErr := json.Marshal(body)
		if mErr != nil {
			t.Fatalf("marshal body for %s: %v", url, mErr)
		}
		req, rErr := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
		if rErr != nil {
			t.Fatalf("build POST request for %s: %v", url, rErr)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, pErr := client.Do(req)
		if pErr != nil {
			t.Fatalf("POST %s: %v", url, pErr)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
			t.Fatalf("POST %s status = %d, want 200/201", url, resp.StatusCode)
		}
	}

	controlBase := fmt.Sprintf("http://%s", app.ControlAddr())
	dataBase := fmt.Sprintf("http://%s", app.DataAddr())

	// 1. Configure an upstream pointing at the fake upstream server.
	mustPost(controlBase+"/__lyrebird/upstreams", map[string]any{
		"match_host":      "api.internal",
		"target_url":      fakeUpstream.URL(),
		"tls_skip_verify": false,
	})

	// 2. Create several regular respond mocks, then delete a few of them.
	const mockCount = 8
	for i := 0; i < mockCount; i++ {
		mustPost(controlBase+"/__lyrebird/mocks", map[string]any{
			"name": fmt.Sprintf("mock-%d", i),
			"match": map[string]any{
				"method": http.MethodGet,
				"path":   fmt.Sprintf("/mock/%d", i),
			},
			"action": map[string]any{
				"respond": map[string]any{"status": 200, "body": fmt.Sprintf("body-%d", i)},
			},
		})
	}
	for i := 0; i < 3; i++ {
		req, rErr := http.NewRequestWithContext(ctx, http.MethodDelete, fmt.Sprintf("%s/__lyrebird/mocks/mock-%d", controlBase, i), nil)
		if rErr != nil {
			t.Fatalf("build delete request: %v", rErr)
		}
		resp, dErr := client.Do(req)
		if dErr != nil {
			t.Fatalf("DELETE mock-%d: %v", i, dErr)
		}
		_ = resp.Body.Close()
	}

	// 3. A FaultTimeout mock — send a couple of requests against it with a
	// client bound to a short timeout so this test never actually waits for
	// the (deliberately unbounded) hang; the fault's own hang is bound to
	// server lifetime and is expected to be reaped by app.Shutdown below,
	// not by anything this test does.
	mustPost(controlBase+"/__lyrebird/mocks", map[string]any{
		"name":   "hangs",
		"match":  map[string]any{"method": http.MethodGet, "path": "/hangs"},
		"action": map[string]any{"fault": map[string]any{"kind": "timeout"}},
	})
	shortTimeoutClient := &http.Client{Timeout: 200 * time.Millisecond, Transport: &http.Transport{DisableKeepAlives: true}}
	for i := 0; i < 2; i++ {
		hangReq, hErr := http.NewRequestWithContext(ctx, http.MethodGet, dataBase+"/hangs", nil)
		if hErr != nil {
			t.Fatalf("build /hangs request: %v", hErr)
		}
		//nolint:bodyclose // the request is expected to error out (client-side timeout) before a body ever exists
		_, _ = shortTimeoutClient.Do(hangReq)
	}

	runtime.GC()

	// 4. A sequenced scenario mock — each real request against it consumes
	// the next slot server-side.
	mustPost(controlBase+"/__lyrebird/mocks", map[string]any{
		"name":  "seq",
		"match": map[string]any{"method": http.MethodGet, "path": "/seq"},
		"action": map[string]any{
			"respond": map[string]any{"status": 200, "body": ""},
		},
		"scenario": map[string]any{
			"responses": []map[string]any{
				{"status": 200, "body": "a"},
				{"status": 200, "body": "b"},
			},
			"on_exhaust": "repeat_last",
		},
	})
	for i := 0; i < 4; i++ {
		seqReq, rErr := http.NewRequestWithContext(ctx, http.MethodGet, dataBase+"/seq", nil)
		if rErr != nil {
			t.Fatalf("build /seq request: %v", rErr)
		}
		resp, gErr := client.Do(seqReq)
		if gErr != nil {
			t.Fatalf("GET /seq (advance %d): %v", i, gErr)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}

	// 5. Proxied requests that hit the fake upstream via the configured
	// upstream (host-based routing, no mock matches these paths).
	for i := 0; i < 10; i++ {
		req, rErr := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/upstream/%d", dataBase, i), nil)
		if rErr != nil {
			t.Fatalf("build proxy request: %v", rErr)
		}
		req.Host = "api.internal"
		resp, gErr := client.Do(req)
		if gErr != nil {
			t.Fatalf("GET /upstream/%d: %v", i, gErr)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}

	runtime.GC()

	// 6. Requests against the surviving regular mocks (3..7 weren't deleted).
	for i := 3; i < mockCount; i++ {
		mockReq, rErr := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/mock/%d", dataBase, i), nil)
		if rErr != nil {
			t.Fatalf("build /mock/%d request: %v", i, rErr)
		}
		resp, gErr := client.Do(mockReq)
		if gErr != nil {
			t.Fatalf("GET /mock/%d: %v", i, gErr)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}

	// Give the GC loop (150ms interval, 100ms traffic TTL) a couple of real
	// sweeps over everything recorded above.
	time.Sleep(400 * time.Millisecond)
	runtime.GC()

	peak := runtime.NumGoroutine()
	t.Logf("peak (post-traffic, pre-shutdown): %d goroutines", peak)

	// --- Shut down and measure settle-back ---

	fakeUpstream.Close()
	client.CloseIdleConnections()
	shortTimeoutClient.CloseIdleConnections()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	shutdownStart := time.Now()
	if shutErr := app.Shutdown(shutdownCtx); shutErr != nil {
		cancel()
		t.Fatalf("Shutdown: %v", shutErr)
	}
	cancel()
	shutDown = true
	shutdownElapsed := time.Since(shutdownStart)
	t.Logf("Shutdown returned after %s (must not hang waiting on the FaultTimeout hijack)", shutdownElapsed)
	if shutdownElapsed > 9*time.Second {
		t.Fatalf("Shutdown took %s — suspiciously close to its own 10s drain budget, "+
			"suggesting something (e.g. the FaultTimeout hijacked connection) is not being released promptly", shutdownElapsed)
	}

	const (
		settleTimeout = 5 * time.Second
		pollInterval  = 100 * time.Millisecond
		stableStreak  = 3
	)
	deadline := time.Now().Add(settleTimeout)
	var final int
	var last = -1
	stable := 0
	for time.Now().Before(deadline) {
		runtime.GC()
		time.Sleep(pollInterval)
		final = runtime.NumGoroutine()
		if final == last {
			stable++
		} else {
			stable = 0
		}
		last = final
		if stable >= stableStreak {
			break
		}
	}
	t.Logf("final settled B (post-shutdown): %d goroutines (baseline A was %d, peak was %d)", final, baselineA, peak)

	// A small, stable delta (test-runner/runtime housekeeping) is normal.
	// It must NOT scale with the ~30 requests / 8 mocks / upstream calls
	// driven above.
	const tolerance = 5
	if delta := final - baselineA; delta > tolerance {
		buf := make([]byte, 1<<20)
		n := runtime.Stack(buf, true)
		t.Fatalf(
			"goroutine leak suspected: settled count %d is %d above baseline %d (tolerance %d), peak was %d.\n"+
				"Full goroutine dump:\n%s",
			final, delta, baselineA, tolerance, peak, buf[:n],
		)
	}
}
