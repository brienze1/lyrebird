package streamplane

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/brienze1/lyrebird/internal/domain"
)

// ErrFrameTooLarge reports that bytes accumulated past the configured cap
// without ever completing a frame. It is a RECOVERABLE condition, not a
// connection-level failure (FR-034): the reader abandons the unterminated
// remainder, resynchronises at the next frame boundary, and the caller
// records an explanatory decision and keeps serving. A caller that treated it
// as fatal would let one runaway writer take the endpoint down, which FR-027
// forbids.
var ErrFrameTooLarge = errors.New("streamplane: frame exceeded the configured byte cap")

// frameReader turns a byte stream into frames according to a declared
// domain.Framing. It holds an incomplete frame until it completes, and never
// reads past the end of one frame into the next.
//
// It is deliberately NOT an io.Reader wrapper with a Split func (bufio.
// Scanner's shape): Scanner treats a token exceeding its buffer as a
// permanent error and stops, whereas FR-034 requires abandoning that one
// oversized run and carrying on.
type frameReader struct {
	src     io.Reader
	framing domain.Framing
	// capBytes bounds how much may accumulate without completing a frame.
	// Zero or negative means unbounded.
	capBytes int64

	buf   []byte
	chunk []byte

	// resync is set after an oversized run in delimiter framing: bytes are
	// dropped until the next delimiter, which is the only recoverable
	// boundary a delimiter-framed stream has.
	resync bool
	// discard counts bytes still to be dropped after an oversized run in
	// length or prefix framing, where the boundary is known by arithmetic
	// rather than by scanning.
	discard int64
}

// newFrameReader builds a reader over src for the given framing. capBytes is
// LYREBIRD_BODY_CAP_BYTES — reusing the cap Lyrebird already has rather than
// inventing a second one.
func newFrameReader(src io.Reader, framing domain.Framing, capBytes int64) *frameReader {
	return &frameReader{
		src:      src,
		framing:  framing,
		capBytes: capBytes,
		chunk:    make([]byte, 4096),
	}
}

// Next returns the next complete frame, INCLUDING its framing overhead (the
// delimiter, or the length prefix), so the traffic log records the exact
// bytes that crossed the wire. Use payloadOf to get just the content a rule
// should be matched against.
//
// It returns ErrFrameTooLarge for an oversized unterminated run — the reader
// stays usable and the following call resumes at the next boundary — and
// io.EOF when the stream ends cleanly with nothing buffered.
func (fr *frameReader) Next() ([]byte, error) {
	for {
		frame, ready, err := fr.take()
		if err != nil {
			return nil, err
		}
		if ready {
			return frame, nil
		}

		n, readErr := fr.src.Read(fr.chunk)
		if n > 0 {
			fr.buf = append(fr.buf, fr.chunk[:n]...)
			continue
		}
		if readErr != nil {
			return nil, readErr
		}
	}
}

// take attempts to extract one frame from what is already buffered. It
// reports ready=false when more bytes are needed, which is the only case in
// which Next reads from the underlying stream — so a frame already sitting in
// the buffer is never delayed by a blocking read.
func (fr *frameReader) take() (frame []byte, ready bool, err error) {
	if done := fr.dropPending(); !done {
		return nil, false, nil
	}

	switch fr.framing.Kind {
	case domain.FramingLength:
		return fr.takeFixed(fr.framing.Length)
	case domain.FramingPrefix:
		return fr.takePrefixed()
	default: // domain.FramingDelimiter
		return fr.takeDelimited()
	}
}

// dropPending consumes bytes owed to a previous oversized run, reporting
// whether the reader is back in sync.
func (fr *frameReader) dropPending() bool {
	if fr.discard > 0 {
		n := fr.discard
		if int64(len(fr.buf)) < n {
			n = int64(len(fr.buf))
		}
		fr.buf = fr.buf[n:]
		fr.discard -= n
		return fr.discard == 0
	}
	for fr.resync {
		delim := fr.framing.Delimiter
		i := bytes.Index(fr.buf, delim)
		if i < 0 {
			// Keep the last len(delim)-1 bytes: a delimiter split across two
			// reads would otherwise be dropped and the stream never resync.
			if keep := len(delim) - 1; keep > 0 && len(fr.buf) > keep {
				fr.buf = fr.buf[len(fr.buf)-keep:]
			} else if keep <= 0 {
				fr.buf = fr.buf[:0]
			}
			return false
		}
		if i > 0 {
			// A delimiter with payload ahead of it is NOT the abandoned
			// run's tail catching up: everything buffered was discarded the
			// moment the run was abandoned (fr.buf reset to empty), so any
			// payload accumulated since then is bytes we have never yet
			// evaluated — indistinguishable from the start of a fresh
			// connection. Leave it for normal (non-resync) framing to serve,
			// which is exactly how a frame this size would be served if
			// resync had never triggered (FR-028: a complete frame is always
			// served whole once a delimiter is found, cap or no cap).
			fr.resync = false
			return true
		}
		// i == 0: a bare delimiter with nothing ahead of it is the
		// abandoned run's own tail finally catching up — discard it and
		// keep looking; a genuinely still-unterminated run can shed several
		// of these before its real content resumes.
		fr.buf = fr.buf[i+len(delim):]
	}
	return true
}

