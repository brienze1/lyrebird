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

type fakeExportSeedsUseCase struct {
	bundle usecase.ExportBundle
	err    error
}

func (f *fakeExportSeedsUseCase) Execute(_ context.Context, _ string) (usecase.ExportBundle, error) {
	return f.bundle, f.err
}

type fakeImportSeedsUseCase struct {
	gotUpstreams []domain.Upstream
	gotMocks     []usecase.MockInput
	result       usecase.ImportResult
	err          error
}

func (f *fakeImportSeedsUseCase) Execute(_ context.Context, _ string, upstreams []domain.Upstream, mocks []usecase.MockInput) (usecase.ImportResult, error) {
	f.gotUpstreams, f.gotMocks = upstreams, mocks
	return f.result, f.err
}

func TestExportConfigReturnsYAML(t *testing.T) {
	uc := &fakeExportSeedsUseCase{bundle: usecase.ExportBundle{
		Space:     "default",
		Upstreams: []domain.Upstream{{MatchHost: "example.local", TargetURL: "https://example.local"}},
		Mocks:     []domain.Mock{{Name: "ping", Action: domain.Action{Kind: domain.ActionRespond, Respond: &domain.RespondAction{Status: 200, Body: []byte("pong")}}}},
	}}
	rr := httptest.NewRecorder()
	ExportConfig(uc)(rr, newGetRequest(t, "/__lyrebird/export"))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rr.Code, rr.Body)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/x-yaml" {
		t.Errorf("Content-Type = %q, want application/x-yaml", ct)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "match_host: example.local") {
		t.Errorf("body = %q, want it to contain the upstream's match_host", body)
	}
	if !strings.Contains(body, "name: ping") {
		t.Errorf("body = %q, want it to contain the mock's name", body)
	}
}

func TestExportConfigMapsUseCaseErrorViaExplain(t *testing.T) {
	uc := &fakeExportSeedsUseCase{err: domain.ErrNotFound}
	rr := httptest.NewRecorder()
	ExportConfig(uc)(rr, newGetRequest(t, "/__lyrebird/export"))
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

func TestImportConfigParsesYAMLAndDelegates(t *testing.T) {
	uc := &fakeImportSeedsUseCase{result: usecase.ImportResult{UpstreamsImported: 1, MocksImported: 1}}
	body := "upstreams:\n  - match_host: example.local\n    target_url: https://example.local\nmocks:\n  - name: ping\n    match: {method: GET, path: /ping}\n    action: {respond: {status: 200, body: pong}}\n"
	rr := httptest.NewRecorder()
	ImportConfig(uc)(rr, newPostRequest(t, "/__lyrebird/import", body))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rr.Code, rr.Body)
	}
	if len(uc.gotUpstreams) != 1 || uc.gotUpstreams[0].MatchHost != "example.local" {
		t.Errorf("use case received upstreams %+v, want one for example.local", uc.gotUpstreams)
	}
	if len(uc.gotMocks) != 1 || uc.gotMocks[0].Name != "ping" {
		t.Errorf("use case received mocks %+v, want one named ping", uc.gotMocks)
	}
	var out usecase.ImportResult
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal response: %v (body: %s)", err, rr.Body)
	}
	if out.UpstreamsImported != 1 || out.MocksImported != 1 {
		t.Errorf("response = %+v, want UpstreamsImported=1 and MocksImported=1", out)
	}
}

func TestImportConfigRejectsMalformedYAML(t *testing.T) {
	uc := &fakeImportSeedsUseCase{}
	rr := httptest.NewRecorder()
	ImportConfig(uc)(rr, newPostRequest(t, "/__lyrebird/import", "not: [valid, yaml :::"))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for malformed YAML", rr.Code)
	}
}

func TestImportConfigMapsUseCaseErrorViaExplain(t *testing.T) {
	uc := &fakeImportSeedsUseCase{err: domain.ErrNotFound}
	body := "upstreams:\n  - match_host: example.local\n    target_url: https://example.local\n"
	rr := httptest.NewRecorder()
	ImportConfig(uc)(rr, newPostRequest(t, "/__lyrebird/import", body))
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

func TestImportConfigRejectsANonEphemeralLifetime(t *testing.T) {
	uc := &fakeImportSeedsUseCase{}
	body := "mocks:\n  - name: bad\n    lifetime: seeded\n    match: {method: GET, path: /x}\n    action: {respond: {status: 200, body: x}}\n"
	rr := httptest.NewRecorder()
	ImportConfig(uc)(rr, newPostRequest(t, "/__lyrebird/import", body))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a caller-supplied seeded lifetime", rr.Code)
	}
}
