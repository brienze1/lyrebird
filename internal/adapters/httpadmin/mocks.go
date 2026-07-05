package httpadmin

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/brienze1/lyrebird/internal/adapters/dto"
	"github.com/brienze1/lyrebird/internal/adapters/httpmw"
	"github.com/brienze1/lyrebird/internal/domain"
	"github.com/brienze1/lyrebird/internal/usecase"
)

type mockCreator interface {
	Create(ctx context.Context, in usecase.MockInput) (domain.Mock, error)
}

type mockGetter interface {
	Get(ctx context.Context, partition, id string) (domain.Mock, error)
}

type mockLister interface {
	List(ctx context.Context, partition, group string) ([]domain.Mock, error)
}

type mockUpdater interface {
	Update(ctx context.Context, partition, id string, in usecase.MockInput) (domain.Mock, error)
}

type mockDeleter interface {
	Delete(ctx context.Context, partition, id string) error
}

// ListMocks handles GET /__lyrebird/mocks (contracts/admin-rest.md).
// Query params: group (optional).
func ListMocks(uc mockLister) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		partition := httpmw.PartitionFromContext(r.Context())
		list, err := uc.List(r.Context(), partition, r.URL.Query().Get("group"))
		if err != nil {
			writeUseCaseError(w, err)
			return
		}
		out := make([]dto.MockDTO, len(list))
		for i, m := range list {
			out[i] = dto.MockToDTO(m)
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// CreateMock handles POST /__lyrebird/mocks (contracts/admin-rest.md).
func CreateMock(uc mockCreator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var d dto.MockDTO
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&d); err != nil {
			writeJSONError(w, http.StatusBadRequest, err)
			return
		}
		in, err := dto.MockInputFromDTO(httpmw.PartitionFromContext(r.Context()), d)
		if err != nil {
			writeUseCaseError(w, err)
			return
		}
		m, err := uc.Create(r.Context(), in)
		if err != nil {
			writeUseCaseError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, dto.MockToDTO(m))
	}
}

// GetMock handles GET /__lyrebird/mocks/{id} (contracts/admin-rest.md).
func GetMock(uc mockGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		partition := httpmw.PartitionFromContext(r.Context())
		m, err := uc.Get(r.Context(), partition, r.PathValue("id"))
		if err != nil {
			writeUseCaseError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, dto.MockToDTO(m))
	}
}

// UpdateMock handles PUT /__lyrebird/mocks/{id} (contracts/admin-rest.md).
func UpdateMock(uc mockUpdater) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var d dto.MockDTO
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&d); err != nil {
			writeJSONError(w, http.StatusBadRequest, err)
			return
		}
		partition := httpmw.PartitionFromContext(r.Context())
		in, err := dto.MockInputFromDTO(partition, d)
		if err != nil {
			writeUseCaseError(w, err)
			return
		}
		m, err := uc.Update(r.Context(), partition, r.PathValue("id"), in)
		if err != nil {
			writeUseCaseError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, dto.MockToDTO(m))
	}
}

// DeleteMock handles DELETE /__lyrebird/mocks/{id} (contracts/admin-rest.md).
func DeleteMock(uc mockDeleter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		partition := httpmw.PartitionFromContext(r.Context())
		if err := uc.Delete(r.Context(), partition, r.PathValue("id")); err != nil {
			writeUseCaseError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
