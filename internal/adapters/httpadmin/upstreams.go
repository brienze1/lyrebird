package httpadmin

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/brienze1/lyrebird/internal/adapters/dto"
	"github.com/brienze1/lyrebird/internal/adapters/httpmw"
	"github.com/brienze1/lyrebird/internal/domain"
)

type setUpstreamUseCase interface {
	Execute(ctx context.Context, u domain.Upstream) error
}

type listUpstreamsUseCase interface {
	Execute(ctx context.Context, partition string) ([]domain.Upstream, error)
}

// ListUpstreams handles GET /__lyrebird/upstreams (contracts/admin-rest.md).
func ListUpstreams(uc listUpstreamsUseCase) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		partition := httpmw.PartitionFromContext(r.Context())
		list, err := uc.Execute(r.Context(), partition)
		if err != nil {
			writeUseCaseError(w, err)
			return
		}
		out := make([]dto.UpstreamDTO, len(list))
		for i, u := range list {
			out[i] = dto.UpstreamToDTO(u)
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// SetUpstream handles POST /__lyrebird/upstreams (contracts/admin-rest.md).
func SetUpstream(uc setUpstreamUseCase) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var d dto.UpstreamDTO
		if err := json.NewDecoder(r.Body).Decode(&d); err != nil {
			writeJSONError(w, http.StatusBadRequest, err)
			return
		}
		u := dto.UpstreamFromDTO(httpmw.PartitionFromContext(r.Context()), d)
		if err := uc.Execute(r.Context(), u); err != nil {
			writeUseCaseError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, d)
	}
}
