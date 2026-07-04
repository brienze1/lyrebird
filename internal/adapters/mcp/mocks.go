package mcp

import (
	"context"
	"errors"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/brienze1/lyrebird/internal/adapters/dto"
	"github.com/brienze1/lyrebird/internal/usecase"
)

// mockInFields is embedded by CreateMockIn/UpdateMockIn — the fields a
// caller may set on a mock, minus ID/space (handled separately) and
// lifetime (validated, not part of the shared shape, since update never
// accepts it at all).
type mockInFields struct {
	Name       string           `json:"name" jsonschema:"human-readable mock name"`
	Match      dto.MatchDTO     `json:"match" jsonschema:"declarative match conditions (method/path/headers/query/body); empty matches every request"`
	Script     *dto.ScriptDTO   `json:"script,omitempty" jsonschema:"optional sandboxed JS match/respond hooks — see the script_sandbox_api content resource"`
	Action     dto.ActionDTO    `json:"action" jsonschema:"exactly one of respond/proxy/fault"`
	Scenario   *dto.ScenarioDTO `json:"scenario,omitempty" jsonschema:"optional sequential responses (requires action kind respond) — successive matching calls return successive responses in order"`
	Priority   int              `json:"priority,omitempty" jsonschema:"resolution priority; higher wins, ties broken by newest-created then id"`
	Group      string           `json:"group,omitempty" jsonschema:"optional label for filtering list_mocks"`
	TTLSeconds *int             `json:"ttl_seconds,omitempty" jsonschema:"optional TTL in seconds after which this mock is auto-removed"`
}

// CreateMockIn is create_mock's input.
type CreateMockIn struct {
	Space string `json:"space,omitempty" jsonschema:"space/partition to create the mock in; defaults to the server's default space"`
	mockInFields
	// Lifetime, if set, must be "ephemeral" — mocks can only ever be created
	// as ephemeral through this API; seeded mocks come only from mounted
	// seed config files (FR-025).
	Lifetime string `json:"lifetime,omitempty" jsonschema:"must be \"ephemeral\" or omitted"`
}

// GetMockIn is get_mock's input.
type GetMockIn struct {
	ID string `json:"id" jsonschema:"mock id, as returned by create_mock/list_mocks"`
}

// ListMocksIn is list_mocks's input.
type ListMocksIn struct {
	Space string `json:"space,omitempty" jsonschema:"space/partition to list; defaults to the server's default space"`
	Group string `json:"group,omitempty" jsonschema:"filter to mocks with this group label"`
}

// ListMocksOut is list_mocks's output.
type ListMocksOut struct {
	Mocks []dto.MockDTO `json:"mocks"`
}

// UpdateMockIn is update_mock's input.
type UpdateMockIn struct {
	Space string `json:"space,omitempty" jsonschema:"space/partition the mock lives in; defaults to the server's default space"`
	ID    string `json:"id" jsonschema:"id of the mock to update"`
	mockInFields
}

// DeleteMockIn is delete_mock's input.
type DeleteMockIn struct {
	Space string `json:"space,omitempty" jsonschema:"space/partition the mock lives in; defaults to the server's default space"`
	ID    string `json:"id" jsonschema:"id of the mock to delete"`
}

// DeleteMockOut is delete_mock's output.
type DeleteMockOut struct {
	Deleted bool `json:"deleted"`
}

// ResetIn is reset's input.
type ResetIn struct {
	Space        string `json:"space,omitempty" jsonschema:"space/partition to reset; defaults to the server's default space"`
	ClearTraffic bool   `json:"clear_traffic,omitempty" jsonschema:"also clear recorded traffic for this space"`
}

// MatchTestIn is match_test's input.
type MatchTestIn struct {
	Space         string                  `json:"space,omitempty" jsonschema:"space/partition to evaluate against; defaults to the server's default space"`
	SampleRequest dto.MatchTestRequestDTO `json:"sample_request" jsonschema:"the request to test matching against, e.g. {\"method\":\"GET\",\"path\":\"/ping\"}"`
}

// explainErr wraps a use-case error via usecase.Explain into a plain error,
// which the MCP SDK packs into a tool-level CallToolResult{IsError:true}.
func explainErr(err error) error {
	return errors.New(usecase.Explain(err).Message)
}

