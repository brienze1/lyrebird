package httpadmin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/brienze1/lyrebird/internal/domain"
)

type fakeListSpacesUseCase struct {
	list []domain.Partition
	err  error
}

func (f *fakeListSpacesUseCase) Execute(_ context.Context) ([]domain.Partition, error) {
	return f.list, f.err
}

type fakeCreateSpaceUseCase struct {
	partition domain.Partition
	err       error
	got       domain.Partition
}

func (f *fakeCreateSpaceUseCase) Execute(_ context.Context, p domain.Partition) (domain.Partition, error) {
	f.got = p
	return f.partition, f.err
}

type fakeDeleteSpaceUseCase struct {
	err   error
	gotID string
}

func (f *fakeDeleteSpaceUseCase) Execute(_ context.Context, id string) error {
	f.gotID = id
	return f.err
}

func TestListSpacesReturnsDecodedList(t *testing.T) {
	uc := &fakeListSpacesUseCase{list: []domain.Partition{{ID: "team-a", Description: "Team A"}}}
	rr := httptest.NewRecorder()
	ListSpaces(uc)(rr, newGetRequest(t, "/__lyrebird/spaces"))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rr.Code, rr.Body)
	}
	var out []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(out) != 1 || out[0]["id"] != "team-a" {
		t.Errorf("response = %+v, want one space with id=team-a", out)
	}
}

func TestListSpacesMapsUseCaseErrorViaExplain(t *testing.T) {
	uc := &fakeListSpacesUseCase{err: domain.ErrDuplicateID}
	rr := httptest.NewRecorder()
	ListSpaces(uc)(rr, newGetRequest(t, "/__lyrebird/spaces"))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 for an uncased use-case error", rr.Code)
	}
}

func TestCreateSpacePersistsAndReturnsTheCreatedSpaceWith200(t *testing.T) {
	uc := &fakeCreateSpaceUseCase{partition: domain.Partition{ID: "team-a", Description: "Team A"}}
	rr := httptest.NewRecorder()
	CreateSpace(uc)(rr, newPostRequest(t, "/__lyrebird/spaces", `{"id":"team-a","description":"Team A"}`))

	// Unlike CreateMock (201 Created), CreateSpace responds 200 OK — verify
	// this explicitly rather than assuming REST-conventional 201.
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (CreateSpace does not return 201, unlike CreateMock) (body: %s)", rr.Code, rr.Body)
	}
	if uc.got.ID != "team-a" || uc.got.Description != "Team A" {
		t.Errorf("use case received %+v, want the decoded partition", uc.got)
	}
	var out map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if out["id"] != "team-a" {
		t.Errorf("response = %+v, want id=team-a", out)
	}
}

func TestCreateSpaceRejectsMalformedJSONBody(t *testing.T) {
	uc := &fakeCreateSpaceUseCase{}
	rr := httptest.NewRecorder()
	CreateSpace(uc)(rr, newPostRequest(t, "/__lyrebird/spaces", `not json`))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a malformed body", rr.Code)
	}
}

func TestCreateSpaceRejectsUnknownFieldInJSONBody(t *testing.T) {
	uc := &fakeCreateSpaceUseCase{}
	rr := httptest.NewRecorder()
	CreateSpace(uc)(rr, newPostRequest(t, "/__lyrebird/spaces", `{"id":"team-a","description":"Team A","totally_bogus_field":"oops"}`))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a body with an unknown field", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "unknown field") {
		t.Errorf("body = %q, want it to mention the unknown field", rr.Body.String())
	}
}

func TestCreateSpaceMapsInvalidPartitionErrorViaExplain(t *testing.T) {
	uc := &fakeCreateSpaceUseCase{err: domain.ErrInvalidPartition}
	rr := httptest.NewRecorder()
	CreateSpace(uc)(rr, newPostRequest(t, "/__lyrebird/spaces", `{"description":"missing id"}`))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for an invalid-partition use-case error (e.g. missing id)", rr.Code)
	}
}

func TestCreateSpaceMapsDuplicateIDErrorToInternalServerError(t *testing.T) {
	// domain.ErrDuplicateID has no explicit case in usecase.Explain, so it
	// falls through to the default KindInternal mapping -> 500, unlike the
	// other validation errors handled above.
	uc := &fakeCreateSpaceUseCase{err: domain.ErrDuplicateID}
	rr := httptest.NewRecorder()
	CreateSpace(uc)(rr, newPostRequest(t, "/__lyrebird/spaces", `{"id":"team-a"}`))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 for domain.ErrDuplicateID (Explain has no case for it)", rr.Code)
	}
}

func TestDeleteSpaceRemovesAndReturnsNoContent(t *testing.T) {
	uc := &fakeDeleteSpaceUseCase{}
	rr := httptest.NewRecorder()
	req := newDeleteRequest(t, "/__lyrebird/spaces/team-a")
	req.SetPathValue("id", "team-a")
	DeleteSpace(uc)(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (body: %s)", rr.Code, rr.Body)
	}
	if uc.gotID != "team-a" {
		t.Errorf("gotID = %q, want team-a", uc.gotID)
	}
}

func TestDeleteSpaceMapsDefaultPartitionProtectedErrorTo400(t *testing.T) {
	// Despite the "protected" name, Explain maps ErrDefaultPartitionProtected
	// to KindValidation (400), not KindConflict (409) - verify this
	// explicitly since it's easy to assume otherwise.
	uc := &fakeDeleteSpaceUseCase{err: domain.ErrDefaultPartitionProtected}
	rr := httptest.NewRecorder()
	req := newDeleteRequest(t, "/__lyrebird/spaces/default")
	req.SetPathValue("id", "default")
	DeleteSpace(uc)(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for default-partition-protected (mapped to Validation, not Conflict)", rr.Code)
	}
}

func TestDeleteSpaceMapsNotFoundErrorViaExplain(t *testing.T) {
	uc := &fakeDeleteSpaceUseCase{err: domain.ErrNotFound}
	rr := httptest.NewRecorder()
	req := newDeleteRequest(t, "/__lyrebird/spaces/missing")
	req.SetPathValue("id", "missing")
	DeleteSpace(uc)(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for a not-found use-case error", rr.Code)
	}
}
