package proxy

import (
	"net/http/httptest"
	"net/http/httputil"
	"testing"
)

func TestStripUpstreamPathPrefix(t *testing.T) {
	cases := []struct {
		name, matchPath, inPath, want string
	}{
		{"prefix stripped", "/graph-fb", "/graph-fb/v23.0/debug_token", "/v23.0/debug_token"},
		{"ig prefix stripped", "/graph-ig", "/graph-ig/v23.0/123/media", "/v23.0/123/media"},
		{"exact prefix leaves root", "/graph-fb", "/graph-fb", "/"},
		{"empty match path is a no-op", "", "/v23.0/debug_token", "/v23.0/debug_token"},
		{"regexp match path is not stripped", "~^/graph-fb", "/graph-fb/v23.0/x", "/graph-fb/v23.0/x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pr := &httputil.ProxyRequest{Out: httptest.NewRequest("GET", "http://upstream"+tc.inPath, nil)}
			stripUpstreamPathPrefix(pr, tc.matchPath)
			if pr.Out.URL.Path != tc.want {
				t.Fatalf("path = %q, want %q", pr.Out.URL.Path, tc.want)
			}
		})
	}
}