func (fr *frameReader) takeDelimited() ([]byte, bool, error) {
	delim := fr.framing.Delimiter
	if i := bytes.Index(fr.buf, delim); i >= 0 {
		end := i + len(delim)
		frame := append([]byte(nil), fr.buf[:end]...)
		fr.buf = fr.buf[end:]
		return frame, true, nil
	}
	if fr.overCap(int64(len(fr.buf))) {
		// Abandon the run and hunt for the next delimiter. Anything already
		// buffered is part of the abandoned run by definition.
		fr.buf = fr.buf[:0]
		fr.resync = true
		return nil, false, ErrFrameTooLarge
	}
	return nil, false, nil
}

func (fr *frameReader) takeFixed(n int) ([]byte, bool, error) {
	if n <= 0 {
		return nil, false, fmt.Errorf("streamplane: length framing needs a positive length, got %d", n)
	}
	if fr.overCap(int64(n)) {
		// Every frame of this endpoint is larger than the cap, so there is
		// no point buffering: skip this one and report it.
		fr.discard = int64(n) - int64(len(fr.buf))
		if fr.discard < 0 {
			fr.discard = 0
		}
		fr.buf = fr.buf[:0]
		return nil, false, ErrFrameTooLarge
	}
	if len(fr.buf) < n {
		return nil, false, nil
	}
	frame := append([]byte(nil), fr.buf[:n]...)
	fr.buf = fr.buf[n:]
	return frame, true, nil
}

func (fr *frameReader) takePrefixed() ([]byte, bool, error) {
	w := fr.framing.PrefixWidth
	if w <= 0 || w > 8 {
		return nil, false, fmt.Errorf("streamplane: prefix framing needs a width of 1..8 bytes, got %d", w)
	}
	if len(fr.buf) < w {
		return nil, false, nil
	}
	payload := int64(decodePrefix(fr.buf[:w], fr.framing.PrefixEndian))
	if payload < 0 {
		return nil, false, fmt.Errorf("streamplane: prefix framing declared a negative payload length")
	}
	if fr.overCap(int64(w) + payload) {
		// The prefix is trusted for arithmetic but not for allocation:
		// drop it and the payload it claims, without ever buffering them.
		fr.discard = int64(w) + payload - int64(len(fr.buf))
		if fr.discard < 0 {
			fr.discard = 0
		}
		fr.buf = fr.buf[:0]
		return nil, false, ErrFrameTooLarge
	}
	total := int64(w) + payload
	if int64(len(fr.buf)) < total {
		return nil, false, nil
	}
	frame := append([]byte(nil), fr.buf[:total]...)
	fr.buf = fr.buf[total:]
	return frame, true, nil
}

// overCap reports whether n bytes exceed the configured cap. A cap of zero or
// less is unbounded, matching how LYREBIRD_BODY_CAP_BYTES is treated
// everywhere else it is consulted.
func (fr *frameReader) overCap(n int64) bool {
	return fr.capBytes > 0 && n > fr.capBytes
}

// decodePrefix reads a big- or little-endian unsigned integer of len(b)
// bytes. Widths other than 1..8 are rejected by the caller. Little-endian is
// opt-in; anything else (including an unset value) is big-endian, which is
// the conventional byte order for a length prefix.
func decodePrefix(b []byte, endian domain.Endianness) uint64 {
	padded := make([]byte, 8)
	if endian == domain.EndianLittle {
		copy(padded, b)
		return binary.LittleEndian.Uint64(padded)
	}
	copy(padded[8-len(b):], b)
	return binary.BigEndian.Uint64(padded)
}

// terminator returns the bytes appended to a built answer so it is a complete
// frame on this endpoint's wire. Only delimiter framing has one: a
// length-framed or prefix-framed answer carries its own shape, and appending
// anything would corrupt it.
func terminator(framing domain.Framing) []byte {
	if framing.Kind == domain.FramingDelimiter {
		return framing.Delimiter
	}
	return nil
}

// payloadOf strips the framing overhead from a complete frame, returning just
// the bytes the protocol carries.
//
// This is what gets PROJECTED into the envelope, while the full frame is what
// gets RECORDED. The distinction matters: the traffic log must show exactly
// what crossed the wire, but a rule author matching on $.text or $.fields.0
// means the content, not the content plus a terminator they did not write.
// Without this, every declared field on a delimiter-framed endpoint would
// silently drag "\r\n" along and no obvious `equals` condition would ever
// hold.
func payloadOf(frame []byte, framing domain.Framing) []byte {
	switch framing.Kind {
	case domain.FramingPrefix:
		// The length prefix is framing, not content — the same way a
		// delimiter is.
		if framing.PrefixWidth > 0 && len(frame) >= framing.PrefixWidth {
			return frame[framing.PrefixWidth:]
		}
		return frame
	case domain.FramingLength:
		// Fixed-length frames are all content; there is no overhead to trim.
		return frame
	default: // domain.FramingDelimiter
		if d := framing.Delimiter; len(d) > 0 && bytes.HasSuffix(frame, d) {
			return frame[:len(frame)-len(d)]
		}
		return frame
	}
}
