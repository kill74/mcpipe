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
