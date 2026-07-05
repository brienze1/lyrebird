package httpadmin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/brienze1/lyrebird/internal/domain"
	"github.com/brienze1/lyrebird/internal/usecase"
)

type fakeListTrafficUseCase struct {
	list []domain.TrafficRecord
	err  error
	// gotFilter captures the last filter Execute was called with, so tests
	// can assert query-param parsing actually reached the use case.
	gotFilter usecase.TrafficFilter
}

func (f *fakeListTrafficUseCase) Execute(_ context.Context, _ string, filter usecase.TrafficFilter) ([]domain.TrafficRecord, error) {
	f.gotFilter = filter
	return f.list, f.err
}

type fakeGetTrafficUseCase struct {
	record domain.TrafficRecord
	err    error
}

func (f *fakeGetTrafficUseCase) Execute(_ context.Context, _, _ string) (domain.TrafficRecord, error) {
	return f.record, f.err
}

func newGetRequest(t *testing.T, url string) *http.Request {
	t.Helper()
	return httptest.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
}

func TestListTrafficReturnsDecodedSummaries(t *testing.T) {
	uc := &fakeListTrafficUseCase{list: []domain.TrafficRecord{
		{ID: "t1", Method: "GET", Host: "example.local", Path: "/x", Status: 200, Timestamp: time.Unix(0, 0)},
	}}
	rr := httptest.NewRecorder()
	ListTraffic(uc)(rr, newGetRequest(t, "/__lyrebird/traffic"))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rr.Code, rr.Body)
	}
	var out []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(out) != 1 || out[0]["id"] != "t1" {
		t.Errorf("response = %+v, want one summary with id=t1", out)
	}
}

func TestListTrafficParsesQueryFilters(t *testing.T) {
	uc := &fakeListTrafficUseCase{}
	rr := httptest.NewRecorder()
	ListTraffic(uc)(rr, newGetRequest(t, "/__lyrebird/traffic?method=POST&host=api.local&path_prefix=/v1&status=404&since=2024-01-01T00:00:00Z&until=2024-06-01T00:00:00Z&limit=10"))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rr.Code, rr.Body)
	}
	f := uc.gotFilter
	if f.Method != "POST" || f.Host != "api.local" || f.PathPrefix != "/v1" || f.Limit != 10 {
		t.Fatalf("parsed filter = %+v, want method=POST host=api.local path_prefix=/v1 limit=10", f)
	}
	if f.Status == nil || *f.Status != 404 {
		t.Errorf("Status = %v, want 404", f.Status)
	}
	if f.Since == nil || f.Until == nil {
		t.Fatalf("Since/Until = %v/%v, want both parsed", f.Since, f.Until)
	}
}

func TestListTrafficRejectsMalformedQueryParams(t *testing.T) {
	cases := map[string]string{
		"status": "/__lyrebird/traffic?status=not-a-number",
		"since":  "/__lyrebird/traffic?since=not-a-date",
		"until":  "/__lyrebird/traffic?until=not-a-date",
		"limit":  "/__lyrebird/traffic?limit=not-a-number",
	}
	for name, url := range cases {
		t.Run(name, func(t *testing.T) {
			uc := &fakeListTrafficUseCase{}
			rr := httptest.NewRecorder()
			ListTraffic(uc)(rr, newGetRequest(t, url))
			if rr.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400 (body: %s)", rr.Code, rr.Body)
			}
		})
	}
}

func TestListTrafficMapsUseCaseErrorViaExplain(t *testing.T) {
	uc := &fakeListTrafficUseCase{err: domain.ErrNotFound}
	rr := httptest.NewRecorder()
	ListTraffic(uc)(rr, newGetRequest(t, "/__lyrebird/traffic"))
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for a not-found use-case error", rr.Code)
	}
}

