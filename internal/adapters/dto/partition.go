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

// NewPartitionDTOFromFields builds a PartitionDTO from its settable fields —
// for adapters (e.g. mcp) that define their own parallel input schema and
// need to construct a PartitionDTO rather than json.Decode one directly off
// the wire the way httpadmin does.
func NewPartitionDTOFromFields(id, description string) PartitionDTO {
	return PartitionDTO{ID: id, Description: description}
}

// PartitionFromDTO converts a PartitionDTO to its domain equivalent.
func PartitionFromDTO(d PartitionDTO) domain.Partition {
	return domain.Partition{ID: d.ID, Description: d.Description}
}
