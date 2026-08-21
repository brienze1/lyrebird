package streamplane

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brienze1/lyrebird/internal/domain"
	"github.com/brienze1/lyrebird/internal/usecase"
)

// recordingHandler is a Handler whose recorder swallows everything, so a
// connection test exercises the writer without needing a store.
func testHandler() *Handler {
	return &Handler{
		record: nopRecorder{},
		clock:  systemClock{},
		log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

type nopRecorder struct{}

func (nopRecorder) Execute(_ context.Context, _ usecase.RecordTrafficInput) (domain.TrafficRecord, error) {
	return domain.TrafficRecord{}, nil
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

func newTestConn(t *testing.T) (*conn, net.Conn) {
	t.Helper()
	server, client := net.Pipe()
	endpoint := domain.Endpoint{Name: "widget", Partition: "default", Framing: delimiterFraming()}
	c := newConn(server, "default", endpoint, handshake{}, testHandler(), NewRegistry(), testHandler().log)
	t.Cleanup(func() {
		c.close()
		_ = client.Close()
	})
	return c, client
}

// FR-033/SC-006: every writer goes through one goroutine, so concurrent
// emissions can never be spliced into each other and repeated runs deliver
// the frames in the order they were queued.
//
// net.Pipe is unbuffered and synchronous, which makes a splice detectable:
// if two goroutines wrote to the socket directly, a reader would see a frame
// with another frame's bytes inside it.
func TestConnWriterNeverSplicesConcurrentFrames(t *testing.T) {
	c, client := newTestConn(t)

	c.wg.Add(1)
	go c.writeLoop()

	const emitters, perEmitter = 8, 12
	var wg sync.WaitGroup
	for e := 0; e < emitters; e++ {
		wg.Add(1)
		go func(e int) {
			defer wg.Done()
			for i := 0; i < perEmitter; i++ {
				spec := fmt.Sprintf(`[{"text":"E%d-%02d"}]`, e, i)
				if err := c.emit(context.Background(), []byte(spec)); err != nil {
					return
				}
			}
		}(e)
	}

	read := make(chan []string, 1)
	go func() {
		r := bufio.NewReader(client)
		var got []string
		for i := 0; i < emitters*perEmitter; i++ {
			line, err := r.ReadString('\n')
			if err != nil {
				break
			}
			got = append(got, strings.TrimRight(line, "\r\n"))
		}
		read <- got
	}()

	wg.Wait()

	var frames []string
	select {
	case frames = <-read:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out reading emitted frames")
	}

	if len(frames) != emitters*perEmitter {
		t.Fatalf("read %d frames, want %d", len(frames), emitters*perEmitter)
	}
	// Every frame must be exactly one well-formed unit: a splice shows up as
	// a frame that is not one of the values any emitter wrote.
	perEmitterSeen := map[int][]string{}
	for _, f := range frames {
		var e, i int
		if _, err := fmt.Sscanf(f, "E%d-%02d", &e, &i); err != nil {
			t.Fatalf("frame %q is not a whole emitted frame — writes were spliced", f)
		}
		perEmitterSeen[e] = append(perEmitterSeen[e], f)
	}
	// Within one emitter, order must be preserved: the writer is FIFO, so
	// what a single caller queued in order arrives in order.
	for e, seen := range perEmitterSeen {
		for i, f := range seen {
			if want := fmt.Sprintf("E%d-%02d", e, i); f != want {
				t.Fatalf("emitter %d frame %d = %q, want %q — per-emitter order was not preserved", e, i, f, want)
			}
		}
	}
}

// A closed connection must report to an emitter rather than blocking it
// forever, which is what makes FR-015's "told so plainly" reachable.
func TestConnEmitAfterCloseReportsInsteadOfBlocking(t *testing.T) {
	c, _ := newTestConn(t)
	c.close()

	done := make(chan error, 1)
	go func() { done <- c.emit(context.Background(), []byte(`[{"text":"X"}]`)) }()

	select {
	case err := <-done:
		if err == nil {
			t.Error("emit() on a closed connection succeeded, want an error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("emit() on a closed connection blocked, want it to report immediately")
	}
}

func TestConnCloseIsIdempotent(t *testing.T) {
	c, _ := newTestConn(t)
	c.close()
	c.close() // must not panic on a second close of the done channel
}

// The exhaustion rules are pure, so they are testable without a socket, a
// ticker or a goroutine.
func TestCadenceFrameAt(t *testing.T) {
	frames := [][]domain.FramePart{{{Text: ptr("A")}}, {{Text: ptr("B")}}}

	tests := []struct {
		name      string
		onExhaust domain.OnExhaustion
		idx       int
		wantText  string
		wantNext  int
		wantOK    bool
	}{
		{"first frame", "", 0, "A", 1, true},
		{"second frame", "", 1, "B", 2, true},
		{"repeat_last by default", "", 2, "B", 2, true},
		{"repeat_last explicitly", domain.OnExhaustionRepeatLast, 5, "B", 5, true},
		{"loop restarts", domain.OnExhaustionLoop, 2, "A", 1, true},
		{"stop ends the cadence", domain.OnExhaustionStop, 2, "", 2, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cad := &domain.Cadence{Interval: time.Second, Frames: frames, OnExhaust: tt.onExhaust}
			parts, next, ok := cadenceFrameAt(cad, tt.idx)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if next != tt.wantNext {
				t.Errorf("next = %d, want %d", next, tt.wantNext)
			}
			if len(parts) != 1 || *parts[0].Text != tt.wantText {
				t.Errorf("frame = %+v, want the one containing %q", parts, tt.wantText)
			}
		})
	}
}

func TestCadenceFrameAtWithNoFrames(t *testing.T) {
	if _, _, ok := cadenceFrameAt(&domain.Cadence{Interval: time.Second}, 0); ok {
		t.Error("cadenceFrameAt() with no frames reported a frame, want none")
	}
}

// runCadence starts the writer and the cadence exactly as serve() wires them
// (conn.go), without the inbound frame-reading loop this test has no need
// for — it exercises only the unprompted-emission side.
func startCadence(t *testing.T, c *conn, cad *domain.Cadence) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	c.wg.Add(1)
	go c.writeLoop()
	c.wg.Add(1)
	go c.runCadence(ctx, cad)
	return cancel
}

// CB5-1 WI-13 correction: a cadence with a positive interval pushes on the
// HOST'S real clock, wholly unrelated to a scenario's substituted device
// clock — the number of frames available inside a given device-time window
// then depends on how fast the harness happens to issue calls (FR-027). An
// Interval of 0 ("immediate") must remove that coupling entirely: every
// frame is queued back-to-back, paced only by the connection's own
// backpressure, never by a timer.
//
// The old ticker-based design needs a full Interval of real time before its
// very *first* frame exists at all, and one Interval per frame after that —
// with Interval at the CB5 seed's old 1000ms, reading even a handful of
// frames would take several real seconds. Reading many frames well inside a
// generous-but-bounded deadline is therefore direct proof the coupling is
// gone, not a tuned guess at "fast enough".
func TestRunCadenceImmediateModeDeliversWithoutWaitingOnAClock(t *testing.T) {
	c, client := newTestConn(t)
	cad := &domain.Cadence{
		Interval:  0,
		Frames:    [][]domain.FramePart{{{Text: ptr("TICK")}}},
		OnExhaust: domain.OnExhaustionRepeatLast,
	}
	cancel := startCadence(t, c, cad)
	defer cancel()

	const wantFrames = 20
	read := make(chan error, 1)
	go func() {
		r := bufio.NewReader(client)
		for i := 0; i < wantFrames; i++ {
			line, err := r.ReadString('\n')
			if err != nil {
				read <- err
				return
			}
			if got := strings.TrimRight(line, "\r\n"); got != "TICK" {
				read <- fmt.Errorf("frame %d = %q, want %q", i, got, "TICK")
				return
			}
		}
		read <- nil
	}()

	// A single 1000ms-interval tick (the old CB5 seed's own configuration)
	// would not even deliver the SECOND frame within this deadline; 20
	// frames prove there is no per-frame wait at all, immediate or
	// otherwise — this is not a tuned interval, it is the absence of one.
	select {
	case err := <-read:
		if err != nil {
			t.Fatalf("reading %d immediate-cadence frames: %v", wantFrames, err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("reading %d immediate-cadence frames did not complete within 500ms — still coupled to a clock", wantFrames)
	}
}

// A stand-in that reads slowly must see exactly the same frames a fast
// reader would — repeat_last never skips, drops or reorders content to
// "catch up" to how quickly bytes were consumed.
func TestRunCadenceImmediateModeSequenceSurvivesASlowReader(t *testing.T) {
	c, client := newTestConn(t)
	cad := &domain.Cadence{
		Interval:  0,
		Frames:    [][]domain.FramePart{{{Text: ptr("FIRST")}}, {{Text: ptr("SECOND")}}},
		OnExhaust: domain.OnExhaustionRepeatLast,
	}
	cancel := startCadence(t, c, cad)
	defer cancel()

	r := bufio.NewReader(client)
	readLine := func() string {
		t.Helper()
		// Bounded, not blocking forever: a read deadline turns "the fix
		// regressed and nothing arrives" into a clean failure instead of a
		// hung test.
		if err := client.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
			t.Fatalf("SetReadDeadline: %v", err)
		}
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		return strings.TrimRight(line, "\r\n")
	}

	if got := readLine(); got != "FIRST" {
		t.Fatalf("frame 0 = %q, want %q", got, "FIRST")
	}
	// Simulate a harness that is busy doing something else — slower than any
	// wall-clock cadence interval this endpoint ever used in practice.
	time.Sleep(150 * time.Millisecond)
	if got := readLine(); got != "SECOND" {
		t.Fatalf("frame 1 after a slow read = %q, want %q — pacing changed the sequence", got, "SECOND")
	}
	if got := readLine(); got != "SECOND" {
		t.Fatalf("frame 2 (repeat_last) = %q, want %q", got, "SECOND")
	}
}

func ptr(s string) *string { return &s }
