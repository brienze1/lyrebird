package proxy

import (
	"context"
	"net"
	"net/http"
	"time"

	"github.com/brienze1/lyrebird/internal/domain"
)

// serveFault injects a chaos-testing failure per fault.Kind instead of a
// normal response (FR-005), and returns the status recorded in traffic: the
// real status sent on the wire for delay (a connection-level fault has no
// HTTP status at all, recorded as 0).
//
// serverCtx (not r.Context()) is what bounds FaultTimeout's hang: net/http
// cancels a request's own context as soon as ServeHTTP returns for that
// request (stdlib behavior, documented on http.Request.Context), which
// would unblock a r.Context()-based hang the instant this call returns —
// defeating the entire point. serverCtx is the process/server-lifetime
// context threaded down from bootstrap.Run, canceled only on real shutdown.
func serveFault(serverCtx context.Context, w http.ResponseWriter, r *http.Request, fault domain.FaultAction) int {
	if fault.DelayMS != nil {
		wait(r, time.Duration(*fault.DelayMS)*time.Millisecond)
	}

	switch fault.Kind {
	case domain.FaultDelay:
		w.WriteHeader(http.StatusOK)
		return http.StatusOK
	case domain.FaultReset:
		hijackAndReset(w)
		return 0
	case domain.FaultTimeout:
		// Genuinely never responds — held open in a background goroutine
		// (see hijackAndHang) rather than blocking this call, so traffic
		// recording for this request still happens promptly even though
		// the connection itself hangs until server shutdown.
		hijackAndHang(serverCtx, w)
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

// hijackAndReset takes over the connection and closes it with SetLinger(0)
// when possible, so the OS sends a real TCP RST instead of a graceful FIN —
// a genuinely distinct wire-level outcome from an ordinary close, which is
// what makes this simulate a connection reset rather than just any
// disconnect. Falls through to writing nothing via the normal
// ResponseWriter (net/http then finishes it as an empty 200) if the
// connection can't be hijacked.
func hijackAndReset(w http.ResponseWriter) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		return
	}
	conn, _, err := hj.Hijack()
	if err != nil {
		return
	}
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetLinger(0)
	}
	_ = conn.Close()
}

// hijackAndHang takes over the connection and holds it open — never writing
// a response, never closing it — until ctx is done (server shutdown),
// simulating a genuine, unbounded hang from the client's point of view.
// Runs the actual hold in a background goroutine so the caller isn't
// blocked for the (potentially unbounded) duration; the one accepted
// trade-off is that this pins a goroutine and a file descriptor per
// in-flight timeout-fault request until the server itself shuts down —
// deliberate, since simulating a true timeout means Lyrebird itself cannot
// be the one to give up.
func hijackAndHang(ctx context.Context, w http.ResponseWriter) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		return
	}
	conn, _, err := hj.Hijack()
	if err != nil {
		return
	}
	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()
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
