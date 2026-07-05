package mcp

import (
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// contentTestDeps builds a minimal Deps for content_test.go's tools, which
// take no dependencies at all — set only DefaultSpace so New(deps) doesn't
// panic wiring up the other tool groups it always registers alongside
// registerContentTools.
func contentTestDeps() Deps {
	return Deps{DefaultSpace: "default"}
}

func TestLyrebirdGuideReturnsEmbeddedGuideMarkdown(t *testing.T) {
	srv := New(contentTestDeps())

	result := callTool(t, srv, "lyrebird_guide", map[string]any{})
	if result.IsError {
		t.Fatalf("lyrebird_guide returned an error: %s", errTextIfError(result))
	}
	if guideMarkdown == "" {
		t.Fatal("guideMarkdown embed is empty — content/guide.md missing?")
	}
	tc, ok := result.Content[0].(*sdkmcp.TextContent)
	if !ok || tc.Text != guideMarkdown {
		t.Errorf("Content[0] text = %q, want the embedded guide markdown verbatim", tc.Text)
	}
	out, ok := result.StructuredContent.(map[string]any)
	if !ok || out["markdown"] != guideMarkdown {
		t.Errorf("structured content = %+v, want markdown = the embedded guide", result.StructuredContent)
	}
}

func TestScriptSandboxAPIReturnsEmbeddedSandboxMarkdown(t *testing.T) {
	srv := New(contentTestDeps())

	result := callTool(t, srv, "script_sandbox_api", map[string]any{})
	if result.IsError {
		t.Fatalf("script_sandbox_api returned an error: %s", errTextIfError(result))
	}
	if sandboxAPIMarkdown == "" {
		t.Fatal("sandboxAPIMarkdown embed is empty — content/sandbox_api.md missing?")
	}
	tc, ok := result.Content[0].(*sdkmcp.TextContent)
	if !ok || tc.Text != sandboxAPIMarkdown {
		t.Errorf("Content[0] text = %q, want the embedded sandbox API markdown verbatim", tc.Text)
	}
	out, ok := result.StructuredContent.(map[string]any)
	if !ok || out["markdown"] != sandboxAPIMarkdown {
		t.Errorf("structured content = %+v, want markdown = the embedded sandbox API doc", result.StructuredContent)
	}
}
