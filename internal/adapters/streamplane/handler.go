package streamplane

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/brienze1/lyrebird/internal/domain"
	"github.com/brienze1/lyrebird/internal/usecase"
)

// mockMatcher is the subset of *usecase.MatchRequest this plane needs, named
// at the point of use per Go convention (the same shape proxy.Handler and
// grpcplane.handler depend on).
//
// It is ExecuteProjected rather than Execute because a byte-stream rule may
// declare its own projection of the frame's bytes, so each candidate can need
// a different MatchInput.Body (003's FR-006).
type mockMatcher interface {
	ExecuteProjected(
		ctx context.Context, partition string, base usecase.MatchInput, p usecase.BodyProjector,
	) (domain.Mock, usecase.MatchInput, bool, error)
}

// trafficRecorder is the subset of *usecase.RecordTraffic this plane needs.
type trafficRecorder interface {
	Execute(ctx context.Context, in usecase.RecordTrafficInput) (domain.TrafficRecord, error)
}

// cadenceResolver is the subset of *usecase.CadenceOverride this plane needs
// at each cadence tick: which mock, if any, currently overrides an
// endpoint's declared cadence. Nil whenever the byte-stream plane is built
// without cadence-override support wired (e.g. a narrower unit test), in
// which case a cadence always resolves to its declared content — today's
// unmodified behaviour.
type cadenceResolver interface {
	Resolve(ctx context.Context, partition, endpoint string) (domain.Mock, bool, error)
}

// Handler turns one inbound frame into the existing match→respond decision
// and records everything that crosses the wire. It holds no per-connection
// state: everything it needs about a connection arrives as an argument, so
// one Handler serves every connection on the plane.
type Handler struct {
	match   mockMatcher
	record  trafficRecorder
	tpl     usecase.Templater
	script  usecase.RespondScriptEval
	bodyCap int64
	clock   usecase.Clock
	cadence cadenceResolver
	log     *slog.Logger
}

// frameProjector is the usecase.BodyProjector for one frame: it answers "what
// body should THIS candidate be evaluated against" by preferring the rule's
// own projection and falling back to the endpoint's default (003's FR-006).
//
// Envelopes are memoised per projection pointer because the common case is
// many candidate rules sharing one endpoint default — projecting the same
// bytes once per candidate would be pure waste on the hot path.
type frameProjector struct {
	frame            []byte
	defaultEnvelope  []byte
	overrideEnvelope map[*domain.Projection][]byte
}

func newFrameProjector(frame []byte, endpointDefault *domain.Projection) (*frameProjector, error) {
	envelope, err := projectFrame(frame, endpointDefault)
	if err != nil {
		return nil, err
	}
	return &frameProjector{
		frame:            frame,
		defaultEnvelope:  envelope,
		overrideEnvelope: map[*domain.Projection][]byte{},
	}, nil
}

// ProjectFor implements usecase.BodyProjector.
func (p *frameProjector) ProjectFor(m domain.Mock) ([]byte, error) {
	if m.Projection == nil {
		return p.defaultEnvelope, nil
	}
	if cached, ok := p.overrideEnvelope[m.Projection]; ok {
		return cached, nil
	}
	envelope, err := projectFrame(p.frame, m.Projection)
	if err != nil {
		return nil, err
	}
	p.overrideEnvelope[m.Projection] = envelope
	return envelope, nil
}

// handleFrame is the per-frame path. It never panics and never hangs
// (FR-027): a recovered panic becomes a recorded internal error and the
// connection stays usable, exactly as grpcplane.handler.handle does for an
// RPC.
func (h *Handler) handleFrame(ctx context.Context, c *conn, frame []byte) {
	defer func() {
		if r := recover(); r != nil {
			h.log.Error("streamplane: recovered panic handling frame", "endpoint", c.endpoint.Name, "panic", r)
			h.recordInbound(ctx, c, frame, domain.DecisionInternalError, nil)
		}
	}()

	start := h.clock.Now()

	// Projected on the PAYLOAD, recorded as the whole frame: a rule matches
	// the content an author wrote, while the traffic log keeps exactly what
	// crossed the wire (see payloadOf).
	projector, err := newFrameProjector(payloadOf(frame, c.endpoint.Framing), c.endpoint.Projection)
	if err != nil {
		h.log.Warn("streamplane: could not project frame", "endpoint", c.endpoint.Name, "err", err)
		h.recordInbound(ctx, c, frame, domain.DecisionInternalError, nil)
		return
	}

	base := usecase.MatchInput{
		Method: domain.StreamMethod,
		Path:   "/" + c.endpoint.Name,
		Host:   domain.StreamHost,
		Header: c.header,
		Body:   projector.defaultEnvelope,
	}

	mock, in, matched, err := h.match.ExecuteProjected(ctx, c.partition, base, projector)
	if err != nil {
		// A script that threw or timed out fails safe: nothing is written,
		// and the recorded decision says why — distinguishable from both a
		// silent endpoint and an unmatched frame.
		decision := domain.DecisionInternalError
		var mockID *string
		if se := scriptErrorOf(err); se != nil {
			decision = domain.DecisionScriptFailed
			id := se.MockID
			mockID = &id
		}
		h.log.Warn("streamplane: matching failed", "endpoint", c.endpoint.Name, "err", err)
		h.recordInbound(ctx, c, frame, decision, mockID)
		return
	}

	if !matched {
		// Write nothing, record it, leave the connection usable (FR-032).
		// A byte stream has no status code to answer with, so inventing any
		// reply would be inventing protocol — and silence is exactly what an
		// unconfigured peripheral looks like. The consumer's own timeout
		// governs from here.
		h.recordInbound(ctx, c, frame, domain.DecisionNotConfigured, nil)
		return
	}

	mockID := mock.ID
	switch mock.Action.Kind {
	case domain.ActionRespond:
		h.serveRespond(ctx, c, frame, mock, in, start)
	case domain.ActionFault:
		h.serveFault(ctx, c, frame, mock, in, start)
	default:
		// ActionProxy, which this plane cannot serve — there is no upstream
		// to forward to. Rules are refused at creation time, so arriving here
		// means the rule predates that check or its endpoint was renamed
		// underneath it. Recorded explicitly, connection left usable.
		h.log.Warn("streamplane: rule action cannot be served on a byte stream",
			"endpoint", c.endpoint.Name, "mock", mock.ID, "action", mock.Action.Kind)
		h.recordInbound(ctx, c, frame, domain.DecisionInternalError, &mockID)
	}
}

