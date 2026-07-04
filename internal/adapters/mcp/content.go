package mcp

import (
	"context"
	_ "embed"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

//go:embed content/guide.md
var guideMarkdown string

//go:embed content/sandbox_api.md
var sandboxAPIMarkdown string

// GuideOut carries lyrebird_guide's markdown as both structured content
// (Markdown field) and raw text content (set explicitly in registration
// below) — an agent reading either gets the full document.
type GuideOut struct {
	Markdown string `json:"markdown"`
}

// SandboxAPIOut carries script_sandbox_api's markdown the same way GuideOut does.
type SandboxAPIOut struct {
	Markdown string `json:"markdown"`
}

func registerContentTools(s *sdkmcp.Server) {
	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "lyrebird_guide",
		Description: "Self-describing guide: concepts, composition, and a minimal valid create_mock example.",
	}, func(_ context.Context, _ *sdkmcp.CallToolRequest, _ struct{}) (*sdkmcp.CallToolResult, GuideOut, error) {
		return &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: guideMarkdown}}},
			GuideOut{Markdown: guideMarkdown}, nil
	})

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "script_sandbox_api",
		Description: "Documentation of the sandboxed JS globals available to a mock's script hook (req, uuid(), now(), faker, jsonpath()).",
	}, func(_ context.Context, _ *sdkmcp.CallToolRequest, _ struct{}) (*sdkmcp.CallToolResult, SandboxAPIOut, error) {
		return &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: sandboxAPIMarkdown}}},
			SandboxAPIOut{Markdown: sandboxAPIMarkdown}, nil
	})
}
