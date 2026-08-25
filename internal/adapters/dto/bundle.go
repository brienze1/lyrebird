package dto

import (
	"gopkg.in/yaml.v3"

	"github.com/brienze1/lyrebird/internal/domain"
	"github.com/brienze1/lyrebird/internal/usecase"
)

// SeedBundleDTO is the wire envelope for import/export, shaped like a mounted seed file. Only ephemeral mocks are included.
type SeedBundleDTO struct {
	Space     string        `json:"space,omitempty" yaml:"space,omitempty"`
	Upstreams []UpstreamDTO `json:"upstreams,omitempty" yaml:"upstreams,omitempty"`
	// Endpoints renders a space's byte-stream boundaries in the seed shape,
	// so an export re-seeds a boundary from scratch alongside its rules
	// (003's FR-023). Placed before Mocks because a rule targets an
	// endpoint, and a file reads better declaring the boundary first.
	Endpoints []EndpointDTO `json:"endpoints,omitempty" yaml:"endpoints,omitempty"`
	Mocks     []MockDTO     `json:"mocks,omitempty" yaml:"mocks,omitempty"`
}

// SeedBundleToDTO converts a usecase.ExportBundle to its wire equivalent.
func SeedBundleToDTO(bundle usecase.ExportBundle) SeedBundleDTO {
	upstreams := make([]UpstreamDTO, len(bundle.Upstreams))
	for i, u := range bundle.Upstreams {
		upstreams[i] = UpstreamToDTO(u)
	}
	mocks := make([]MockDTO, len(bundle.Mocks))
	for i, m := range bundle.Mocks {
		mocks[i] = MockToDTO(m)
	}
	endpoints := make([]EndpointDTO, len(bundle.Endpoints))
	for i, e := range bundle.Endpoints {
		// Occupancy is live runtime state, never part of a configuration
		// file: exporting "this was connected when I dumped it" would make
		// the file describe a moment rather than a boundary.
		endpoints[i] = EndpointToDTO(e, false)
	}
	return SeedBundleDTO{Space: bundle.Space, Upstreams: upstreams, Endpoints: endpoints, Mocks: mocks}
}

// SeedBundleFromDTO converts a SeedBundleDTO into the domain and use-case
// values an import applies for partition.
func SeedBundleFromDTO(partition string, bundle SeedBundleDTO) ([]domain.Upstream, []usecase.MockInput, []usecase.EndpointInput, error) {
	upstreams := make([]domain.Upstream, len(bundle.Upstreams))
	for i, u := range bundle.Upstreams {
		upstreams[i] = UpstreamFromDTO(partition, u)
	}
	mocks := make([]usecase.MockInput, len(bundle.Mocks))
	for i, m := range bundle.Mocks {
		in, err := MockInputFromDTO(partition, m)
		if err != nil {
			return nil, nil, nil, err
		}
		mocks[i] = in
	}
	endpoints := make([]usecase.EndpointInput, len(bundle.Endpoints))
	for i, e := range bundle.Endpoints {
		in, err := EndpointInputFromDTO(partition, e)
		if err != nil {
			return nil, nil, nil, err
		}
		endpoints[i] = in
	}
	return upstreams, mocks, endpoints, nil
}

// BundleHasCapturedTraffic reports whether the bundle carries any mock
// derived from recorded traffic, so an exporter can warn that the file may
// contain content captured from a real run (003's FR-035).
//
// It is deliberately plane-agnostic: a promoted HTTP mock carries captured
// bytes just as a promoted byte-stream one does, so the warning is honest
// wherever it appears.
func BundleHasCapturedTraffic(bundle SeedBundleDTO) bool {
	for _, m := range bundle.Mocks {
		if m.FromCapture {
			return true
		}
	}
	return false
}

// capturedTrafficWarning opens an exported file that carries any mock derived
// from recorded traffic (003's FR-035).
//
// It is a comment rather than a field so it survives being pasted into a seed
// directory: YAML ignores it, `seeds.Load` ignores it, and a human reviewing
// a pull request cannot miss it. The point is that a file which may contain
// real recorded content cannot be committed unexamined.
const capturedTrafficWarning = "# WARNING: this export contains mocks promoted from recorded traffic.\n" +
	"# Their match conditions and response bodies are bytes that actually crossed a data plane\n" +
	"# and may carry real content. Review before committing this file.\n"

// RenderSeedBundleYAML marshals a bundle as a seed file, prefixing the
// captured-traffic warning when one applies.
//
// It lives here, not in an adapter, so the MCP tool and its Admin REST twin
// render byte-identical files — a warning that appeared over one surface but
// not the other would be worse than no warning at all (constitution
// Principle II).
func RenderSeedBundleYAML(bundle usecase.ExportBundle) ([]byte, error) {
	d := SeedBundleToDTO(bundle)
	raw, err := yaml.Marshal(d)
	if err != nil {
		return nil, err
	}
	if BundleHasCapturedTraffic(d) {
		return append([]byte(capturedTrafficWarning), raw...), nil
	}
	return raw, nil
}
