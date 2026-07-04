package support

import (
	"context"

	"github.com/cucumber/godog"
)

// scriptingState creates scripted mocks via the same real POST
// /__lyrebird/mocks call steps_mock.go's mockState uses — reusing its
// createMock helper and mockDTO (extended with a Script field) rather than
// introducing a second wire-format representation.
type scriptingState struct{ s *appState }

func (t *scriptingState) aMockNamedMatchingPathWithScriptRespondSrcThatResponds(
	ctx context.Context, name, method, path, respondSrc string, status int,
) error {
	m := &mockState{s: t.s}
	return m.createMock(ctx, mockDTO{
		Name:   name,
		Match:  matchDTO{Method: method, Path: path},
		Script: &scriptDTO{RespondSrc: respondSrc},
		Action: actionDTO{Respond: &respondDTO{Status: status}},
	})
}

// RegisterScriptingSteps wires scripting.feature's mock-creation step
// against the shared appState s. Request-sending and response/traffic
// assertions are reused verbatim from steps_spy.go/steps_mock.go.
func RegisterScriptingSteps(sc *godog.ScenarioContext, s *appState) {
	t := &scriptingState{s: s}

	sc.Step(`^a mock named "([^"]*)" matching (GET|POST|PUT|PATCH|DELETE) path "([^"]*)" with script\.respond_src "([^"]*)" that responds (\d+)$`,
		t.aMockNamedMatchingPathWithScriptRespondSrcThatResponds)
}
