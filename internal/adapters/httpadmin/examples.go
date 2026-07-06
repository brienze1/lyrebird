package httpadmin

import (
	"net/http"

	"github.com/brienze1/lyrebird/internal/adapters/examples"
	"github.com/brienze1/lyrebird/internal/domain"
)

// ListExamples handles GET /__lyrebird/examples (contracts/admin-rest.md).
// Query param: query (substring filter over id/title/provider/service).
func ListExamples(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, examples.List(r.URL.Query().Get("query")))
}

// GetExample handles GET /__lyrebird/examples/{id} (contracts/admin-rest.md).
func GetExample(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	recipe, ok := examples.Get(id)
	if !ok {
		writeUseCaseError(w, domain.ErrNotFound)
		return
	}
	writeJSON(w, http.StatusOK, recipe)
}
