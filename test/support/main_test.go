// Package support wires Lyrebird's BDD feature files into `go test ./...`
// via godog, so a single `go test` run covers unit tests and behavior
// scenarios alike — no separate BDD target to remember (T007).
package support

import (
	"testing"

	"github.com/cucumber/godog"
)

func TestFeatures(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "lyrebird",
		ScenarioInitializer: InitializeScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../features"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("non-zero status from godog: feature tests failed")
	}
}

// InitializeScenario registers every feature's step definitions against one
// shared appState per scenario, so features that all need a running
// Lyrebird instance (disposability.feature, spy_record.feature, ...) share
// one "Lyrebird boots" step rather than each registering their own
// (godog would treat duplicate patterns as an ambiguous match).
func InitializeScenario(ctx *godog.ScenarioContext) {
	s := &appState{}
	RegisterCoreAppSteps(ctx, s)
	RegisterDisposabilitySteps(ctx, s)
	RegisterSpySteps(ctx, s)
	RegisterMockSteps(ctx, s)
	RegisterMcpSteps(ctx, s)
	RegisterScriptingSteps(ctx, s)
	RegisterPartitionSteps(ctx, s)
	RegisterLifetimeSteps(ctx, s)
	RegisterAdvancedProxySteps(ctx, s)
	RegisterMITMSteps(ctx, s)
	RegisterAuthSteps(ctx, s)
	RegisterExamplesSteps(ctx, s)
}
