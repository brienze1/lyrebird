// Package gc runs the background sweeper that keeps Lyrebird's disposable
// state bounded: traffic older than the retention window and ephemeral
// mocks past their TTL are pruned on an interval (constitution Principle III,
// FR-027).
package gc

import (
	"context"
	"log/slog"
	"time"
)

// Pruner is the subset of *store.Store the GC loop depends on. Defined here
// (rather than imported from store) so gc has no compile-time dependency on
// the store package's concrete type — only the two operations it needs.
type Pruner interface {
	PruneTraffic(ctx context.Context, olderThan time.Time) (int, error)
	PruneExpiredEphemeralMocks(ctx context.Context, now time.Time) (int, error)
}

// Loop periodically sweeps expired traffic and ephemeral mocks.
type Loop struct {
	interval   time.Duration
	trafficTTL time.Duration
	store      Pruner
	log        *slog.Logger

	cancel context.CancelFunc
	done   chan struct{}
}

// New builds a Loop. Call Start to begin sweeping and Stop to end it.
func New(interval, trafficTTL time.Duration, store Pruner, log *slog.Logger) *Loop {
	return &Loop{interval: interval, trafficTTL: trafficTTL, store: store, log: log}
}

// Start begins the sweep loop in a background goroutine. It returns
// immediately; the loop stops when ctx is done or Stop is called.
func (l *Loop) Start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	l.cancel = cancel
	l.done = make(chan struct{})

	go func() {
		defer close(l.done)
		ticker := time.NewTicker(l.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				l.sweep(ctx)
			}
		}
	}()
}

// Stop cancels the loop and waits for the background goroutine to exit.
func (l *Loop) Stop() {
	if l.cancel == nil {
		return
	}
	l.cancel()
	<-l.done
}

func (l *Loop) sweep(ctx context.Context) {
	now := time.Now()

	if n, err := l.store.PruneTraffic(ctx, now.Add(-l.trafficTTL)); err != nil {
		l.log.Warn("gc: prune traffic failed", "err", err)
	} else if n > 0 {
		l.log.Info("gc: pruned traffic", "count", n)
	}

	if n, err := l.store.PruneExpiredEphemeralMocks(ctx, now); err != nil {
		l.log.Warn("gc: prune expired ephemeral mocks failed", "err", err)
	} else if n > 0 {
		l.log.Info("gc: pruned expired ephemeral mocks", "count", n)
	}
}
