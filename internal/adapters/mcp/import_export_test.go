package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/brienze1/lyrebird/internal/domain"
	"github.com/brienze1/lyrebird/internal/usecase"
)

type fakeExportSeedsPort struct {
	bundle usecase.ExportBundle
	err    error
}

func (f *fakeExportSeedsPort) Execute(_ context.Context, _ string) (usecase.ExportBundle, error) {
	return f.bundle, f.err
}

type fakeImportSeedsPort struct {
	gotUpstreams []domain.Upstream
	gotMocks     []usecase.MockInput
	result       usecase.ImportResult
	err          error
}

func (f *fakeImportSeedsPort) Execute(_ context.Context, _ string, upstreams []domain.Upstream, mocks []usecase.MockInput) (usecase.ImportResult, error) {
	f.gotUpstreams, f.gotMocks = upstreams, mocks
	return f.result, f.err
}

func importExportTestDeps(export *fakeExportSeedsPort, importUC *fakeImportSeedsPort) Deps {
	deps := contentTestDeps()
	deps.ExportSeeds = export
	deps.ImportSeeds = importUC
	return deps
}

func TestExportConfigReturnsYAMLContainingTheMock(t *testing.T) {
	export := &fakeExportSeedsPort{bundle: usecase.ExportBundle{
		Space: "default",
		Mocks: []domain.Mock{{Name: "ping", Action: domain.Action{Kind: domain.ActionRespond, Respond: &domain.RespondAction{Status: 200, Body: []byte("pong")}}}},
	}}
	srv := New(importExportTestDeps(export, &fakeImportSeedsPort{}))

	result := callTool(t, srv, "export_config", map[string]any{})
	if result.IsError {
		t.Fatalf("export_config returned an error: %s", errTextIfError(result))
	}
	out, ok := result.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("structured content = %+v, want a map", result.StructuredContent)
	}
	yamlText, _ := out["yaml"].(string)
	if !strings.Contains(yamlText, "name: ping") {
		t.Errorf("yaml = %q, want it to contain the mock's name", yamlText)
	}
}

func TestImportConfigParsesYAMLAndDelegates(t *testing.T) {
	importUC := &fakeImportSeedsPort{result: usecase.ImportResult{UpstreamsImported: 1, MocksImported: 1}}
	srv := New(importExportTestDeps(&fakeExportSeedsPort{}, importUC))

	yamlBody := "upstreams:\n  - match_host: example.local\n    target_url: https://example.local\nmocks:\n  - name: ping\n    match: {method: GET, path: /ping}\n    action: {respond: {status: 200, body: pong}}\n"
	result := callTool(t, srv, "import_config", map[string]any{"yaml": yamlBody})
	if result.IsError {
		t.Fatalf("import_config returned an error: %s", errTextIfError(result))
	}
	out, ok := result.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("structured content = %+v, want a map", result.StructuredContent)
	}
	if out["upstreams_imported"] != float64(1) || out["mocks_imported"] != float64(1) {
		t.Errorf("structured content = %+v, want 1 upstream and 1 mock imported", out)
	}
	if len(importUC.gotUpstreams) != 1 || importUC.gotUpstreams[0].MatchHost != "example.local" {
		t.Errorf("use case received upstreams %+v, want one for example.local", importUC.gotUpstreams)
	}
	if len(importUC.gotMocks) != 1 || importUC.gotMocks[0].Name != "ping" {
		t.Errorf("use case received mocks %+v, want one named ping", importUC.gotMocks)
	}
}

func TestImportConfigFailsWithAnExplanatoryErrorOnMalformedYAML(t *testing.T) {
	srv := New(importExportTestDeps(&fakeExportSeedsPort{}, &fakeImportSeedsPort{}))

	result := callTool(t, srv, "import_config", map[string]any{"yaml": "not: [valid, yaml :::"})
	if !result.IsError {
		t.Fatal("import_config with malformed YAML succeeded, want a tool error")
	}
	if errTextIfError(result) == "" {
		t.Fatal("import_config errored with no explanatory text")
	}
}
