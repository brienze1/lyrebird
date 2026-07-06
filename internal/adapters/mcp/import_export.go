package mcp

import (
	"context"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"gopkg.in/yaml.v3"

	"github.com/brienze1/lyrebird/internal/adapters/dto"
)

// ExportConfigIn is export_config's input.
type ExportConfigIn struct {
	Space string `json:"space,omitempty" jsonschema:"space/partition to export; defaults to the server's default space"`
}

// ExportConfigOut is export_config's output.
type ExportConfigOut struct {
	YAML string `json:"yaml"`
}

// ImportConfigIn is import_config's input.
type ImportConfigIn struct {
	Space string `json:"space,omitempty" jsonschema:"space/partition to import into; defaults to the server's default space"`
	YAML  string `json:"yaml" jsonschema:"the YAML bundle to import — a seed-config.md-shaped file, or export_config's own output"`
}

// ImportConfigOut is import_config's output.
type ImportConfigOut struct {
	UpstreamsImported int `json:"upstreams_imported"`
	MocksImported     int `json:"mocks_imported"`
}

func registerImportExportTools(s *sdkmcp.Server, deps Deps) {
	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name: "export_config",
		Description: "Export a space's runtime upstreams + ephemeral mocks as a YAML bundle, reusable as a " +
			`/config seed file (seeded mocks are never included). Example: {}`,
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in ExportConfigIn) (*sdkmcp.CallToolResult, ExportConfigOut, error) {
		partition := resolveSpace(in.Space, deps.DefaultSpace)
		bundle, err := deps.ExportSeeds.Execute(ctx, partition)
		if err != nil {
			return nil, ExportConfigOut{}, explainErr(err)
		}
		raw, err := yaml.Marshal(dto.SeedBundleToDTO(bundle))
		if err != nil {
			return nil, ExportConfigOut{}, explainErr(err)
		}
		return &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: string(raw)}}},
			ExportConfigOut{YAML: string(raw)}, nil
	})

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name: "import_config",
		Description: "Import a YAML bundle (a seed-config.md-shaped file, or export_config's own output) into a " +
			`space — additive, existing upstreams/mocks are left untouched. Example: {"yaml":"mocks:\n  - name: ping\n    match: {method: GET, path: /ping}\n    action: {respond: {status: 200, body: pong}}\n"}`,
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in ImportConfigIn) (*sdkmcp.CallToolResult, ImportConfigOut, error) {
		partition := resolveSpace(in.Space, deps.DefaultSpace)

		var bundle dto.SeedBundleDTO
		if err := yaml.Unmarshal([]byte(in.YAML), &bundle); err != nil {
			return nil, ImportConfigOut{}, explainErr(err)
		}
		upstreams, mocks, err := dto.SeedBundleFromDTO(partition, bundle)
		if err != nil {
			return nil, ImportConfigOut{}, explainErr(err)
		}
		result, err := deps.ImportSeeds.Execute(ctx, partition, upstreams, mocks)
		if err != nil {
			return nil, ImportConfigOut{}, explainErr(err)
		}
		return nil, ImportConfigOut{UpstreamsImported: result.UpstreamsImported, MocksImported: result.MocksImported}, nil
	})
}
