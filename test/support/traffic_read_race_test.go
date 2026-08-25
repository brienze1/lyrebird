package support

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/brienze1/lyrebird/internal/adapters/proxy"
	"github.com/brienze1/lyrebird/internal/domain"
	"github.com/brienze1/lyrebird/internal/usecase"
)

// raceUpstreamLister reports one fixed upstream, so an unmatched request
// falls through serveProxied into a real httputil.ReverseProxy round trip —
// which preserves the fake upstream's own Content-Length framing, unlike a
// hand-written respond/error body (WriteHeader+Write+explicit Flush, which
// this repro confirmed forces chunked framing and so cannot complete a
// client-side body read before Handler.ServeHTTP itself returns). That
// framing difference is exactly what lets a proxied response reach the
// client before its own recordTraffic call — the same seam the production
// race exploits.
type raceUpstreamLister struct{ upstream domain.Upstream }

func (r raceUpstreamLister) Execute(context.Context, string) ([]domain.Upstream, error) {
	return []domain.Upstream{r.upstream}, nil
}

// raceMockMatcher matches exactly once (the first call), then reports no
// match for every call after — modelling "request 1 hits a mock, request 2
// falls through to spy passthrough", the same shape as mock_override.
// feature's failing scenario (gold->mocked, then basic->proxied).
type raceMockMatcher struct {
	mu      sync.Mutex
	matched bool
}

func (m *raceMockMatcher) Execute(context.Context, string, usecase.MatchInput) (domain.Mock, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.matched {
		return domain.Mock{}, false, nil
	}
	m.matched = true
	return domain.Mock{
		ID:     "gold",
		Action: domain.Action{Kind: domain.ActionRespond, Respond: &domain.RespondAction{Status: 200, Body: []byte("mocked")}},
	}, true, nil
}

type raceScriptEvaluator struct{}

func (raceScriptEvaluator) EvalRespond(string, usecase.MatchInput) ([]byte, error) { return nil, nil }

type raceTemplater struct{}

func (raceTemplater) Render(body []byte, _ usecase.MatchInput) []byte { return body }
func (raceTemplater) RenderHeaders(h map[string]string, _ usecase.MatchInput) map[string]string {
	return h
}

type raceScenarioAdvancer struct{}

func (raceScenarioAdvancer) AdvanceScenario(context.Context, string, string) (int, error) {
	return 0, nil
}
func (raceScenarioAdvancer) AdvanceEphemeralScenario(context.Context, string, string) (int, error) {
	return 0, nil
}

type raceClock struct{}

func (raceClock) Now() time.Time { return time.Now() }

// delayingTrafficRecorder is the store wrapper double the race proof needs:
// it holds every recorded entry newest-last (mirroring the real store's
// AppendTraffic/ListTraffic-newest-first pair, just without SQLite), and
// serializes every Execute call behind one lock — modelling "the store's
// single serialized SQLite writer" from handler.go's recordTraffic doc
// comment — while stalling the append for one chosen decision, to model
// that decision's write queuing behind others under load.
type delayingTrafficRecorder struct {
	writeMu sync.Mutex // the single serialized writer

	mu      sync.Mutex // guards records/nextID below
	records []domain.TrafficRecord
	nextID  int

	delayFor domain.Decision
	delay    time.Duration
}

func (d *delayingTrafficRecorder) Execute(_ context.Context, in usecase.RecordTrafficInput) (domain.TrafficRecord, error) {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()
	if in.Decision == d.delayFor {
		time.Sleep(d.delay)
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	d.nextID++
	rec := domain.TrafficRecord{ID: fmt.Sprintf("id-%d", d.nextID), Decision: in.Decision, Status: in.Status}
	d.records = append(d.records, rec)
	return rec, nil
}

// head returns the newest record, mirroring Store.ListTraffic's "newest
// first" list[0] — and whether any record exists yet at all.
func (d *delayingTrafficRecorder) head() (domain.TrafficRecord, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.records) == 0 {
		return domain.TrafficRecord{}, false
	}
	return d.records[len(d.records)-1], true
}

// newRaceHandler wires a proxy.Handler identically for both race tests
// below: one mock ("gold") that matches exactly once, and one real upstream
// (a tiny httptest server, so its response carries a genuine Content-Length)
// for every request after that to fall through to.
func newRaceHandler(rec *delayingTrafficRecorder) (h *proxy.Handler, upstreamSrv *httptest.Server) {
	upstreamSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("basic")) // small, fixed body -> Go sets a real Content-Length
	}))

	h = proxy.NewHandler(
		context.Background(),
		raceUpstreamLister{upstream: domain.Upstream{Partition: "default", MatchHost: "example.local", TargetURL: upstreamSrv.URL}},
		rec, &raceMockMatcher{}, raceTemplater{}, raceScriptEvaluator{}, raceScenarioAdvancer{},
		proxy.NewEngine(time.Second, nil, nil), 1<<20, raceClock{}, nil, nil, nil,
	)
	return h, upstreamSrv
}

