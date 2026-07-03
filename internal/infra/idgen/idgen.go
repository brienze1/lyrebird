// Package idgen provides the production implementation of usecase.IDGen.
package idgen

import "github.com/google/uuid"

// UUID generates random (v4) UUID strings.
type UUID struct{}

// NewID returns a new random (v4) UUID string.
func (UUID) NewID() string { return uuid.NewString() }
