package support

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/cucumber/godog"
)

// mockState creates mocks via a real POST /__lyrebird/mocks call against the
// shared appState's control plane — exercising the actual Admin REST path
// (T030) rather than bypassing it through the store, so mock_override.feature
// proves T026-T031 work together, not just the handler in isolation.
type mockState struct {
	s *appState

	lastAdminStatus int
	lastMatchTest   matchTestResponseDTO
}

// These DTOs mirror httpadmin's wire schema exactly (Matcher fields
// flattened, no separate "matcher" wrapper key; ActionKind inferred from
// which of respond/proxy/fault is present, not a "kind" field) — see
// contracts/seed-config.md and internal/adapters/httpadmin/mocks.go.
type matcherDTO struct {
	Equals *string `json:"equals,omitempty"`
}

type bodyMatcherDTO struct {
	JSONPath string  `json:"jsonpath"`
	Equals   *string `json:"equals,omitempty"`
}

type matchDTO struct {
	Method  string                `json:"method,omitempty"`
	Path    string                `json:"path,omitempty"`
	Headers map[string]matcherDTO `json:"headers,omitempty"`
	Body    []bodyMatcherDTO      `json:"body,omitempty"`
}

type respondDTO struct {
	Status int    `json:"status"`
	Body   string `json:"body"`
}

type actionDTO struct {
	Respond *respondDTO `json:"respond,omitempty"`
}

type mockDTO struct {
	Name     string    `json:"name"`
	Priority int       `json:"priority"`
	Match    matchDTO  `json:"match"`
	Action   actionDTO `json:"action"`
}

type matchTestResponseDTO struct {
	Winner *mockDTO `json:"winner,omitempty"`
	Status int      `json:"status,omitempty"`
	Body   string   `json:"body,omitempty"`
}

func (m *mockState) createMock(ctx context.Context, dto mockDTO) error {
	raw, err := json.Marshal(dto)
	if err != nil {
		return fmt.Errorf("marshal mock dto: %w", err)
	}
	url := fmt.Sprintf("http://%s/__lyrebird/mocks", m.s.app.ControlAddr())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("build create-mock request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("POST /__lyrebird/mocks: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("POST /__lyrebird/mocks status = %d, want 200/201", resp.StatusCode)
	}
	return nil
}

func (m *mockState) aMockNamedMatchingPathThatRespondsWithBody(ctx context.Context, name, method, path string, status int, body string) error {
	return m.createMock(ctx, mockDTO{
		Name: name, Match: matchDTO{Method: method, Path: path},
		Action: actionDTO{Respond: &respondDTO{Status: status, Body: body}},
	})
}

func (m *mockState) aMockNamedWithPriorityMatchingPathThatRespondsWithBody(ctx context.Context, name string, priority int, method, path string, status int, body string) error {
	return m.createMock(ctx, mockDTO{
		Name: name, Priority: priority, Match: matchDTO{Method: method, Path: path},
		Action: actionDTO{Respond: &respondDTO{Status: status, Body: body}},
	})
}

func (m *mockState) aMockNamedMatchingPathWithHeaderEqualsThatRespondsWithBody(
	ctx context.Context, name, method, path, header, value string, status int, body string,
) error {
	return m.createMock(ctx, mockDTO{
		Name: name,
		Match: matchDTO{
			Method:  method,
			Path:    path,
			Headers: map[string]matcherDTO{header: {Equals: &value}},
		},
		Action: actionDTO{Respond: &respondDTO{Status: status, Body: body}},
	})
}

func (m *mockState) aMockNamedMatchingPathWithBodyPathEqualsThatRespondsWithBody(
	ctx context.Context, name, method, path, bodyPath, value string, status int, respBody string,
) error {
	return m.createMock(ctx, mockDTO{
		Name: name,
		Match: matchDTO{
			Method: method,
			Path:   path,
			Body:   []bodyMatcherDTO{{JSONPath: bodyPath, Equals: &value}},
		},
		Action: actionDTO{Respond: &respondDTO{Status: status, Body: respBody}},
	})
}

// aSeedFileDeclaresAMockNamedInPartitionMatchingPathThatRespondsWithBody
// writes a seed YAML fixture directly (rather than via Admin REST — seeded
// mocks are loaded from mounted config, never created at runtime) so this
// step MUST run before "Lyrebird boots" in a scenario.
func (m *mockState) aSeedFileDeclaresAMockNamedInPartitionMatchingPathThatRespondsWithBody(
	name, partition, method, path string, status int, body string,
) error {
	content := fmt.Sprintf(
		"space: %s\nmocks:\n  - name: %s\n    match:\n      method: %s\n      path: %s\n    action:\n      respond:\n        status: %d\n        body: %q\n",
		partition, name, method, path, status, body,
	)
	return os.WriteFile(filepath.Join(m.s.seedDir, "mock_override_seed.yaml"), []byte(content), 0o600)
}

func (m *mockState) iAttemptToUpdateTheMockInPartitionViaTheControlPlane(ctx context.Context, name, partition string) error {
	dto := mockDTO{Name: name, Action: actionDTO{Respond: &respondDTO{Status: 200, Body: "irrelevant"}}}
	raw, err := json.Marshal(dto)
	if err != nil {
		return fmt.Errorf("marshal mock dto: %w", err)
	}
	url := fmt.Sprintf("http://%s/__lyrebird/mocks/%s", m.s.app.ControlAddr(), name)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("build update-mock request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lyrebird-Space", partition)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("PUT /__lyrebird/mocks/%s: %w", name, err)
	}
	defer func() { _ = resp.Body.Close() }()
	m.lastAdminStatus = resp.StatusCode
	return nil
}

