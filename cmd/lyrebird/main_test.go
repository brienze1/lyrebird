package main

import (
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestRunReturnsZeroOnCleanStdioSignalShutdown proves the exit-code fix for
// stdio mode: an ordinary SIGINT/SIGTERM during LYREBIRD_MCP_STDIO=true is a
// clean, intentional shutdown (bootstrap.RunStdio returns context.Canceled
// once signal.NotifyContext's ctx fires — see internal/bootstrap/app_test.go's
// TestRunStdioServesMCPOverStdinStdout, which proves that return value), so
// run() must report exit code 0, not 1, for it.
//
// This mirrors internal/bootstrap/app_test.go's stdio pipe-swap pattern
// (RunStdio hardcodes os.Stdin/os.Stdout, so the test temporarily swaps them
// for pipe ends it controls, restored via t.Cleanup) but drives shutdown with
// a genuine OS signal sent to the current process, exactly like main.go's own
// signal.NotifyContext(..., syscall.SIGINT, syscall.SIGTERM) listens for,
// rather than a context.CancelFunc.
func TestRunReturnsZeroOnCleanStdioSignalShutdown(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LYREBIRD_MCP_STDIO", "true")
	t.Setenv("LYREBIRD_DB_PATH", filepath.Join(dir, "lyrebird.db"))
	t.Setenv("LYREBIRD_SEED_DIR", filepath.Join(dir, "config"))
	t.Setenv("LYREBIRD_GC_INTERVAL", "1h")

	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe (stdin): %v", err)
	}
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

	// Pre-register a SIGINT listener before run() ever gets a chance to
	// install its own via signal.NotifyContext. Once any handler is
	// registered for a signal, the Go runtime suppresses that signal's
	// default disposition (process termination) for the whole process —
	// this guards against the SIGINT below killing the test binary outright
	// if it happens to race ahead of run()'s own registration.
	guard := make(chan os.Signal, 1)
	signal.Notify(guard, syscall.SIGINT)
	t.Cleanup(func() { signal.Stop(guard) })

	codeCh := make(chan int, 1)
	go func() { codeCh <- run() }()

	// Give run() time to reach its own signal.NotifyContext registration
	// (a handful of fast, non-blocking statements at the very top of run(),
	// well before RunStdio's slower store/seed setup) before signaling it.
	time.Sleep(300 * time.Millisecond)

	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatalf("send SIGINT to self: %v", err)
	}

	select {
	case code := <-codeCh:
		if code != 0 {
			t.Fatalf("run() = %d after a clean SIGINT shutdown in stdio mode, want 0", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run() did not return within 5s of SIGINT")
	}
}

// TestRunReturnsOneOnConfigLoadFailure proves the exit-code fix above did not
// make run() swallow genuine failures: an invalid LYREBIRD_MCP_STDIO value
// fails config.Load() itself (see internal/infra/config/config.go's
// strconv.ParseBool check) before signal handling or bootstrap ever run, and
// must still exit 1.
func TestRunReturnsOneOnConfigLoadFailure(t *testing.T) {
	t.Setenv("LYREBIRD_MCP_STDIO", "not-a-bool")

	if code := run(); code != 1 {
		t.Fatalf("run() = %d for an invalid LYREBIRD_MCP_STDIO config, want 1", code)
	}
}
