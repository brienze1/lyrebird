package streamplane

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/brienze1/lyrebird/internal/domain"
)

// outboundQueueDepth bounds how many frames may be waiting for the writer.
// It is small on purpose: a stand-in that has stopped reading should make an
// emitter block briefly rather than let Lyrebird buffer an unbounded backlog
// of frames nobody will ever receive.
const outboundQueueDepth = 64

// outbound is one frame queued for delivery, already built. Building happens
// at queue time rather than at write time so that a malformed frame spec
// fails where the caller can see it — an injection returns the error to its
// HTTP caller, a cadence logs it once — instead of dying silently inside the
// writer goroutine.
type outbound struct {
	bytes []byte
	// direction is OUT for an answer to an inbound frame, EMIT for anything
	// unprompted (FR-013).
	direction string
	decision  domain.Decision
	mockID    *string
	// requestFrame is the inbound frame an OUT answers, recorded alongside it
	// so one traffic row shows the exchange the way the other planes do.
	requestFrame []byte
	// startedAt is when that inbound frame arrived, so latency_ms measures
	// what a consumer actually waited — including any delay fault.
	startedAt time.Time
	// closeAfter drops the connection once this frame is written. Only the
	// reset fault sets it, and it sets it with no bytes at all.
	closeAfter bool
	// result is how writeLoop reports this specific frame's write-and-record
	// outcome back to queue, which sets it on every call — buffered by one so
	// writeLoop's send can never block on it.
	result chan error
}

// conn is one stand-in's live connection to one endpoint.
//
// Every byte leaving Lyrebird on this socket passes through the single
// writeLoop goroutine fed by out. That is what makes FR-033 true: an answer,
// a cadence tick and an injection cannot interleave mid-frame, and the order
// they were queued in is the order they arrive in — which is in turn what
// makes SC-006's "ten identical runs" achievable at all.
type conn struct {
	nc        net.Conn
	partition string
	endpoint  domain.Endpoint
	header    map[string][]string

	out  chan outbound
	done chan struct{}
	once sync.Once

	// wg tracks the writer and cadence goroutines so close returns only once
	// nothing is still trying to use the socket.
	wg sync.WaitGroup

	handler  *Handler
	registry *Registry
	log      *slog.Logger
}

func newConn(nc net.Conn, partition string, endpoint domain.Endpoint, hs handshake, h *Handler, reg *Registry, log *slog.Logger) *conn {
	return &conn{
		nc:        nc,
		partition: partition,
		endpoint:  endpoint,
		header:    hs.Header,
		out:       make(chan outbound, outboundQueueDepth),
		done:      make(chan struct{}),
		handler:   h,
		registry:  reg,
		log:       log,
	}
}

// serve runs the connection until the stand-in disconnects, the frame reader
// fails, or close is called. It owns the writer and cadence goroutines and
// does not return until both have stopped.
func (c *conn) serve(ctx context.Context, capBytes int64) {
	c.wg.Add(1)
	go c.writeLoop()

	if c.endpoint.Cadence != nil {
		c.wg.Add(1)
		go c.runCadence(ctx, c.endpoint.Cadence)
	}

	defer func() {
		c.close()
		c.wg.Wait()
	}()

	fr := newFrameReader(c.nc, c.endpoint.Framing, capBytes)
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.done:
			return
		default:
		}

		frame, err := fr.Next()
		if err != nil {
			if errors.Is(err, ErrFrameTooLarge) {
				// Recoverable: the oversized run is abandoned and the reader
				// resynchronises, so the connection keeps serving (FR-034).
				c.handler.recordOversized(ctx, c)
				continue
			}
			if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
				c.log.Debug("streamplane: connection read ended", "endpoint", c.endpoint.Name, "err", err)
			}
			return
		}
		c.handler.handleFrame(ctx, c, frame)
	}
}

// writeLoop is the connection's single writer. Nothing else ever touches
// c.nc for writing.
func (c *conn) writeLoop() {
	defer c.wg.Done()
	for {
		select {
		case <-c.done:
			return
		case o := <-c.out:
			var werr error
			if len(o.bytes) > 0 {
				if _, err := c.nc.Write(o.bytes); err != nil {
					c.log.Debug("streamplane: write failed", "endpoint", c.endpoint.Name, "err", err)
					werr = err
				}
			}
			if werr == nil {
				// Recorded from inside the writer so the traffic log's order
				// matches the wire's order, not the order handlers happened to
				// finish in.
				c.handler.recordOutbound(context.WithoutCancel(context.Background()), c, o)
			}
			// Reported before closeAfter/the write-failure path tears the
			// connection down, so a queueAndWait caller learns the outcome
			// exactly once, whichever it is. Nil for every fire-and-forget
			// queue() caller (an answer, a cadence tick) — see queueAndWait.
			if o.result != nil {
				o.result <- werr
			}
			if werr != nil {
				c.close()
				return
			}
			if o.closeAfter {
				c.close()
				return
			}
		}
	}
}

