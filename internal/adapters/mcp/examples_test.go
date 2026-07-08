package mcp

import "testing"

func TestListExamplesReturnsEveryRecipe(t *testing.T) {
	srv := New(contentTestDeps())

	result := callTool(t, srv, "list_examples", map[string]any{})
	if result.IsError {
		t.Fatalf("list_examples returned an error: %s", errTextIfError(result))
	}
	out, ok := result.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("structured content = %+v, want a map", result.StructuredContent)
	}
	list, ok := out["examples"].([]any)
	if !ok {
		t.Fatalf("examples field = %+v, want an array", out["examples"])
	}
	if len(list) != 11 {
		t.Errorf("list_examples returned %d entries, want 11", len(list))
	}
}

func TestListExamplesFiltersByQuery(t *testing.T) {
	srv := New(contentTestDeps())

	result := callTool(t, srv, "list_examples", map[string]any{"query": "aws"})
	if result.IsError {
		t.Fatalf("list_examples returned an error: %s", errTextIfError(result))
	}
	out := result.StructuredContent.(map[string]any)
	list := out["examples"].([]any)
	if len(list) != 5 {
		t.Errorf(`list_examples(query="aws") returned %d entries, want 5`, len(list))
	}
}

func TestGetExampleReturnsTheFullRecipe(t *testing.T) {
	srv := New(contentTestDeps())

	result := callTool(t, srv, "get_example", map[string]any{"id": "aws-sns"})
	if result.IsError {
		t.Fatalf("get_example returned an error: %s", errTextIfError(result))
	}
	out, ok := result.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("structured content = %+v, want a map", result.StructuredContent)
	}
	if out["id"] != "aws-sns" {
		t.Errorf("id = %v, want aws-sns", out["id"])
	}
	if out["mock"] == nil {
		t.Error("mock field is nil, want the recipe's ready-to-adapt payload")
	}
}

func TestGetExampleTheHowToEntryHasNoMockField(t *testing.T) {
	srv := New(contentTestDeps())

	result := callTool(t, srv, "get_example", map[string]any{"id": "endpoint-injection-howto"})
	if result.IsError {
		t.Fatalf("get_example returned an error: %s", errTextIfError(result))
	}
	out := result.StructuredContent.(map[string]any)
	if out["mock"] != nil {
		t.Errorf("mock field = %v, want nil for the how-to entry", out["mock"])
	}
}

func TestGetExampleUnknownIDFailsWithAnExplanatoryError(t *testing.T) {
	srv := New(contentTestDeps())

	result := callTool(t, srv, "get_example", map[string]any{"id": "does-not-exist"})
	if !result.IsError {
		t.Fatal("get_example(does-not-exist) succeeded, want a tool error")
	}
	if errTextIfError(result) == "" {
		t.Fatal("get_example(does-not-exist) errored with no explanatory text")
	}
}
