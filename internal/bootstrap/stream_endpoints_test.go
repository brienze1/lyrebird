package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/brienze1/lyrebird/internal/adapters/dto"
	"github.com/brienze1/lyrebird/internal/infra/config"
)

// streamTestSpace is the partition startStreamApp boots into. Kept separate
// from cb5_endpoints_test.go's cb5Space: this file carries no CB5 seed and
// must stay repo-neutral so it cherry-picks standalone onto
// brienze1/lyrebird's origin/cb5-2 for upstream contribution.
const streamTestSpace = "default"

// TestDeleteEndpointRouteAcceptsNameContainingSlash proves the byte-stream
// plane's DELETE /__lyrebird/stream/endpoints/{name...} route captures a
// namespaced endpoint name (e.g. "a/b") in full, rather than truncating it
// at the first "/" the way Go's single-segment {name} wildcard would. Every
// other DeleteEndpoint-adjacent test in this repo calls req.SetPathValue
// directly, bypassing real http.ServeMux pattern matching entirely, so none
// of them could have caught a {name} vs {name...} regression — this one
// drives bootstrap.Run's actual mux over a real socket.
//
// Deliberately repo-neutral (no CB5 seed, no CB5-only fixture): a maintainer
// taking this commit gets a self-contained regression test, not one that
// references a seed file their repo does not have.
func TestDeleteEndpointRouteAcceptsNameContainingSlash(t *testing.T) {
	app := startStreamApp(t)

	create := map[string]any{"name": "a/b", "framing": map[string]any{"delimiter": "\r\n"}}
	if status, raw := streamAdminCall(t, app, http.MethodPost, "/__lyrebird/stream/endpoints", create); status != http.StatusCreated {
		t.Fatalf("POST .../endpoints = %d: %s", status, raw)
	}

	status, raw := streamAdminCall(t, app, http.MethodDelete, "/__lyrebird/stream/endpoints/a/b", nil)
	if status != http.StatusNoContent {
		t.Fatalf("DELETE .../a/b = %d: %s", status, raw)
	}

	status, raw = streamAdminCall(t, app, http.MethodGet, "/__lyrebird/stream/endpoints", nil)
	if status != http.StatusOK {
		t.Fatalf("GET .../endpoints = %d: %s", status, raw)
	}
	var endpoints []dto.EndpointDTO
	if err := json.Unmarshal(raw, &endpoints); err != nil {
		t.Fatalf("unmarshal endpoint list: %v (%s)", err, raw)
	}
	for _, e := range endpoints {
		if e.Name == "a/b" {
			t.Fatalf("endpoint %q still declared after DELETE — the {name...} route did not "+
				"capture the whole name, or Delete truncated it at the first slash", e.Name)
		}
	}
}

// TestCreateEndpointRejectsDotSegmentName proves a name containing a "."
// or ".." path segment is rejected at declaration time, rather than
// admitted and then permanently undeletable: http.ServeMux cleans dot
// segments out of the request path before pattern matching, so DELETE
// /__lyrebird/stream/endpoints/cb5/../evil can never reach an endpoint
// actually named "cb5/../evil".
func TestCreateEndpointRejectsDotSegmentName(t *testing.T) {
	for _, name := range []string{"cb5/../evil", "cb5/./x"} {
		t.Run(name, func(t *testing.T) {
			app := startStreamApp(t)
			create := map[string]any{"name": name, "framing": map[string]any{"delimiter": "\r\n"}}
			status, raw := streamAdminCall(t, app, http.MethodPost, "/__lyrebird/stream/endpoints", create)
			if status != http.StatusBadRequest {
				t.Fatalf("POST .../endpoints (name=%q) = %d, want 400: %s", name, status, raw)
			}
		})
	}
}

// streamAdminCall issues one Admin REST call against app's control plane,
// scoped to streamTestSpace. Duplicated rather than shared with
// cb5_endpoints_test.go's adminCall on purpose: this file must remain
// self-contained for a standalone cherry-pick, without depending on a file
// this branch's CB5-specific commit added.
func streamAdminCall(t *testing.T, app *App, method, path string, body any) (int, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal admin body: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, "http://"+app.ControlAddr()+path, reader)
	if err != nil {
		t.Fatalf("build admin request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-Lyrebird-Space", streamTestSpace)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read admin response: %v", err)
	}
	return resp.StatusCode, raw
}

// startStreamApp boots a real Lyrebird with the byte-stream plane enabled
// and no seed loaded — repo-neutral, unlike cb5_endpoints_test.go's
// startCB5App, which depends on this branch's CB5-only seed file.
func startStreamApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Config{
		DataPlaneAddr:    "127.0.0.1:0",
		ControlPlaneAddr: "127.0.0.1:0",
		StreamPlaneAddr:  "127.0.0.1:0",
		DefaultSpace:     streamTestSpace,
		DBPath:           filepath.Join(dir, "lyrebird.db"),
		SeedDir:          filepath.Join(dir, "config"),
		GCInterval:       time.Hour,
		TrafficTTL:       time.Hour,
		ScriptTimeout:    time.Second,
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	app, err := Run(context.Background(), cfg, log)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	t.Cleanup(func() {
		shCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := app.Shutdown(shCtx); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	})
	return app
}
