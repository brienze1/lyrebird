package httpadmin

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/brienze1/lyrebird/internal/adapters/dto"
	"github.com/brienze1/lyrebird/internal/adapters/httpmw"
	"github.com/brienze1/lyrebird/internal/usecase"
)

type matchTester interface {
	Execute(ctx context.Context, partition string, in usecase.MatchInput) (usecase.MatchTestOutput, error)
}

// MatchTest handles POST /__lyrebird/match-test (contracts/admin-rest.md,
// FR-011): a dry-run reporting which mock would fire, every candidate's
// per-condition detail, and the resolved response — never forwarding
// anything onward.
func MatchTest(uc matchTester) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req dto.MatchTestRequestDTO
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, err)
			return
		}
		partition := httpmw.PartitionFromContext(r.Context())

		out, err := uc.Execute(r.Context(), partition, dto.MatchTestInputFromDTO(req))
		if err != nil {
			writeUseCaseError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, dto.MatchTestOutputToDTO(out))
	}
}
