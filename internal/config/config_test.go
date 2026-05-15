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

func TestPluginsAndAgentsMergeIntoSteps(t *testing.T) {
	p, err := Load(strings.NewReader(`{
		"version": "1.0.0",
		"plugins": {
			"search": {
				"mcp_servers": {
					"brave_search": {"transport": "stdio", "command": "npx"}
				},
				"tools": {"allow": ["brave_search.*"]},
				"policy": {"brave_search.*": {"max_calls": 2}}
			}
		},
		"agents": {
			"researcher": {
				"llm": {"backend": "ollama", "model": "qwen"},
				"agent": {"enabled": true, "max_iterations": 3, "stop_on": "no_tool_call"}
			}
		},
		"steps": [
			{"id": "a", "plugins": ["search"], "agent_ref": "researcher", "prompt": {"user": "a"}, "outputs": {"out": "{{ response.text }}"}}
		]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	step := p.Steps[0]
	if len(step.Tools.Allow) != 1 || step.Tools.Allow[0] != "brave_search.*" {
		t.Fatalf("plugin tools were not merged: %#v", step.Tools.Allow)
	}
	if _, ok := p.MCPServers["brave_search"]; !ok {
		t.Fatalf("plugin mcp server was not registered: %#v", p.MCPServers)
	}
	if p.Policy["brave_search.*"].MaxCalls != 2 {
		t.Fatalf("plugin policy was not merged: %#v", p.Policy)
	}
	effective := p.EffectiveStep(step)
	if effective.LLM.Backend != "ollama" || effective.LLM.Model != "qwen" {
		t.Fatalf("agent llm was not merged: %#v", effective.LLM)
	}
	if effective.Step.Agent == nil || !effective.Step.Agent.Enabled || effective.Step.Agent.MaxIterations != 3 {
		t.Fatalf("agent runtime config was not merged: %#v", effective.Step.Agent)
	}
}

func TestValidateRejectsUnknownPluginAndAgent(t *testing.T) {
	p, err := Load(strings.NewReader(`{
		"version": "1.0.0",
		"defaults": {"llm": {"backend": "ollama", "model": "qwen"}},
		"steps": [
			{"id": "a", "plugins": ["missing_plugin"], "agent_ref": "missing_agent", "prompt": {"user": "a"}, "outputs": {"out": "{{ response.text }}"}}
		]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	err = p.Validate()
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), `unknown plugin "missing_plugin"`) || !strings.Contains(err.Error(), `unknown agent "missing_agent"`) {
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

func TestResolveInputsNumber(t *testing.T) {
	p, err := Load(strings.NewReader(`{
		"version": "1.0.0",
		"inputs": {
			"threshold": {"type": "number", "default": 0.5},
			"count": {"type": "number", "required": true}
		},
		"defaults": {"llm": {"backend": "ollama", "model": "qwen"}},
		"steps": [
			{"id": "a", "prompt": {"user": "a"}, "outputs": {"out": "{{ response.text }}"}}
		]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := p.ResolveInputs(map[string]string{"count": "42"})
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := resolved["threshold"].(float64); !ok || v != 0.5 {
		t.Fatalf("expected default 0.5, got %#v", resolved["threshold"])
	}
	if v, ok := resolved["count"].(float64); !ok || v != 42.0 {
		t.Fatalf("expected 42.0, got %#v", resolved["count"])
	}
	if _, err := p.ResolveInputs(map[string]string{"count": "not-a-number"}); err == nil {
		t.Fatal("expected number parse error")
	}
}

func TestResolveInputsBoolean(t *testing.T) {
	p, err := Load(strings.NewReader(`{
		"version": "1.0.0",
		"inputs": {
			"verbose": {"type": "boolean", "default": false},
			"enabled": {"type": "boolean", "required": true}
		},
		"defaults": {"llm": {"backend": "ollama", "model": "qwen"}},
		"steps": [
			{"id": "a", "prompt": {"user": "a"}, "outputs": {"out": "{{ response.text }}"}}
		]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := p.ResolveInputs(map[string]string{"enabled": "yes"})
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := resolved["verbose"].(bool); !ok || v != false {
		t.Fatalf("expected default false, got %#v", resolved["verbose"])
	}
	if v, ok := resolved["enabled"].(bool); !ok || v != true {
		t.Fatalf("expected true, got %#v", resolved["enabled"])
	}
	for _, bad := range []string{"maybe", "on", ""} {
		if _, err := p.ResolveInputs(map[string]string{"enabled": bad}); err == nil {
			t.Fatalf("expected boolean error for %q", bad)
		}
	}
}

func TestResolveInputsArray(t *testing.T) {
	p, err := Load(strings.NewReader(`{
		"version": "1.0.0",
		"inputs": {
			"tags": {"type": "array", "default": ["default"]},
			"fields": {"type": "array", "required": true}
		},
		"defaults": {"llm": {"backend": "ollama", "model": "qwen"}},
		"steps": [
			{"id": "a", "prompt": {"user": "a"}, "outputs": {"out": "{{ response.text }}"}}
		]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := p.ResolveInputs(map[string]string{"fields": `["name","email"]`})
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := resolved["tags"].([]string); !ok || len(v) != 1 || v[0] != "default" {
		t.Fatalf("expected default [default], got %#v", resolved["tags"])
	}
	if v, ok := resolved["fields"].([]string); !ok || len(v) != 2 || v[0] != "name" || v[1] != "email" {
		t.Fatalf("expected [name, email], got %#v", resolved["fields"])
	}
	if _, err := p.ResolveInputs(map[string]string{"fields": `not-json`}); err == nil {
		t.Fatal("expected array parse error")
	}
}
