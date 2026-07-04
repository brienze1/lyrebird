package httpadmin

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/brienze1/lyrebird/internal/adapters/dto"
	"github.com/brienze1/lyrebird/internal/adapters/httpmw"
	"github.com/brienze1/lyrebird/internal/domain"
	"github.com/brienze1/lyrebird/internal/usecase"
)

type listTrafficUseCase interface {
	Execute(ctx context.Context, partition string, filter usecase.TrafficFilter) ([]domain.TrafficRecord, error)
}

type getTrafficUseCase interface {
	Execute(ctx context.Context, partition, id string) (domain.TrafficRecord, error)
}

// ListTraffic handles GET /__lyrebird/traffic (contracts/admin-rest.md).
// Query params: method, host, path_prefix, status, since, until (RFC3339), limit.
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
			writeUseCaseError(w, err)
			return
		}
		out := make([]dto.TrafficSummaryDTO, len(list))
		for i, t := range list {
			out[i] = dto.TrafficToSummaryDTO(t)
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
		if err != nil {
			writeUseCaseError(w, err)
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

		writeJSON(w, http.StatusOK, dto.TrafficDetailDTO{
			TrafficSummaryDTO: dto.TrafficToSummaryDTO(t),
			Request:           dto.RecordedMessageToDTO(req),
			Response:          dto.RecordedMessageToDTO(resp),
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
	if raw := q.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil {
			return usecase.TrafficFilter{}, fmt.Errorf("invalid query parameter limit=%q", raw)
		}
		filter.Limit = limit
	}
	return filter, nil
}
