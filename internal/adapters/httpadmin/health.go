// Package httpadmin implements Lyrebird's Admin REST control-plane handlers
// (contracts/admin-rest.md). At M0 only health/readiness exist; the rest of
// the surface lands with the use-cases that back it (M1+).
package httpadmin

import (
	"net/http"
	"sync/atomic"
)

// Readiness is a simple flip-once-true gate: it reports ready only after
// every startup step that matters for correctness (store opened, seeds
// loaded) has actually succeeded — not merely that the process is running.
type Readiness struct {
	ready atomic.Bool
}

// MarkReady flips the gate to ready. Called once, after bootstrap succeeds.
func (r *Readiness) MarkReady() { r.ready.Store(true) }

// Ready reports the current state.
func (r *Readiness) Ready() bool { return r.ready.Load() }

// Healthz reports liveness: if the process can answer HTTP at all, it is
// alive. Never gated on anything else, and never authenticated (FR-030).
func Healthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// Readyz reports readiness: only once bootstrap has actually completed.
// Never authenticated (FR-030) — readiness must be checkable without a token
// even when control-plane auth is enabled.
func Readyz(readiness *Readiness) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if !readiness.Ready() {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("not ready"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
	}
}
