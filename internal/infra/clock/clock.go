// Package clock provides the production implementation of usecase.Clock.
package clock

import "time"

// System is the real wall-clock implementation of usecase.Clock.
type System struct{}

// Now returns the current wall-clock time.
func (System) Now() time.Time { return time.Now() }
