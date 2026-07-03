package domain

// Upstream is the real target Lyrebird forwards unmatched (spy) requests to,
// for a given host pattern within a partition.
type Upstream struct {
	Partition     string
	MatchHost     string
	TargetURL     string
	TLSSkipVerify bool
}
