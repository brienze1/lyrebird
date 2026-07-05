package httpmw

import (
	"net/http"
	"strings"
)

// tokenVerifier is the subset of *auth.Issuer's behavior Auth depends on —
// this package has zero compile-time dependency on internal/infra/auth.
type tokenVerifier interface {
	Verify(token string) error
}

// Auth enforces "Authorization: Bearer <token>" on every request except
// exemptPaths; any failure reason produces the identical generic 401 (FR-033).
func Auth(verifier tokenVerifier, exemptPaths ...string) func(http.Handler) http.Handler {
	exempt := make(map[string]bool, len(exemptPaths))
	for _, p := range exemptPaths {
		exempt[p] = true
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if exempt[r.URL.Path] {
				next.ServeHTTP(w, r)
				return
			}

			const prefix = "Bearer "
			authz := r.Header.Get("Authorization")
			if !strings.HasPrefix(authz, prefix) {
				writeUnauthorized(w)
				return
			}
			token := strings.TrimPrefix(authz, prefix)
			if err := verifier.Verify(token); err != nil {
				writeUnauthorized(w)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func writeUnauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"error":"missing or invalid bearer token"}`))
}
