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
	"github.com/brienze1/lyrebird/internal/usecase"
)

// newPutRequest and newDeleteRequest mirror newPostRequest (upstreams_test.go)
// for the two HTTP methods no existing helper covers: PUT (UpdateMock) and
// DELETE (DeleteMock/DeleteSpace).
func newPutRequest(t *testing.T, url, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPut, url, bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func newDeleteRequest(t *testing.T, url string) *http.Request {
	t.Helper()
	return httptest.NewRequestWithContext(context.Background(), http.MethodDelete, url, nil)
}

type fakeMockLister struct {
	list         []domain.Mock
	err          error
	gotGroup     string
	gotPartition string
}

func (f *fakeMockLister) List(_ context.Context, partition, group string) ([]domain.Mock, error) {
	f.gotPartition = partition
	f.gotGroup = group
	return f.list, f.err
}

type fakeMockCreator struct {
	mock domain.Mock
	err  error
	got  usecase.MockInput
}

func (f *fakeMockCreator) Create(_ context.Context, in usecase.MockInput) (domain.Mock, error) {
	f.got = in
	return f.mock, f.err
}

type fakeMockGetter struct {
	mock    domain.Mock
	err     error
	gotID   string
	gotPart string
}

func (f *fakeMockGetter) Get(_ context.Context, partition, id string) (domain.Mock, error) {
	f.gotPart = partition
	f.gotID = id
	return f.mock, f.err
}

type fakeMockUpdater struct {
	mock    domain.Mock
	err     error
	gotID   string
	gotPart string
	got     usecase.MockInput
}

func (f *fakeMockUpdater) Update(_ context.Context, partition, id string, in usecase.MockInput) (domain.Mock, error) {
	f.gotPart = partition
	f.gotID = id
	f.got = in
	return f.mock, f.err
}

type fakeMockDeleter struct {
	err     error
	gotID   string
	gotPart string
}

func (f *fakeMockDeleter) Delete(_ context.Context, partition, id string) error {
	f.gotPart = partition
	f.gotID = id
	return f.err
}

func TestListMocksReturnsDecodedList(t *testing.T) {
	uc := &fakeMockLister{list: []domain.Mock{{ID: "m1", Name: "hello"}}}
	rr := httptest.NewRecorder()
	ListMocks(uc)(rr, newGetRequest(t, "/__lyrebird/mocks"))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rr.Code, rr.Body)
	}
	var out []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(out) != 1 || out[0]["id"] != "m1" {
		t.Errorf("response = %+v, want one mock with id=m1", out)
	}
}

func TestListMocksPassesGroupQueryParamToUseCase(t *testing.T) {
	uc := &fakeMockLister{}
	rr := httptest.NewRecorder()
	ListMocks(uc)(rr, newGetRequest(t, "/__lyrebird/mocks?group=canary"))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rr.Code, rr.Body)
	}
	if uc.gotGroup != "canary" {
		t.Errorf("gotGroup = %q, want canary", uc.gotGroup)
	}
}

func TestListMocksMapsUseCaseErrorViaExplain(t *testing.T) {
	uc := &fakeMockLister{err: domain.ErrNotFound}
	rr := httptest.NewRecorder()
	ListMocks(uc)(rr, newGetRequest(t, "/__lyrebird/mocks"))
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for a not-found use-case error", rr.Code)
	}
}

func TestCreateMockPersistsAndReturnsTheCreatedMock(t *testing.T) {
	uc := &fakeMockCreator{mock: domain.Mock{ID: "m1", Name: "hello"}}
	rr := httptest.NewRecorder()
	body := `{"name":"hello","match":{"method":"GET","path":"/x"},"action":{"respond":{"status":200,"body":"ok"}}}`
	CreateMock(uc)(rr, newPostRequest(t, "/__lyrebird/mocks", body))

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body: %s)", rr.Code, rr.Body)
	}
	if uc.got.Name != "hello" || uc.got.Match.Method != "GET" || uc.got.Match.Path != "/x" {
		t.Errorf("use case received %+v, want the decoded mock input", uc.got)
	}
	if uc.got.Action.Respond == nil || uc.got.Action.Respond.Status != 200 {
		t.Errorf("use case received action %+v, want respond status=200", uc.got.Action)
	}
	var out map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if out["id"] != "m1" {
		t.Errorf("response = %+v, want id=m1", out)
	}
}

