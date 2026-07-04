package mcp

import (
	"context"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// RunStdio runs srv against stdin/stdout until the transport closes or ctx
// is done — the local-agent mode (contracts/mcp-tools.md: "stdio (local)"),
// mutually exclusive with running the HTTP daemon in the same process
// invocation (see bootstrap.RunStdio).
func RunStdio(ctx context.Context, srv *sdkmcp.Server) error {
	return srv.Run(ctx, &sdkmcp.StdioTransport{})
}
