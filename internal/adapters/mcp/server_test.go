package mcp

import (
	"context"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/brienze1/lyrebird/internal/domain"
	"github.com/brienze1/lyrebird/internal/usecase"
)

// callTool connects an in-memory client/server pair over srv and calls tool
// name with args, returning the raw result. mcp.AddTool registers anonymous
// closures directly against *sdkmcp.Server rather than exporting testable
// handler functions the way httpadmin does (CreateMock(uc) http.HandlerFunc
// etc.), so a real client/server round trip over NewInMemoryTransports is
// the only way to exercise a registered tool's handler, jsonschema
// validation, and error wrapping exactly as production does.
func callTool(t *testing.T, srv *sdkmcp.Server, name string, args any) *sdkmcp.CallToolResult {
	t.Helper()
	ctx := context.Background()
	t1, t2 := sdkmcp.NewInMemoryTransports()
	if _, err := srv.Connect(ctx, t1, nil); err != nil {
		t.Fatalf("server.Connect: %v", err)
	}
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-client", Version: "0.0.1"}, nil)
	cs, err := client.Connect(ctx, t2, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	defer cs.Close()
	result, err := cs.CallTool(ctx, &sdkmcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", name, err)
	}
	return result
}

// errText extracts a tool-error result's message text, failing the test if
// the result isn't actually an error.
func errText(t *testing.T, result *sdkmcp.CallToolResult) string {
	t.Helper()
	if !result.IsError {
		t.Fatalf("result.IsError = false, want true (structured content: %+v)", result.StructuredContent)
	}
	tc, ok := result.Content[0].(*sdkmcp.TextContent)
	if !ok {
		t.Fatalf("Content[0] = %T, want *sdkmcp.TextContent", result.Content[0])
	}
	return tc.Text
}

type fakeMockCRUD struct{}

func (fakeMockCRUD) Create(context.Context, usecase.MockInput) (domain.Mock, error) {
	return domain.Mock{}, nil
}
func (fakeMockCRUD) Get(context.Context, string, string) (domain.Mock, error) {
	return domain.Mock{}, nil
}
func (fakeMockCRUD) List(context.Context, string, string) ([]domain.Mock, error) { return nil, nil }
func (fakeMockCRUD) Update(context.Context, string, string, usecase.MockInput) (domain.Mock, error) {
	return domain.Mock{}, nil
}
func (fakeMockCRUD) Delete(context.Context, string, string) error { return nil }

type fakeReset struct{}

func (fakeReset) Execute(context.Context, usecase.ResetInput) (usecase.ResetOutput, error) {
	return usecase.ResetOutput{}, nil
}

type fakeMatchTest struct{}

func (fakeMatchTest) Execute(context.Context, string, usecase.MatchInput) (usecase.MatchTestOutput, error) {
	return usecase.MatchTestOutput{}, nil
}

type fakeSetUpstream struct{}

func (fakeSetUpstream) Execute(context.Context, domain.Upstream) error { return nil }

type fakeListUpstreams struct{}

func (fakeListUpstreams) Execute(context.Context, string) ([]domain.Upstream, error) { return nil, nil }

type fakeListTraffic struct{}

func (fakeListTraffic) Execute(context.Context, string, usecase.TrafficFilter) ([]domain.TrafficRecord, error) {
	return nil, nil
}

type fakeGetTraffic struct{}

func (fakeGetTraffic) Execute(context.Context, string, string) (domain.TrafficRecord, error) {
	return domain.TrafficRecord{}, nil
}

type fakeClearTraffic struct{}

func (fakeClearTraffic) Execute(context.Context, string) error { return nil }

type fakeMetrics struct{}

func (fakeMetrics) Execute(context.Context, usecase.MetricsInput) (usecase.MetricsOutput, error) {
	return usecase.MetricsOutput{}, nil
}

type fakePromoteTraffic struct{}

func (fakePromoteTraffic) Execute(context.Context, usecase.PromoteTrafficInput) (domain.Mock, error) {
	return domain.Mock{}, nil
}

func fakeDeps() Deps {
	return Deps{
		DefaultSpace:   "default",
		MockCRUD:       fakeMockCRUD{},
		Reset:          fakeReset{},
		MatchTest:      fakeMatchTest{},
		SetUpstream:    fakeSetUpstream{},
		ListUpstreams:  fakeListUpstreams{},
		ListTraffic:    fakeListTraffic{},
		GetTraffic:     fakeGetTraffic{},
		ClearTraffic:   fakeClearTraffic{},
		Metrics:        fakeMetrics{},
		PromoteTraffic: fakePromoteTraffic{},
	}
}

// TestNewRegistersEveryToolWithoutPanicking guards against mcp.AddTool's
// registration-time panic on a bad jsonschema tag or unsupported In/Out
// field type — a mistake here must fail `go test`, not surface only when a
// live server boots.
func TestNewRegistersEveryToolWithoutPanicking(t *testing.T) {
	srv := New(fakeDeps())
	if srv == nil {
		t.Fatal("New() returned nil")
	}
}
