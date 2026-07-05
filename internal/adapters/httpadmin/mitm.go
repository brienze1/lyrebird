package httpadmin

import (
	"context"
	"net/http"
)

type getMITMCACertUseCase interface {
	Execute(ctx context.Context) []byte
}

// GetMITMCACert handles GET /__lyrebird/mitm/ca-cert (contracts/admin-rest.md)
// — served as a raw PEM body (application/x-pem-file, not JSON), mirroring
// health.go's plain non-JSON responses, so a client can pipe the response
// straight into its trust store. Only registered when MITM is enabled
// (bootstrap.Run); contains no key material.
func GetMITMCACert(uc getMITMCACertUseCase) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pem := uc.Execute(r.Context())
		w.Header().Set("Content-Type", "application/x-pem-file")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(pem)
	}
}
