package proxy

import (
	"net/http"
	"time"

	"github.com/brienze1/lyrebird/internal/domain"
)

// serveFault injects a chaos-testing failure per fault.Kind instead of a
// normal response (FR-005), and returns the status recorded in traffic:
// the real status sent on the wire for delay (a connection-level fault has
// no HTTP status at all, recorded as 0). Every domain.FaultKind is wired to
// a safe, real implementation here; rich behavioral depth per kind (exact
// protocol-level nuances) is M6's job (tasks.md Phase 9, T051) — proven by
// unit tests, not BDD scenarios, at M2.
func serveFault(w http.ResponseWriter, r *http.Request, fault domain.FaultAction) int {
	if fault.DelayMS != nil {
		wait(r, time.Duration(*fault.DelayMS)*time.Millisecond)
	}

	switch fault.Kind {
	case domain.FaultDelay:
		w.WriteHeader(http.StatusOK)
		return http.StatusOK
	case domain.FaultReset:
		hijackAndClose(w)
		return 0
	case domain.FaultTimeout:
		// Deliberately never writes a response, leaving the client to reach
		// its own timeout — the point of this fault.
		hijackAndClose(w)
		return 0
	case domain.FaultMalformed:
		hijackAndWriteGarbage(w)
		return 0
	default:
		w.WriteHeader(http.StatusOK)
		return http.StatusOK
	}
}

func wait(r *http.Request, d time.Duration) {
	if d <= 0 {
		return
	}
	select {
	case <-r.Context().Done():
	case <-time.After(d):
	}
}

// hijackAndClose takes over the connection and closes it without writing a
// response, simulating a reset or a hang from the client's point of view.
// Falls through to writing nothing via the normal ResponseWriter (net/http
// then finishes it as an empty 200) if the connection can't be hijacked.
func hijackAndClose(w http.ResponseWriter) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		return
	}
	conn, _, err := hj.Hijack()
	if err != nil {
		return
	}
	_ = conn.Close()
}

// hijackAndWriteGarbage writes bytes that are not a valid HTTP response,
// then closes the connection.
func hijackAndWriteGarbage(w http.ResponseWriter) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		return
	}
	conn, rw, err := hj.Hijack()
	if err != nil {
		return
	}
	_, _ = rw.WriteString("not a valid HTTP response\r\n")
	_ = rw.Flush()
	_ = conn.Close()
}
