//go:build !race

package bootstrap_test

// raceEnabled is false in a normal (non -race) build; see race_enabled_test.go.
const raceEnabled = false
