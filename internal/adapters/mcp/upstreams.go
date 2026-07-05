package mcp

import (
	"context"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/brienze1/lyrebird/internal/adapters/dto"
)

// SetUpstreamIn is set_upstream's input.
type SetUpstreamIn struct {
	Space         string `json:"space,omitempty" jsonschema:"space/partition to configure; defaults to the server's default space"`
	MatchHost     string `json:"match_host" jsonschema:"host this upstream applies to, e.g. api.example.com"`
	TargetURL     string `json:"target_url" jsonschema:"absolute http(s) URL spy passthrough forwards unmatched requests to"`
	TLSSkipVerify bool   `json:"tls_skip_verify,omitempty" jsonschema:"skip TLS certificate verification when forwarding to target_url"`
}

// ListUpstreamsIn is list_upstreams's input.
type ListUpstreamsIn struct {
	Space string `json:"space,omitempty" jsonschema:"space/partition to list; defaults to the server's default space"`
}

// ListUpstreamsOut is list_upstreams's output.
type ListUpstreamsOut struct {
	Upstreams []dto.UpstreamDTO `json:"upstreams"`
}

func registerUpstreamTools(s *sdkmcp.Server, deps Deps) {
	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name: "set_upstream",
		Description: "Configure the real target spy passthrough forwards unmatched requests to for a host. " +
			`Example: {"match_host":"api.example.com","target_url":"https://api.example.com"}`,
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in SetUpstreamIn) (*sdkmcp.CallToolResult, dto.UpstreamDTO, error) {
		partition := resolveSpace(in.Space, deps.DefaultSpace)
		d := dto.NewUpstreamDTOFromFields(in.MatchHost, in.TargetURL, in.TLSSkipVerify)
		if err := deps.SetUpstream.Execute(ctx, dto.UpstreamFromDTO(partition, d)); err != nil {
			return nil, dto.UpstreamDTO{}, explainErr(err)
		}
		return nil, d, nil
	})

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "list_upstreams",
		Description: `List every upstream configured in a space. Example: {}`,
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in ListUpstreamsIn) (*sdkmcp.CallToolResult, ListUpstreamsOut, error) {
		partition := resolveSpace(in.Space, deps.DefaultSpace)
		list, err := deps.ListUpstreams.Execute(ctx, partition)
		if err != nil {
			return nil, ListUpstreamsOut{}, explainErr(err)
		}
		out := make([]dto.UpstreamDTO, len(list))
		for i, u := range list {
			out[i] = dto.UpstreamToDTO(u)
		}
		return nil, ListUpstreamsOut{Upstreams: out}, nil
	})
}
