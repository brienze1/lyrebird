package streamplane

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/brienze1/lyrebird/internal/domain"
	"github.com/brienze1/lyrebird/internal/usecase"
)

// capturingRecorder keeps every traffic record the handler wrote, which is
// how the "silent but recorded" outcomes are asserted: nothing reaches the
// wire, so the record is the only observable.
type capturingRecorder struct {
	mu      sync.Mutex
	records []usecase.RecordTrafficInput
}

func (c *capturingRecorder) Execute(_ context.Context, in usecase.RecordTrafficInput) (domain.TrafficRecord, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.records = append(c.records, in)
	return domain.TrafficRecord{}, nil
}

func (c *capturingRecorder) decisions() []domain.Decision {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]domain.Decision, 0, len(c.records))
	for _, r := range c.records {
		out = append(out, r.Decision)
	}
	return out
}

// stubMatcher returns one fixed outcome, so a handler test isolates the
// handler rather than re-testing MatchRequest.
type stubMatcher struct {
	mock    domain.Mock
	matched bool
	err     error
	// panics makes ExecuteProjected blow up, to prove the recover guard.
	panics bool
}

func (s stubMatcher) ExecuteProjected(
	_ context.Context, _ string, base usecase.MatchInput, p usecase.BodyProjector,
) (domain.Mock, usecase.MatchInput, bool, error) {
	if s.panics {
		panic("deliberate panic from a stub matcher")
	}
	if p != nil {
		if body, err := p.ProjectFor(s.mock); err == nil {
			base.Body = body
		}
	}
	return s.mock, base, s.matched, s.err
}