func (m *mockState) iAttemptToDeleteTheMockInPartitionViaTheControlPlane(ctx context.Context, name, partition string) error {
	url := fmt.Sprintf("http://%s/__lyrebird/mocks/%s", m.s.app.ControlAddr(), name)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return fmt.Errorf("build delete-mock request: %w", err)
	}
	req.Header.Set("X-Lyrebird-Space", partition)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("DELETE /__lyrebird/mocks/%s: %w", name, err)
	}
	defer func() { _ = resp.Body.Close() }()
	m.lastAdminStatus = resp.StatusCode
	return nil
}

func (m *mockState) theControlPlaneRespondsWithStatus(want int) error {
	if m.lastAdminStatus != want {
		return fmt.Errorf("control plane responded with status %d, want %d", m.lastAdminStatus, want)
	}
	return nil
}

func (m *mockState) iRequestAMatchTestForPath(ctx context.Context, method, path string) error {
	raw, err := json.Marshal(map[string]string{"method": method, "path": path})
	if err != nil {
		return fmt.Errorf("marshal match-test request: %w", err)
	}
	url := fmt.Sprintf("http://%s/__lyrebird/match-test", m.s.app.ControlAddr())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("build match-test request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("POST /__lyrebird/match-test: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("POST /__lyrebird/match-test status = %d, want 200", resp.StatusCode)
	}
	var out matchTestResponseDTO
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return fmt.Errorf("decode match-test response: %w", err)
	}
	m.lastMatchTest = out
	return nil
}

func (m *mockState) theMatchTestWinnerIsWithStatusAndBody(name string, status int, body string) error {
	if m.lastMatchTest.Winner == nil {
		return fmt.Errorf("match-test winner = nil, want %q", name)
	}
	if m.lastMatchTest.Winner.Name != name {
		return fmt.Errorf("match-test winner = %q, want %q", m.lastMatchTest.Winner.Name, name)
	}
	if m.lastMatchTest.Status != status || m.lastMatchTest.Body != body {
		return fmt.Errorf("match-test resolved status/body = %d/%q, want %d/%q", m.lastMatchTest.Status, m.lastMatchTest.Body, status, body)
	}
	return nil
}

func (m *mockState) theMatchTestHasNoWinner() error {
	if m.lastMatchTest.Winner != nil {
		return fmt.Errorf("match-test winner = %+v, want nil", m.lastMatchTest.Winner)
	}
	return nil
}

// RegisterMockSteps wires mock_override.feature's mock-creation steps against
// the shared appState s. Request-sending and response/traffic assertions are
// deliberately NOT redeclared here — they reuse steps_spy.go's existing
// patterns (registered by RegisterSpySteps against the same appState), since
// godog matches step patterns across every file registered into one
// ScenarioContext.
func RegisterMockSteps(sc *godog.ScenarioContext, s *appState) {
	m := &mockState{s: s}

	sc.Step(`^a mock named "([^"]*)" matching (GET|POST|PUT|PATCH|DELETE) path "([^"]*)" that responds (\d+) with body "([^"]*)"$`,
		m.aMockNamedMatchingPathThatRespondsWithBody)
	sc.Step(`^a mock named "([^"]*)" with priority (\d+) matching (GET|POST|PUT|PATCH|DELETE) path "([^"]*)" that responds (\d+) with body "([^"]*)"$`,
		m.aMockNamedWithPriorityMatchingPathThatRespondsWithBody)
	sc.Step(`^a mock named "([^"]*)" matching (GET|POST|PUT|PATCH|DELETE) path "([^"]*)" with header "([^"]*)" equals "([^"]*)" that responds (\d+) with body "([^"]*)"$`,
		m.aMockNamedMatchingPathWithHeaderEqualsThatRespondsWithBody)
	sc.Step(`^a mock named "([^"]*)" matching (GET|POST|PUT|PATCH|DELETE) path "([^"]*)" with body path "([^"]*)" equals "([^"]*)" that responds (\d+) with body "([^"]*)"$`,
		m.aMockNamedMatchingPathWithBodyPathEqualsThatRespondsWithBody)

	sc.Step(`^a seed file declares a mock named "([^"]*)" in partition "([^"]*)" matching (GET|POST|PUT|PATCH|DELETE) path "([^"]*)" that responds (\d+) with body "([^"]*)"$`,
		m.aSeedFileDeclaresAMockNamedInPartitionMatchingPathThatRespondsWithBody)
	sc.Step(`^I attempt to update the mock "([^"]*)" in partition "([^"]*)" via the control plane$`,
		m.iAttemptToUpdateTheMockInPartitionViaTheControlPlane)
	sc.Step(`^I attempt to delete the mock "([^"]*)" in partition "([^"]*)" via the control plane$`,
		m.iAttemptToDeleteTheMockInPartitionViaTheControlPlane)
	sc.Step(`^the control plane responds with status (\d+)$`, m.theControlPlaneRespondsWithStatus)

	sc.Step(`^I request a match test for (GET|POST|PUT|PATCH|DELETE) path "([^"]*)"$`, m.iRequestAMatchTestForPath)
	sc.Step(`^the match-test winner is "([^"]*)" with status (\d+) and body "([^"]*)"$`, m.theMatchTestWinnerIsWithStatusAndBody)
	sc.Step(`^the match-test has no winner$`, m.theMatchTestHasNoWinner)
}
