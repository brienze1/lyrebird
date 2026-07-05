package store

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/brienze1/lyrebird/internal/domain"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	st, err := Open(context.Background(), filepath.Join(dir, "lyrebird.db"), mustSealer(t), silentLogger())
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestSetUpstreamThenListReturnsIt(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	u := domain.Upstream{Partition: "default", MatchHost: "api.example.com", TargetURL: "https://api.example.com", TLSSkipVerify: false}
	if err := st.SetUpstream(ctx, u); err != nil {
		t.Fatalf("SetUpstream(): %v", err)
	}

	got, err := st.ListUpstreams(ctx, "default")
	if err != nil {
		t.Fatalf("ListUpstreams(): %v", err)
	}
	if len(got) != 1 || got[0] != u {
		t.Fatalf("ListUpstreams() = %+v, want [%+v]", got, u)
	}
}

func TestSetUpstreamIsIdempotentAndUpdatesInPlace(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	first := domain.Upstream{Partition: "default", MatchHost: "api.example.com", TargetURL: "https://old.example.com"}
	if err := st.SetUpstream(ctx, first); err != nil {
		t.Fatalf("SetUpstream() first: %v", err)
	}
	second := domain.Upstream{Partition: "default", MatchHost: "api.example.com", TargetURL: "https://new.example.com", TLSSkipVerify: true}
	if err := st.SetUpstream(ctx, second); err != nil {
		t.Fatalf("SetUpstream() second: %v", err)
	}

	got, err := st.ListUpstreams(ctx, "default")
	if err != nil {
		t.Fatalf("ListUpstreams(): %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ListUpstreams() = %d upstreams, want 1 (update, not duplicate)", len(got))
	}
	if got[0] != second {
		t.Fatalf("ListUpstreams()[0] = %+v, want the updated value %+v", got[0], second)
	}
}

func TestListUpstreamsIsolatedByPartition(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	if err := st.SetUpstream(ctx, domain.Upstream{Partition: "team-a", MatchHost: "api.example.com", TargetURL: "https://a.example.com"}); err != nil {
		t.Fatalf("SetUpstream() team-a: %v", err)
	}
	if err := st.SetUpstream(ctx, domain.Upstream{Partition: "team-b", MatchHost: "api.example.com", TargetURL: "https://b.example.com"}); err != nil {
		t.Fatalf("SetUpstream() team-b: %v", err)
	}

	gotA, err := st.ListUpstreams(ctx, "team-a")
	if err != nil {
		t.Fatalf("ListUpstreams(team-a): %v", err)
	}
	if len(gotA) != 1 || gotA[0].TargetURL != "https://a.example.com" {
		t.Fatalf("ListUpstreams(team-a) = %+v, want just the team-a upstream", gotA)
	}
}

func TestDeleteUpstreamsByPartitionOnlyAffectsThatPartition(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	if err := st.SetUpstream(ctx, domain.Upstream{Partition: "team-a", MatchHost: "api.example.com", TargetURL: "https://a.example.com"}); err != nil {
		t.Fatalf("SetUpstream() team-a: %v", err)
	}
	if err := st.SetUpstream(ctx, domain.Upstream{Partition: "team-b", MatchHost: "api.example.com", TargetURL: "https://b.example.com"}); err != nil {
		t.Fatalf("SetUpstream() team-b: %v", err)
	}

	if err := st.DeleteUpstreamsByPartition(ctx, "team-a"); err != nil {
		t.Fatalf("DeleteUpstreamsByPartition(): %v", err)
	}

	gotA, err := st.ListUpstreams(ctx, "team-a")
	if err != nil || len(gotA) != 0 {
		t.Fatalf("ListUpstreams(team-a) after delete = %+v, %v, want empty", gotA, err)
	}
	gotB, err := st.ListUpstreams(ctx, "team-b")
	if err != nil || len(gotB) != 1 {
		t.Fatalf("ListUpstreams(team-b) after deleting team-a = %+v, %v, want untouched", gotB, err)
	}
}

// TestSetUpstreamConcurrentCallsAllPersist exercises SetUpstream under real
// concurrent load: the Admin REST API can receive many simultaneous upstream
// configuration requests for the same partition, so a lost or corrupted
// write here would silently drop or scramble a routing target. Each
// goroutine sets a distinct, uniquely-identifiable upstream (distinct
// MatchHost, distinguishing TargetURL); after all finish, ListUpstreams must
// return exactly all of them, with each upstream's MatchHost/TargetURL
// pairing intact — proving no write is lost, duplicated, or
// cross-contaminated with another upstream's fields under concurrent
// access. Like TestAppendTrafficConcurrentCallsAllPersist in
// traffic_test.go, this proves Go-level race-freedom through store.Open's
// single-connection pool (db.SetMaxOpenConns(1)), not multi-connection
// SQLite atomicity.
func TestSetUpstreamConcurrentCallsAllPersist(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	const n = 50
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			u := domain.Upstream{
				Partition: "default",
				MatchHost: fmt.Sprintf("host-%d.example.com", i),
				TargetURL: fmt.Sprintf("https://target-%d.example.com", i),
			}
			errs[i] = st.SetUpstream(ctx, u)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("SetUpstream() goroutine %d: %v", i, err)
		}
	}

	got, err := st.ListUpstreams(ctx, "default")
	if err != nil {
		t.Fatalf("ListUpstreams(): %v", err)
	}
	if len(got) != n {
		t.Fatalf("ListUpstreams() returned %d upstreams, want %d (some concurrent writes lost or duplicated)", len(got), n)
	}

	byHost := make(map[string]domain.Upstream, n)
	for _, u := range got {
		if _, dup := byHost[u.MatchHost]; dup {
			t.Fatalf("duplicate upstream match_host %q in ListUpstreams() result", u.MatchHost)
		}
		byHost[u.MatchHost] = u
	}
	for i := 0; i < n; i++ {
		host := fmt.Sprintf("host-%d.example.com", i)
		u, ok := byHost[host]
		if !ok {
			t.Fatalf("upstream %q missing from ListUpstreams() result after concurrent SetUpstream calls", host)
		}
		wantTarget := fmt.Sprintf("https://target-%d.example.com", i)
		if u.TargetURL != wantTarget {
			t.Errorf("upstream %q TargetURL = %s, want %s (cross-contamination between concurrent writes)", host, u.TargetURL, wantTarget)
		}
	}
}