// raceGet sends a GET and returns only the status code — every caller below
// asserts status, never the body, and returning just the int (rather than
// the *http.Response) lets this helper close the body itself in one place,
// matching what the production step this repro mirrors (steps_spy.go's
// sendRequest) does too.
func raceGet(t *testing.T, baseURL string) int {
	t.Helper()
	// A fresh http.Client per call, matching steps_spy.go's sendRequest, so
	// nothing in the test itself serializes request 2 on request 1's
	// connection or goroutine.
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, baseURL+"/x", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Host = "example.local"
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("send request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

// TestNaiveHeadReadRacesAgainstQueuedRecordTraffic is the deterministic
// proof of the race described in handler.go's recordTraffic doc comment: the
// response is written and flushed to the client BEFORE recordTraffic's
// store append runs, so a caller that reads the traffic log immediately
// after receiving its response can observe the PREVIOUS request's record
// instead of its own — exactly the CI symptom (recorded decision = "mocked",
// want "proxied") from mock_override.feature:58, where request 1 (gold ->
// mocked) precedes request 2 (basic -> proxied) and request 2's own append
// is still queued behind the shared writer when the naive read runs.
func TestNaiveHeadReadRacesAgainstQueuedRecordTraffic(t *testing.T) {
	rec := &delayingTrafficRecorder{delayFor: domain.DecisionProxied, delay: 300 * time.Millisecond}
	h, upstreamSrv := newRaceHandler(rec)
	defer upstreamSrv.Close()

	srv := httptest.NewServer(h)
	defer srv.Close()

	status1 := raceGet(t, srv.URL) // matches the mock: "mocked", appended immediately
	if status1 != http.StatusOK {
		t.Fatalf("request 1 status = %d, want 200", status1)
	}

	status2 := raceGet(t, srv.URL) // falls through to the real upstream: "proxied", append delayed 300ms
	if status2 != http.StatusOK {
		t.Fatalf("request 2 status = %d, want 200", status2)
	}

	// The naive read this test proves wrong: right after request 2's client
	// call returns, read the head of the traffic log with no wait at all —
	// exactly what lastTraffic did before the fix.
	got, ok := rec.head()
	if !ok {
		t.Fatalf("no traffic recorded at all yet")
	}
	if got.Decision != domain.DecisionMocked {
		t.Fatalf("race did not reproduce: naive head-read returned decision %q, want the stale %q — "+
			"either the repro's timing assumptions no longer hold, or the race is already fixed upstream",
			got.Decision, domain.DecisionMocked)
	}
	t.Logf("confirmed: naive head-read returned decision %q for what should have been request 2's own %q record",
		got.Decision, domain.DecisionProxied)
}

// TestPollingHeadReadWaitsForTheRightRecord proves the fix side of the same
// setup: bounded polling against a captured pre-request marker (mirroring
// the fixed lastTraffic in steps_spy.go) waits out the queued append and
// returns request 2's own record instead of request 1's stale one.
func TestPollingHeadReadWaitsForTheRightRecord(t *testing.T) {
	rec := &delayingTrafficRecorder{delayFor: domain.DecisionProxied, delay: 300 * time.Millisecond}
	h, upstreamSrv := newRaceHandler(rec)
	defer upstreamSrv.Close()

	srv := httptest.NewServer(h)
	defer srv.Close()

	status1 := raceGet(t, srv.URL)
	if status1 != http.StatusOK {
		t.Fatalf("request 1 status = %d, want 200", status1)
	}

	// Capture the pre-send marker for request 2, exactly as the fixed
	// send-step now does.
	marker, _ := rec.head()

	status2 := raceGet(t, srv.URL)
	if status2 != http.StatusOK {
		t.Fatalf("request 2 status = %d, want 200", status2)
	}

	got, err := pollHeadUntilChanged(rec, marker.ID)
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if got.Decision != domain.DecisionProxied {
		t.Fatalf("recorded decision = %q, want %q — the poll must wait for request 2's own record, not settle for request 1's stale one",
			got.Decision, domain.DecisionProxied)
	}
}

// pollHeadUntilChanged mirrors the polling shape added to lastTraffic in
// steps_spy.go: up to 30 attempts, 100ms apart (3s total), waiting for the
// head record's ID to differ from the pre-send marker.
func pollHeadUntilChanged(rec *delayingTrafficRecorder, marker string) (domain.TrafficRecord, error) {
	for i := 0; i < 30; i++ {
		if got, ok := rec.head(); ok && got.ID != marker {
			return got, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return domain.TrafficRecord{}, fmt.Errorf("traffic record for the last request never appeared after 3s")
}
