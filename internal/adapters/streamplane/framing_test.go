package streamplane

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/brienze1/lyrebird/internal/domain"
)

func delimiterFraming() domain.Framing {
	return domain.Framing{Kind: domain.FramingDelimiter, Delimiter: []byte("\r\n")}
}

func TestFrameReaderDelimiterSplitsAndKeepsTheDelimiter(t *testing.T) {
	fr := newFrameReader(strings.NewReader("A\r\nBB\r\n"), delimiterFraming(), 0)

	for _, want := range []string{"A\r\n", "BB\r\n"} {
		got, err := fr.Next()
		if err != nil {
			t.Fatalf("Next(): %v", err)
		}
		if string(got) != want {
			t.Errorf("frame = %q, want %q", got, want)
		}
	}
	if _, err := fr.Next(); !errors.Is(err, io.EOF) {
		t.Errorf("Next() after the last frame = %v, want io.EOF", err)
	}
}

// A frame arriving in pieces must not be delivered until it is complete —
// otherwise a rule would match half a frame and the other half would look
// like a frame of its own.
func TestFrameReaderHoldsAnIncompleteFrame(t *testing.T) {
	pr, pw := io.Pipe()
	fr := newFrameReader(pr, delimiterFraming(), 0)

	go func() {
		_, _ = pw.Write([]byte("PAR"))
		_, _ = pw.Write([]byte("TIAL\r\n"))
		_ = pw.Close()
	}()

	got, err := fr.Next()
	if err != nil {
		t.Fatalf("Next(): %v", err)
	}
	if string(got) != "PARTIAL\r\n" {
		t.Errorf("frame = %q, want %q", got, "PARTIAL\r\n")
	}
}

// FR-034: bytes that never complete a frame are abandoned past the cap and
// the reader resynchronises, rather than buffering forever or taking the
// connection down.
//
// The oversized run is written WITHOUT a terminator and the good frame only
// afterwards, because that is the condition the cap actually guards: an
// endless writer, not a large complete frame (which FR-028 says must still be
// served — see the test below).
func TestFrameReaderAbandonsAnUnterminatedRunAndResynchronises(t *testing.T) {
	pr, pw := io.Pipe()
	fr := newFrameReader(pr, delimiterFraming(), 32)

	written := make(chan struct{})
	go func() {
		_, _ = pw.Write([]byte(strings.Repeat("x", 200)))
		<-written
		_, _ = pw.Write([]byte("\r\nGOOD\r\n"))
		_ = pw.Close()
	}()

	if _, err := fr.Next(); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("Next() over the cap = %v, want ErrFrameTooLarge", err)
	}
	close(written)

	got, err := fr.Next()
	if err != nil {
		t.Fatalf("Next() after an oversized run: %v", err)
	}
	if string(got) != "GOOD\r\n" {
		t.Errorf("frame after resync = %q, want %q", got, "GOOD\r\n")
	}
}

// FR-028: a frame LARGER than the recorded-body cap is still served in full.
// The cap bounds what the traffic log stores and how long an unterminated run
// may accumulate — it is not a limit on how big a legitimate frame may be.
func TestFrameReaderServesACompleteFrameLargerThanTheCap(t *testing.T) {
	body := strings.Repeat("y", 200)
	fr := newFrameReader(strings.NewReader(body+"\r\n"), delimiterFraming(), 32)

	got, err := fr.Next()
	if err != nil {
		t.Fatalf("Next(): %v", err)
	}
	if string(got) != body+"\r\n" {
		t.Errorf("frame length = %d, want the whole %d-byte frame served", len(got), len(body)+2)
	}
}

