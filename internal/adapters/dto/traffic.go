package dto

import (
	"github.com/brienze1/lyrebird/internal/domain"
	"github.com/brienze1/lyrebird/internal/usecase"
)

// TrafficSummaryDTO is the wire shape of a domain.TrafficRecord without its
// decrypted request/response bodies.
type TrafficSummaryDTO struct {
	ID            string  `json:"id"`
	Timestamp     string  `json:"timestamp"`
	Method        string  `json:"method"`
	Host          string  `json:"host"`
	Path          string  `json:"path"`
	Status        int     `json:"status"`
	LatencyMS     int     `json:"latency_ms"`
	Decision      string  `json:"decision"`
	MatchedMockID *string `json:"matched_mock_id,omitempty"`
}

// RecordedMessageDTO is the wire shape of usecase.RecordedMessage.
type RecordedMessageDTO struct {
	Headers       map[string][]string `json:"headers"`
	Body          []byte              `json:"body"`
	BodyTruncated bool                `json:"body_truncated"`
	BodyTotalSize int64               `json:"body_total_size"`
}

// TrafficDetailDTO is TrafficSummaryDTO plus the decrypted request/response.
type TrafficDetailDTO struct {
	TrafficSummaryDTO
	Request  RecordedMessageDTO `json:"request"`
	Response RecordedMessageDTO `json:"response"`
}

// TrafficToSummaryDTO converts a domain.TrafficRecord to its wire summary.
func TrafficToSummaryDTO(t domain.TrafficRecord) TrafficSummaryDTO {
	return TrafficSummaryDTO{
		ID: t.ID, Timestamp: t.Timestamp.UTC().Format("2006-01-02T15:04:05.000Z"),
		Method: t.Method, Host: t.Host, Path: t.Path,
		Status: t.Status, LatencyMS: t.LatencyMS, Decision: string(t.Decision),
		MatchedMockID: t.MatchedMockID,
	}
}

// RecordedMessageToDTO converts a usecase.RecordedMessage to its wire equivalent.
func RecordedMessageToDTO(m usecase.RecordedMessage) RecordedMessageDTO {
	return RecordedMessageDTO{Headers: m.Headers, Body: m.Body, BodyTruncated: m.BodyTruncated, BodyTotalSize: m.BodyTotalSize}
}