// queue hands a built frame to the writer and returns once it is accepted
// onto the outbound channel — NOT once it has actually reached the wire.
// This is what an answer to an inbound frame and a cadence tick both want:
// both run inline on a goroutine with other work still to do on this
// connection (the read loop, the next tick), and waiting for the physical
// write to complete would re-couple exactly what the channel's buffering
// exists to decouple — a slow reader stalling the connection's own read
// loop, not just its writer. See queueAndWait for the call site that
// deliberately gives up that decoupling for a stronger guarantee.
//
// It reports an error rather than blocking forever when the connection is
// gone, so a caller is told plainly instead of hanging.
func (c *conn) queue(ctx context.Context, o outbound) error {
	// Checked BEFORE the select, not only inside it. c.out is buffered, so on
	// an already-closed connection both cases are ready at once and Go picks
	// between them at random — which would let an injection into a dead
	// stand-in succeed roughly half the time, exactly the silent success
	// FR-015 forbids.
	select {
	case <-c.done:
		return c.disconnectedErr()
	default:
	}

	select {
	case <-c.done:
		return c.disconnectedErr()
	case <-ctx.Done():
		return ctx.Err()
	case c.out <- o:
		return nil
	}
}

// queueAndWait is queue, plus blocking until this specific frame has
// actually been written to the wire and recorded — not merely accepted onto
// the outbound channel for eventual delivery by writeLoop. That is the
// control-plane contract an unprompted injection makes to its HTTP caller
// (and to cb5-e2e's harness, which reads a served frame back with no retry
// budget of its own on the strength of it — test/integration/steps/
// stream_steps_test.go's pollUtil comment: "the plane records a frame before
// the step that caused it returns"): by the time this call returns, whatever
// it queued is already observable on GET /traffic.
//
// Only emit() uses this. An answer to an inbound frame and a cadence tick
// deliberately keep queue's weaker, non-blocking guarantee instead — see
// queue's own comment for why re-coupling them to the physical write would
// be a regression, not a fix.
func (c *conn) queueAndWait(ctx context.Context, o outbound) error {
	select {
	case <-c.done:
		return c.disconnectedErr()
	default:
	}

	result := make(chan error, 1)
	o.result = result

	select {
	case <-c.done:
		return c.disconnectedErr()
	case <-ctx.Done():
		return ctx.Err()
	case c.out <- o:
	}

	// Waits for writeLoop to actually process THIS frame — the whole point:
	// queuing it is not the same as it being on the wire yet. If the
	// connection closes with the frame still sitting unprocessed in c.out
	// (writeLoop picked the done case over draining the backlog), that is
	// reported the same way an outright rejection would be, never as a
	// silent success.
	select {
	case err := <-result:
		return err
	case <-c.done:
		return c.disconnectedErr()
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *conn) disconnectedErr() error {
	return fmt.Errorf("%w: the stand-in on endpoint %q has disconnected", domain.ErrNotFound, c.endpoint.Name)
}

// emit builds an unprompted frame from a frame spec and, unless an opted-in
// rule answers it first (handleEmission, CB5-15 WI-04), queues it through
// queueAndWait — the control-plane injection path (FR-012), which is the one
// caller that needs the frame confirmed written and recorded, not merely
// accepted, before it hands control back (see queueAndWait).
func (c *conn) emit(ctx context.Context, frameSpecJSON []byte) error {
	spec, err := parseFrameSpec(frameSpecJSON)
	if err != nil {
		return err
	}
	// An emission has no triggering frame, so there is no envelope for a
	// copyFrom to resolve against; buildFrame renders such a part as nothing.
	bytes, err := buildFrame(spec, nil, c.endpoint.Framing)
	if err != nil {
		return err
	}
	if c.handler.handleEmission(ctx, c, bytes) {
		// A rule answered inline: nothing reaches the peer, and both the
		// EMIT record and its synthetic IN answer are already written.
		return nil
	}
	return c.queueAndWait(ctx, outbound{
		bytes:     bytes,
		direction: domain.StreamDirectionEmit,
		// AC-2 (CB5-15 WI-04): this used to be unconditionally
		// DecisionMocked, which made every plain injection lie about being
		// answered by a rule even with zero rules installed. A plain
		// injection with no opted-in rule passes straight through, so it is
		// not_configured — the same label an unmatched inbound frame gets.
		decision: domain.DecisionNotConfigured,
	})
}

// close is idempotent and safe from any goroutine. It releases the
// endpoint's claim so a stand-in can reconnect immediately, then drops the
// socket, which also unblocks the reader.
func (c *conn) close() {
	c.once.Do(func() {
		close(c.done)
		c.registry.release(c.partition, c.endpoint.Name, c)
		_ = c.nc.Close()
	})
}
