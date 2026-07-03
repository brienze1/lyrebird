package httpadmin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/brienze1/lyrebird/internal/adapters/httpmw"
	"github.com/brienze1/lyrebird/internal/domain"
)

type upstreamDTO struct {
	MatchHost     string `json:"match_host"`
	TargetURL     string `json:"target_url"`
	TLSSkipVerify bool   `json:"tls_skip_verify"`
}

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
			writeJSONError(w, http.StatusInternalServerError, err)
			return
		}
		out := make([]upstreamDTO, len(list))
		for i, u := range list {
			out[i] = upstreamDTO{MatchHost: u.MatchHost, TargetURL: u.TargetURL, TLSSkipVerify: u.TLSSkipVerify}
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// SetUpstream handles POST /__lyrebird/upstreams (contracts/admin-rest.md).
func SetUpstream(uc setUpstreamUseCase) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var dto upstreamDTO
		if err := json.NewDecoder(r.Body).Decode(&dto); err != nil {
			writeJSONError(w, http.StatusBadRequest, err)
			return
		}
		u := domain.Upstream{
			Partition: httpmw.PartitionFromContext(r.Context()),
			MatchHost: dto.MatchHost, TargetURL: dto.TargetURL, TLSSkipVerify: dto.TLSSkipVerify,
		}
		if err := uc.Execute(r.Context(), u); err != nil {
			if errors.Is(err, domain.ErrInvalidUpstream) {
				writeJSONError(w, http.StatusBadRequest, err)
				return
			}
			writeJSONError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, dto)
	}
}
