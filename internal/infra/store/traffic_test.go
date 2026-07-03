package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/brienze1/lyrebird/internal/domain"
	"github.com/brienze1/lyrebird/internal/usecase"
)

func sampleTraffic(id, partition string, ts time.Time) domain.TrafficRecord {
	return domain.TrafficRecord{
		ID: id, Partition: partition, Timestamp: ts,
		Method: "GET", Host: "example.com", Path: "/foo",
		Request: []byte(`{"headers":{},"body":null}`), Decision: domain.DecisionProxied,
		Response: []byte(`{"headers":{},"body":null}`), Status: 200, LatencyMS: 5,
	}
}

func TestAppendThenGetTrafficRoundTrip(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	mockID := "mock-1"
	rec := sampleTraffic("t1", "default", time.Now())
	rec.MatchedMockID = &mockID

	if err := st.AppendTraffic(ctx, rec); err != nil {
		t.Fatalf("AppendTraffic(): %v", err)
	}

	got, err := st.GetTraffic(ctx, "default", "t1")
	if err != nil {
		t.Fatalf("GetTraffic(): %v", err)
	}
	if got.ID != rec.ID || got.Method != rec.Method || got.Status != rec.Status || got.Decision != rec.Decision {
		t.Errorf("GetTraffic() = %+v, want fields matching %+v", got, rec)
	}
	if got.MatchedMockID == nil || *got.MatchedMockID != mockID {
		t.Errorf("MatchedMockID = %v, want %q", got.MatchedMockID, mockID)
	}
	if string(got.Request) != string(rec.Request) || string(got.Response) != string(rec.Response) {
		t.Errorf("Request/Response blobs did not round-trip: got req=%s resp=%s", got.Request, got.Response)
	}
	// The "timestamp" column is documented (schema.sql) as unix millis, so a
	// round trip is only guaranteed at millisecond granularity, not the
	// original time.Now()'s nanosecond precision.
	if !got.Timestamp.Equal(rec.Timestamp.Truncate(time.Millisecond)) {
		t.Errorf("Timestamp = %v, want %v (truncated to ms)", got.Timestamp, rec.Timestamp.Truncate(time.Millisecond))
	}
}

func TestGetTrafficHandlesNilMatchedMockID(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	rec := sampleTraffic("t1", "default", time.Now()) // MatchedMockID left nil
	if err := st.AppendTraffic(ctx, rec); err != nil {
		t.Fatalf("AppendTraffic(): %v", err)
	}

	got, err := st.GetTraffic(ctx, "default", "t1")
	if err != nil {
		t.Fatalf("GetTraffic(): %v", err)
	}
	if got.MatchedMockID != nil {
		t.Errorf("MatchedMockID = %v, want nil", got.MatchedMockID)
	}
}

func TestGetTrafficNotFound(t *testing.T) {
	st := openTestStore(t)
	_, err := st.GetTraffic(context.Background(), "default", "does-not-exist")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetTraffic() on missing id = %v, want ErrNotFound", err)
	}
}

func TestGetTrafficUndecryptableRowIsTreatedAsAbsent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lyrebird.db")
	sealerA := mustSealer(t)
	st, err := Open(context.Background(), path, sealerA, silentLogger())
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	defer func() { _ = st.Close() }()

	if err := st.AppendTraffic(context.Background(), sampleTraffic("t1", "default", time.Now())); err != nil {
		t.Fatalf("AppendTraffic(): %v", err)
	}

	// Reopen under a different key — same FR-029 discipline as the
	// disposability suite: undecryptable rows are absent, not errors.
	_ = st.Close()
	st2, err := Open(context.Background(), path, mustSealer(t), silentLogger())
	if err != nil {
		t.Fatalf("Open() with different key: %v", err)
	}
	defer func() { _ = st2.Close() }()

	_, err = st2.GetTraffic(context.Background(), "default", "t1")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetTraffic() under a different key = %v, want ErrNotFound (treated as absent)", err)
	}
}

