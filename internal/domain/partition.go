package domain

import "time"

// DefaultPartitionID is the reserved partition used when a request carries no
// X-Lyrebird-Space header. It can never be deleted.
const DefaultPartitionID = "default"

// Partition is an isolation boundary owning mocks, traffic, and upstream
// configuration for one agent/session/tenant.
type Partition struct {
	ID          string
	CreatedAt   time.Time
	Description string
}
