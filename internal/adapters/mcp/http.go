package mcp

import (
	"net/http"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Handler wraps srv as a Streamable HTTP handler, mountable directly on the
// control-plane mux. getServer returns the same long-lived server for every
// session — this process runs one Lyrebird instance, not a multi-tenant
// server farm.
func Handler(srv *sdkmcp.Server) http.Handler {
	return sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server { return srv }, nil)
}
