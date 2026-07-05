package httpadmin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/brienze1/lyrebird/internal/adapters/dto"
	"github.com/brienze1/lyrebird/internal/adapters/httpmw"
	"github.com/brienze1/lyrebird/internal/usecase"
)

type resetUseCase interface {
	Execute(ctx context.Context, in usecase.ResetInput) (usecase.ResetOutput, error)
}

type resetRequestDTO struct {
	ClearTraffic bool `json:"clear_traffic,omitempty"`
}

// Reset handles POST /__lyrebird/reset (contracts/admin-rest.md, FR-028):
// removes ephemeral mocks (and, per request, recorded traffic) while
// preserving seeded mocks.
func Reset(uc resetUseCase) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req resetRequestDTO
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeJSONError(w, http.StatusBadRequest, err)
			return
		}
		partition := httpmw.PartitionFromContext(r.Context())
		out, err := uc.Execute(r.Context(), usecase.ResetInput{Partition: partition, ClearTraffic: req.ClearTraffic})
		if err != nil {
			writeUseCaseError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, dto.ResetResultToDTO(out))
	}
}
