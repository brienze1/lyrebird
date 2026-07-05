package mcp

import (
	"context"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/brienze1/lyrebird/internal/adapters/dto"
	"github.com/brienze1/lyrebird/internal/domain"
)

// resolveSpace substitutes configuredDefault (cfg.DefaultSpace, threaded in
// at server construction) for an empty space argument, falling back to
// domain.DefaultPartitionID only if that's also empty — matching
// httpmw.Partition's behavior exactly, so REST and MCP never diverge on
// default-space semantics (constitution Principle II). MCP tool calls have
// no HTTP header to read a space from, unlike REST's X-Lyrebird-Space, so
// every tool's input carries its own optional Space field instead.
func resolveSpace(space, configuredDefault string) string {
	if space != "" {
		return space
	}
	if configuredDefault != "" {
		return configuredDefault
	}
	return domain.DefaultPartitionID
}

// CreateSpaceIn is create_space's input.
type CreateSpaceIn struct {
	ID          string `json:"id" jsonschema:"unique id for the new space, e.g. agent-a"`
	Description string `json:"description,omitempty" jsonschema:"optional human-readable note about this space's purpose"`
}

// ListSpacesOut is list_spaces's output.
type ListSpacesOut struct {
	Spaces []dto.PartitionDTO `json:"spaces"`
}

// DeleteSpaceIn is delete_space's input.
type DeleteSpaceIn struct {
	ID string `json:"id" jsonschema:"id of the space to delete; the default space can never be deleted"`
}

func registerSpaceTools(s *sdkmcp.Server, deps Deps) {
	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "create_space",
		Description: `Register a new space (partition) for isolating mocks/traffic/upstreams. Example: {"id":"agent-a"}`,
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in CreateSpaceIn) (*sdkmcp.CallToolResult, dto.PartitionDTO, error) {
		p, err := deps.CreateSpace.Execute(ctx, dto.PartitionFromDTO(dto.NewPartitionDTOFromFields(in.ID, in.Description)))
		if err != nil {
			return nil, dto.PartitionDTO{}, explainErr(err)
		}
		return nil, dto.PartitionToDTO(p), nil
	})

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name: "list_spaces",
		Description: "List every space registered via create_space (the default space is always included). " +
			"A space used only via a mock/upstream's space argument or the X-Lyrebird-Space header, without " +
			"ever having been created through create_space, will not appear here. Example: {}",
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, _ struct{}) (*sdkmcp.CallToolResult, ListSpacesOut, error) {
		list, err := deps.ListSpaces.Execute(ctx)
		if err != nil {
			return nil, ListSpacesOut{}, explainErr(err)
		}
		out := make([]dto.PartitionDTO, len(list))
		for i, p := range list {
			out[i] = dto.PartitionToDTO(p)
		}
		return nil, ListSpacesOut{Spaces: out}, nil
	})

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name: "delete_space",
		Description: "Delete a space, cascading its ephemeral mocks, recorded traffic, and upstream configuration. " +
			`Refuses the default space. Example: {"id":"agent-a"}`,
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in DeleteSpaceIn) (*sdkmcp.CallToolResult, struct{}, error) {
		if err := deps.DeleteSpace.Execute(ctx, in.ID); err != nil {
			return nil, struct{}{}, explainErr(err)
		}
		return nil, struct{}{}, nil
	})
}