func TestCreateMockRejectsMalformedJSONBody(t *testing.T) {
	uc := &fakeMockCreator{}
	rr := httptest.NewRecorder()
	CreateMock(uc)(rr, newPostRequest(t, "/__lyrebird/mocks", `not json`))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a malformed body", rr.Code)
	}
}

func TestCreateMockRejectsUnknownFieldInJSONBody(t *testing.T) {
	uc := &fakeMockCreator{}
	rr := httptest.NewRecorder()
	body := `{"name":"hello","match":{"method":"GET","path":"/x"},"action":{"respond":{"status":200,"body":"ok"}},"totally_bogus_field":"oops"}`
	CreateMock(uc)(rr, newPostRequest(t, "/__lyrebird/mocks", body))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a body with an unknown field", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "unknown field") {
		t.Errorf("body = %q, want it to mention the unknown field", rr.Body.String())
	}
}

func TestCreateMockRejectsInvalidLifetimeInDTO(t *testing.T) {
	uc := &fakeMockCreator{}
	rr := httptest.NewRecorder()
	body := `{"name":"hello","lifetime":"seeded","match":{"method":"GET","path":"/x"},"action":{"respond":{"status":200,"body":"ok"}}}`
	CreateMock(uc)(rr, newPostRequest(t, "/__lyrebird/mocks", body))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a caller-supplied lifetime=seeded", rr.Code)
	}
}

func TestCreateMockRejectsInvalidActionInDTO(t *testing.T) {
	uc := &fakeMockCreator{}
	rr := httptest.NewRecorder()
	body := `{"name":"hello","match":{"method":"GET","path":"/x"}}`
	CreateMock(uc)(rr, newPostRequest(t, "/__lyrebird/mocks", body))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 when no action variant is set", rr.Code)
	}
}

func TestCreateMockMapsUseCaseErrorViaExplain(t *testing.T) {
	uc := &fakeMockCreator{err: domain.ErrInvalidMock}
	rr := httptest.NewRecorder()
	body := `{"name":"hello","match":{"method":"GET","path":"/x"},"action":{"respond":{"status":200,"body":"ok"}}}`
	CreateMock(uc)(rr, newPostRequest(t, "/__lyrebird/mocks", body))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for an invalid-mock use-case error", rr.Code)
	}
}

func TestGetMockReturnsDecodedMockAndPathID(t *testing.T) {
	uc := &fakeMockGetter{mock: domain.Mock{ID: "m1", Name: "hello"}}
	rr := httptest.NewRecorder()
	req := newGetRequest(t, "/__lyrebird/mocks/m1")
	req.SetPathValue("id", "m1")
	GetMock(uc)(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rr.Code, rr.Body)
	}
	if uc.gotID != "m1" {
		t.Errorf("gotID = %q, want m1 (path value not plumbed through)", uc.gotID)
	}
	var out map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if out["id"] != "m1" {
		t.Errorf("response = %+v, want id=m1", out)
	}
}

func TestGetMockMapsUseCaseErrorViaExplain(t *testing.T) {
	uc := &fakeMockGetter{err: domain.ErrNotFound}
	rr := httptest.NewRecorder()
	req := newGetRequest(t, "/__lyrebird/mocks/missing")
	req.SetPathValue("id", "missing")
	GetMock(uc)(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for a not-found use-case error", rr.Code)
	}
}

func TestUpdateMockPersistsAndReturnsTheUpdatedMockWithPathID(t *testing.T) {
	uc := &fakeMockUpdater{mock: domain.Mock{ID: "m1", Name: "updated"}}
	rr := httptest.NewRecorder()
	body := `{"name":"updated","match":{"method":"GET","path":"/x"},"action":{"respond":{"status":200,"body":"ok"}}}`
	req := newPutRequest(t, "/__lyrebird/mocks/m1", body)
	req.SetPathValue("id", "m1")
	UpdateMock(uc)(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rr.Code, rr.Body)
	}
	if uc.gotID != "m1" {
		t.Errorf("gotID = %q, want m1", uc.gotID)
	}
	if uc.got.Name != "updated" {
		t.Errorf("use case received %+v, want name=updated", uc.got)
	}
	var out map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if out["name"] != "updated" {
		t.Errorf("response = %+v, want name=updated", out)
	}
}

