package streamplane

import (
	"bufio"
	"context"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brienze1/lyrebird/internal/domain"
)

// lineReader drains a net.Pipe peer continuously (a pipe write blocks until
// somebody reads) and exposes what arrived as lines, so a test can count what
// reached the wire without racing the writer.
type lineReader struct {
	mu    sync.Mutex
	lines []string
	ch    chan string
}

func newLineReader(client net.Conn) *lineReader {
	lr := &lineReader{ch: make(chan string, 4096)}
	go func() {
		r := bufio.NewReader(client)
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			lr.mu.Lock()
			lr.lines = append(lr.lines, line)
			lr.mu.Unlock()
			lr.ch <- line
		}
	}()
	return lr
}

func (lr *lineReader) count() int {
	lr.mu.Lock()
	defer lr.mu.Unlock()
	return len(lr.lines)
}

func (lr *lineReader) next(t *testing.T, within time.Duration) string {
	t.Helper()
	select {
	case l := <-lr.ch:
		return l
	case <-time.After(within):
		t.Fatalf("no frame arrived within %v", within)
		return ""
	}
}

// swappableResolver is a cadenceResolver whose answer a test changes while the
// cadence goroutine is reading it.
type swappableResolver struct {
	mu   sync.Mutex
	mock *domain.Mock
}

func (s *swappableResolver) set(m *domain.Mock) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mock = m
}

func (s *swappableResolver) Resolve(_ context.Context, _, _ string) (domain.Mock, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.mock == nil {
		return domain.Mock{}, false, nil
	}
	return *s.mock, true, nil
}

// settled returns the number of frames the writer wrote, once the reader has
// consumed them all. After pauseCadence returns, rec's count is exact (the
// writer records before it processes the barrier); lr.count() lags it by the
// reader goroutine, so snapshotting lr.count() directly races.
func settled(t *testing.T, lr *lineReader, rec *capturingRecorder) int {
	t.Helper()
	want := len(rec.decisions())
	deadline := time.Now().Add(2 * time.Second)
	for lr.count() != want {
		if time.Now().After(deadline) {
			t.Fatalf("reader saw %d frames, writer recorded %d", lr.count(), want)
		}
		time.Sleep(time.Millisecond)
	}
	return want
}

