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

// InitializeScenario registers every feature's step definitions. Each story
// adds its own Register*Steps call here as it lands.
func InitializeScenario(ctx *godog.ScenarioContext) {
	RegisterDisposabilitySteps(ctx)
}
