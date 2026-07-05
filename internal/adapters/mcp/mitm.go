package mcp

import (
	"context"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// GetMITMCACertOut carries get_mitm_ca_cert's PEM as both raw text content
// (an agent can pipe it straight into a trust-store file) and structured
// content, mirroring content.go's GuideOut pattern. Contains no key
// material.
type GetMITMCACertOut struct {
	PEM string `json:"pem"`
}

// registerMITMTools registers get_mitm_ca_cert only when deps.GetMITMCACert
// is set (i.e. MITM is enabled) — Deps' one deliberately-optional field, so
// this tool simply doesn't exist when the feature is off (constitution
// Principle V: a security-adjacent feature's surface only appears once
// explicitly enabled).
func registerMITMTools(s *sdkmcp.Server, deps Deps) {
	if deps.GetMITMCACert == nil {
		return
	}
	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name: "get_mitm_ca_cert",
		Description: "Fetch Lyrebird's MITM CA certificate (PEM) so an HTTP client can trust it before being " +
			"routed through the transparent forward-proxy/MITM data-plane path. Example: {}",
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, _ struct{}) (*sdkmcp.CallToolResult, GetMITMCACertOut, error) {
		pem := string(deps.GetMITMCACert.Execute(ctx))
		return &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: pem}}},
			GetMITMCACertOut{PEM: pem}, nil
	})
}
