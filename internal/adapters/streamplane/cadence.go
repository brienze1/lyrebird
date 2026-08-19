package streamplane

import (
	"context"
	"time"

	"github.com/brienze1/lyrebird/internal/domain"
)

// runCadence emits the endpoint's declared sequence on its interval, with no
// inbound frame provoking any of it (FR-011).
//
// This is what makes a streaming source honest: the real thing pushes into a
// buffer its consumer drains on its own schedule, so modelling it as a reply
// to a read would exercise a different system from the one that ships. Every
// frame goes through the connection's single writer, so a tick can never
// interleave with an answer or an injection (FR-033).
//
// It starts when a stand-in occupies the endpoint and stops when the
// connection ends or the space is reset — nothing survives a reset by being
// mid-stream (FR-031).
func (c *conn) runCadence(ctx context.Context, cad *domain.Cadence) {
	defer c.wg.Done()

	if cad.Interval <= 0 || len(cad.Frames) == 0 {
		// Rejected at declaration time; guarded here too so a hand-edited
		// store row cannot spin a goroutine at full speed.
		c.log.Warn("streamplane: cadence has no interval or no frames, not started", "endpoint", c.endpoint.Name)
		return
	}

	ticker := time.NewTicker(cad.Interval)
	defer ticker.Stop()

	for idx := 0; ; {
		select {
		case <-ctx.Done():
			return
		case <-c.done:
			return
		case <-ticker.C:
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
