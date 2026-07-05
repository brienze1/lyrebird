package gc

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/brienze1/lyrebird/internal/infra/clock"
)

type fakePruner struct {
	trafficCalls int32
	expiredCalls int32

	mu             sync.Mutex
	lastTrafficArg time.Time
	lastExpiredArg time.Time
}

func (f *fakePruner) PruneTraffic(_ context.Context, olderThan time.Time) (int, error) {
	f.mu.Lock()
	f.lastTrafficArg = olderThan
	f.mu.Unlock()
	atomic.AddInt32(&f.trafficCalls, 1)
	return 1, nil
}

func (f *fakePruner) PruneExpiredEphemeralMocks(_ context.Context, now time.Time) (int, error) {
	f.mu.Lock()
	f.lastExpiredArg = now
	f.mu.Unlock()
	atomic.AddInt32(&f.expiredCalls, 1)
	return 1, nil
}

func (f *fakePruner) lastExpired() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastExpiredArg
}

func (f *fakePruner) lastTraffic() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastTrafficArg
}

// fakeClock is a trivial, concurrency-safe Clock double letting tests pin
// "now" to an arbitrary, obviously-not-real-wall-clock instant, so a sweep's
// use of it (as opposed to a stray direct time.Now() call) can be proven
// rather than assumed.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock(t time.Time) *fakeClock {
	return &fakeClock{now: t}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestLoopSweepsOnInterval(t *testing.T) {
	pruner := &fakePruner{}
	l := New(10*time.Millisecond, time.Hour, pruner, clock.System{}, silentLogger())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l.Start(ctx)
	defer l.Stop()

	deadline := time.After(2 * time.Second)
	for {
		if atomic.LoadInt32(&pruner.trafficCalls) > 0 && atomic.LoadInt32(&pruner.expiredCalls) > 0 {
			return
		}
		select {
		case <-deadline:
			t.Fatal("GC loop did not sweep within 2s")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestStopEndsTheLoop(t *testing.T) {
	pruner := &fakePruner{}
	l := New(5*time.Millisecond, time.Hour, pruner, clock.System{}, silentLogger())
	l.Start(context.Background())

	time.Sleep(20 * time.Millisecond) // let at least one sweep happen
	l.Stop()

	before := atomic.LoadInt32(&pruner.trafficCalls)
	time.Sleep(50 * time.Millisecond)
	after := atomic.LoadInt32(&pruner.trafficCalls)

	if after != before {
		t.Fatalf("sweeps continued after Stop(): before=%d after=%d", before, after)
	}
}

// TestSweepUsesInjectedClockNotWallClock proves the "now" value each sweep
// compares TTL/retention windows against comes from the injected Clock, not
// a stray direct time.Now() call — the exact bug class this test exists to
// catch (found during pass 10's adversarial audit: every sibling usecase
// struct already threaded a Clock through consistently, gc.Loop alone did
// not). The fixed instant here (year 2000) is picked specifically because it
// can never coincide with a real time.Now() reading, so a regression back to
// a direct time.Now() call would make this test fail deterministically, not
// flakily.
func TestSweepUsesInjectedClockNotWallClock(t *testing.T) {
	fixed := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	clk := newFakeClock(fixed)
	pruner := &fakePruner{}
	trafficTTL := 30 * time.Minute
	l := New(10*time.Millisecond, trafficTTL, pruner, clk, silentLogger())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l.Start(ctx)
	defer l.Stop()

	deadline := time.After(2 * time.Second)
	for atomic.LoadInt32(&pruner.expiredCalls) == 0 {
		select {
		case <-deadline:
			t.Fatal("GC loop did not sweep within 2s")
		case <-time.After(5 * time.Millisecond):
		}
	}

	if got := pruner.lastExpired(); !got.Equal(fixed) {
		t.Fatalf("PruneExpiredEphemeralMocks was called with now=%v, want the injected clock's fixed %v (not real wall-clock time)", got, fixed)
	}
	wantTrafficCutoff := fixed.Add(-trafficTTL)
	if got := pruner.lastTraffic(); !got.Equal(wantTrafficCutoff) {
		t.Fatalf("PruneTraffic was called with olderThan=%v, want %v (fixed clock minus trafficTTL, not real wall-clock time)", got, wantTrafficCutoff)
	}
}
