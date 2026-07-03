package gc

import (
	"context"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"
)

type fakePruner struct {
	trafficCalls int32
	expiredCalls int32
}

func (f *fakePruner) PruneTraffic(_ context.Context, _ time.Time) (int, error) {
	atomic.AddInt32(&f.trafficCalls, 1)
	return 1, nil
}

func (f *fakePruner) PruneExpiredEphemeralMocks(_ context.Context, _ time.Time) (int, error) {
	atomic.AddInt32(&f.expiredCalls, 1)
	return 1, nil
}

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestLoopSweepsOnInterval(t *testing.T) {
	pruner := &fakePruner{}
	l := New(10*time.Millisecond, time.Hour, pruner, silentLogger())

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
	l := New(5*time.Millisecond, time.Hour, pruner, silentLogger())
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
