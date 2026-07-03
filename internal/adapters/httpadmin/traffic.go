package httpadmin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/brienze1/lyrebird/internal/adapters/httpmw"
	"github.com/brienze1/lyrebird/internal/domain"
	"github.com/brienze1/lyrebird/internal/usecase"
)

type trafficSummaryDTO struct {
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

type recordedMessageDTO struct {
	Headers       map[string][]string `json:"headers"`
	Body          []byte              `json:"body"`
	BodyTruncated bool                `json:"body_truncated"`
	BodyTotalSize int64               `json:"body_total_size"`
}

type trafficDetailDTO struct {
	trafficSummaryDTO
	Request  recordedMessageDTO `json:"request"`
	Response recordedMessageDTO `json:"response"`
}

func toSummaryDTO(t domain.TrafficRecord) trafficSummaryDTO {
	return trafficSummaryDTO{
		ID: t.ID, Timestamp: t.Timestamp.UTC().Format(time.RFC3339Nano),
		Method: t.Method, Host: t.Host, Path: t.Path,
		Status: t.Status, LatencyMS: t.LatencyMS, Decision: string(t.Decision),
		MatchedMockID: t.MatchedMockID,
	}
}

func toRecordedMessageDTO(m usecase.RecordedMessage) recordedMessageDTO {
	return recordedMessageDTO{Headers: m.Headers, Body: m.Body, BodyTruncated: m.BodyTruncated, BodyTotalSize: m.BodyTotalSize}
}

type listTrafficUseCase interface {
	Execute(ctx context.Context, partition string, filter usecase.TrafficFilter) ([]domain.TrafficRecord, error)
}

type getTrafficUseCase interface {
	Execute(ctx context.Context, partition, id string) (domain.TrafficRecord, error)
}

// ListTraffic handles GET /__lyrebird/traffic (contracts/admin-rest.md).
// Query params: method, host, path_prefix, status, since, until (RFC3339).
func ListTraffic(uc listTrafficUseCase) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		partition := httpmw.PartitionFromContext(r.Context())
		filter, err := parseTrafficFilter(r)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err)
			return
		}

		list, err := uc.Execute(r.Context(), partition, filter)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err)
			return
		}
		out := make([]trafficSummaryDTO, len(list))
		for i, t := range list {
			out[i] = toSummaryDTO(t)
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// GetTraffic handles GET /__lyrebird/traffic/{id} (contracts/admin-rest.md).
func GetTraffic(uc getTrafficUseCase) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		partition := httpmw.PartitionFromContext(r.Context())
		id := r.PathValue("id")

		t, err := uc.Execute(r.Context(), partition, id)
		if errors.Is(err, domain.ErrNotFound) {
			writeJSONError(w, http.StatusNotFound, err)
			return
		}
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err)
			return
		}

		req, err := usecase.DecodeRecordedMessage(t.Request)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err)
			return
		}
		resp, err := usecase.DecodeRecordedMessage(t.Response)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err)
			return
		}

		writeJSON(w, http.StatusOK, trafficDetailDTO{
			trafficSummaryDTO: toSummaryDTO(t),
			Request:           toRecordedMessageDTO(req),
			Response:          toRecordedMessageDTO(resp),
		})
	}
}

func parseTrafficFilter(r *http.Request) (usecase.TrafficFilter, error) {
	q := r.URL.Query()
	var filter usecase.TrafficFilter

	filter.Method = q.Get("method")
	filter.Host = q.Get("host")
	filter.PathPrefix = q.Get("path_prefix")

	if raw := q.Get("status"); raw != "" {
		status, err := strconv.Atoi(raw)
		if err != nil {
			return usecase.TrafficFilter{}, fmt.Errorf("invalid query parameter status=%q", raw)
		}
		filter.Status = &status
	}
	if raw := q.Get("since"); raw != "" {
		since, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return usecase.TrafficFilter{}, fmt.Errorf("invalid query parameter since=%q", raw)
		}
		filter.Since = &since
	}
	if raw := q.Get("until"); raw != "" {
		until, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return usecase.TrafficFilter{}, fmt.Errorf("invalid query parameter until=%q", raw)
		}
		filter.Until = &until
	}
	return filter, nil
}
