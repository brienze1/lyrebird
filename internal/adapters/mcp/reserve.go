// Package mcp will host the MCP control-plane adapter (the official Go SDK,
// Streamable HTTP + stdio) starting at M3 (specs/001-lyrebird/tasks.md). The
// blank import below keeps the dependency pinned in go.mod/go.sum so an
// intermediate `go mod tidy` between now and M3 cannot silently drop it.
package mcp

import (
	// Blank-imported to keep this dependency pinned in go.mod/go.sum until
	// the M3 control-plane adapter actually uses it — see the package doc above.
	_ "github.com/modelcontextprotocol/go-sdk/mcp"
)
