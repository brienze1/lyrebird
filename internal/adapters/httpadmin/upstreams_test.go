package httpadmin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/brienze1/lyrebird/internal/domain"
)

type fakeListUpstreamsUseCase struct {
	list []domain.Upstream
	err  error
}

func (f *fakeListUpstreamsUseCase) Execute(_ context.Context, _ string) ([]domain.Upstream, error) {
	return f.list, f.err
}

type fakeSetUpstreamUseCase struct {
	got domain.Upstream
	err error
}

func (f *fakeSetUpstreamUseCase) Execute(_ context.Context, u domain.Upstream) error {
	f.got = u
	return f.err
}

func TestListUpstreamsReturnsDecodedList(t *testing.T) {
	uc := &fakeListUpstreamsUseCase{list: []domain.Upstream{{MatchHost: "api.example.com", TargetURL: "https://api.example.com"}}}
	rr := httptest.NewRecorder()
	ListUpstreams(uc)(rr, newGetRequest(t, "/__lyrebird/upstreams"))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rr.Code, rr.Body)
	}
	var out []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(out) != 1 || out[0]["match_host"] != "api.example.com" {
		t.Errorf("response = %+v, want one upstream for api.example.com", out)
	}
}

func TestListUpstreamsMapsUseCaseErrorViaExplain(t *testing.T) {
	uc := &fakeListUpstreamsUseCase{err: domain.ErrNotFound}
	rr := httptest.NewRecorder()
	ListUpstreams(uc)(rr, newGetRequest(t, "/__lyrebird/upstreams"))
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for a not-found use-case error", rr.Code)
	}
}

func newPostRequest(t *testing.T, url, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestSetUpstreamPersistsAndEchoesTheDecodedDTO(t *testing.T) {
	uc := &fakeSetUpstreamUseCase{}
	rr := httptest.NewRecorder()
	SetUpstream(uc)(rr, newPostRequest(t, "/__lyrebird/upstreams", `{"match_host":"api.example.com","target_url":"https://api.example.com"}`))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rr.Code, rr.Body)
	}
	if uc.got.MatchHost != "api.example.com" || uc.got.TargetURL != "https://api.example.com" {
		t.Errorf("use case received %+v, want the decoded upstream", uc.got)
	}
	var out map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if out["match_host"] != "api.example.com" {
		t.Errorf("response = %+v, want the echoed match_host", out)
	}
}

func TestSetUpstreamRejectsMalformedJSONBody(t *testing.T) {
	uc := &fakeSetUpstreamUseCase{}
	rr := httptest.NewRecorder()
	SetUpstream(uc)(rr, newPostRequest(t, "/__lyrebird/upstreams", `not json`))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a malformed body", rr.Code)
	}
}

func TestSetUpstreamRejectsUnknownFieldInJSONBody(t *testing.T) {
	uc := &fakeSetUpstreamUseCase{}
	rr := httptest.NewRecorder()
	body := `{"match_host":"api.example.com","target_url":"https://api.example.com","totally_bogus_field":"oops"}`
	SetUpstream(uc)(rr, newPostRequest(t, "/__lyrebird/upstreams", body))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a body with an unknown field", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "unknown field") {
		t.Errorf("body = %q, want it to mention the unknown field", rr.Body.String())
	}
}

func TestSetUpstreamMapsUseCaseErrorViaExplain(t *testing.T) {
	uc := &fakeSetUpstreamUseCase{err: domain.ErrInvalidUpstream}
	rr := httptest.NewRecorder()
	SetUpstream(uc)(rr, newPostRequest(t, "/__lyrebird/upstreams", `{"match_host":"","target_url":""}`))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for an invalid-upstream use-case error", rr.Code)
	}
}
