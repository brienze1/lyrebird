package dto

import "github.com/brienze1/lyrebird/internal/domain"

// PartitionDTO is the wire shape of domain.Partition. CreatedAt is
// server-assigned, not caller-settable, so it is intentionally omitted here
// (mirrors UpstreamDTO omitting Partition).
type PartitionDTO struct {
	ID          string `json:"id"`
	Description string `json:"description,omitempty"`
}

// PartitionToDTO converts a domain.Partition to its wire equivalent.
func PartitionToDTO(p domain.Partition) PartitionDTO {
	return PartitionDTO{ID: p.ID, Description: p.Description}
}

// PartitionFromDTO converts a PartitionDTO to its domain equivalent.
func PartitionFromDTO(d PartitionDTO) domain.Partition {
	return domain.Partition{ID: d.ID, Description: d.Description}
}
