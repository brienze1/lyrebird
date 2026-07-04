package proxy

import (
	"net"
	"path"
	"strings"

	"github.com/brienze1/lyrebird/internal/domain"
)

// ResolveUpstream finds the best-matching Upstream for requestHost among
// upstreams (already filtered to one partition by the caller). MatchHost is
// glob syntax (data-model.md), matched via stdlib path.Match — "." isn't a
// path.Match separator, so "*.example.com" naturally matches
// "a.b.example.com" and a literal pattern degrades to an exact match.
func ResolveUpstream(upstreams []domain.Upstream, requestHost string) (domain.Upstream, bool) {
	host := strings.ToLower(hostOnly(requestHost))

	var best domain.Upstream
	found := false
	for _, u := range upstreams {
		ok, err := path.Match(strings.ToLower(u.MatchHost), host)
		if err != nil || !ok {
			continue
		}
		if !found || moreSpecific(u.MatchHost, best.MatchHost) {
			best, found = u, true
		}
	}
	return best, found
}

// HostAllowed reports whether requestHost may be proxied under the
// LYREBIRD_ALLOW_PROXY_HOSTS policy (FR-006). An empty allowHosts means
// "allow every host" — today's de-facto behavior, preserved (constitution
// Principle V: a security feature activates only once the operator
// explicitly configures it). A non-empty list is an allowlist matched with
// the exact same glob convention as Upstream.MatchHost, for consistency.
func HostAllowed(allowHosts []string, requestHost string) bool {
	if len(allowHosts) == 0 {
		return true
	}
	host := strings.ToLower(hostOnly(requestHost))
	for _, pattern := range allowHosts {
		if ok, err := path.Match(strings.ToLower(pattern), host); err == nil && ok {
			return true
		}
	}
	return false
}

func hostOnly(hostHeader string) string {
	if h, _, err := net.SplitHostPort(hostHeader); err == nil {
		return h
	}
	return hostHeader
}

// moreSpecific prefers an exact (glob-metacharacter-free) pattern over a
// glob, then the longer pattern, as a deterministic tie-break for the case
// (not specified by data-model.md) where two configured upstreams could
// both match the same host.
func moreSpecific(candidate, current string) bool {
	candidateExact := !strings.ContainsAny(candidate, "*?[")
	currentExact := !strings.ContainsAny(current, "*?[")
	if candidateExact != currentExact {
		return candidateExact
	}
	return len(candidate) > len(current)
}