func registerMockTools(s *sdkmcp.Server, deps Deps) {
	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name: "create_mock",
		Description: "Create a mock that intercepts matching requests ahead of spy passthrough. " +
			`Example: {"name":"ping","match":{"method":"GET","path":"/ping"},"action":{"respond":{"status":200,"body":"pong"}}}`,
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in CreateMockIn) (*sdkmcp.CallToolResult, dto.MockDTO, error) {
		partition := resolveSpace(in.Space, deps.DefaultSpace)
		// Lifetime is validated inside dto.MockInputFromDTO — shared with
		// REST's CreateMock so a caller-supplied "seeded" is rejected
		// identically on both adapters (constitution Principle II).
		mockIn, err := dto.MockInputFromDTO(partition, dto.MockDTO{
			Name: in.Name, Match: in.Match, Script: in.Script, Action: in.Action, Priority: in.Priority, Group: in.Group,
			TTLSeconds: in.TTLSeconds, Lifetime: in.Lifetime, Scenario: in.Scenario,
		})
		if err != nil {
			return nil, dto.MockDTO{}, explainErr(err)
		}
		m, err := deps.MockCRUD.Create(ctx, mockIn)
		if err != nil {
			return nil, dto.MockDTO{}, explainErr(err)
		}
		return nil, dto.MockToDTO(m), nil
	})

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "get_mock",
		Description: `Fetch one mock by id. Example: {"id":"<mock-id>"}`,
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in GetMockIn) (*sdkmcp.CallToolResult, dto.MockDTO, error) {
		partition := resolveSpace("", deps.DefaultSpace)
		m, err := deps.MockCRUD.Get(ctx, partition, in.ID)
		if err != nil {
			return nil, dto.MockDTO{}, explainErr(err)
		}
		return nil, dto.MockToDTO(m), nil
	})

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "list_mocks",
		Description: `List every mock (seeded and ephemeral) in a space. Example: {}`,
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in ListMocksIn) (*sdkmcp.CallToolResult, ListMocksOut, error) {
		partition := resolveSpace(in.Space, deps.DefaultSpace)
		list, err := deps.MockCRUD.List(ctx, partition, in.Group)
		if err != nil {
			return nil, ListMocksOut{}, explainErr(err)
		}
		out := make([]dto.MockDTO, len(list))
		for i, m := range list {
			out[i] = dto.MockToDTO(m)
		}
		return nil, ListMocksOut{Mocks: out}, nil
	})

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name: "update_mock",
		Description: "Overwrite an existing ephemeral mock's fields (seeded mocks reject this). " +
			`Example: {"id":"<mock-id>","name":"ping","match":{"method":"GET","path":"/ping"},"action":{"respond":{"status":200,"body":"pong"}}}`,
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in UpdateMockIn) (*sdkmcp.CallToolResult, dto.MockDTO, error) {
		partition := resolveSpace(in.Space, deps.DefaultSpace)
		mockIn, err := dto.MockInputFromDTO(partition, dto.MockDTO{
			Name: in.Name, Match: in.Match, Script: in.Script, Action: in.Action, Priority: in.Priority, Group: in.Group,
			TTLSeconds: in.TTLSeconds, Scenario: in.Scenario,
		})
		if err != nil {
			return nil, dto.MockDTO{}, explainErr(err)
		}
		m, err := deps.MockCRUD.Update(ctx, partition, in.ID, mockIn)
		if err != nil {
			return nil, dto.MockDTO{}, explainErr(err)
		}
		return nil, dto.MockToDTO(m), nil
	})

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "delete_mock",
		Description: `Delete an ephemeral mock (seeded mocks reject this). Example: {"id":"<mock-id>"}`,
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in DeleteMockIn) (*sdkmcp.CallToolResult, DeleteMockOut, error) {
		partition := resolveSpace(in.Space, deps.DefaultSpace)
		if err := deps.MockCRUD.Delete(ctx, partition, in.ID); err != nil {
			return nil, DeleteMockOut{}, explainErr(err)
		}
		return nil, DeleteMockOut{Deleted: true}, nil
	})

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name: "reset",
		Description: "Remove every ephemeral mock in a space (seeded mocks are preserved), optionally clearing " +
			`recorded traffic too. Example: {"clear_traffic":false}`,
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in ResetIn) (*sdkmcp.CallToolResult, dto.ResetResultDTO, error) {
		partition := resolveSpace(in.Space, deps.DefaultSpace)
		out, err := deps.Reset.Execute(ctx, usecase.ResetInput{Partition: partition, ClearTraffic: in.ClearTraffic})
		if err != nil {
			return nil, dto.ResetResultDTO{}, explainErr(err)
		}
		return nil, dto.ResetResultToDTO(out), nil
	})

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name: "match_test",
		Description: "Dry-run: report which mock would fire for a sample request, every candidate's " +
			"per-condition pass/fail, and the resolved response — never forwards anything onward. " +
			`Example: {"sample_request":{"method":"GET","path":"/ping"}}`,
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in MatchTestIn) (*sdkmcp.CallToolResult, dto.MatchTestResponseDTO, error) {
		partition := resolveSpace(in.Space, deps.DefaultSpace)
		out, err := deps.MatchTest.Execute(ctx, partition, dto.MatchTestInputFromDTO(in.SampleRequest))
		if err != nil {
			return nil, dto.MatchTestResponseDTO{}, explainErr(err)
		}
		return nil, dto.MatchTestOutputToDTO(out), nil
	})
}
