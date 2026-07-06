package httpadmin

import (
	"context"
	"net/http"

	"gopkg.in/yaml.v3"

	"github.com/brienze1/lyrebird/internal/adapters/dto"
	"github.com/brienze1/lyrebird/internal/adapters/httpmw"
	"github.com/brienze1/lyrebird/internal/domain"
	"github.com/brienze1/lyrebird/internal/usecase"
)

type exportSeedsUseCase interface {
	Execute(ctx context.Context, partition string) (usecase.ExportBundle, error)
}

type importSeedsUseCase interface {
	Execute(ctx context.Context, partition string, upstreams []domain.Upstream, mocks []usecase.MockInput) (usecase.ImportResult, error)
}

// ExportConfig handles GET /__lyrebird/export (contracts/admin-rest.md).
func ExportConfig(uc exportSeedsUseCase) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		partition := httpmw.PartitionFromContext(r.Context())
		bundle, err := uc.Execute(r.Context(), partition)
		if err != nil {
			writeUseCaseError(w, err)
			return
		}
		raw, err := yaml.Marshal(dto.SeedBundleToDTO(bundle))
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err)
			return
		}
		w.Header().Set("Content-Type", "application/x-yaml")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(raw)
	}
}

// ImportConfig handles POST /__lyrebird/import (contracts/admin-rest.md).
func ImportConfig(uc importSeedsUseCase) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		partition := httpmw.PartitionFromContext(r.Context())

		var bundle dto.SeedBundleDTO
		dec := yaml.NewDecoder(r.Body)
		dec.KnownFields(true)
		if err := dec.Decode(&bundle); err != nil {
			writeJSONError(w, http.StatusBadRequest, err)
			return
		}

		upstreams, mocks, err := dto.SeedBundleFromDTO(partition, bundle)
		if err != nil {
			writeUseCaseError(w, err)
			return
		}

		result, err := uc.Execute(r.Context(), partition, upstreams, mocks)
		if err != nil {
			writeUseCaseError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}
