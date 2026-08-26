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
//
// Runtime cadence overrides (WI-02): what a tick actually emits is resolved
// FRESH right before each emission (after the wait, not before it) — the
// endpoint's own declared cadence unless a higher-priority mock is currently
// overriding it — so a tick picks up whatever is winning AT THE MOMENT IT
// FIRES, not whatever was winning when the wait for it began. Resolving
// before the wait instead would make an override posted mid-wait invisible
// to the very tick it should first appear on, showing up only on the tick
// after. Pacing, by contrast, is necessarily set up BEFORE the wait it
// governs, so it follows the PREVIOUS resolution — one tick's-worth stale at
// most, and only when an override actually retunes the interval (content-only
// overrides, the common case, never touch it at all). The declared cadence's
// own Interval/Frames/OnExhaust stay exactly what they always were: an
// override is content-only by default (WI-02 AC-3) and never mutates the
// declaration itself.
func (c *conn) runCadence(ctx context.Context, declared *domain.Cadence) {
	defer c.wg.Done()

	if declared.Interval < 0 || len(declared.Frames) == 0 {
		// Rejected at declaration time; guarded here too so a hand-edited
		// store row cannot misbehave.
		c.log.Warn("streamplane: cadence has a negative interval or no frames, not started", "endpoint", c.endpoint.Name)
		return
	}

	// tick is nil in immediate mode: a nil channel blocks forever in a
	// select, so the branch below simply never waits on it and falls
	// straight to the non-blocking done/ctx check instead. ticker is
	// (re)built only when the effective pacing changes between resolutions,
	// so the common case — no override, or an override that never sets its
	// own interval — never touches it after the first iteration.
	var ticker *time.Ticker
	var tick <-chan time.Time
	paced := time.Duration(-1) // sentinel: forces the ticker to be set up on the first iteration
	defer func() {
		if ticker != nil {
			ticker.Stop()
		}
	}()

	idx := 0
	identity := ""
	// eff seeds the very first iteration's pacing decision with the declared
	// cadence, since nothing has been resolved yet at that point.
	eff := effectiveCadence{cadence: declared, identity: declaredCadenceIdentity}

	for {
		if eff.cadence.Interval != paced {
			paced = eff.cadence.Interval
			if ticker != nil {
				ticker.Stop()
				ticker = nil
			}
			if paced > 0 {
				ticker = time.NewTicker(paced)
				tick = ticker.C
			} else {
				tick = nil
			}
		}

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

		eff = c.resolveCadence(ctx, declared)
		if eff.cadence.Interval < 0 || len(eff.cadence.Frames) == 0 {
			// Same defensive guard as above, for an override whose content
			// somehow reached here malformed (write-time validation already
			// rejects this — a hand-edited store row is the only way in).
			c.log.Warn("streamplane: effective cadence has a negative interval or no frames, not started",
				"endpoint", c.endpoint.Name)
			return
		}
		if eff.identity != identity {
			// A switch between sources — declared to override, override to
			// declared, or one override to a different one — starts the
			// sequence over: resuming mid-index into content that was never
			// sequenced would be arbitrary, not a continuation of anything.
			idx = 0
			identity = eff.identity
		}

		parts, next, ok := cadenceFrameAt(eff.cadence, idx)
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
		// never a clock. Recorded exactly like a seeded tick — direction
		// EMIT, decision mocked — whether or not an override is currently
		// winning: overriding what a cadence emits changes the content, never
		// the traffic shape (CB5-54 caution).
		if err := c.queue(ctx, outbound{
			bytes:     bytes,
			direction: domain.StreamDirectionEmit,
			decision:  domain.DecisionMocked,
		}); err != nil {
			return
		}
	}
}

// effectiveCadence is what runCadence actually emits from at a given moment,
// resolved fresh each tick: either the endpoint's own declared cadence, or
// the winning active cadence-action mock's content merged onto it. identity
// distinguishes sources so a switch between them — an override taking over,
// being deleted, or a different mock outranking the current one — is
// detectable between ticks, which is what tells the loop above to restart
// the frame index at 0 rather than resume mid-sequence into content that was
// never sequenced.
type effectiveCadence struct {
	cadence  *domain.Cadence
	identity string
}

// declaredCadenceIdentity is the stable identity used whenever no override is
// active, so two consecutive resolutions that both fall back to the declared
// cadence are recognised as the SAME source (no index reset) even though a
// fresh *domain.Cadence is never re-allocated for it.
const declaredCadenceIdentity = "declared"

// resolveEffectiveCadence merges an active override mock onto the endpoint's
// declared cadence (WI-02 AC-3's content-only default: Frames always comes
// from the override, Interval/OnExhaust only when the override sets them).
// A nil active mock — or one carrying no cadence action, a defensive check
// for a hand-edited store row — means nothing currently overrides the
// endpoint, so the declared cadence is used as-is.
//
// Pure: no I/O, no clock, no socket — which is what makes it, and
// cadenceFrameAt beside it, testable without any of those (WI-02's Test
// Plan item 1).
func resolveEffectiveCadence(declared *domain.Cadence, active *domain.Mock) effectiveCadence {
	if active == nil || active.Action.Cadence == nil {
		return effectiveCadence{cadence: declared, identity: declaredCadenceIdentity}
	}
	ov := active.Action.Cadence
	merged := &domain.Cadence{
		Frames:    ov.Frames,
		Interval:  declared.Interval,
		OnExhaust: declared.OnExhaust,
	}
	if ov.Interval != nil {
		merged.Interval = *ov.Interval
	}
	if ov.OnExhaust != "" {
		merged.OnExhaust = ov.OnExhaust
	}
	return effectiveCadence{cadence: merged, identity: "mock:" + active.ID}
}

// resolveCadence asks the handler's cadence resolver (if wired) which mock,
// if any, currently overrides this endpoint's cadence in this connection's
// space, and merges it onto declared. A nil resolver — a byte-stream plane
// built without cadence-override support wired, which every cadence test
// that predates WI-02 exercises — behaves exactly as if nothing ever
// overrides: today's behaviour, unchanged. A resolution error is logged once
// and treated the same as "no override", rather than killing the
// connection over what is, from the stand-in's point of view, still a
// perfectly healthy peripheral.
func (c *conn) resolveCadence(ctx context.Context, declared *domain.Cadence) effectiveCadence {
	if c.handler == nil || c.handler.cadence == nil {
		return effectiveCadence{cadence: declared, identity: declaredCadenceIdentity}
	}
	m, ok, err := c.handler.cadence.Resolve(ctx, c.partition, c.endpoint.Name)
	if err != nil {
		c.log.Warn("streamplane: cadence override lookup failed, using the declared cadence",
			"endpoint", c.endpoint.Name, "err", err)
		return effectiveCadence{cadence: declared, identity: declaredCadenceIdentity}
	}
	if !ok {
		return effectiveCadence{cadence: declared, identity: declaredCadenceIdentity}
	}
	return resolveEffectiveCadence(declared, &m)
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
