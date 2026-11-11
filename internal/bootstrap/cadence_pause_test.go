package bootstrap

import (
	"net/http"
	"testing"
	"time"
)

// TestCadencePauseRoutes drives POST /__lyrebird/stream/pause and /resume
// through bootstrap.Run's real mux: the status mapping of AC 1, 2 and 4.
func TestCadencePauseRoutes(t *testing.T) {
	app := startCB5App(t)

	mk := func(name string, cadence any) {
		body := map[string]any{"name": name, "framing": map[string]any{"delimiter": "\r\n"}}
		if cadence != nil {
			body["cadence"] = cadence
		}
		if status, raw := adminCall(t, app, http.MethodPost, "/__lyrebird/stream/endpoints", body); status != http.StatusCreated {
			t.Fatalf("create %s = %d: %s", name, status, raw)
		}
	}
	mk("ticker", map[string]any{"interval": "20ms", "frames": []string{`[{"text":"TICK"}]`}})
	mk("plain", nil)

	call := func(path string, body any) int {
		status, _ := adminCall(t, app, http.MethodPost, path, body)
		return status
	}

	for _, path := range []string{"/__lyrebird/stream/pause", "/__lyrebird/stream/resume"} {
		t.Run(path, func(t *testing.T) {
			if got := call(path, map[string]any{"endpoint": "nosuch"}); got != http.StatusNotFound {
				t.Errorf("unknown endpoint = %d, want 404", got)
			}
			if got := call(path, map[string]any{"endpoint": "ticker"}); got != http.StatusNotFound {
				t.Errorf("unoccupied endpoint = %d, want 404", got)
			}
			if got := call(path, map[string]any{"endpoint": "plain"}); got != http.StatusBadRequest {
				t.Errorf("no cadence = %d, want 400", got)
			}
			if got := call(path, map[string]any{}); got != http.StatusBadRequest {
				t.Errorf("missing endpoint = %d, want 400", got)
			}
			if got := call(path, map[string]any{"endpoint": "ticker", "bogus": 1}); got != http.StatusBadRequest {
				t.Errorf("unknown field = %d, want 400", got)
			}
		})
	}

	client := connectCB5(t, app, "ticker")
	if _, err := client.readFrame(handshakeTimeout); err != nil {
		t.Fatalf("first tick: %v", err)
	}
	if got := call("/__lyrebird/stream/pause", map[string]any{"endpoint": "ticker"}); got != http.StatusNoContent {
		t.Fatalf("pause = %d, want 204", got)
	}
	if got := call("/__lyrebird/stream/pause", map[string]any{"endpoint": "ticker"}); got != http.StatusNoContent {
		t.Fatalf("second pause = %d, want 204 (idempotent)", got)
	}
	if got := call("/__lyrebird/stream/resume", map[string]any{"endpoint": "ticker"}); got != http.StatusNoContent {
		t.Fatalf("resume = %d, want 204", got)
	}
	if got := call("/__lyrebird/stream/resume", map[string]any{"endpoint": "ticker"}); got != http.StatusNoContent {
		t.Fatalf("second resume = %d, want 204 (idempotent)", got)
	}
	// Ticks flow again after resume.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if f, err := client.readFrame(200 * time.Millisecond); err == nil && f == "TICK" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no TICK after resume")
		}
	}
}
