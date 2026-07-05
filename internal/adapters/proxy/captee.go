package proxy

import (
	"bytes"
	"io"
)

// cappedCapture accumulates at most capBytes into an in-memory buffer while
// tracking the true total size seen, so a recording can be truncated
// without ever buffering more than capBytes regardless of how large the
// real body is (contract step 7: "bodies stream through unbounded
// regardless of the recording cap").
type cappedCapture struct {
	cap   int64
	buf   bytes.Buffer
	total int64
}

func newCappedCapture(capBytes int64) *cappedCapture {
	c := &cappedCapture{cap: capBytes}
	if capBytes > 0 && capBytes < 1<<24 {
		c.buf.Grow(int(capBytes))
	}
	return c
}

// Write never errors — a capture failure must never break the real stream
// it is teeing off of.
func (c *cappedCapture) Write(p []byte) (int, error) {
	c.total += int64(len(p))
	if room := c.cap - int64(c.buf.Len()); room > 0 {
		if int64(len(p)) > room {
			c.buf.Write(p[:room])
		} else {
			c.buf.Write(p)
		}
	}
	return len(p), nil
}

// Result reports the (possibly truncated) captured bytes, whether
// truncation occurred, and the true total size seen. Safe to call once the
// source reader has been fully drained (or reading has stopped).
func (c *cappedCapture) Result() (body []byte, truncated bool, totalSize int64) {
	return c.buf.Bytes(), c.total > c.cap, c.total
}

// newCappedTee wraps src so every byte read through it is also written into
// a cappedCapture, while the bytes returned to the caller are completely
// unmodified and unbounded.
func newCappedTee(src io.ReadCloser, capBytes int64) (io.ReadCloser, *cappedCapture) {
	c := newCappedCapture(capBytes)
	return readCloser{Reader: io.TeeReader(src, c), Closer: src}, c
}
