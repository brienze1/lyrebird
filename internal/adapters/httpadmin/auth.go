package httpadmin

import (
	"context"
	"encoding/json"
	"net/http"
)

type issueTokenUseCase interface {
	Execute(ctx context.Context, clientKey string) (string, error)
}

type issueTokenRequest struct {
	ClientKey string `json:"client_key"`
}

type issueTokenResponse struct {
	Token string `json:"token"`
}

// IssueToken handles POST /__lyrebird/auth/token (contracts/admin-rest.md);
// deliberately exempt from Auth middleware since it must be reachable pre-token (FR-031).
func IssueToken(uc issueTokenUseCase) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var d issueTokenRequest
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&d); err != nil {
			writeJSONError(w, http.StatusBadRequest, err)
			return
		}
		token, err := uc.Execute(r.Context(), d.ClientKey)
		if err != nil {
			writeUseCaseError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, issueTokenResponse{Token: token})
	}
}
