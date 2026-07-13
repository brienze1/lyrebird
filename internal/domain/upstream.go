package domain

// Upstream is the real target Lyrebird forwards unmatched (spy) requests to,
// for a given host pattern within a partition.
type Upstream struct {
	Partition string
	MatchHost string
	// MatchPath optionally narrows an upstream to requests whose path also
	// matches. Empty means "any path" (host-only, the original behavior). A
	// leading "~" selects a regexp matched against the request path; otherwise
	// it is a path PREFIX (the request path must start with it). This lets two
	// upstreams share one host and route to different real targets by path.
	MatchPath     string
	TargetURL     string
	TLSSkipVerify bool
}
