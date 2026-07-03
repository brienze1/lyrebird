// Package httpmw holds HTTP middleware shared by both the data-plane and
// control-plane listeners.
package httpmw

import (
	"context"
	"net/http"

	"github.com/brienze1/lyrebird/internal/domain"
)

// SpaceHeader is the request header a caller sets to select a partition.
const SpaceHeader = "X-Lyrebird-Space"

type partitionCtxKey struct{}

// Partition resolves the request's partition from the X-Lyrebird-Space
// header (falling back to defaultSpace when absent) and stores it in the
// request context, so both listeners share one resolution path (T014,
// FR-023).
func Partition(defaultSpace string) func(http.Handler) http.Handler {
	if defaultSpace == "" {
		defaultSpace = domain.DefaultPartitionID
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			space := r.Header.Get(SpaceHeader)
			if space == "" {
				space = defaultSpace
			}
			ctx := context.WithValue(r.Context(), partitionCtxKey{}, space)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// PartitionFromContext returns the partition resolved by Partition, or the
// default partition if none was resolved (e.g. in tests that skip the
// middleware).
func PartitionFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(partitionCtxKey{}).(string); ok && v != "" {
		return v
	}
	return domain.DefaultPartitionID
}
