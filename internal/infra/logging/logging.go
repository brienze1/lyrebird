// Package logging provides Lyrebird's structured logger. It exists as a
// single choke point specifically so that no code path can accidentally log
// secret material (tokens, keys, client secrets) — constitution Principle V,
// FR-033. Callers must never pass raw key/token values as log attributes;
// pass a KeySource or similar safe descriptor instead.
package logging

import (
	"log/slog"
	"os"
)

// New returns a JSON structured logger writing to stderr.
func New() *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
}
