package template

import (
	"testing"
	"time"
)

func TestRenderExampleExpressions(t *testing.T) {
	now := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	got, err := RenderString("digest-{{ inputs.topic | slugify }}-{{ now | date: \"%Y%m%d\" }}.md", Context{
		Inputs: map[string]any{"topic": "Quantum Computing Breakthroughs 2026"},
		Now:    now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != "digest-quantum-computing-breakthroughs-2026-20260509.md" {
		t.Fatalf("unexpected render: %s", got)
	}
}

func TestRenderStepAndToolResultReferences(t *testing.T) {
	got, err := RenderString("{{ steps.search.outputs.findings }} -> {{ response.tool_results[0].path }}", Context{
		StepOutputs: map[string]map[string]string{
			"search": {"findings": "facts"},
		},
		Response: Response{ToolResults: []map[string]any{{"path": "digest.md"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != "facts -> digest.md" {
		t.Fatalf("unexpected render: %s", got)
	}
}

func TestUnknownReferenceErrors(t *testing.T) {
	_, err := RenderString("{{ steps.nope.outputs.x }}", Context{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestUpperFilter(t *testing.T) {
	got, err := RenderString("{{ inputs.name | upper }}", Context{
		Inputs: map[string]any{"name": "hello world"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != "HELLO WORLD" {
		t.Fatalf("unexpected: %s", got)
	}
}

func TestLowerFilter(t *testing.T) {
	got, err := RenderString("{{ inputs.name | lower }}", Context{
		Inputs: map[string]any{"name": "HELLO WORLD"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != "hello world" {
		t.Fatalf("unexpected: %s", got)
	}
}

func TestTrimFilter(t *testing.T) {
	got, err := RenderString("{{ inputs.name | trim }}", Context{
		Inputs: map[string]any{"name": "  hello  "},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != "hello" {
		t.Fatalf("unexpected: %s", got)
	}
}

func TestTruncateFilter(t *testing.T) {
	got, err := RenderString("{{ inputs.text | truncate: 10 }}", Context{
		Inputs: map[string]any{"text": "this is a very long string"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != "this is..." {
		t.Fatalf("unexpected: %s", got)
	}

	got2, err := RenderString("{{ inputs.text | truncate: 50 }}", Context{
		Inputs: map[string]any{"text": "short"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got2 != "short" {
		t.Fatalf("unexpected: %s", got2)
	}
}

func TestJsonFilter(t *testing.T) {
	got, err := RenderString("{{ inputs.data | json }}", Context{
		Inputs: map[string]any{"data": []string{"a", "b", "c"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != `["a","b","c"]` {
		t.Fatalf("unexpected: %s", got)
	}
}

func TestBase64Filter(t *testing.T) {
	got, err := RenderString("{{ inputs.text | base64 }}", Context{
		Inputs: map[string]any{"text": "hello"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != "aGVsbG8=" {
		t.Fatalf("unexpected: %s", got)
	}
}

func TestEnvVarFromContext(t *testing.T) {
	got, err := RenderString("Bearer {{ env.TOKEN }}", Context{
		Env: map[string]string{"TOKEN": "secret-123"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != "Bearer secret-123" {
		t.Fatalf("unexpected: %s", got)
	}
}

func TestEnvVarFallbackToOS(t *testing.T) {
	t.Setenv("MCPIPE_TEST_VAR", "from-os")
	got, err := RenderString("{{ env.MCPIPE_TEST_VAR }}", Context{})
	if err != nil {
		t.Fatal(err)
	}
	if got != "from-os" {
		t.Fatalf("unexpected: %s", got)
	}
}

func TestEnvVarMissingReturnsEmpty(t *testing.T) {
	got, err := RenderString("prefix-{{ env.NONEXISTENT_VAR_XYZ }}-suffix", Context{})
	if err != nil {
		t.Fatal(err)
	}
	if got != "prefix--suffix" {
		t.Fatalf("unexpected: %s", got)
	}
}

func TestEnvVarOverridesOS(t *testing.T) {
	t.Setenv("MCPIPE_OVERRIDE", "from-os")
	got, err := RenderString("{{ env.MCPIPE_OVERRIDE }}", Context{
		Env: map[string]string{"MCPIPE_OVERRIDE": "from-context"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != "from-context" {
		t.Fatalf("expected context to override OS env, got: %s", got)
	}
}

func TestEnvVarWithFilter(t *testing.T) {
	got, err := RenderString("{{ env.VALUE | upper }}", Context{
		Env: map[string]string{"VALUE": "hello"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != "HELLO" {
		t.Fatalf("unexpected: %s", got)
	}
}
