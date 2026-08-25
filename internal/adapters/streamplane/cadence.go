package streamplane

import (
	"context"
	"time"

	"github.com/brienze1/lyrebird/internal/domain"
)

// runCadence emits the endpoint's declared sequence with no inbound frame
// provoking any of it (FR-011), in one of two pacing modes:
//
//   - Interval > 0: a real-time heartbeat, ticking on Lyrebird's own host
//     clock — a keepalive or a telemetry beacon, something that is genuinely
//     periodic in wall time.
//   - Interval == 0 ("immediate"): every frame is queued back-to-back with
//     no wait at all, paced only by the connection's own backpressure
//     (queue's bounded channel and, beneath it, the peer's TCP receive
//     window). This is a source whose bytes are simply *there* for whoever
//     reads them — the classic case being a peripheral that streams
//     continuously in reality, faked by a scenario or a seed that has no
//     clock of its own to synchronise against. A wall-clock tick would make
//     what is available depend on how much real time has passed since
//     occupancy, which is exactly wrong when the only clock that is
//     supposed to matter is a substituted one nobody but the scenario
//     drives (CB5-1 WI-13 correction, cadence used this way for the CB5
//     position source: FR-027, "no assertion can depend on how loaded the
//     machine is").
//
// This is what makes a streaming source honest either way: the real thing
// pushes into a buffer its consumer drains on its own schedule, so modelling
// it as a reply to a read would exercise a different system from the one
// that ships. Every frame goes through the connection's single writer, so a
// tick can never interleave with an answer or an injection (FR-033).
//
// It starts when a stand-in occupies the endpoint and stops when the
// connection ends or the space is reset — nothing survives a reset by being
// mid-stream (FR-031).
func (c *conn) runCadence(ctx context.Context, cad *domain.Cadence) {
	defer c.wg.Done()

	if cad.Interval < 0 || len(cad.Frames) == 0 {
		// Rejected at declaration time; guarded here too so a hand-edited
		// store row cannot misbehave.
		c.log.Warn("streamplane: cadence has a negative interval or no frames, not started", "endpoint", c.endpoint.Name)
		return
	}

	// tick is nil in immediate mode: a nil channel blocks forever in a
	// select, so the branch below simply never waits on it and falls
	// straight to the non-blocking done/ctx check instead.
	var tick <-chan time.Time
	if cad.Interval > 0 {
		ticker := time.NewTicker(cad.Interval)
		defer ticker.Stop()
		tick = ticker.C
	}

	for idx := 0; ; {
		if tick != nil {
			select {
			case <-ctx.Done():
				return
			case <-c.done:
				return
			case <-tick:
			}
		} else {
			select {
			case <-ctx.Done():
				return
			case <-c.done:
				return
			default:
			}
		}

		parts, next, ok := cadenceFrameAt(cad, idx)
		if !ok {
			return // on_exhaustion: stop
		}
		idx = next

		bytes, err := buildFrame(frameSpec{Parts: parts}, nil, c.endpoint.Framing)
		if err != nil {
			// Logged once per tick rather than killing the connection: a bad
			// frame in a sequence is an authoring mistake, not a reason for
			// the stand-in to observe its peripheral being unplugged.
			c.log.Warn("streamplane: cadence frame could not be built",
				"endpoint", c.endpoint.Name, "err", err)
			continue
		}
		// queue() blocks once the outbound channel is full, which — in
		// immediate mode, with no ticker to pace it — is the only thing
		// throttling this loop: real backpressure from the connection
		// (bounded channel depth, then the peer's own TCP receive window),
		// never a clock.
		if err := c.queue(ctx, outbound{
			bytes:     bytes,
			direction: domain.StreamDirectionEmit,
			decision:  domain.DecisionMocked,
		}); err != nil {
			return
		}
	}
}

// cadenceFrameAt resolves which frame a tick at idx emits and what the next
// index is, applying the declared exhaustion behaviour. It returns ok=false
// only for `stop`, which is the one mode that ends the cadence.
//
// It is a pure function of (cadence, idx) so the exhaustion rules are
// testable without a socket, a ticker or a goroutine.
func cadenceFrameAt(cad *domain.Cadence, idx int) (parts []domain.FramePart, next int, ok bool) {
	n := len(cad.Frames)
	if n == 0 {
		return nil, idx, false
	}
	if idx < n {
		return cad.Frames[idx], idx + 1, true
	}
	switch cad.OnExhaust {
	case domain.OnExhaustionLoop:
		return cad.Frames[0], 1, true
	case domain.OnExhaustionStop:
		return nil, idx, false
	default:
		// OnExhaustionRepeatLast, and the unset zero value it defaults to —
		// what a stationary source does (FR-011).
		return cad.Frames[n-1], idx, true
	}
}
