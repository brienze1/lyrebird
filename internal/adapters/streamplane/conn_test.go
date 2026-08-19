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

func ptr(s string) *string { return &s }
