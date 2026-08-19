package streamplane

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/brienze1/lyrebird/internal/domain"
)

// TestFrameReaderSurvivesManyMalformedInputs is SC-008 at the layer that
// decides it: a hundred runs of bytes that never complete a frame must leave
// the reader working, so one runaway writer never takes an endpoint down for
// the connections beside it.
//
// It exercises the reader directly rather than through a socket because that
// is where the property lives — the connection loop's only contribution is to
// treat ErrFrameTooLarge as recoverable, which conn.serve does explicitly.
func TestFrameReaderSurvivesManyMalformedInputs(t *testing.T) {
	pr, pw := io.Pipe()
	fr := newFrameReader(pr, delimiterFraming(), 64)

	const rounds = 100
	go func() {
		for i := 0; i < rounds; i++ {
			_, _ = pw.Write([]byte(strings.Repeat("x", 200)))
			_, _ = pw.Write([]byte("\r\n"))
		}
		_, _ = pw.Write([]byte("GOOD\r\n"))
		_ = pw.Close()
	}()

	deadline := time.After(20 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("reader stopped making progress under repeated malformed input")
		default:
		}

		frame, err := fr.Next()
		if err != nil {
			if errors.Is(err, ErrFrameTooLarge) {
				continue // abandoned and resynchronising, exactly as designed
			}
			t.Fatalf("Next(): %v", err)
		}
		if string(frame) == "GOOD\r\n" {
			return // the good frame after a hundred bad ones was still served
		}
	}
}

// TestCadenceSequenceIsIdenticalAcrossRuns is SC-006's determinism claim
// expressed where it is decidable: the frame a tick emits is a pure function
// of (cadence, index), so ten runs produce ten identical sequences by
// construction rather than by luck of scheduling. The single writer
// (TestConnWriterNeverSplicesConcurrentFrames) is what carries that
// determinism to the wire.
func TestCadenceSequenceIsIdenticalAcrossRuns(t *testing.T) {
	cad := &domain.Cadence{
		Interval: time.Millisecond,
		Frames: [][]domain.FramePart{
			{{Text: ptr("A")}}, {{Text: ptr("B")}}, {{Text: ptr("C")}},
		},
		OnExhaust: domain.OnExhaustionLoop,
	}

	sequence := func() []string {
		var out []string
		idx := 0
		for i := 0; i < 10; i++ {
			parts, next, ok := cadenceFrameAt(cad, idx)
			if !ok {
				break
			}
			idx = next
			built, err := buildFrame(frameSpec{Parts: parts}, nil, delimiterFraming())
			if err != nil {
				t.Fatalf("buildFrame(): %v", err)
			}
			out = append(out, string(built))
		}
		return out
	}

	want := sequence()
	if len(want) != 10 {
		t.Fatalf("first run produced %d frames, want 10", len(want))
	}
	for run := 2; run <= 10; run++ {
		got := sequence()
		if len(got) != len(want) {
			t.Fatalf("run %d produced %d frames, want %d", run, len(got), len(want))
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("run %d frame %d = %q, want %q — the sequence is not reproducible",
					run, i, got[i], want[i])
			}
		}
	}
}

// A binary frame must survive the whole round trip — projected, matched on,
// and echoed back — without a single byte changing. This is the property
// latin-1 decoding exists for, asserted end-to-end across project and build
// rather than only inside each.
func TestBinaryFrameRoundTripsThroughProjectionAndCopyFrom(t *testing.T) {
	frame := []byte{0x00, 0x01, 0x7F, 0x80, 0xFE, 0xFF}
	envelope, err := projectFrame(frame, &domain.Projection{
		At: []domain.ProjectionField{{Name: "all", Offset: 0, Length: len(frame)}},
	})
	if err != nil {
		t.Fatalf("projectFrame(): %v", err)
	}

	spec, err := parseFrameSpec([]byte(`{"parts":[{"copyFrom":"$.at.all"}],"raw":true}`))
	if err != nil {
		t.Fatalf("parseFrameSpec(): %v", err)
	}
	got, err := buildFrame(spec, envelope, delimiterFraming())
	if err != nil {
		t.Fatalf("buildFrame(): %v", err)
	}
	if !bytes.Equal(got, frame) {
		t.Errorf("round-tripped %x, want %x", got, frame)
	}
}