func TestUpdateMockRejectsMalformedJSONBody(t *testing.T) {
	uc := &fakeMockUpdater{}
	rr := httptest.NewRecorder()
	req := newPutRequest(t, "/__lyrebird/mocks/m1", `not json`)
	req.SetPathValue("id", "m1")
	UpdateMock(uc)(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a malformed body", rr.Code)
	}
}

func TestUpdateMockRejectsUnknownFieldInJSONBody(t *testing.T) {
	uc := &fakeMockUpdater{}
	rr := httptest.NewRecorder()
	body := `{"name":"updated","match":{"method":"GET","path":"/x"},"action":{"respond":{"status":200,"body":"ok"}},"totally_bogus_field":"oops"}`
	req := newPutRequest(t, "/__lyrebird/mocks/m1", body)
	req.SetPathValue("id", "m1")
	UpdateMock(uc)(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a body with an unknown field", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "unknown field") {
		t.Errorf("body = %q, want it to mention the unknown field", rr.Body.String())
	}
}

func TestUpdateMockRejectsInvalidDTO(t *testing.T) {
	uc := &fakeMockUpdater{}
	rr := httptest.NewRecorder()
	body := `{"name":"updated","lifetime":"seeded","match":{"method":"GET","path":"/x"},"action":{"respond":{"status":200,"body":"ok"}}}`
	req := newPutRequest(t, "/__lyrebird/mocks/m1", body)
	req.SetPathValue("id", "m1")
	UpdateMock(uc)(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a caller-supplied lifetime=seeded", rr.Code)
	}
}

func TestUpdateMockMapsSeededMockImmutableErrorViaExplain(t *testing.T) {
	uc := &fakeMockUpdater{err: domain.ErrSeededMockImmutable}
	rr := httptest.NewRecorder()
	body := `{"name":"updated","match":{"method":"GET","path":"/x"},"action":{"respond":{"status":200,"body":"ok"}}}`
	req := newPutRequest(t, "/__lyrebird/mocks/seeded-1", body)
	req.SetPathValue("id", "seeded-1")
	UpdateMock(uc)(rr, req)
	if rr.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409 for a seeded-mock-immutable use-case error", rr.Code)
	}
}

func TestDeleteMockRemovesAndReturnsNoContentWithPathIDAndPartition(t *testing.T) {
	uc := &fakeMockDeleter{}
	rr := httptest.NewRecorder()
	req := newDeleteRequest(t, "/__lyrebird/mocks/m1")
	req.SetPathValue("id", "m1")
	DeleteMock(uc)(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (body: %s)", rr.Code, rr.Body)
	}
	if uc.gotID != "m1" {
		t.Errorf("gotID = %q, want m1", uc.gotID)
	}
	if uc.gotPart != domain.DefaultPartitionID {
		t.Errorf("gotPart = %q, want default partition %q", uc.gotPart, domain.DefaultPartitionID)
	}
}

func TestDeleteMockMapsSeededMockImmutableErrorViaExplain(t *testing.T) {
	uc := &fakeMockDeleter{err: domain.ErrSeededMockImmutable}
	rr := httptest.NewRecorder()
	req := newDeleteRequest(t, "/__lyrebird/mocks/m1")
	req.SetPathValue("id", "m1")
	DeleteMock(uc)(rr, req)
	if rr.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409 for a seeded-mock-immutable use-case error", rr.Code)
	}
}

func TestDeleteMockMapsNotFoundErrorViaExplain(t *testing.T) {
	uc := &fakeMockDeleter{err: domain.ErrNotFound}
	rr := httptest.NewRecorder()
	req := newDeleteRequest(t, "/__lyrebird/mocks/missing")
	req.SetPathValue("id", "missing")
	DeleteMock(uc)(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for a not-found use-case error", rr.Code)
	}
}
