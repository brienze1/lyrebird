package support

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/cucumber/godog"

	"github.com/brienze1/lyrebird/internal/domain"
)

// lifetimeState carries lifetimes.feature's own fixtures on top of the
// shared appState.
type lifetimeState struct {
	s *appState
}

func (l *lifetimeState) anEphemeralMockNamedMatchingPathThatRespondsWithBodyAndTTLSeconds(
	ctx context.Context, name, method, path string, status int, body string, ttlSeconds int,
) error {
	return createMockDTO(ctx, l.s, mockDTO{
		Name: name, Match: matchDTO{Method: method, Path: path},
		Action: actionDTO{Respond: &respondDTO{Status: status, Body: body}}, TTLSeconds: &ttlSeconds,
	})
}

func (l *lifetimeState) iWait(d string) error {
	parsed, err := time.ParseDuration(d)
	if err != nil {
		return fmt.Errorf("parse wait duration %q: %w", d, err)
	}
	time.Sleep(parsed)
	return nil
}

func (l *lifetimeState) iResetTheSpace(ctx context.Context, space string) error {
	return l.doReset(ctx, space, false)
}

func (l *lifetimeState) iResetTheSpaceAndClearTraffic(ctx context.Context, space string) error {
	return l.doReset(ctx, space, true)
}

func (l *lifetimeState) doReset(ctx context.Context, space string, clearTraffic bool) error {
	body := "{}"
	if clearTraffic {
		body = `{"clear_traffic":true}`
	}
	url := fmt.Sprintf("http://%s/__lyrebird/reset", l.s.app.ControlAddr())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader([]byte(body)))
	if err != nil {
		return fmt.Errorf("build reset request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lyrebird-Space", space)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("POST /__lyrebird/reset: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("POST /__lyrebird/reset status = %d, want 200", resp.StatusCode)
	}
	return nil
}

// trafficOlderThanTheRetentionWindowWasRecordedInSpace fabricates a traffic
// record dated well in the past directly through the store — a real request
// can't be time-traveled, so this bypasses the data plane the same way
// steps_disposability.go's fixture helpers bypass MockRepo for pre-existing
// on-disk state.
func (l *lifetimeState) trafficOlderThanTheRetentionWindowWasRecordedInSpace(ctx context.Context, space string) error {
	return l.s.app.Store.AppendTraffic(ctx, domain.TrafficRecord{
		ID: "stale-traffic", Partition: space, Timestamp: time.Now().Add(-24 * time.Hour),
		Method: "GET", Host: "example.local", Path: "/old", Status: 200, Decision: domain.DecisionProxied,
	})
}

// RegisterLifetimeSteps wires lifetimes.feature's steps against the shared
// appState s. Mock creation reuses createMockDTO from steps_mock.go;
// request-sending, response, and traffic assertions are reused verbatim
// from steps_spy.go/steps_partitions.go.
func RegisterLifetimeSteps(sc *godog.ScenarioContext, s *appState) {
	l := &lifetimeState{s: s}

	sc.Step(`^an ephemeral mock named "([^"]*)" matching (GET|POST|PUT|PATCH|DELETE) path "([^"]*)" that responds (\d+) with body "([^"]*)" and ttl_seconds (\d+)$`,
		l.anEphemeralMockNamedMatchingPathThatRespondsWithBodyAndTTLSeconds)
	sc.Step(`^I wait "([^"]*)"$`, l.iWait)
	sc.Step(`^I reset the space "([^"]*)"$`, l.iResetTheSpace)
	sc.Step(`^I reset the space "([^"]*)" and clear traffic$`, l.iResetTheSpaceAndClearTraffic)
	sc.Step(`^traffic older than the retention window was recorded in space "([^"]*)"$`,
		l.trafficOlderThanTheRetentionWindowWasRecordedInSpace)
}