// A stand-in that says nothing must not hold an accept slot — or, worse, an
// endpoint claim — forever.
func TestHandshakeParsing(t *testing.T) {
	tests := []struct {
		name         string
		line         string
		wantErr      bool
		wantEndpoint string
		wantSpace    string
		wantHeader   map[string][]string
	}{
		{name: "endpoint only", line: "LYREBIRD/1 widget\r\n", wantEndpoint: "widget"},
		{name: "with a space", line: "LYREBIRD/1 widget space=other\r\n", wantEndpoint: "widget", wantSpace: "other"},
		{
			name: "extra keys become headers", line: "LYREBIRD/1 widget serial=abc123\r\n",
			wantEndpoint: "widget", wantHeader: map[string][]string{"serial": {"abc123"}},
		},
		{name: "header keys are lowercased", line: "LYREBIRD/1 widget Serial=abc\r\n",
			wantEndpoint: "widget", wantHeader: map[string][]string{"serial": {"abc"}}},
		{name: "space is case-insensitive", line: "LYREBIRD/1 widget SPACE=other\r\n",
			wantEndpoint: "widget", wantSpace: "other"},
		{name: "wrong protocol token", line: "HELLO widget\r\n", wantErr: true},
		{name: "no endpoint", line: "LYREBIRD/1\r\n", wantErr: true},
		{name: "an option in the endpoint position", line: "LYREBIRD/1 space=other\r\n", wantErr: true},
		{name: "an option that is not key=value", line: "LYREBIRD/1 widget bare\r\n", wantErr: true},
		{name: "empty", line: "\r\n", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseHandshake(tt.line)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseHandshake(%q) succeeded, want an error", tt.line)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseHandshake(%q): %v", tt.line, err)
			}
			if got.Endpoint != tt.wantEndpoint {
				t.Errorf("Endpoint = %q, want %q", got.Endpoint, tt.wantEndpoint)
			}
			if got.Space != tt.wantSpace {
				t.Errorf("Space = %q, want %q", got.Space, tt.wantSpace)
			}
			for k, want := range tt.wantHeader {
				if len(got.Header[k]) != len(want) || got.Header[k][0] != want[0] {
					t.Errorf("Header[%q] = %v, want %v", k, got.Header[k], want)
				}
			}
		})
	}
}

// The registry is what enforces one stand-in per endpoint, and what a reset
// reaches through to close everything in a space.
func TestRegistryEnforcesOneStandInPerEndpoint(t *testing.T) {
	r := NewRegistry()
	first, firstClient := newTestConnOn(t, r)
	second, _ := newTestConnOn(t, r)

	if err := r.claim("default", "widget", first); err != nil {
		t.Fatalf("claim(): %v", err)
	}
	var occupied *errEndpointOccupied
	if err := r.claim("default", "widget", second); !errors.As(err, &occupied) {
		t.Fatalf("second claim = %v, want errEndpointOccupied", err)
	}
	if !r.Occupied("default", "widget") {
		t.Error("Occupied() = false after a claim, want true")
	}
	// A different space is a different endpoint entirely (FR-024).
	if err := r.claim("other", "widget", second); err != nil {
		t.Errorf("claim() of the same name in another space = %v, want it allowed", err)
	}

	r.CloseSpace("default")
	if r.Occupied("default", "widget") {
		t.Error("Occupied() = true after CloseSpace, want the claim released")
	}
	if r.Occupied("other", "widget") != true {
		t.Error("CloseSpace(default) released another space's claim, want it untouched")
	}
	// The stand-in observes the peripheral going away.
	if _, err := bufio.NewReader(firstClient).ReadString('\n'); err == nil {
		t.Error("the closed connection still reads, want it dropped")
	}
}

// A claim released by a connection that CloseSpace already replaced must not
// evict its successor.
func TestRegistryReleaseOnlyDropsTheCurrentHolder(t *testing.T) {
	r := NewRegistry()
	old, _ := newTestConnOn(t, r)
	fresh, _ := newTestConnOn(t, r)

	if err := r.claim("default", "widget", old); err != nil {
		t.Fatalf("claim(): %v", err)
	}
	r.release("default", "widget", old)
	if err := r.claim("default", "widget", fresh); err != nil {
		t.Fatalf("re-claim after release: %v", err)
	}

	r.release("default", "widget", old) // the stale connection's deferred release
	if !r.Occupied("default", "widget") {
		t.Error("a stale release evicted the current holder, want it kept")
	}
}

func TestSplitRegistryKeyRoundTrips(t *testing.T) {
	// A "/" separator would let space "a" + endpoint "b/c" collide with
	// space "a/b" + endpoint "c"; NUL cannot appear in either.
	partition, endpoint := splitRegistryKey(registryKey("a/b", "c/d"))
	if partition != "a/b" || endpoint != "c/d" {
		t.Errorf("splitRegistryKey() = (%q, %q), want (a/b, c/d)", partition, endpoint)
	}
}

// newTestConnOn builds a test connection registered against r, so registry
// behaviour can be exercised without a listener.
func newTestConnOn(t *testing.T, r *Registry) (*conn, io.ReadWriteCloser) {
	t.Helper()
	c, client := newTestConn(t)
	c.registry = r
	return c, client
}
