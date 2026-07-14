package proxy

import (
	"net/http/httptest"
	"testing"

	"github.com/brienze1/lyrebird/internal/domain"
)

func ingressUpstreams() []domain.Upstream {
	return []domain.Upstream{
		{MatchHost: "creatorads-lyrebird", MatchPath: "/coreai", TargetURL: "https://staging.coreai"},
		{MatchHost: "creatorads-lyrebird", MatchPath: "/graph-fb", TargetURL: "https://graph.facebook.com"},
		{MatchHost: "creatorads-lyrebird", MatchPath: "/antecipame", TargetURL: ""}, // strip-only
		{MatchHost: "plain-host", TargetURL: "https://plain"},                       // host-only
		{MatchHost: "creatorads-lyrebird", MatchPath: "~^/rx/", TargetURL: "https://rx"},
	}
}

func TestApplyIngressRouteStripsPrefixAndStashes(t *testing.T) {
	r := httptest.NewRequest("GET", "http://creatorads-lyrebird/coreai/request_anticipation", nil)
	r = applyIngressRoute(r, ingressUpstreams())
	if r.URL.Path != "/request_anticipation" {
		t.Fatalf("path = %q, want /request_anticipation", r.URL.Path)
	}
	up, ok := routedUpstream(r.Context())
	if !ok || up.TargetURL != "https://staging.coreai" {
		t.Fatalf("routed = %+v, %v, want staging.coreai", up, ok)
	}
}

func TestApplyIngressRouteStripOnlyRouteHasEmptyTarget(t *testing.T) {
	r := httptest.NewRequest("GET", "http://creatorads-lyrebird/antecipame/api/v1/pix/transfers", nil)
	r = applyIngressRoute(r, ingressUpstreams())
	if r.URL.Path != "/api/v1/pix/transfers" {
		t.Fatalf("path = %q, want /api/v1/pix/transfers", r.URL.Path)
	}
	up, ok := routedUpstream(r.Context())
	if !ok || up.TargetURL != "" {
		t.Fatalf("routed = %+v, %v, want a strip-only route (empty target)", up, ok)
	}
}

func TestApplyIngressRouteHostOnlyDoesNotStripOrStash(t *testing.T) {
	r := httptest.NewRequest("GET", "http://plain-host/whatever", nil)
	r = applyIngressRoute(r, ingressUpstreams())
	if r.URL.Path != "/whatever" {
		t.Fatalf("path = %q, want unchanged /whatever", r.URL.Path)
	}
	if _, ok := routedUpstream(r.Context()); ok {
		t.Fatal("host-only upstream should not stash a routed upstream")
	}
}

func TestApplyIngressRouteRegexIsMatchOnlyNoStrip(t *testing.T) {
	r := httptest.NewRequest("GET", "http://creatorads-lyrebird/rx/thing", nil)
	r = applyIngressRoute(r, ingressUpstreams())
	if r.URL.Path != "/rx/thing" {
		t.Fatalf("path = %q, want unchanged /rx/thing (regex match_path is match-only)", r.URL.Path)
	}
	if _, ok := routedUpstream(r.Context()); ok {
		t.Fatal("regex match_path should not be stripped/stashed at ingress")
	}
}

func TestApplyIngressRouteNoMatchLeavesRequestUnchanged(t *testing.T) {
	r := httptest.NewRequest("GET", "http://creatorads-lyrebird/unrouted/x", nil)
	r = applyIngressRoute(r, ingressUpstreams())
	if r.URL.Path != "/unrouted/x" {
		t.Fatalf("path = %q, want unchanged", r.URL.Path)
	}
	if _, ok := routedUpstream(r.Context()); ok {
		t.Fatal("no matching prefix should not stash")
	}
}
