package proxy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/brienze1/lyrebird/internal/usecase"
)

func strPtr(s string) *string { return &s }
func intPtr(n int) *int       { return &n }

func TestApplyRewriteChangesMethodPathHeadersBody(t *testing.T) {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.local/original", nil)
	applyRewrite(req, usecase.RewrittenRequest{
		Method: strPtr("POST"), Path: strPtr("/rewritten"),
		Headers: map[string][]string{"X-Injected": {"yes"}}, Body: []byte("new body"), BodySet: true,
	})

	if req.Method != "POST" {
		t.Errorf("Method = %q, want POST", req.Method)
	}
	if req.URL.Path != "/rewritten" {
		t.Errorf("Path = %q, want /rewritten", req.URL.Path)
	}
	if req.Header.Get("X-Injected") != "yes" {
		t.Errorf("Header X-Injected = %q, want yes", req.Header.Get("X-Injected"))
	}
	body, _ := io.ReadAll(req.Body)
	if string(body) != "new body" {
		t.Errorf("Body = %q, want %q", body, "new body")
	}
	if req.ContentLength != int64(len("new body")) {
		t.Errorf("ContentLength = %d, want %d", req.ContentLength, len("new body"))
	}
}

func TestApplyRewriteNilFieldsLeaveRequestUnchanged(t *testing.T) {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.local/original", nil)
	applyRewrite(req, usecase.RewrittenRequest{})

	if req.Method != http.MethodGet {
		t.Errorf("Method = %q, want unchanged GET", req.Method)
	}
	if req.URL.Path != "/original" {
		t.Errorf("Path = %q, want unchanged /original", req.URL.Path)
	}
}

func TestApplyRewriteNilHeaderValueDeletesHeader(t *testing.T) {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.local/x", nil)
	req.Header.Set("X-Remove", "was-here")
	applyRewrite(req, usecase.RewrittenRequest{Headers: map[string][]string{"X-Remove": nil}})

	if req.Header.Get("X-Remove") != "" {
		t.Errorf("Header X-Remove = %q, want deleted", req.Header.Get("X-Remove"))
	}
}

func TestApplyRewritePreservesUntouchedHeaders(t *testing.T) {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.local/x", nil)
	req.Header.Set("X-Keep", "original")
	applyRewrite(req, usecase.RewrittenRequest{Headers: map[string][]string{"X-New": {"added"}}})

	if req.Header.Get("X-Keep") != "original" {
		t.Errorf("Header X-Keep = %q, want untouched original", req.Header.Get("X-Keep"))
	}
	if req.Header.Get("X-New") != "added" {
		t.Errorf("Header X-New = %q, want added", req.Header.Get("X-New"))
	}
}

func TestApplyTransformChangesStatusHeadersBody(t *testing.T) {
	resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: http.NoBody}
	applyTransform(resp, usecase.TransformedResponse{
		Status: intPtr(201), Headers: map[string][]string{"X-Transformed": {"yes"}}, Body: []byte("new"), BodySet: true,
	})

	if resp.StatusCode != 201 {
		t.Errorf("StatusCode = %d, want 201", resp.StatusCode)
	}
	if resp.Header.Get("X-Transformed") != "yes" {
		t.Errorf("Header X-Transformed = %q, want yes", resp.Header.Get("X-Transformed"))
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "new" {
		t.Errorf("Body = %q, want %q", body, "new")
	}
}

func TestApplyTransformNilFieldsLeaveResponseUnchanged(t *testing.T) {
	resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: http.NoBody}
	applyTransform(resp, usecase.TransformedResponse{})

	if resp.StatusCode != 200 {
		t.Errorf("StatusCode = %d, want unchanged 200", resp.StatusCode)
	}
	if resp.Body != http.NoBody {
		t.Error("Body was replaced, want left untouched when BodySet is false")
	}
}
