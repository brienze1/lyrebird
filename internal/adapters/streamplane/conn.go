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
			if len(o.bytes) > 0 {
				if _, err := c.nc.Write(o.bytes); err != nil {
					c.log.Debug("streamplane: write failed", "endpoint", c.endpoint.Name, "err", err)
					c.close()
					return
				}
			}
			// Recorded from inside the writer so the traffic log's order
			// matches the wire's order, not the order handlers happened to
			// finish in.
			c.handler.recordOutbound(context.WithoutCancel(context.Background()), c, o)
			if o.closeAfter {
				c.close()
				return
			}
		}
	}
}

// queue hands a built frame to the writer. It reports an error rather than
// blocking forever when the connection is gone, so a caller (an injection,
// most importantly) is told plainly instead of hanging.
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

func (c *conn) disconnectedErr() error {
	return fmt.Errorf("%w: the stand-in on endpoint %q has disconnected", domain.ErrNotFound, c.endpoint.Name)
}

// emit builds and queues an unprompted frame from a frame spec — the path
// both control-plane injection and the cadence take (FR-012, FR-011).
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
	return c.queue(ctx, outbound{
		bytes:     bytes,
		direction: domain.StreamDirectionEmit,
		decision:  domain.DecisionMocked,
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
