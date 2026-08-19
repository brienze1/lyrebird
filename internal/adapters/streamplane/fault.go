package streamplane

import (
	"context"
	"time"

	"github.com/brienze1/lyrebird/internal/domain"
	"github.com/brienze1/lyrebird/internal/usecase"
)

// malformedFrameSpec is what a `malformed` fault writes: a short,
// obviously-invented run of bytes, emitted WITHOUT the endpoint's framing
// terminator.
//
// Omitting the terminator is the corruption that matters. It is the one
// defect every framed protocol notices, whatever it carries, so producing it
// needs no knowledge of what the endpoint actually speaks — which is the only
// kind of corruption this plane is allowed to know how to produce (FR-008).
//
// It is the byte-stream counterpart of the HTTP plane's
// hijackAndWriteGarbage, which likewise writes a fixed, plainly-invalid run
// of bytes rather than anything the matched mock declared.
var malformedFrameSpec = []byte(`[{"text":"not a valid frame"}]`)

// serveFault maps the existing domain.FaultKinds onto a byte stream
// (FR-016). All four are reachable by authoring a rule alone, with no source
// file touched (SC-003), because the fault is selected by exactly the same
// matching that selects an ordinary answer (FR-017).
//
//	delay     — the answer is queued late. The line is slow, not broken.
//	reset     — the connection is dropped. The stand-in observes the
//	            peripheral going away; a reconnect is served normally.
//	timeout   — nothing is written. Distinguishable from an unmatched frame
//	            ONLY by the recorded decision, which is precisely why both are
//	            recorded (FR-032).
//	malformed — the answer's bytes are corrupted in the declared way.
func (h *Handler) serveFault(
	ctx context.Context, c *conn, frame []byte, mock domain.Mock, in usecase.MatchInput, start time.Time,
) {
	mockID := mock.ID
	fault := mock.Action.Fault
	h.recordInbound(ctx, c, frame, domain.DecisionFaulted, &mockID)

	switch fault.Kind {
	case domain.FaultTimeout:
		// Deliberately nothing. The stand-in's own timeout governs, which is
		// what a source that has gone silent looks like.
		return

	case domain.FaultReset:
		// An empty frame with closeAfter set goes through the writer rather
		// than closing here, so any frame already queued ahead of it is still
		// delivered — the peripheral goes away after what it had already
		// said, not in the middle of it.
		h.queueAnswer(ctx, c, outbound{
			direction:    domain.StreamDirectionOut,
			decision:     domain.DecisionFaulted,
			mockID:       &mockID,
			requestFrame: frame,
			startedAt:    start,
			closeAfter:   true,
		}, 0)
		return

	case domain.FaultDelay:
		// An empty frame — just the endpoint's terminator — arriving late.
		// A fault action carries no body of its own (domain.Action allows
		// exactly one variant), and inventing content would be inventing
		// protocol. This is the exact counterpart of the HTTP plane's delay
		// fault, which likewise answers with an empty 200 after waiting.
		h.serveFaultFrame(ctx, c, frame, mockID, in, nil, false, latencyOf(fault.DelayMS), start)
		return

	case domain.FaultMalformed:
		h.serveFaultFrame(ctx, c, frame, mockID, in, malformedFrameSpec, true, 0, start)
		return

	default:
		h.log.Warn("streamplane: unknown fault kind", "endpoint", c.endpoint.Name, "kind", fault.Kind)
	}
}

// serveFaultFrame builds and queues the bytes a fault writes. raw suppresses
// the framing terminator, which is what makes a malformed frame malformed.
func (h *Handler) serveFaultFrame(
	ctx context.Context, c *conn, frame []byte, mockID string,
	in usecase.MatchInput, body []byte, raw bool, delay time.Duration, start time.Time,
) {
	built, err := h.buildAnswer(body, in.Body, c.endpoint.Framing, raw)
	if err != nil {
		h.log.Warn("streamplane: could not build fault frame",
			"endpoint", c.endpoint.Name, "mock", mockID, "err", err)
		return
	}
	h.queueAnswer(ctx, c, outbound{
		bytes:        built,
		direction:    domain.StreamDirectionOut,
		decision:     domain.DecisionFaulted,
		mockID:       &mockID,
		requestFrame: frame,
		startedAt:    start,
	}, delay)
}