func newTestHandlerWith(m mockMatcher, rec trafficRecorder) *Handler {
	return &Handler{
		match:  m,
		record: rec,
		clock:  systemClock{},
		log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func respondMock(id, body string) domain.Mock {
	return domain.Mock{
		ID: id, Partition: "default",
		Action: domain.Action{Kind: domain.ActionRespond, Respond: &domain.RespondAction{Body: []byte(body)}},
	}
}

// readOne drains one frame the handler queued, or reports that nothing was
// written within the window.
func readOne(t *testing.T, client io.Reader) (string, bool) {
	t.Helper()
	got := make(chan string, 1)
	go func() {
		buf := make([]byte, 256)
		n, err := client.Read(buf)
		if err == nil && n > 0 {
			got <- string(buf[:n])
		}
	}()
	select {
	case f := <-got:
		return f, true
	case <-time.After(300 * time.Millisecond):
		return "", false
	}
}

func TestHandlerServesAMatchedRespond(t *testing.T) {
	rec := &capturingRecorder{}
	c, client := newTestConn(t)
	c.handler = newTestHandlerWith(stubMatcher{mock: respondMock("m1", `[{"text":"OK"}]`), matched: true}, rec)

	c.wg.Add(1)
	go c.writeLoop()

	c.handler.handleFrame(context.Background(), c, []byte("READ\r\n"))

	frame, ok := readOne(t, client)
	if !ok || frame != "OK\r\n" {
		t.Fatalf("wrote %q (received=%v), want %q", frame, ok, "OK\r\n")
	}
}

// FR-032: nothing is written, the frame is recorded as unmatched, and the
// connection stays usable. Silence is what an unconfigured peripheral looks
// like; inventing a reply would be inventing protocol.
func TestHandlerUnmatchedFrameIsSilentAndRecorded(t *testing.T) {
	rec := &capturingRecorder{}
	c, client := newTestConn(t)
	c.handler = newTestHandlerWith(stubMatcher{matched: false}, rec)

	c.wg.Add(1)
	go c.writeLoop()

	c.handler.handleFrame(context.Background(), c, []byte("NOPE\r\n"))

	if frame, ok := readOne(t, client); ok {
		t.Errorf("wrote %q for an unmatched frame, want silence", frame)
	}
	if got := rec.decisions(); len(got) != 1 || got[0] != domain.DecisionNotConfigured {
		t.Errorf("decisions = %v, want exactly [not_configured]", got)
	}
}

// FR-027: a panic anywhere in the frame path becomes a recorded internal
// error and the connection survives — one bad frame must never take an
// endpoint down for every other connection on it.
func TestHandlerRecoversFromAPanic(t *testing.T) {
	rec := &capturingRecorder{}
	c, _ := newTestConn(t)
	c.handler = newTestHandlerWith(stubMatcher{panics: true}, rec)

	c.handler.handleFrame(context.Background(), c, []byte("BOOM\r\n"))

	if got := rec.decisions(); len(got) != 1 || got[0] != domain.DecisionInternalError {
		t.Errorf("decisions = %v, want exactly [internal_error]", got)
	}
	select {
	case <-c.done:
		t.Error("the connection was closed by a recovered panic, want it left usable")
	default:
	}
}

// A script that threw or timed out fails safe: nothing is written, and the
// recorded decision names the cause — distinguishable from both a silent
// endpoint and an unmatched frame.
func TestHandlerRecordsAScriptFailureDistinctly(t *testing.T) {
	rec := &capturingRecorder{}
	c, client := newTestConn(t)
	c.handler = newTestHandlerWith(stubMatcher{
		err: &usecase.ScriptError{MockID: "scripted", Phase: "match"},
	}, rec)

	c.wg.Add(1)
	go c.writeLoop()

	c.handler.handleFrame(context.Background(), c, []byte("READ\r\n"))

	if frame, ok := readOne(t, client); ok {
		t.Errorf("wrote %q after a script failure, want silence", frame)
	}
	if got := rec.decisions(); len(got) != 1 || got[0] != domain.DecisionScriptFailed {
		t.Errorf("decisions = %v, want exactly [script_failed]", got)
	}
	if id := rec.records[0].MatchedMockID; id == nil || *id != "scripted" {
		t.Errorf("MatchedMockID = %v, want the mock whose script failed", id)
	}
}

// A proxy rule can never be served here — it is refused at creation — so
// reaching serve time means the rule predates that check or its endpoint was
// renamed underneath it. It must be recorded explicitly and leave the
// connection usable, not silently do nothing.
func TestHandlerRecordsAnUnservableProxyRule(t *testing.T) {
	rec := &capturingRecorder{}
	c, _ := newTestConn(t)
	proxy := domain.Mock{ID: "p1", Partition: "default",
		Action: domain.Action{Kind: domain.ActionProxy, Proxy: &domain.ProxyAction{}}}
	c.handler = newTestHandlerWith(stubMatcher{mock: proxy, matched: true}, rec)

	c.handler.handleFrame(context.Background(), c, []byte("READ\r\n"))

	if got := rec.decisions(); len(got) != 1 || got[0] != domain.DecisionInternalError {
		t.Errorf("decisions = %v, want exactly [internal_error]", got)
	}
	select {
	case <-c.done:
		t.Error("the connection was closed by an unservable rule, want it left usable")
	default:
	}
}

// FR-028: an oversized frame is still SERVED in full while its record says
// plainly that what was stored is truncated.
func TestCapBodyMarksTruncationWithoutLosingTheTrueSize(t *testing.T) {
	stored, truncated, total := capBody([]byte("abcdefghij"), 4)
	if string(stored) != "abcd" || !truncated || total != 10 {
		t.Errorf("capBody() = (%q, %v, %d), want (abcd, true, 10)", stored, truncated, total)
	}

	stored, truncated, total = capBody([]byte("abc"), 0)
	if string(stored) != "abc" || truncated || total != 3 {
		t.Errorf("capBody() with an unbounded cap = (%q, %v, %d), want (abc, false, 3)", stored, truncated, total)
	}
}

// FR-006: a rule with its own projection is evaluated against its own view of
// the bytes, and a rule without one inherits the endpoint's — memoised, so
// the common case does not re-project once per candidate.
func TestFrameProjectorPrefersTheRuleOverTheEndpointDefault(t *testing.T) {
	endpointDefault := &domain.Projection{Split: ","}
	p, err := newFrameProjector([]byte("A,12"), endpointDefault)
	if err != nil {
		t.Fatalf("newFrameProjector(): %v", err)
	}

	inherited, err := p.ProjectFor(domain.Mock{ID: "inherits"})
	if err != nil {
		t.Fatalf("ProjectFor(): %v", err)
	}
	if string(inherited) != string(p.defaultEnvelope) {
		t.Error("a rule with no projection did not get the endpoint's default envelope")
	}

	override := &domain.Projection{At: []domain.ProjectionField{{Name: "kind", Offset: 0, Length: 1}}}
	own, err := p.ProjectFor(domain.Mock{ID: "overrides", Projection: override})
	if err != nil {
		t.Fatalf("ProjectFor(): %v", err)
	}
	if string(own) == string(inherited) {
		t.Error("a rule with its own projection got the endpoint's envelope, want its own")
	}

	again, err := p.ProjectFor(domain.Mock{ID: "overrides", Projection: override})
	if err != nil {
		t.Fatalf("ProjectFor(): %v", err)
	}
	if string(again) != string(own) {
		t.Error("the memoised envelope differs from the first projection of the same rule")
	}
}