func TestListTrafficOrdersNewestFirstAndIsolatesByPartition(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	base := time.Now()

	if err := st.AppendTraffic(ctx, sampleTraffic("older", "default", base)); err != nil {
		t.Fatalf("AppendTraffic(older): %v", err)
	}
	if err := st.AppendTraffic(ctx, sampleTraffic("newer", "default", base.Add(time.Second))); err != nil {
		t.Fatalf("AppendTraffic(newer): %v", err)
	}
	if err := st.AppendTraffic(ctx, sampleTraffic("other-partition", "other", base)); err != nil {
		t.Fatalf("AppendTraffic(other-partition): %v", err)
	}

	got, err := st.ListTraffic(ctx, "default", usecase.TrafficFilter{})
	if err != nil {
		t.Fatalf("ListTraffic(): %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListTraffic(default) = %d records, want 2 (partition isolation)", len(got))
	}
	if got[0].ID != "newer" || got[1].ID != "older" {
		t.Fatalf("ListTraffic() order = [%s, %s], want [newer, older]", got[0].ID, got[1].ID)
	}
}

func TestListTrafficFiltersByStatusHostMethodAndPathPrefix(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	match := sampleTraffic("match", "default", time.Now())
	match.Status, match.Host, match.Method, match.Path = 404, "match.example.com", "POST", "/api/v1/thing"
	miss := sampleTraffic("miss", "default", time.Now())
	miss.Status, miss.Host, miss.Method, miss.Path = 200, "other.example.com", "GET", "/health"

	if err := st.AppendTraffic(ctx, match); err != nil {
		t.Fatalf("AppendTraffic(match): %v", err)
	}
	if err := st.AppendTraffic(ctx, miss); err != nil {
		t.Fatalf("AppendTraffic(miss): %v", err)
	}

	status404 := 404
	got, err := st.ListTraffic(ctx, "default", usecase.TrafficFilter{
		Status: &status404, Host: "match.example.com", Method: "POST", PathPrefix: "/api/",
	})
	if err != nil {
		t.Fatalf("ListTraffic(): %v", err)
	}
	if len(got) != 1 || got[0].ID != "match" {
		t.Fatalf("ListTraffic(filtered) = %+v, want just [match]", got)
	}
}

func TestListTrafficPathPrefixEscapesLikeMetacharacters(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	literal := sampleTraffic("literal", "default", time.Now())
	literal.Path = "/100%/done"
	decoy := sampleTraffic("decoy", "default", time.Now())
	decoy.Path = "/100X/done" // would also match if '%' were treated as a SQL wildcard instead of literal

	if err := st.AppendTraffic(ctx, literal); err != nil {
		t.Fatalf("AppendTraffic(literal): %v", err)
	}
	if err := st.AppendTraffic(ctx, decoy); err != nil {
		t.Fatalf("AppendTraffic(decoy): %v", err)
	}

	got, err := st.ListTraffic(ctx, "default", usecase.TrafficFilter{PathPrefix: "/100%/"})
	if err != nil {
		t.Fatalf("ListTraffic(): %v", err)
	}
	if len(got) != 1 || got[0].ID != "literal" {
		t.Fatalf("ListTraffic(PathPrefix with literal %%) = %+v, want just [literal]", got)
	}
}

func TestClearTrafficOnlyAffectsItsPartition(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	if err := st.AppendTraffic(ctx, sampleTraffic("a", "default", time.Now())); err != nil {
		t.Fatalf("AppendTraffic(a): %v", err)
	}
	if err := st.AppendTraffic(ctx, sampleTraffic("b", "other", time.Now())); err != nil {
		t.Fatalf("AppendTraffic(b): %v", err)
	}

	if err := st.ClearTraffic(ctx, "default"); err != nil {
		t.Fatalf("ClearTraffic(): %v", err)
	}

	gotDefault, err := st.ListTraffic(ctx, "default", usecase.TrafficFilter{})
	if err != nil || len(gotDefault) != 0 {
		t.Fatalf("ListTraffic(default) after clear = %+v, %v, want empty", gotDefault, err)
	}
	gotOther, err := st.ListTraffic(ctx, "other", usecase.TrafficFilter{})
	if err != nil || len(gotOther) != 1 {
		t.Fatalf("ListTraffic(other) after clearing default = %+v, %v, want untouched", gotOther, err)
	}
}
