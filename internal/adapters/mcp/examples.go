package mcp

import (
	"context"
	"encoding/json"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/brienze1/lyrebird/internal/adapters/examples"
	"github.com/brienze1/lyrebird/internal/domain"
)

// ListExamplesIn is list_examples' input.
type ListExamplesIn struct {
	Query string `json:"query,omitempty" jsonschema:"substring filter over id/title/provider/service; omitted returns every recipe"`
}

// ListExamplesOut is list_examples' output.
type ListExamplesOut struct {
	Examples []examples.Summary `json:"examples"`
}

// GetExampleIn is get_example's input.
type GetExampleIn struct {
	ID string `json:"id" jsonschema:"recipe id, as returned by list_examples"`
}

// ExampleOut is get_example's output. Mock is decoded to a generic `any`
// so the MCP SDK's schema generator doesn't mis-describe it as a base64 string.
type ExampleOut struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Provider    string `json:"provider"`
	Service     string `json:"service"`
	Description string `json:"description"`
	Mock        any    `json:"mock,omitempty"`
}

func registerExampleTools(s *sdkmcp.Server) {
	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "list_examples",
		Description: `Recipe index for mocking common third-party APIs/cloud SDKs as plain HTTP (AWS/GCP/generic). Content only. Example: {}`,
	}, func(_ context.Context, _ *sdkmcp.CallToolRequest, in ListExamplesIn) (*sdkmcp.CallToolResult, ListExamplesOut, error) {
		return nil, ListExamplesOut{Examples: examples.List(in.Query)}, nil
	})

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "get_example",
		Description: `One recipe's full, ready-to-adapt create_mock payload. Example: {"id":"aws-sns"}`,
	}, func(_ context.Context, _ *sdkmcp.CallToolRequest, in GetExampleIn) (*sdkmcp.CallToolResult, ExampleOut, error) {
		r, ok := examples.Get(in.ID)
		if !ok {
			return nil, ExampleOut{}, explainErr(domain.ErrNotFound)
		}
		out := ExampleOut{ID: r.ID, Title: r.Title, Provider: r.Provider, Service: r.Service, Description: r.Description}
		if r.Mock != nil {
			if err := json.Unmarshal(r.Mock, &out.Mock); err != nil {
				return nil, ExampleOut{}, explainErr(err)
			}
		}
		return nil, out, nil
	})
}
