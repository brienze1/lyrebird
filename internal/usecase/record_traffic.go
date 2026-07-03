package usecase

import (
	"context"
	"fmt"

	"github.com/brienze1/lyrebird/internal/domain"
)

// RecordTraffic persists one recorded interaction (FR-002). It is
// deliberately pure — no net/http, no io.Reader — so it is trivially unit
// testable; the adapter layer (proxy handler) is responsible for capturing
// and capping request/response bodies before calling Execute.
type RecordTraffic struct {
	repo  TrafficRepo
	clock Clock
	ids   IDGen
}

// NewRecordTraffic builds a RecordTraffic use case.
func NewRecordTraffic(repo TrafficRepo, clock Clock, ids IDGen) *RecordTraffic {
	return &RecordTraffic{repo: repo, clock: clock, ids: ids}
}

// RecordTrafficInput carries everything needed to build one TrafficRecord.
// Request/Response bodies are already truncated-to-cap by the caller.
type RecordTrafficInput struct {
	Partition, Method, Host, Path string

	RequestHeaders       map[string][]string
	RequestBody          []byte
	RequestBodyTruncated bool
	RequestBodyTotalSize int64

	Decision      domain.Decision
	MatchedMockID *string

	ResponseHeaders       map[string][]string
	ResponseBody          []byte
	ResponseBodyTruncated bool
	ResponseBodyTotalSize int64

	Status    int
	LatencyMS int
}

// Execute encodes and appends the traffic record, returning it as stored.
func (uc *RecordTraffic) Execute(ctx context.Context, in RecordTrafficInput) (domain.TrafficRecord, error) {
	reqJSON, err := EncodeRecordedMessage(RecordedMessage{
		Headers: in.RequestHeaders, Body: in.RequestBody,
		BodyTruncated: in.RequestBodyTruncated, BodyTotalSize: in.RequestBodyTotalSize,
	})
	if err != nil {
		return domain.TrafficRecord{}, fmt.Errorf("usecase: encode request: %w", err)
	}
	respJSON, err := EncodeRecordedMessage(RecordedMessage{
		Headers: in.ResponseHeaders, Body: in.ResponseBody,
		BodyTruncated: in.ResponseBodyTruncated, BodyTotalSize: in.ResponseBodyTotalSize,
	})
	if err != nil {
		return domain.TrafficRecord{}, fmt.Errorf("usecase: encode response: %w", err)
	}

	rec := domain.TrafficRecord{
		ID:            uc.ids.NewID(),
		Partition:     in.Partition,
		Timestamp:     uc.clock.Now(),
		Method:        in.Method,
		Host:          in.Host,
		Path:          in.Path,
		Request:       reqJSON,
		MatchedMockID: in.MatchedMockID,
		Decision:      in.Decision,
		Response:      respJSON,
		Status:        in.Status,
		LatencyMS:     in.LatencyMS,
	}
	if err := uc.repo.AppendTraffic(ctx, rec); err != nil {
		return domain.TrafficRecord{}, fmt.Errorf("usecase: append traffic: %w", err)
	}
	return rec, nil
}
