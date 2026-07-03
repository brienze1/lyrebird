package proxy

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestCappedCaptureNeverExceedsCap(t *testing.T) {
	input := []byte("this is way more than ten bytes")
	c := newCappedCapture(10)
	n, err := c.Write(input)
	if err != nil {
		t.Fatalf("Write(): %v", err)
	}
	if n != len(input) {
		t.Errorf("Write() reported n=%d, want %d (full length, even though only 10 were stored)", n, len(input))
	}
	body, truncated, total := c.Result()
	if len(body) != 10 {
		t.Errorf("captured body length = %d, want 10 (the cap)", len(body))
	}
	if !truncated {
		t.Error("truncated = false, want true")
	}
	if total != int64(len(input)) {
		t.Errorf("total = %d, want %d", total, len(input))
	}
}

func TestCappedCaptureUnderCapIsNotTruncated(t *testing.T) {
	c := newCappedCapture(100)
	_, _ = c.Write([]byte("short"))
	body, truncated, total := c.Result()
	if string(body) != "short" || truncated || total != 5 {
		t.Errorf("Result() = (%q, %v, %d), want (\"short\", false, 5)", body, truncated, total)
	}
}

func TestCappedCaptureWithZeroOrNegativeCapStoresNothing(t *testing.T) {
	for _, cap := range []int64{0, -1} {
		c := newCappedCapture(cap)
		n, err := c.Write([]byte("anything"))
		if err != nil || n != 8 {
			t.Fatalf("cap=%d: Write() = (%d, %v), want (8, nil) — must still report full length, never error", cap, n, err)
		}
		body, truncated, total := c.Result()
		if len(body) != 0 || !truncated || total != 8 {
			t.Fatalf("cap=%d: Result() = (%q, %v, %d), want (\"\", true, 8)", cap, body, truncated, total)
		}
	}
}

func TestCappedCaptureAcrossMultipleWrites(t *testing.T) {
	c := newCappedCapture(5)
	_, _ = c.Write([]byte("abc"))
	_, _ = c.Write([]byte("def")) // total now 6, cap 5 — only "d" fits into the remaining room
	body, truncated, total := c.Result()
	if string(body) != "abcde" {
		t.Errorf("captured body = %q, want %q", body, "abcde")
	}
	if !truncated || total != 6 {
		t.Errorf("Result() truncated/total = %v/%d, want true/6", truncated, total)
	}
}

func TestNewCappedTeePassesFullStreamThroughUnmodified(t *testing.T) {
	original := strings.Repeat("x", 10_000)
	src := io.NopCloser(bytes.NewReader([]byte(original)))

	tee, capture := newCappedTee(src, 16)
	got, err := io.ReadAll(tee)
	if err != nil {
		t.Fatalf("ReadAll(tee): %v", err)
	}
	if string(got) != original {
		t.Fatalf("tee altered the stream: got %d bytes, want %d bytes unmodified", len(got), len(original))
	}

	body, truncated, total := capture.Result()
	if len(body) != 16 {
		t.Errorf("captured body length = %d, want 16 (the cap)", len(body))
	}
	if !truncated || total != 10_000 {
		t.Errorf("Result() truncated/total = %v/%d, want true/10000", truncated, total)
	}
}
