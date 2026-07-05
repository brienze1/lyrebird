package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/brienze1/lyrebird/internal/infra/clock"
	"github.com/brienze1/lyrebird/internal/infra/config"
	"github.com/brienze1/lyrebird/internal/infra/crypto"
	"github.com/brienze1/lyrebird/internal/infra/gc"
	"github.com/brienze1/lyrebird/internal/infra/store"
)

// TestRunStdioServesMCPOverStdinStdout proves the stdio MCP transport mode
// (cfg.MCPStdio → RunStdio, wired from cmd/lyrebird/main.go, contracts/
// mcp-tools.md's "stdio (local)" transport) actually works end-to-end: the
// same core wiring buildCore assembles for Run is reachable over real
// stdin/stdout, not just over sdkmcp.NewInMemoryTransports the way
// internal/adapters/mcp's own tests exercise a *sdkmcp.Server directly.
//
// RunStdio's transport (internal/adapters/mcp/stdio.go's sdkmcp.StdioTransport)
// hardcodes os.Stdin/os.Stdout rather than accepting injectable readers/
// writers, so this test temporarily swaps the process-wide os.Stdin/os.Stdout
// for pipe ends it controls — restored via t.Cleanup regardless of outcome —
// and drives the other end with a genuine sdkmcp.Client, mirroring how
// test/support/steps_mcp.go drives Run's HTTP-mounted MCP endpoint with a
// real client rather than bypassing the transport.
func TestRunStdioServesMCPOverStdinStdout(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Config{
		DBPath:        filepath.Join(dir, "lyrebird.db"),
		SeedDir:       filepath.Join(dir, "config"),
		DefaultSpace:  "default",
		GCInterval:    time.Hour,
		TrafficTTL:    time.Hour,
		ScriptTimeout: time.Second,
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	// stdinR/stdinW stand in for the real process stdin: RunStdio reads from
	// stdinR (as os.Stdin), the test client writes requests to stdinW.
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe (stdin): %v", err)
	}
	// stdoutR/stdoutW stand in for the real process stdout: RunStdio writes
	// responses to stdoutW (as os.Stdout), the test client reads from stdoutR.
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe (stdout): %v", err)
	}
	t.Cleanup(func() {
		_ = stdinW.Close()
		_ = stdoutR.Close()
	})

	origStdin, origStdout := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = stdinR, stdoutW
	t.Cleanup(func() {
		os.Stdin, os.Stdout = origStdin, origStdout
	})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	runErrCh := make(chan error, 1)
	go func() { runErrCh <- RunStdio(ctx, cfg, log) }()

	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "bootstrap-test", Version: "0.0.1"}, nil)
	session, err := client.Connect(ctx, &sdkmcp.IOTransport{Reader: stdoutR, Writer: stdinW}, nil)
	if err != nil {
		t.Fatalf("client.Connect over stdio pipes: %v", err)
	}

	result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "list_spaces"})
	if err != nil {
		t.Fatalf("CallTool(list_spaces) over stdio: %v", err)
	}
	if result.IsError {
		t.Fatalf("list_spaces returned a tool error over stdio: %+v", result.Content)
	}

	// The default space is registered by buildCore itself (shared by both
	// Run and RunStdio), so its presence here proves RunStdio reached the
	// same real core wiring, not a stub.
	structured, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	var out struct {
		Spaces []struct {
			ID string `json:"id"`
		} `json:"spaces"`
	}
	if err := json.Unmarshal(structured, &out); err != nil {
		t.Fatalf("unmarshal list_spaces result %s: %v", structured, err)
	}
	found := false
	for _, s := range out.Spaces {
		if s.ID == "default" {
			found = true
		}
	}
	if !found {
		t.Fatalf("list_spaces over stdio = %s, want the default space present", structured)
	}

	_ = session.Close()

	// Canceling ctx is RunStdio's only shutdown hook (it blocks on
	// mcp.RunStdio, which blocks on srv.Run(ctx, ...) until ctx is done or
	// the transport closes) — assert it actually unblocks and returns
	// promptly instead of hanging forever.
	cancel()
	select {
	case runErr := <-runErrCh:
		if runErr != nil && !errors.Is(runErr, context.Canceled) {
			t.Fatalf("RunStdio returned unexpected error: %v", runErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunStdio did not return within 5s of ctx cancellation")
	}
}

// TestShutdownDrainsBothServersConcurrently is a regression test for a bug
// where App.Shutdown passed both servers' Shutdown calls directly as
// errors.Join arguments: errors.Join(a.controlServer.Shutdown(shCtx),
// a.dataServer.Shutdown(shCtx), a.Store.Close()). Go evaluates function-call
// arguments left-to-right before invoking the function being called, so
// that was NOT concurrent — a.controlServer.Shutdown(shCtx) had to run to
// completion (or hit shCtx's deadline) before a.dataServer.Shutdown(shCtx)
// even started, sharing whatever was left of the same 10s budget. A slow
// control-plane drain could starve the data-plane shutdown (which would
// then return early without having actually finished draining), and
// Store.Close() ran regardless, closing the store out from under a
// still-in-flight data-plane handler.
//
// This proves the fix has teeth by observing the concrete, directly caused
// symptom: http.Server.Shutdown closes its listener synchronously the
// moment it is called (before it ever waits on anything), so the
// data-plane listener must stop accepting brand-new connections almost
// immediately once App.Shutdown is called — not only once the
// control-plane's own (slow) drain has finished. It holds a genuine
// in-flight request open against the control-plane server long enough
// that the old sequential code would still be blocked inside
// controlServer.Shutdown when this test checks, then asserts the
// data-plane listener has already stopped accepting connections well
// before that control-plane drain completes.
func TestShutdownDrainsBothServersConcurrently(t *testing.T) {
	dir := t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	sealer, err := crypto.New(make([]byte, 32))
	if err != nil {
		t.Fatalf("crypto.New: %v", err)
	}
	st, err := store.Open(context.Background(), filepath.Join(dir, "lyrebird.db"), sealer, log)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	gcLoop := gc.New(time.Hour, time.Hour, st, clock.System{}, log) // never Started — Stop() no-ops

	var lc net.ListenConfig
	dataLn, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen data plane: %v", err)
	}
	controlLn, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen control plane: %v", err)
	}

	// controlDrainDelay is how long the in-flight control-plane request
	// takes to finish — long enough that, under the old sequential bug,
	// controlServer.Shutdown(shCtx) would still be blocked waiting for it
	// well past the point this test checks the data-plane listener.
	const controlDrainDelay = 800 * time.Millisecond
	handlerStarted := make(chan struct{})
	var once sync.Once
	controlSrv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		once.Do(func() { close(handlerStarted) })
		time.Sleep(controlDrainDelay)
		w.WriteHeader(http.StatusOK)
	})}
	dataSrv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})}

	go func() { _ = controlSrv.Serve(controlLn) }()
	go func() { _ = dataSrv.Serve(dataLn) }()

	_, cancelServerCtx := context.WithCancel(context.Background())
	app := &App{
		Store:           st,
		GC:              gcLoop,
		dataListener:    dataLn,
		controlListener: controlLn,
		dataServer:      dataSrv,
		controlServer:   controlSrv,
		cancelServerCtx: cancelServerCtx,
	}

	// Fire a real request at the control plane and wait until the handler
	// has actually started running: its connection is now genuinely
	// in-flight and will hold controlServer.Shutdown busy for
	// controlDrainDelay once Shutdown is called.
	reqDone := make(chan struct{})
	go func() {
		defer close(reqDone)
		req, reqErr := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+controlLn.Addr().String()+"/", nil)
		if reqErr != nil {
			return
		}
		resp, getErr := http.DefaultClient.Do(req)
		if getErr == nil {
			_ = resp.Body.Close()
		}
	}()
	select {
	case <-handlerStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("control-plane handler never started")
	}

	shutdownErrCh := make(chan error, 1)
	go func() { shutdownErrCh <- app.Shutdown(context.Background()) }()

	// While the control-plane drain is still in progress (well before
	// controlDrainDelay elapses), the data-plane listener must already be
	// refusing new connections — proving dataServer.Shutdown was invoked
	// concurrently with controlServer.Shutdown, not serialized after it.
	checkWindow := controlDrainDelay - 200*time.Millisecond
	deadline := time.Now().Add(checkWindow)
	dataClosed := false
	dialer := net.Dialer{Timeout: 50 * time.Millisecond}
	for time.Now().Before(deadline) {
		dialCtx, dialCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		conn, dialErr := dialer.DialContext(dialCtx, "tcp", dataLn.Addr().String())
		dialCancel()
		if dialErr != nil {
			dataClosed = true
			break
		}
		_ = conn.Close()
		time.Sleep(10 * time.Millisecond)
	}
	if !dataClosed {
		t.Fatalf(
			"data-plane listener was still accepting new connections %s into Shutdown, while the "+
				"control-plane drain (%s) was still in progress — controlServer.Shutdown and "+
				"dataServer.Shutdown are not running concurrently",
			checkWindow, controlDrainDelay,
		)
	}

	select {
	case shutdownErr := <-shutdownErrCh:
		if shutdownErr != nil {
			t.Fatalf("Shutdown returned unexpected error: %v", shutdownErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Shutdown did not return within 5s")
	}

	<-reqDone
}