// The delimiter arriving split across two reads must still be found, or the
// stream never resynchronises and every later frame is lost.
func TestFrameReaderResynchronisesOnASplitDelimiter(t *testing.T) {
	pr, pw := io.Pipe()
	fr := newFrameReader(pr, delimiterFraming(), 16)

	go func() {
		_, _ = pw.Write([]byte(strings.Repeat("x", 40)))
		_, _ = pw.Write([]byte("\r"))
		_, _ = pw.Write([]byte("\nGOOD\r\n"))
		_ = pw.Close()
	}()

	if _, err := fr.Next(); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("Next() over the cap = %v, want ErrFrameTooLarge", err)
	}
	got, err := fr.Next()
	if err != nil {
		t.Fatalf("Next() after a split-delimiter resync: %v", err)
	}
	if string(got) != "GOOD\r\n" {
		t.Errorf("frame = %q, want %q", got, "GOOD\r\n")
	}
}

func TestFrameReaderFixedLength(t *testing.T) {
	framing := domain.Framing{Kind: domain.FramingLength, Length: 3}
	fr := newFrameReader(strings.NewReader("ABCDEF"), framing, 0)

	for _, want := range []string{"ABC", "DEF"} {
		got, err := fr.Next()
		if err != nil {
			t.Fatalf("Next(): %v", err)
		}
		if string(got) != want {
			t.Errorf("frame = %q, want %q", got, want)
		}
	}
}

func TestFrameReaderLengthPrefix(t *testing.T) {
	tests := []struct {
		name   string
		endian domain.Endianness
		stream []byte
	}{
		{name: "big", endian: domain.EndianBig, stream: append([]byte{0x00, 0x03}, []byte("ABC")...)},
		{name: "little", endian: domain.EndianLittle, stream: append([]byte{0x03, 0x00}, []byte("ABC")...)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			framing := domain.Framing{Kind: domain.FramingPrefix, PrefixWidth: 2, PrefixEndian: tt.endian}
			fr := newFrameReader(bytes.NewReader(tt.stream), framing, 0)

			got, err := fr.Next()
			if err != nil {
				t.Fatalf("Next(): %v", err)
			}
			if !bytes.Equal(got, tt.stream) {
				t.Errorf("frame = %q, want the prefix and payload together (%q)", got, tt.stream)
			}
			if payload := payloadOf(got, framing); string(payload) != "ABC" {
				t.Errorf("payloadOf() = %q, want ABC", payload)
			}
		})
	}
}

// A prefix declaring more bytes than the cap must never be buffered: trusting
// it for allocation is how a two-byte write becomes a multi-gigabyte one.
func TestFrameReaderRefusesAnOversizedDeclaredPrefix(t *testing.T) {
	framing := domain.Framing{Kind: domain.FramingPrefix, PrefixWidth: 2, PrefixEndian: domain.EndianBig}
	fr := newFrameReader(bytes.NewReader([]byte{0xFF, 0xFF, 'x'}), framing, 16)

	if _, err := fr.Next(); !errors.Is(err, ErrFrameTooLarge) {
		t.Errorf("Next() with a huge declared prefix = %v, want ErrFrameTooLarge", err)
	}
}

func TestPayloadOfStripsFramingOverheadOnly(t *testing.T) {
	tests := []struct {
		name    string
		framing domain.Framing
		frame   string
		want    string
	}{
		{"delimiter trimmed", delimiterFraming(), "READ\r\n", "READ"},
		{"delimiter absent leaves the frame alone", delimiterFraming(), "READ", "READ"},
		{"fixed length is all content", domain.Framing{Kind: domain.FramingLength, Length: 3}, "ABC", "ABC"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := payloadOf([]byte(tt.frame), tt.framing); string(got) != tt.want {
				t.Errorf("payloadOf(%q) = %q, want %q", tt.frame, got, tt.want)
			}
		})
	}
}

func TestTerminatorOnlyForDelimiterFraming(t *testing.T) {
	if got := terminator(delimiterFraming()); string(got) != "\r\n" {
		t.Errorf("terminator(delimiter) = %q, want CRLF", got)
	}
	if got := terminator(domain.Framing{Kind: domain.FramingLength, Length: 4}); got != nil {
		t.Errorf("terminator(length) = %q, want nil — a fixed-length answer carries its own shape", got)
	}
}
