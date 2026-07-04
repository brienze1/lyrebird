package httpadmin

import (
	"encoding/json"
	"net/http"

	"github.com/brienze1/lyrebird/internal/usecase"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

// writeUseCaseError maps a use-case error to an HTTP status via
// usecase.Explain (FR-020) and writes it as a JSON error body — the single
// place REST maps use-case errors onto status codes, so its wording can
// never drift from MCP's (constitution Principle II).
func writeUseCaseError(w http.ResponseWriter, err error) {
	explained := usecase.Explain(err)
	status := http.StatusInternalServerError
	switch explained.Kind {
	case usecase.KindValidation:
		status = http.StatusBadRequest
	case usecase.KindNotFound:
		status = http.StatusNotFound
	case usecase.KindConflict:
		status = http.StatusConflict
	}
	writeJSON(w, status, map[string]string{"error": explained.Message})
}
