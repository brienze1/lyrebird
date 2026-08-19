package streamplane

import (
	"context"
	"fmt"
	"sync"

	"github.com/brienze1/lyrebird/internal/domain"
)

// Registry tracks which stand-in currently holds which endpoint, keyed by
// space. It is the concrete usecase.ConnectionRegistry.
//
// This is runtime state, deliberately never persisted: occupancy dies with
// the process, and a stale "connected" row surviving a restart would refuse
// every reconnect until someone cleared it by hand.
type Registry struct {
	mu    sync.Mutex
	conns map[string]*conn // "partition\x00endpoint" -> the holder
}

// NewRegistry builds an empty registry.
func NewRegistry() *Registry {
	return &Registry{conns: map[string]*conn{}}
}

// errEndpointOccupied reports that another stand-in already holds the
// endpoint. A wire carries one peripheral (FR-030), and a second silent
// claimant is almost always a stale process from a previous run — so it is
// refused by name rather than served alongside the first.
type errEndpointOccupied struct{ endpoint string }

func (e *errEndpointOccupied) Error() string {
	return fmt.Sprintf("streamplane: endpoint %q already connected", e.endpoint)
}

// claim registers c as the holder of (partition, endpoint), or returns
// *errEndpointOccupied if one is already held.
func (r *Registry) claim(partition, endpoint string, c *conn) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := registryKey(partition, endpoint)
	if _, taken := r.conns[key]; taken {
		return &errEndpointOccupied{endpoint: endpoint}
	}
	r.conns[key] = c
	return nil
}

// release drops c's claim, but only if c is still the holder: a connection
// that was closed by CloseSpace and replaced by a fresh one before its own
// deferred release ran must not evict its successor.
func (r *Registry) release(partition, endpoint string, c *conn) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := registryKey(partition, endpoint)
	if r.conns[key] == c {
		delete(r.conns, key)
	}
}

// Emit queues a frame for the stand-in holding this endpoint, through that
// connection's single writer so it can never interleave with an answer or a
// cadence tick (FR-033).
//
// It returns domain.ErrNotFound when nothing holds the endpoint. That is the
// whole point of FR-015: a test injecting into a connection that has already
// closed must be told so plainly, because the alternative — succeeding
// silently — turns a dead stand-in into a scenario that fails much later for
// a reason nobody can trace back here.
func (r *Registry) Emit(ctx context.Context, partition, endpoint string, frameSpec []byte) error {
	r.mu.Lock()
	c := r.conns[registryKey(partition, endpoint)]
	r.mu.Unlock()

	if c == nil {
		return fmt.Errorf("%w: no stand-in is connected to endpoint %q in space %q",
			domain.ErrNotFound, endpoint, partition)
	}
	return c.emit(ctx, frameSpec)
}

// CloseSpace stops every cadence and closes every connection in the
// partition, so nothing survives a reset by being mid-stream (FR-031). The
// stand-in observes exactly what it would observe if the peripheral were
// unplugged, and reconnects into clean state.
func (r *Registry) CloseSpace(partition string) {
	r.mu.Lock()
	var doomed []*conn
	for key, c := range r.conns {
		if p, _ := splitRegistryKey(key); p == partition {
			doomed = append(doomed, c)
			delete(r.conns, key)
		}
	}
	r.mu.Unlock()

	// Closing happens outside the lock: a connection's close path releases
	// its own claim, which would deadlock against a held mutex.
	for _, c := range doomed {
		c.close()
	}
}

// Occupied reports whether a stand-in currently holds this endpoint.
func (r *Registry) Occupied(partition, endpoint string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.conns[registryKey(partition, endpoint)]
	return ok
}

// closeAll drops every connection, used when the whole server shuts down.
func (r *Registry) closeAll() {
	r.mu.Lock()
	doomed := make([]*conn, 0, len(r.conns))
	for key, c := range r.conns {
		doomed = append(doomed, c)
		delete(r.conns, key)
	}
	r.mu.Unlock()

	for _, c := range doomed {
		c.close()
	}
}

// registryKey joins a space and an endpoint name with a NUL, which neither
// can contain — a plain "/" separator would let the space "a" with endpoint
// "b/c" collide with the space "a/b" and endpoint "c".
func registryKey(partition, endpoint string) string {
	return partition + "\x00" + endpoint
}

func splitRegistryKey(key string) (partition, endpoint string) {
	for i := 0; i < len(key); i++ {
		if key[i] == 0 {
			return key[:i], key[i+1:]
		}
	}
	return key, ""
}
