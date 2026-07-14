package proxy

import (
	"context"
	"net/http"
	"strings"

	"github.com/brienze1/lyrebird/internal/domain"
)

// routedUpstreamKey stashes the upstream an ingress route-prefix resolved to,
// so serveProxied forwards there without re-resolving the (already stripped)
// path.
type routedUpstreamKey struct{}

// applyIngressRoute implements path-prefix routing: it strips a prefix-style
// upstream MatchPath (a "route prefix" like /coreai, /antecipame, /graph-fb)
// from r's path BEFORE mock-matching and upstream resolution run, and stashes
// the matched upstream. The effect is "one route prefix per external provider,
// and everything behind that prefix belongs to that provider" — the mock AND
// the forwarded request both see the clean path (/request_anticipation), so
// mocks never have to encode the routing prefix.
//
// A route whose upstream has an empty TargetURL is strip-only: the prefix is
// still stripped (so mocks match clean paths), but an unmatched request has no
// passthrough and serveProxied answers not_configured (404) — e.g. Antecipame,
// which is fully mocked with no real backend.
//
// Returns r unchanged when the best-matching upstream is host-only (empty
// MatchPath) or a "~" regex (match-only, nothing to strip) — preserving the
// original host/regex behaviour for every non-prefix upstream.
func applyIngressRoute(r *http.Request, upstreams []domain.Upstream) *http.Request {
	up, ok := ResolveUpstream(upstreams, r.Host, r.URL.Path)
	if !ok || up.MatchPath == "" || strings.HasPrefix(up.MatchPath, "~") {
		return r
	}
	stripped := strings.TrimPrefix(r.URL.Path, up.MatchPath)
	if !strings.HasPrefix(stripped, "/") {
		stripped = "/" + stripped
	}
	r.URL.Path = stripped
	r.URL.RawPath = ""
	return r.WithContext(context.WithValue(r.Context(), routedUpstreamKey{}, up))
}

// routedUpstream returns the upstream an ingress route-prefix resolved to, if
// applyIngressRoute stashed one for this request.
func routedUpstream(ctx context.Context) (domain.Upstream, bool) {
	up, ok := ctx.Value(routedUpstreamKey{}).(domain.Upstream)
	return up, ok
}