func pauseTestConn(t *testing.T, interval time.Duration) (*conn, *lineReader, *capturingRecorder, *swappableResolver, context.CancelFunc) {
	t.Helper()
	c, client := newTestConn(t)
	rec := &capturingRecorder{}
	res := &swappableResolver{}
	c.handler = &Handler{
		match: stubMatcher{matched: false}, record: rec, clock: systemClock{}, cadence: res,
		log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	cad := &domain.Cadence{
		Interval:  interval,
		Frames:    [][]domain.FramePart{{{Text: ptr("TICK")}}},
		OnExhaust: domain.OnExhaustionRepeatLast,
	}
	lr := newLineReader(client)
	return c, lr, rec, res, startCadence(t, c, cad)
}

func TestPauseCadence_StopsTicksAndFlushes(t *testing.T) {
	const interval = 40 * time.Millisecond
	c, lr, rec, _, cancel := pauseTestConn(t, interval)
	defer cancel()

	if got := lr.next(t, 2*time.Second); got != "TICK" {
		t.Fatalf("first frame = %q, want TICK", got)
	}
	if err := c.pauseCadence(context.Background()); err != nil {
		t.Fatalf("pauseCadence: %v", err)
	}
	frames := settled(t, lr, rec)
	emits := len(rec.decisions())

	time.Sleep(10 * interval)

	if got := lr.count(); got != frames {
		t.Errorf("frames after pause = %d, want %d (no growth)", got, frames)
	}
	if got := len(rec.decisions()); got != emits {
		t.Errorf("recorded EMITs after pause = %d, want %d (no growth)", got, emits)
	}
}

func TestPauseCadence_ImmediateModeDoesNotSpin(t *testing.T) {
	c, lr, rec, _, cancel := pauseTestConn(t, 0)
	defer cancel()

	lr.next(t, 2*time.Second)
	if err := c.pauseCadence(context.Background()); err != nil {
		t.Fatalf("pauseCadence: %v", err)
	}
	frames := settled(t, lr, rec)
	time.Sleep(200 * time.Millisecond)
	if got := lr.count(); got != frames {
		t.Errorf("frames after pause = %d, want %d (parked, not spinning)", got, frames)
	}
}

func TestPausedCadence_EmitStillDelivers(t *testing.T) {
	c, lr, rec, _, cancel := pauseTestConn(t, 40*time.Millisecond)
	defer cancel()

	lr.next(t, 2*time.Second)
	if err := c.pauseCadence(context.Background()); err != nil {
		t.Fatalf("pauseCadence: %v", err)
	}
	before := len(rec.decisions())

	if err := c.emit(context.Background(), []byte(`[{"text":"ONE"}]`)); err != nil {
		t.Fatalf("emit while paused: %v", err)
	}
	if got := len(rec.decisions()); got != before+1 {
		t.Errorf("recorded entries = %d, want %d (the EMIT recorded before emit returned)", got, before+1)
	}
	var saw bool
	for i := 0; i < 5 && !saw; i++ {
		saw = lr.next(t, time.Second) == "ONE"
	}
	if !saw {
		t.Error("the injected frame ONE never arrived while paused")
	}
}

func TestResumeCadence_EmitsAgainWithCurrentContent(t *testing.T) {
	const interval = 20 * time.Millisecond
	c, lr, rec, res, cancel := pauseTestConn(t, interval)
	defer cancel()

	lr.next(t, 2*time.Second)
	if err := c.pauseCadence(context.Background()); err != nil {
		t.Fatalf("pauseCadence: %v", err)
	}
	res.set(cadenceOverrideMock([][]domain.FramePart{{{Text: ptr("OVERRIDE")}}}))
	frames := settled(t, lr, rec)

	c.resumeCadence()

	deadline := time.After(2 * time.Second)
	for {
		select {
		case l := <-lr.ch:
			if l == "OVERRIDE" {
				if lr.count() <= frames {
					t.Fatal("override seen but frame count did not grow")
				}
				return
			}
		case <-deadline:
			t.Fatal("no OVERRIDE frame after resume")
		}
	}
}

func TestPauseResume_Idempotent(t *testing.T) {
	c, lr, rec, _, cancel := pauseTestConn(t, 20*time.Millisecond)
	defer cancel()
	lr.next(t, 2*time.Second)

	c.resumeCadence() // not paused: no-op
	for i := 0; i < 2; i++ {
		if err := c.pauseCadence(context.Background()); err != nil {
			t.Fatalf("pauseCadence #%d: %v", i+1, err)
		}
	}
	frames := settled(t, lr, rec)
	time.Sleep(100 * time.Millisecond)
	if got := lr.count(); got != frames {
		t.Errorf("frames while double-paused grew: %d -> %d", frames, got)
	}
	c.resumeCadence()
	c.resumeCadence()
	deadline := time.Now().Add(2 * time.Second)
	for lr.count() <= frames {
		if time.Now().After(deadline) {
			t.Fatal("cadence did not emit after resume")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestPausedCadence_ExitsOnClose(t *testing.T) {
	c, lr, _, _, cancel := pauseTestConn(t, 20*time.Millisecond)
	defer cancel()
	lr.next(t, 2*time.Second)
	if err := c.pauseCadence(context.Background()); err != nil {
		t.Fatalf("pauseCadence: %v", err)
	}

	c.close()
	done := make(chan struct{})
	go func() { c.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("paused cadence goroutine did not exit on connection close")
	}
}

func TestBarrierOutbound_NotWrittenNotRecorded(t *testing.T) {
	c, client := newTestConn(t)
	rec := &capturingRecorder{}
	c.handler = &Handler{record: rec, clock: systemClock{}, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	lr := newLineReader(client)
	c.wg.Add(1)
	go c.writeLoop()

	if err := c.queueAndWait(context.Background(), outbound{barrier: true}); err != nil {
		t.Fatalf("barrier: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if n := len(rec.decisions()); n != 0 {
		t.Errorf("recorded %d entries for a barrier, want 0", n)
	}
	if n := lr.count(); n != 0 {
		t.Errorf("barrier wrote %d frames, want 0", n)
	}
}
