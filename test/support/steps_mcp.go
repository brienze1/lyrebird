package support

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cucumber/godog"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/brienze1/lyrebird/internal/usecase"
)

// mcpState drives the shared appState's real MCP-mounted control plane via a
// genuine sdkmcp.Client over Streamable HTTP — proving the actual
// bootstrap.Run wiring end-to-end, the same way steps_mock.go proves the
// real Admin REST wiring rather than bypassing it through the store.
type mcpState struct {
	s *appState

	client  *sdkmcp.Client
	session *sdkmcp.ClientSession

	lastResult *sdkmcp.CallToolResult
	lastErr    error
}

func (m *mcpState) anMCPClientIsConnectedToTheControlPlane(ctx context.Context) error {
	m.client = sdkmcp.NewClient(&sdkmcp.Implementation{Name: "lyrebird-bdd", Version: "0.0.0"}, nil)
	session, err := m.client.Connect(ctx, &sdkmcp.StreamableClientTransport{
		Endpoint: fmt.Sprintf("http://%s/mcp", m.s.app.ControlAddr()),
	}, nil)
	if err != nil {
		return fmt.Errorf("connect MCP client: %w", err)
	}
	m.session = session
	return nil
}

func (m *mcpState) callTool(ctx context.Context, name string, argsJSON string) error {
	var args map[string]any
	if strings.TrimSpace(argsJSON) != "" {
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return fmt.Errorf("parse tool arguments %q: %w", argsJSON, err)
		}
	}
	result, err := m.session.CallTool(ctx, &sdkmcp.CallToolParams{Name: name, Arguments: args})
	m.lastResult, m.lastErr = result, err
	return nil // outcome asserted by a later step, not here
}

func (m *mcpState) iCallTheMCPToolWithArguments(ctx context.Context, name, argsJSON string) error {
	return m.callTool(ctx, name, argsJSON)
}

// iPromoteTheLastRecordedTrafficIntoAMockNamed fetches the most recently
// recorded traffic id directly from the store (mirroring steps_spy.go's
// lastTraffic helper) and calls promote_traffic with it — a test
// convenience for finding "the traffic id from the request I just sent",
// which no step before this one has any reason to have captured.
func (m *mcpState) iPromoteTheLastRecordedTrafficIntoAMockNamed(ctx context.Context, name string) error {
	list, err := m.s.app.Store.ListTraffic(ctx, "default", usecase.TrafficFilter{})
	if err != nil {
		return fmt.Errorf("list traffic: %w", err)
	}
	if len(list) == 0 {
		return fmt.Errorf("no traffic recorded in partition %q", "default")
	}
	argsJSON, err := json.Marshal(map[string]any{"traffic_id": list[0].ID, "name": name})
	if err != nil {
		return fmt.Errorf("marshal promote_traffic arguments: %w", err)
	}
	return m.callTool(ctx, "promote_traffic", string(argsJSON))
}

func (m *mcpState) theMCPCallSucceeds() error {
	if m.lastErr != nil {
		return fmt.Errorf("MCP call transport error: %w", m.lastErr)
	}
	if m.lastResult == nil {
		return fmt.Errorf("no MCP call result recorded")
	}
	if m.lastResult.IsError {
		return fmt.Errorf("MCP call returned a tool error: %s", contentText(m.lastResult))
	}
	return nil
}

func (m *mcpState) theMCPCallFailsWithAnExplanatoryError() error {
	if m.lastErr != nil {
		return fmt.Errorf("MCP call transport error (want a tool-level error instead): %w", m.lastErr)
	}
	if m.lastResult == nil {
		return fmt.Errorf("no MCP call result recorded")
	}
	if !m.lastResult.IsError {
		return fmt.Errorf("MCP call succeeded, want a tool error")
	}
	text := contentText(m.lastResult)
	if text == "" {
		return fmt.Errorf("MCP call errored with no explanatory text content")
	}
	return nil
}

func (m *mcpState) theMCPResultTextContains(want string) error {
	text := contentText(m.lastResult)
	if !strings.Contains(text, want) {
		return fmt.Errorf("MCP result text = %q, want it to contain %q", text, want)
	}
	return nil
}

func (m *mcpState) theMCPStructuredResultFieldEquals(path, want string) error {
	if m.lastResult == nil || m.lastResult.StructuredContent == nil {
		return fmt.Errorf("MCP result has no structured content")
	}
	raw, err := json.Marshal(m.lastResult.StructuredContent)
	if err != nil {
		return fmt.Errorf("marshal structured content: %w", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("unmarshal structured content: %w", err)
	}
	got, err := navigateField(doc, path)
	if err != nil {
		return err
	}
	gotStr := fmt.Sprintf("%v", got)
	if gotStr != want {
		return fmt.Errorf("structured field %q = %q, want %q", path, gotStr, want)
	}
	return nil
}

func navigateField(doc map[string]any, path string) (any, error) {
	parts := strings.Split(path, ".")
	var cur any = doc
	for i, p := range parts {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("structured field %q: %q is not an object at %q", path, strings.Join(parts[:i], "."), p)
		}
		v, ok := m[p]
		if !ok {
			return nil, fmt.Errorf("structured field %q: no key %q (available: %v)", path, p, mapKeys(m))
		}
		cur = v
	}
	return cur, nil
}

func mapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func contentText(result *sdkmcp.CallToolResult) string {
	if result == nil {
		return ""
	}
	var sb strings.Builder
	for _, c := range result.Content {
		if tc, ok := c.(*sdkmcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}

// RegisterMcpSteps wires mcp_control.feature's steps against the shared
// appState s.
func RegisterMcpSteps(sc *godog.ScenarioContext, s *appState) {
	m := &mcpState{s: s}

	sc.Step(`^an MCP client is connected to the control plane$`, m.anMCPClientIsConnectedToTheControlPlane)
	sc.Step(`^I call the MCP tool "([^"]*)" with arguments '(.*)'$`, m.iCallTheMCPToolWithArguments)
	sc.Step(`^I promote the last recorded traffic into a mock named "([^"]*)"$`, m.iPromoteTheLastRecordedTrafficIntoAMockNamed)
	sc.Step(`^the MCP call succeeds$`, m.theMCPCallSucceeds)
	sc.Step(`^the MCP call fails with an explanatory error$`, m.theMCPCallFailsWithAnExplanatoryError)
	sc.Step(`^the MCP result text contains "([^"]*)"$`, m.theMCPResultTextContains)
	sc.Step(`^the MCP structured result field "([^"]*)" equals "([^"]*)"$`, m.theMCPStructuredResultFieldEquals)

	// Closing the client session here — via appState's preShutdownCleanup,
	// run before app.Shutdown — matters: Shutdown's graceful-drain otherwise
	// waits out its own timeout for the session's still-open Streamable HTTP
	// long-poll connection on every scenario that connects an MCP client.
	s.preShutdownCleanup = append(s.preShutdownCleanup, func() {
		if m.session != nil {
			_ = m.session.Close()
		}
	})
}
