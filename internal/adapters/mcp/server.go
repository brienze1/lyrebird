// Package mcp implements Lyrebird's MCP control-plane adapter (T033):
// Streamable HTTP + stdio transports over the exact same use-cases Admin
// REST calls (internal/adapters/httpadmin), per constitution Principle II
// (MCP is the primary control plane, REST a thin twin — neither may
// duplicate the other's business logic).
package mcp

import (
	"context"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/brienze1/lyrebird/internal/domain"
	"github.com/brienze1/lyrebird/internal/usecase"
)

type mockCRUDPort interface {
	Create(ctx context.Context, in usecase.MockInput) (domain.Mock, error)
	Get(ctx context.Context, partition, id string) (domain.Mock, error)
	List(ctx context.Context, partition, group string) ([]domain.Mock, error)
	Update(ctx context.Context, partition, id string, in usecase.MockInput) (domain.Mock, error)
	Delete(ctx context.Context, partition, id string) error
}

type resetPort interface {
	Execute(ctx context.Context, in usecase.ResetInput) (usecase.ResetOutput, error)
}

type matchTestPort interface {
	Execute(ctx context.Context, partition string, in usecase.MatchInput) (usecase.MatchTestOutput, error)
}

type setUpstreamPort interface {
	Execute(ctx context.Context, u domain.Upstream) error
}

type listUpstreamsPort interface {
	Execute(ctx context.Context, partition string) ([]domain.Upstream, error)
}

type listTrafficPort interface {
	Execute(ctx context.Context, partition string, filter usecase.TrafficFilter) ([]domain.TrafficRecord, error)
}

type getTrafficPort interface {
	Execute(ctx context.Context, partition, id string) (domain.TrafficRecord, error)
}

type clearTrafficPort interface {
	Execute(ctx context.Context, partition string) error
}

type metricsPort interface {
	Execute(ctx context.Context, in usecase.MetricsInput) (usecase.MetricsOutput, error)
}

type promoteTrafficPort interface {
	Execute(ctx context.Context, in usecase.PromoteTrafficInput) (domain.Mock, error)
}

// Deps is every use-case (interface-shaped, matching httpadmin's own
// constructor-injection convention) a tool handler needs, collected into
// one struct because mcp.AddTool registers eagerly against one *Server at
// construction time — unlike httpadmin, there is no per-request handler
// factory to inject into.
type Deps struct {
	DefaultSpace string

	MockCRUD       mockCRUDPort
	Reset          resetPort
	MatchTest      matchTestPort
	SetUpstream    setUpstreamPort
	ListUpstreams  listUpstreamsPort
	ListTraffic    listTrafficPort
	GetTraffic     getTrafficPort
	ClearTraffic   clearTrafficPort
	Metrics        metricsPort
	PromoteTraffic promoteTrafficPort
}

// New builds one fully-registered MCP server — every tool in
// contracts/mcp-tools.md's M3 scope, wired over deps. Called once from
// bootstrap.Run (mounted over Streamable HTTP) and once from
// bootstrap.RunStdio (run against stdin/stdout) — both share this exact
// registration code, so tool schemas/behavior cannot drift between the two
// transports.
func New(deps Deps) *sdkmcp.Server {
	s := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "lyrebird", Version: "0.1.0"}, nil)
	registerMockTools(s, deps)
	registerUpstreamTools(s, deps)
	registerTrafficTools(s, deps)
	registerContentTools(s)
	return s
}
