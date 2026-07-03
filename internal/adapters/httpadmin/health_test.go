package httpadmin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthzAlwaysOK(t *testing.T) {
	rr := httptest.NewRecorder()
	Healthz(rr, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/__lyrebird/healthz", nil))
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
}

func TestReadyzNotReadyBeforeMarkReady(t *testing.T) {
	r := &Readiness{}
	rr := httptest.NewRecorder()
	Readyz(r)(rr, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/__lyrebird/readyz", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rr.Code)
	}
}

func TestReadyzReadyAfterMarkReady(t *testing.T) {
	r := &Readiness{}
	r.MarkReady()
	rr := httptest.NewRecorder()
	Readyz(r)(rr, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/__lyrebird/readyz", nil))
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
}
