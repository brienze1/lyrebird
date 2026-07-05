package httpadmin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/brienze1/lyrebird/internal/domain"
	"github.com/brienze1/lyrebird/internal/usecase"
)

type fakeResetUseCase struct {
	res usecase.ResetOutput
	err error
	got usecase.ResetInput
}

func (f *fakeResetUseCase) Execute(_ context.Context, in usecase.ResetInput) (usecase.ResetOutput, error) {
	f.got = in
	return f.res, f.err
}

func newBodilessPostRequest(t *testing.T, url string) *http.Request {
	t.Helper()
	return httptest.NewRequestWithContext(context.Background(), http.MethodPost, url, nil)
}

// newChunkedEmptyBodyPostRequest simulates a real Transfer-Encoding: chunked
// request with an empty body: Go's server sets ContentLength to -1 (unknown)
// for chunked requests rather than 0, since chunked encoding carries no
// Content-Length header at all. http.NoBody's Read returns io.EOF
// immediately, matching what an actually-empty chunked body yields.
func newChunkedEmptyBodyPostRequest(t *testing.T, url string) *http.Request {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, url, http.NoBody)
	req.ContentLength = -1
	req.TransferEncoding = []string{"chunked"}
	return req
}

func TestResetWithNoBodyDefaultsClearTrafficFalse(t *testing.T) {
	uc := &fakeResetUseCase{res: usecase.ResetOutput{MocksRemoved: 3}}
	rr := httptest.NewRecorder()
	Reset(uc)(rr, newBodilessPostRequest(t, "/__lyrebird/reset"))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rr.Code, rr.Body)
	}
	if uc.got.ClearTraffic {
		t.Errorf("got.ClearTraffic = true, want false when no body is sent")
	}
	var out map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if v, _ := out["mocks_removed"].(float64); int(v) != 3 {
		t.Errorf("mocks_removed = %v, want 3", out["mocks_removed"])
	}
}

func TestResetWithChunkedEmptyBodyDefaultsClearTrafficFalse(t *testing.T) {
	uc := &fakeResetUseCase{res: usecase.ResetOutput{MocksRemoved: 3}}
	rr := httptest.NewRecorder()
	Reset(uc)(rr, newChunkedEmptyBodyPostRequest(t, "/__lyrebird/reset"))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for a chunked request with an empty body (ContentLength=-1 must still be treated as \"no body sent\", not as a decode failure) (body: %s)", rr.Code, rr.Body)
	}
	if uc.got.ClearTraffic {
		t.Errorf("got.ClearTraffic = true, want false when no body is sent")
	}
}

func TestResetWithClearTrafficTrueInBodyIsCaptured(t *testing.T) {
	uc := &fakeResetUseCase{res: usecase.ResetOutput{MocksRemoved: 1, TrafficCleared: true}}
	rr := httptest.NewRecorder()
	Reset(uc)(rr, newPostRequest(t, "/__lyrebird/reset", `{"clear_traffic":true}`))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rr.Code, rr.Body)
	}
	if !uc.got.ClearTraffic {
		t.Errorf("got.ClearTraffic = false, want true when body sets clear_traffic")
	}
	var out map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if v, _ := out["traffic_cleared"].(bool); !v {
		t.Errorf("traffic_cleared = %v, want true", out["traffic_cleared"])
	}
}

func TestResetRejectsMalformedJSONBody(t *testing.T) {
	uc := &fakeResetUseCase{}
	rr := httptest.NewRecorder()
	Reset(uc)(rr, newPostRequest(t, "/__lyrebird/reset", `not json`))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a malformed body", rr.Code)
	}
}

func TestResetRejectsUnknownFieldInJSONBody(t *testing.T) {
	uc := &fakeResetUseCase{}
	rr := httptest.NewRecorder()
	Reset(uc)(rr, newPostRequest(t, "/__lyrebird/reset", `{"clear_traffic":true,"totally_bogus_field":"oops"}`))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a body with an unknown field", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "unknown field") {
		t.Errorf("body = %q, want it to mention the unknown field", rr.Body.String())
	}
}

func TestResetMapsUseCaseErrorViaExplain(t *testing.T) {
	uc := &fakeResetUseCase{err: domain.ErrDuplicateID}
	rr := httptest.NewRecorder()
	Reset(uc)(rr, newBodilessPostRequest(t, "/__lyrebird/reset"))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 for an uncased use-case error (falls to Explain's default)", rr.Code)
	}
}
