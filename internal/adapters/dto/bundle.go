package dto

import (
	"github.com/brienze1/lyrebird/internal/domain"
	"github.com/brienze1/lyrebird/internal/usecase"
)

// SeedBundleDTO is the wire envelope for import/export, shaped like a mounted seed file. Only ephemeral mocks are included.
type SeedBundleDTO struct {
	Space     string        `json:"space,omitempty" yaml:"space,omitempty"`
	Upstreams []UpstreamDTO `json:"upstreams,omitempty" yaml:"upstreams,omitempty"`
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
	return SeedBundleDTO{Space: bundle.Space, Upstreams: upstreams, Mocks: mocks}
}

// SeedBundleFromDTO converts a SeedBundleDTO into domain.Upstream/usecase.MockInput values for partition.
func SeedBundleFromDTO(partition string, bundle SeedBundleDTO) ([]domain.Upstream, []usecase.MockInput, error) {
	upstreams := make([]domain.Upstream, len(bundle.Upstreams))
	for i, u := range bundle.Upstreams {
		upstreams[i] = UpstreamFromDTO(partition, u)
	}
	mocks := make([]usecase.MockInput, len(bundle.Mocks))
	for i, m := range bundle.Mocks {
		in, err := MockInputFromDTO(partition, m)
		if err != nil {
			return nil, nil, err
		}
		mocks[i] = in
	}
	return upstreams, mocks, nil
}
