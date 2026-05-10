package config

import (
	"strings"
	"testing"

	"mcpipe/internal/sample"
)

func TestSamplePipelineValidates(t *testing.T) {
	p, err := Load(strings.NewReader(sample.ResearchDigestPipeline))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	inputs, err := p.ResolveInputs(map[string]string{"topic": "quantum computing"})
	if err != nil {
		t.Fatal(err)
	}
	if inputs["depth"] != "standard" {
		t.Fatalf("expected default depth, got %#v", inputs["depth"])
	}
	if inputs["output_lang"] != "en" {
		t.Fatalf("expected default output_lang, got %#v", inputs["output_lang"])
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	_, err := Load(strings.NewReader(`{"version":"1.0.0","steps":[],"surprise":true}`))
	if err == nil {
		t.Fatal("expected unknown field error")
	}
	if !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("expected unknown field error, got %v", err)
	}
}

func TestValidateFindsDuplicateStepID(t *testing.T) {
	p, err := Load(strings.NewReader(`{
		"version": "1.0.0",
		"defaults": {"llm": {"backend": "ollama", "model": "qwen"}},
		"steps": [
			{"id": "a", "prompt": {"user": "a"}, "outputs": {"out": "{{ response.text }}"}},
			{"id": "a", "prompt": {"user": "dup"}, "outputs": {"out": "{{ response.text }}"}}
		]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	err = p.Validate()
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "duplicate step id") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateFindsCycle(t *testing.T) {
	p, err := Load(strings.NewReader(`{
		"version": "1.0.0",
		"defaults": {"llm": {"backend": "ollama", "model": "qwen"}},
		"steps": [
			{"id": "a", "depends_on": ["b"], "prompt": {"user": "a"}, "outputs": {"out": "{{ response.text }}"}},
			{"id": "b", "depends_on": ["a"], "prompt": {"user": "b"}, "outputs": {"out": "{{ response.text }}"}}
		]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	err = p.Validate()
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "dependency cycle") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateRequiresReferencedStepDependency(t *testing.T) {
	p, err := Load(strings.NewReader(`{
		"version": "1.0.0",
		"defaults": {"llm": {"backend": "ollama", "model": "qwen"}},
		"steps": [
			{"id": "a", "prompt": {"user": "a"}, "outputs": {"out": "{{ response.text }}"}},
			{"id": "b", "prompt": {"user": "{{ steps.a.outputs.out }}"}, "outputs": {"out": "{{ response.text }}"}}
		]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	err = p.Validate()
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), `references step "a" but does not depend on it`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveInputsValidatesEnumAndPattern(t *testing.T) {
	p, err := Load(strings.NewReader(`{
		"version": "1.0.0",
		"inputs": {
			"mode": {"type": "enum", "values": ["fast", "deep"], "default": "fast"},
			"lang": {"type": "string", "pattern": "^[a-z]{2}$", "required": true}
		},
		"defaults": {"llm": {"backend": "ollama", "model": "qwen"}},
		"steps": [
			{"id": "a", "prompt": {"user": "a"}, "outputs": {"out": "{{ response.text }}"}}
		]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	if _, err := p.ResolveInputs(map[string]string{"lang": "eng"}); err == nil {
		t.Fatal("expected pattern error")
	}
	if _, err := p.ResolveInputs(map[string]string{"lang": "en", "mode": "wide"}); err == nil {
		t.Fatal("expected enum error")
	}
}

func TestRuntimeWarnings(t *testing.T) {
	p, err := Load(strings.NewReader(sample.ResearchDigestPipeline))
	if err != nil {
		t.Fatal(err)
	}
	warnings := p.RuntimeWarnings()
	joined := strings.Join(warnings, "\n")
	for _, want := range []string{"scheduler daemon", "sse transport", "SQLite history"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected warning containing %q, got:\n%s", want, joined)
		}
	}
}
