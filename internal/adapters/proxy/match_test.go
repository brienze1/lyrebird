package proxy

import (
	"testing"

	"github.com/brienze1/lyrebird/internal/domain"
)

func TestResolveUpstreamExactMatch(t *testing.T) {
	upstreams := []domain.Upstream{
		{MatchHost: "api.example.com", TargetURL: "https://a"},
	}
	got, ok := ResolveUpstream(upstreams, "api.example.com", "/")
	if !ok || got.TargetURL != "https://a" {
		t.Fatalf("ResolveUpstream() = %+v, %v, want a match on https://a", got, ok)
	}
}

func TestResolveUpstreamStripsPortAndLowercases(t *testing.T) {
	upstreams := []domain.Upstream{{MatchHost: "API.example.com", TargetURL: "https://a"}}
	got, ok := ResolveUpstream(upstreams, "api.EXAMPLE.com:8443", "/")
	if !ok || got.TargetURL != "https://a" {
		t.Fatalf("ResolveUpstream() = %+v, %v, want a case-insensitive, port-stripped match", got, ok)
	}
}

func TestResolveUpstreamGlobMatch(t *testing.T) {
	upstreams := []domain.Upstream{{MatchHost: "*.example.com", TargetURL: "https://wildcard"}}
	got, ok := ResolveUpstream(upstreams, "api.example.com", "/")
	if !ok || got.TargetURL != "https://wildcard" {
		t.Fatalf("ResolveUpstream() = %+v, %v, want a glob match", got, ok)
	}
}

func TestResolveUpstreamNoMatch(t *testing.T) {
	upstreams := []domain.Upstream{{MatchHost: "api.example.com", TargetURL: "https://a"}}
	_, ok := ResolveUpstream(upstreams, "other.example.com", "/")
	if ok {
		t.Fatal("ResolveUpstream() matched a host it should not have")
	}
}

func TestResolveUpstreamPrefersExactOverGlob(t *testing.T) {
	upstreams := []domain.Upstream{
		{MatchHost: "*.example.com", TargetURL: "https://wildcard"},
		{MatchHost: "api.example.com", TargetURL: "https://exact"},
	}
	got, ok := ResolveUpstream(upstreams, "api.example.com", "/")
	if !ok || got.TargetURL != "https://exact" {
		t.Fatalf("ResolveUpstream() = %+v, %v, want the exact match to win over the glob", got, ok)
	}
}

func TestResolveUpstreamPrefersLongerGlobOnTie(t *testing.T) {
	upstreams := []domain.Upstream{
		{MatchHost: "*.com", TargetURL: "https://short"},
		{MatchHost: "*.example.com", TargetURL: "https://long"},
	}
	got, ok := ResolveUpstream(upstreams, "api.example.com", "/")
	if !ok || got.TargetURL != "https://long" {
		t.Fatalf("ResolveUpstream() = %+v, %v, want the longer/more specific glob to win", got, ok)
	}
}

func TestHostAllowedWithEmptyListAllowsEverything(t *testing.T) {
	if !HostAllowed(nil, "anything.example.com") {
		t.Error("HostAllowed(nil, ...) = false, want true (empty list means allow all)")
	}
}

func TestHostAllowedExactMatch(t *testing.T) {
	if !HostAllowed([]string{"example.local"}, "example.local") {
		t.Error("HostAllowed() = false, want true for an exact match")
	}
}

func TestHostAllowedGlobMatch(t *testing.T) {
	if !HostAllowed([]string{"*.example.com"}, "api.example.com") {
		t.Error("HostAllowed() = false, want true for a glob match")
	}
}

func TestHostAllowedStripsPortAndLowercases(t *testing.T) {
	if !HostAllowed([]string{"Example.LOCAL"}, "example.local:8080") {
		t.Error("HostAllowed() = false, want a case-insensitive, port-stripped match")
	}
}

func TestHostAllowedRejectsUnlistedHost(t *testing.T) {
	if HostAllowed([]string{"example.local"}, "other.local") {
		t.Error("HostAllowed() = true, want false for a host not in the list")
	}
}

func TestResolveUpstreamRoutesByPathPrefix(t *testing.T) {
	upstreams := []domain.Upstream{
		{MatchHost: "lyrebird", MatchPath: "/graph-fb", TargetURL: "https://graph.facebook.com"},
		{MatchHost: "lyrebird", MatchPath: "/graph-ig", TargetURL: "https://graph.instagram.com"},
	}
	fb, ok := ResolveUpstream(upstreams, "lyrebird", "/graph-fb/v23.0/debug_token")
	if !ok || fb.TargetURL != "https://graph.facebook.com" {
		t.Fatalf("fb path = %+v, %v, want graph.facebook.com", fb, ok)
	}
	ig, ok := ResolveUpstream(upstreams, "lyrebird", "/graph-ig/v23.0/123/media")
	if !ok || ig.TargetURL != "https://graph.instagram.com" {
		t.Fatalf("ig path = %+v, %v, want graph.instagram.com", ig, ok)
	}
}

func TestResolveUpstreamEmptyPathMatchesAnyPath(t *testing.T) {
	upstreams := []domain.Upstream{{MatchHost: "api.example.com", TargetURL: "https://a"}}
	got, ok := ResolveUpstream(upstreams, "api.example.com", "/anything/at/all")
	if !ok || got.TargetURL != "https://a" {
		t.Fatalf("host-only upstream = %+v, %v, want it to match any path", got, ok)
	}
}

func TestResolveUpstreamPrefersPathScopedOverHostOnly(t *testing.T) {
	upstreams := []domain.Upstream{
		{MatchHost: "lyrebird", TargetURL: "https://host-only"},
		{MatchHost: "lyrebird", MatchPath: "/graph-fb", TargetURL: "https://path-scoped"},
	}
	got, ok := ResolveUpstream(upstreams, "lyrebird", "/graph-fb/x")
	if !ok || got.TargetURL != "https://path-scoped" {
		t.Fatalf("ResolveUpstream() = %+v, %v, want the path-scoped upstream to win", got, ok)
	}
}

func TestResolveUpstreamNoMatchWhenPathDiffers(t *testing.T) {
	upstreams := []domain.Upstream{
		{MatchHost: "lyrebird", MatchPath: "/graph-fb", TargetURL: "https://fb"},
	}
	_, ok := ResolveUpstream(upstreams, "lyrebird", "/something-else")
	if ok {
		t.Fatal("ResolveUpstream() matched a path it should not have")
	}
}

func TestResolveUpstreamRegexPath(t *testing.T) {
	upstreams := []domain.Upstream{
		{MatchHost: "lyrebird", MatchPath: "~^/v[0-9]+/debug_token$", TargetURL: "https://fb"},
	}
	got, ok := ResolveUpstream(upstreams, "lyrebird", "/v23/debug_token")
	if !ok || got.TargetURL != "https://fb" {
		t.Fatalf("regex path = %+v, %v, want a regex match", got, ok)
	}
	if _, ok := ResolveUpstream(upstreams, "lyrebird", "/v23/media"); ok {
		t.Fatal("regex path matched a non-matching path")
	}
}
