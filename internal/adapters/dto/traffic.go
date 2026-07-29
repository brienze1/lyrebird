package dto

import (
	"encoding/json"

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
//
// Body is a []byte, so it marshals as base64 — faithful, but unreadable to any
// consumer that asserts on a JSON path (a BDD suite checking what a service
// actually sent an upstream, for instance). JSON carries the same bytes already
// parsed when the body IS JSON, so those consumers can address
// `request.json.<path>` directly instead of decoding base64 themselves. It is
// omitted entirely for non-JSON and empty bodies, so nothing about the existing
// shape changes for consumers that only read Body.
type RecordedMessageDTO struct {
	Headers       map[string][]string `json:"headers"`
	Body          []byte              `json:"body"`
	JSON          json.RawMessage     `json:"json,omitempty"`
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
	return RecordedMessageDTO{
		Headers: m.Headers, Body: m.Body, JSON: jsonBodyOrNil(m.Body),
		BodyTruncated: m.BodyTruncated, BodyTotalSize: m.BodyTotalSize,
	}
}

// jsonBodyOrNil returns body verbatim when it is valid JSON, else nil. Returning
// the original bytes rather than a re-marshalled value keeps the numbers exactly
// as the sender wrote them — a rate of 0.011 stays 0.011 and never becomes
// 0.011000000000000001 through a float64 round trip. A truncated body is not
// valid JSON, so it is correctly omitted rather than half-parsed.
func jsonBodyOrNil(body []byte) json.RawMessage {
	if len(body) == 0 || !json.Valid(body) {
		return nil
	}
	return json.RawMessage(body)
}
