package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/brienze1/lyrebird/internal/domain"
	"github.com/brienze1/lyrebird/internal/infra/crypto"
)

// slowSealer wraps a real crypto.Sealer, adding a deliberate delay to every
// Open call. It exists only to make Store.ListMocks's per-row decrypt work
// slow and predictable, so a single ListMocks call's row iteration is
// unambiguously still in flight (not yet exhausted/closed) when a concurrent
// GC call is fired — without touching any production code path.
type slowSealer struct {
	inner crypto.Sealer
	delay time.Duration
}

func (s slowSealer) Seal(pt []byte) ([]byte, error) { return s.inner.Seal(pt) }
func (s slowSealer) Open(ct []byte) ([]byte, error) {
	time.Sleep(s.delay)
	return s.inner.Open(ct)
}

// TestListMocksHoldsConnectionExclusively is the deterministic (not
// stress/timing-noise-dependent) proof for this investigation's central
// architectural question: since store.go's openAndMigrate calls
// db.SetMaxOpenConns(1), there is exactly one physical *sql.DB connection for
// the whole Store. database/sql's documented contract is that a
// Query/QueryContext's returned *sql.Rows holds its connection checked out,
// exclusively, from the call until rows.Close() (explicit or automatic on
// exhaustion) — meaning a concurrent ExecContext/BeginTx (GC's
// PruneExpiredEphemeralMocks/PruneTraffic) has no free connection to run on
// and must block until the in-flight ListMocks call releases it.
//
// This is verified directly against the real Store.ListMocks and
// Store.PruneExpiredEphemeralMocks — not a toy database/sql example — using
// a slowSealer to make one ListMocks call's row iteration take long enough
// (n rows * a real, deliberate per-row sleep) that firing a concurrent GC
// call partway through is unambiguous, then asserting the GC call's
// completion timestamp is never before the ListMocks call's own completion
// timestamp — i.e. GC never actually finishes its work while ListMocks is
// still iterating.
//
// A companion stress test, TestConcurrentReadsAgainstGCPruneStress, tried a
// similar wall-clock "interval containment" check under heavy multi-goroutine
// contention and found it produces false positives (a goroutine's recorded
// start timestamp can precede the moment the Go scheduler actually lets it
// dial the DB, letting another operation's genuinely fast, correctly
// serialized work complete inside that scheduling gap) — see that test's own
// comment. THIS test is the trustworthy one for the exclusivity question,
// since with only two goroutines and no other contention, that scheduling-gap
// effect isn't in play.
func TestListMocksHoldsConnectionExclusively(t *testing.T) {
	const runs = 5
	for run := 0; run < runs; run++ {
		dir := t.TempDir()
		realSealer := mustSealer(t)
		sealer := slowSealer{inner: realSealer, delay: 5 * time.Millisecond}
		st, err := Open(context.Background(), filepath.Join(dir, "lyrebird.db"), sealer, silentLogger())
		if err != nil {
			t.Fatalf("Open(): %v", err)
		}
		ctx := context.Background()
		const partition = "p"
		const n = 60 // 60 rows * 5ms/row decrypt delay = a single ListMocks call spanning ~300ms+
		for i := 0; i < n; i++ {
			id := "m" + string(rune('a'+i%26)) + string(rune('0'+i/26))
			m := domain.Mock{
				ID: id, Partition: partition, Name: id, Lifetime: domain.LifetimeEphemeral,
				CreatedAt: time.Now(),
				Match:     domain.Match{},
				Action:    domain.Action{Kind: domain.ActionRespond, Respond: &domain.RespondAction{Status: 200}},
			}
			if err := st.CreateMock(ctx, m); err != nil {
				t.Fatalf("CreateMock: %v", err)
			}
		}

		type outcome struct {
			start, end time.Time
			err        error
		}
		listDone := make(chan outcome, 1)
		gcDone := make(chan outcome, 1)

		go func() {
			start := time.Now()
			_, err := st.ListMocks(ctx, partition)
			listDone <- outcome{start, time.Now(), err}
		}()

		// Give ListMocks a moment to genuinely be mid-iteration (well past
		// its first few rows, each costing ~5ms to decrypt) before firing
		// GC concurrently from another goroutine.
		time.Sleep(60 * time.Millisecond)
		go func() {
			start := time.Now()
			_, err := st.PruneExpiredEphemeralMocks(ctx, time.Now())
			gcDone <- outcome{start, time.Now(), err}
		}()

		lo := <-listDone
		gc := <-gcDone
		_ = st.Close()

		if lo.err != nil {
			t.Fatalf("run %d: ListMocks: %v", run, lo.err)
		}
		if gc.err != nil {
			t.Fatalf("run %d: PruneExpiredEphemeralMocks: %v", run, gc.err)
		}

		t.Logf("run %d: ListMocks=[%s,%s] (dur=%s)  GCPrune=[%s,%s] (dur=%s)",
			run, lo.start.Format("15:04:05.000000000"), lo.end.Format("15:04:05.000000000"), lo.end.Sub(lo.start),
			gc.start.Format("15:04:05.000000000"), gc.end.Format("15:04:05.000000000"), gc.end.Sub(gc.start))

		if gc.end.Before(lo.end) {
			t.Fatalf("run %d: GC's PruneExpiredEphemeralMocks completed at %s, BEFORE ListMocks finished at %s — "+
				"this would be a deterministic, non-stress proof that GC's Exec ran concurrently with an in-flight "+
				"ListMocks iteration on the single shared connection, contradicting the exclusive-checkout hypothesis",
				run, gc.end.Format("15:04:05.000000000"), lo.end.Format("15:04:05.000000000"))
		}
	}
}
