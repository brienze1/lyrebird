package httpadmin

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/brienze1/lyrebird/internal/adapters/dto"
	"github.com/brienze1/lyrebird/internal/domain"
)

type createSpaceUseCase interface {
	Execute(ctx context.Context, p domain.Partition) (domain.Partition, error)
}

type listSpacesUseCase interface {
	Execute(ctx context.Context) ([]domain.Partition, error)
}

type deleteSpaceUseCase interface {
	Execute(ctx context.Context, id string) error
}

// ListSpaces handles GET /__lyrebird/spaces (contracts/admin-rest.md).
func ListSpaces(uc listSpacesUseCase) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		list, err := uc.Execute(r.Context())
		if err != nil {
			writeUseCaseError(w, err)
			return
		}
		out := make([]dto.PartitionDTO, len(list))
		for i, p := range list {
			out[i] = dto.PartitionToDTO(p)
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// CreateSpace handles POST /__lyrebird/spaces (contracts/admin-rest.md).
func CreateSpace(uc createSpaceUseCase) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var d dto.PartitionDTO
		if err := json.NewDecoder(r.Body).Decode(&d); err != nil {
			writeJSONError(w, http.StatusBadRequest, err)
			return
		}
		p, err := uc.Execute(r.Context(), dto.PartitionFromDTO(d))
		if err != nil {
			writeUseCaseError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, dto.PartitionToDTO(p))
	}
}

// DeleteSpace handles DELETE /__lyrebird/spaces/{id} (contracts/admin-rest.md).
func DeleteSpace(uc deleteSpaceUseCase) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := uc.Execute(r.Context(), r.PathValue("id")); err != nil {
			writeUseCaseError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
