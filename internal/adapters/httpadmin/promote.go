package httpadmin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/brienze1/lyrebird/internal/adapters/dto"
	"github.com/brienze1/lyrebird/internal/adapters/httpmw"
	"github.com/brienze1/lyrebird/internal/domain"
	"github.com/brienze1/lyrebird/internal/usecase"
)

type promoteTrafficUseCase interface {
	Execute(ctx context.Context, in usecase.PromoteTrafficInput) (domain.Mock, error)
}

type promoteTrafficRequestDTO struct {
	Name       string `json:"name,omitempty"`
	TTLSeconds *int   `json:"ttl_seconds,omitempty"`
}

// PromoteTraffic handles POST /__lyrebird/traffic/{id}/promote
// (contracts/admin-rest.md, FR-012): turns a recorded interaction into a
// persistent mock reproducing it.
func PromoteTraffic(uc promoteTrafficUseCase) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req promoteTrafficRequestDTO
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeJSONError(w, http.StatusBadRequest, err)
			return
		}
		partition := httpmw.PartitionFromContext(r.Context())
		m, err := uc.Execute(r.Context(), usecase.PromoteTrafficInput{
			Partition: partition, TrafficID: r.PathValue("id"), Name: req.Name, TTLSeconds: req.TTLSeconds,
		})
		if err != nil {
			writeUseCaseError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, dto.MockToDTO(m))
	}
}