func TestGetTrafficReturnsDecodedDetail(t *testing.T) {
	reqMsg, err := usecase.EncodeRecordedMessage(usecase.RecordedMessage{Body: []byte("req-body")})
	if err != nil {
		t.Fatalf("EncodeRecordedMessage(req): %v", err)
	}
	respMsg, err := usecase.EncodeRecordedMessage(usecase.RecordedMessage{Body: []byte("resp-body")})
	if err != nil {
		t.Fatalf("EncodeRecordedMessage(resp): %v", err)
	}
	uc := &fakeGetTrafficUseCase{record: domain.TrafficRecord{
		ID: "t1", Method: "GET", Host: "example.local", Path: "/x", Status: 200,
		Request: reqMsg, Response: respMsg,
	}}
	rr := httptest.NewRecorder()
	GetTraffic(uc)(rr, newGetRequest(t, "/__lyrebird/traffic/t1"))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rr.Code, rr.Body)
	}
	var out map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if out["id"] != "t1" {
		t.Errorf("id = %v, want t1", out["id"])
	}
	reqOut, _ := out["request"].(map[string]any)
	if reqOut == nil || reqOut["body"] == nil {
		t.Errorf("response = %+v, want a decoded request body", out)
	}
}

func TestGetTrafficMapsUseCaseErrorViaExplain(t *testing.T) {
	uc := &fakeGetTrafficUseCase{err: domain.ErrNotFound}
	rr := httptest.NewRecorder()
	GetTraffic(uc)(rr, newGetRequest(t, "/__lyrebird/traffic/missing"))
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for a not-found use-case error", rr.Code)
	}
}

func TestGetTrafficRejectsUndecodableStoredRequest(t *testing.T) {
	uc := &fakeGetTrafficUseCase{record: domain.TrafficRecord{ID: "t1", Request: []byte("not json")}}
	rr := httptest.NewRecorder()
	GetTraffic(uc)(rr, newGetRequest(t, "/__lyrebird/traffic/t1"))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 when the stored request can't be decoded", rr.Code)
	}
}

func TestGetTrafficRejectsUndecodableStoredResponse(t *testing.T) {
	reqMsg, err := usecase.EncodeRecordedMessage(usecase.RecordedMessage{})
	if err != nil {
		t.Fatalf("EncodeRecordedMessage(req): %v", err)
	}
	uc := &fakeGetTrafficUseCase{record: domain.TrafficRecord{ID: "t1", Request: reqMsg, Response: []byte("not json")}}
	rr := httptest.NewRecorder()
	GetTraffic(uc)(rr, newGetRequest(t, "/__lyrebird/traffic/t1"))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 when the stored response can't be decoded", rr.Code)
	}
}

// noopTrafficRepo satisfies usecase.TrafficRepo with no-op behavior on every
// method other than ListTraffic — enough to wire a *real*
// usecase.NewListTraffic use case (rather than fakeListTrafficUseCase's
// canned Execute) into the handler, so
// TestListTrafficRejectsNegativeLimitEndToEnd proves the negative-limit
// rejection added to ListTraffic.Execute is actually reachable through the
// HTTP handler, not just exercised at the usecase layer in isolation.
type noopTrafficRepo struct{}

func (noopTrafficRepo) AppendTraffic(context.Context, domain.TrafficRecord) error { return nil }
func (noopTrafficRepo) GetTraffic(context.Context, string, string) (domain.TrafficRecord, error) {
	return domain.TrafficRecord{}, domain.ErrNotFound
}
func (noopTrafficRepo) ListTraffic(context.Context, string, usecase.TrafficFilter) ([]domain.TrafficRecord, error) {
	return nil, nil
}
func (noopTrafficRepo) PruneTraffic(context.Context, time.Time) (int, error) { return 0, nil }
func (noopTrafficRepo) ClearTraffic(context.Context, string) error           { return nil }

func TestListTrafficRejectsNegativeLimitEndToEnd(t *testing.T) {
	uc := usecase.NewListTraffic(noopTrafficRepo{})
	rr := httptest.NewRecorder()
	ListTraffic(uc)(rr, newGetRequest(t, "/__lyrebird/traffic?limit=-1"))

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a negative limit (body: %s)", rr.Code, rr.Body)
	}
	if !strings.Contains(rr.Body.String(), "invalid traffic filter") {
		t.Errorf("body = %s, want it to mention %q", rr.Body, "invalid traffic filter")
	}
}