// serveRespond builds the answer and queues it.
//
// It goes through usecase.BuildRespondOutputWithScript rather than reading
// Action.Respond.Body directly, which is what makes a mock's respond script
// run on this plane — the gap the gRPC plane left open (FR-010).
func (h *Handler) serveRespond(
	ctx context.Context, c *conn, frame []byte, mock domain.Mock, in usecase.MatchInput, start time.Time,
) {
	mockID := mock.ID
	_, _, body, err := usecase.BuildRespondOutputWithScript(*mock.Action.Respond, mock.Script, in, h.tpl, h.script)
	if err != nil {
		h.log.Warn("streamplane: respond script failed", "endpoint", c.endpoint.Name, "mock", mock.ID, "err", err)
		h.recordInbound(ctx, c, frame, domain.DecisionScriptFailed, &mockID)
		return
	}

	built, err := h.buildAnswer(body, in.Body, c.endpoint.Framing, false)
	if err != nil {
		h.log.Warn("streamplane: could not build answer", "endpoint", c.endpoint.Name, "mock", mock.ID, "err", err)
		h.recordInbound(ctx, c, frame, domain.DecisionInternalError, &mockID)
		return
	}

	h.recordInbound(ctx, c, frame, domain.DecisionMocked, &mockID)
	h.queueAnswer(ctx, c, outbound{
		bytes:        built,
		direction:    domain.StreamDirectionOut,
		decision:     domain.DecisionMocked,
		mockID:       &mockID,
		requestFrame: frame,
		startedAt:    start,
	}, latencyOf(mock.Action.Respond.LatencyMS))
}

// buildAnswer turns a respond body into wire bytes. raw suppresses the
// endpoint's framing terminator, which is how the malformed fault produces an
// unterminated frame.
func (h *Handler) buildAnswer(body, envelope []byte, framing domain.Framing, forceRaw bool) ([]byte, error) {
	spec, err := parseFrameSpec(body)
	if err != nil {
		return nil, err
	}
	if forceRaw {
		spec.Raw = true
	}
	return buildFrame(spec, envelope, framing)
}

// queueAnswer hands a built answer to the writer, after delay when one
// applies. A delay is scheduled on its own goroutine rather than slept in
// line, so one slow rule cannot stall the frames arriving behind it — the
// line is slow, not blocked.
func (h *Handler) queueAnswer(ctx context.Context, c *conn, o outbound, delay time.Duration) {
	if delay <= 0 {
		if err := c.queue(ctx, o); err != nil {
			h.log.Debug("streamplane: answer not delivered", "endpoint", c.endpoint.Name, "err", err)
		}
		return
	}
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		t := time.NewTimer(delay)
		defer t.Stop()
		select {
		case <-c.done:
			return
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if err := c.queue(ctx, o); err != nil {
			h.log.Debug("streamplane: delayed answer not delivered", "endpoint", c.endpoint.Name, "err", err)
		}
	}()
}

func latencyOf(ms *int) time.Duration {
	if ms == nil || *ms <= 0 {
		return 0
	}
	return time.Duration(*ms) * time.Millisecond
}

// scriptErrorOf unwraps a *usecase.ScriptError, or returns nil. A script
// failure is recorded distinctly from an internal error so a test can tell
// "the rule's script blew up" from "Lyrebird could not read the frame".
func scriptErrorOf(err error) *usecase.ScriptError {
	var se *usecase.ScriptError
	if errors.As(err, &se) {
		return se
	}
	return nil
}
