package proxy

import (
	"testing"

	"github.com/brienze1/lyrebird/internal/domain"
)

func TestResolveUpstreamExactMatch(t *testing.T) {
	upstreams := []domain.Upstream{
		{MatchHost: "api.example.com", TargetURL: "https://a"},
	}
	got, ok := ResolveUpstream(upstreams, "api.example.com")
	if !ok || got.TargetURL != "https://a" {
		t.Fatalf("ResolveUpstream() = %+v, %v, want a match on https://a", got, ok)
	}
}

func TestResolveUpstreamStripsPortAndLowercases(t *testing.T) {
	upstreams := []domain.Upstream{{MatchHost: "API.example.com", TargetURL: "https://a"}}
	got, ok := ResolveUpstream(upstreams, "api.EXAMPLE.com:8443")
	if !ok || got.TargetURL != "https://a" {
		t.Fatalf("ResolveUpstream() = %+v, %v, want a case-insensitive, port-stripped match", got, ok)
	}
}

func TestResolveUpstreamGlobMatch(t *testing.T) {
	upstreams := []domain.Upstream{{MatchHost: "*.example.com", TargetURL: "https://wildcard"}}
	got, ok := ResolveUpstream(upstreams, "api.example.com")
	if !ok || got.TargetURL != "https://wildcard" {
		t.Fatalf("ResolveUpstream() = %+v, %v, want a glob match", got, ok)
	}
}

func TestResolveUpstreamNoMatch(t *testing.T) {
	upstreams := []domain.Upstream{{MatchHost: "api.example.com", TargetURL: "https://a"}}
	_, ok := ResolveUpstream(upstreams, "other.example.com")
	if ok {
		t.Fatal("ResolveUpstream() matched a host it should not have")
	}
}

func TestResolveUpstreamPrefersExactOverGlob(t *testing.T) {
	upstreams := []domain.Upstream{
		{MatchHost: "*.example.com", TargetURL: "https://wildcard"},
		{MatchHost: "api.example.com", TargetURL: "https://exact"},
	}
	got, ok := ResolveUpstream(upstreams, "api.example.com")
	if !ok || got.TargetURL != "https://exact" {
		t.Fatalf("ResolveUpstream() = %+v, %v, want the exact match to win over the glob", got, ok)
	}
}

func TestResolveUpstreamPrefersLongerGlobOnTie(t *testing.T) {
	upstreams := []domain.Upstream{
		{MatchHost: "*.com", TargetURL: "https://short"},
		{MatchHost: "*.example.com", TargetURL: "https://long"},
	}
	got, ok := ResolveUpstream(upstreams, "api.example.com")
	if !ok || got.TargetURL != "https://long" {
		t.Fatalf("ResolveUpstream() = %+v, %v, want the longer/more specific glob to win", got, ok)
	}
}
