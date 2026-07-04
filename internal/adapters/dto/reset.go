package dto

import "github.com/brienze1/lyrebird/internal/usecase"

// ResetResultDTO is the wire shape of usecase.ResetOutput, shared by REST's
// POST /__lyrebird/reset and MCP's reset tool so their result shape can't
// silently drift (constitution Principle II).
type ResetResultDTO struct {
	MocksRemoved   int  `json:"mocks_removed"`
	TrafficCleared bool `json:"traffic_cleared"`
}

// ResetResultToDTO converts a usecase.ResetOutput to its wire equivalent.
func ResetResultToDTO(out usecase.ResetOutput) ResetResultDTO {
	return ResetResultDTO{MocksRemoved: out.MocksRemoved, TrafficCleared: out.TrafficCleared}
}
