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

type fakePromoteTrafficUseCase struct {
	mock domain.Mock
	err  error
	got  usecase.PromoteTrafficInput
}

func (f *fakePromoteTrafficUseCase) Execute(_ context.Context, in usecase.PromoteTrafficInput) (domain.Mock, error) {
	f.got = in
	return f.mock, f.err
}

func TestPromoteTrafficWithNoBodyUsesDefaultsAndPathID(t *testing.T) {
	uc := &fakePromoteTrafficUseCase{mock: domain.Mock{ID: "m1", Name: "promoted-t1"}}
	rr := httptest.NewRecorder()
	req := newBodilessPostRequest(t, "/__lyrebird/traffic/t1/promote")
	req.SetPathValue("id", "t1")
	PromoteTraffic(uc)(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body: %s)", rr.Code, rr.Body)
	}
	if uc.got.Name != "" {
		t.Errorf("got.Name = %q, want empty (no body sent)", uc.got.Name)
	}
	if uc.got.TTLSeconds != nil {
		t.Errorf("got.TTLSeconds = %v, want nil (no body sent)", uc.got.TTLSeconds)
	}
	if uc.got.TrafficID != "t1" {
		t.Errorf("got.TrafficID = %q, want t1 (path value not plumbed through)", uc.got.TrafficID)
	}
	var out map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if out["id"] != "m1" {
		t.Errorf("response = %+v, want id=m1", out)
	}
}

func TestPromoteTrafficWithChunkedEmptyBodyUsesDefaultsAndPathID(t *testing.T) {
	uc := &fakePromoteTrafficUseCase{mock: domain.Mock{ID: "m1", Name: "promoted-t1"}}
	rr := httptest.NewRecorder()
	req := newChunkedEmptyBodyPostRequest(t, "/__lyrebird/traffic/t1/promote")
	req.SetPathValue("id", "t1")
	PromoteTraffic(uc)(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 for a chunked request with an empty body (ContentLength=-1 must still be treated as \"no body sent\", not as a decode failure) (body: %s)", rr.Code, rr.Body)
	}
	if uc.got.Name != "" {
		t.Errorf("got.Name = %q, want empty (no body sent)", uc.got.Name)
	}
	if uc.got.TTLSeconds != nil {
		t.Errorf("got.TTLSeconds = %v, want nil (no body sent)", uc.got.TTLSeconds)
	}
	if uc.got.TrafficID != "t1" {
		t.Errorf("got.TrafficID = %q, want t1 (path value not plumbed through)", uc.got.TrafficID)
	}
}

func TestPromoteTrafficWithBodyCapturesNameAndTTL(t *testing.T) {
	uc := &fakePromoteTrafficUseCase{mock: domain.Mock{ID: "m2", Name: "x"}}
	rr := httptest.NewRecorder()
	req := newPostRequest(t, "/__lyrebird/traffic/t1/promote", `{"name":"x","ttl_seconds":30}`)
	req.SetPathValue("id", "t1")
	PromoteTraffic(uc)(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body: %s)", rr.Code, rr.Body)
	}
	if uc.got.Name != "x" {
		t.Errorf("got.Name = %q, want x", uc.got.Name)
	}
	if uc.got.TTLSeconds == nil || *uc.got.TTLSeconds != 30 {
		t.Errorf("got.TTLSeconds = %v, want 30", uc.got.TTLSeconds)
	}
	if uc.got.TrafficID != "t1" {
		t.Errorf("got.TrafficID = %q, want t1", uc.got.TrafficID)
	}
}

func TestPromoteTrafficRejectsMalformedJSONBody(t *testing.T) {
	uc := &fakePromoteTrafficUseCase{}
	rr := httptest.NewRecorder()
	req := newPostRequest(t, "/__lyrebird/traffic/t1/promote", `not json`)
	req.SetPathValue("id", "t1")
	PromoteTraffic(uc)(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a malformed body", rr.Code)
	}
}

func TestPromoteTrafficRejectsUnknownFieldInJSONBody(t *testing.T) {
	uc := &fakePromoteTrafficUseCase{}
	rr := httptest.NewRecorder()
	req := newPostRequest(t, "/__lyrebird/traffic/t1/promote", `{"name":"x","ttl_seconds":30,"totally_bogus_field":"oops"}`)
	req.SetPathValue("id", "t1")
	PromoteTraffic(uc)(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a body with an unknown field", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "unknown field") {
		t.Errorf("body = %q, want it to mention the unknown field", rr.Body.String())
	}
}

func TestPromoteTrafficMapsNotFoundErrorViaExplain(t *testing.T) {
	uc := &fakePromoteTrafficUseCase{err: domain.ErrNotFound}
	rr := httptest.NewRecorder()
	req := newBodilessPostRequest(t, "/__lyrebird/traffic/missing/promote")
	req.SetPathValue("id", "missing")
	PromoteTraffic(uc)(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 when promoting missing traffic", rr.Code)
	}
}
