package dto

import "github.com/brienze1/lyrebird/internal/domain"

// UpstreamDTO is the wire shape of domain.Upstream.
type UpstreamDTO struct {
	MatchHost     string `json:"match_host" yaml:"match_host"`
	TargetURL     string `json:"target_url" yaml:"target_url"`
	TLSSkipVerify bool   `json:"tls_skip_verify" yaml:"tls_skip_verify"`
}

// UpstreamToDTO converts a domain.Upstream to its wire equivalent.
func UpstreamToDTO(u domain.Upstream) UpstreamDTO {
	return UpstreamDTO{MatchHost: u.MatchHost, TargetURL: u.TargetURL, TLSSkipVerify: u.TLSSkipVerify}
}

// NewUpstreamDTOFromFields builds an UpstreamDTO from its settable fields —
// for adapters (e.g. mcp) that define their own parallel input schema and
// need to construct an UpstreamDTO rather than json.Decode one directly off
// the wire the way httpadmin does.
func NewUpstreamDTOFromFields(matchHost, targetURL string, tlsSkipVerify bool) UpstreamDTO {
	return UpstreamDTO{MatchHost: matchHost, TargetURL: targetURL, TLSSkipVerify: tlsSkipVerify}
}

// UpstreamFromDTO converts an UpstreamDTO to its domain equivalent for partition.
func UpstreamFromDTO(partition string, d UpstreamDTO) domain.Upstream {
	return domain.Upstream{Partition: partition, MatchHost: d.MatchHost, TargetURL: d.TargetURL, TLSSkipVerify: d.TLSSkipVerify}
}
