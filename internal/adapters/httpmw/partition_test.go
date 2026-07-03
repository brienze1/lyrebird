package httpmw

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/brienze1/lyrebird/internal/domain"
)

func TestPartitionDefaultsWhenHeaderAbsent(t *testing.T) {
	var got string
	h := Partition("default")(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = PartitionFromContext(r.Context())
	}))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)

	if got != "default" {
		t.Errorf("partition = %q, want default", got)
	}
}

func TestPartitionUsesHeaderWhenPresent(t *testing.T) {
	var got string
	h := Partition("default")(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = PartitionFromContext(r.Context())
	}))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	req.Header.Set(SpaceHeader, "agent-a")
	h.ServeHTTP(httptest.NewRecorder(), req)

	if got != "agent-a" {
		t.Errorf("partition = %q, want agent-a", got)
	}
}

func TestPartitionFromContextFallsBackToDefault(t *testing.T) {
	if got := PartitionFromContext(context.Background()); got != domain.DefaultPartitionID {
		t.Errorf("PartitionFromContext() with no value = %q, want %q", got, domain.DefaultPartitionID)
	}
}
