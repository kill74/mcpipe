package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIInitValidateDryRunAndRunMock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pipeline.json")

	var out bytes.Buffer
	var stderr bytes.Buffer
	if err := run([]string{"init", path}, &out, &stderr); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	if err := run([]string{"validate", "-f", path}, &out, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "valid:") {
		t.Fatalf("unexpected validate output: %s", out.String())
	}

	out.Reset()
	if err := run([]string{"dry-run", "-f", path, "--input", "topic=Quantum Computing"}, &out, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Run order:") {
		t.Fatalf("unexpected dry-run output: %s", out.String())
	}

	out.Reset()
	if err := run([]string{"run", "-f", path, "--input", "topic=Quantum Computing", "--mock", "--no-audit", "--output-dir", t.TempDir()}, &out, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "digest-quantum-computing-") {
		t.Fatalf("unexpected run output: %s", out.String())
	}
}

func TestCLINewDiffReplayAndSummary(t *testing.T) {
	dir := t.TempDir()
	research := filepath.Join(dir, "research.json")
	review := filepath.Join(dir, "review.json")
	var out bytes.Buffer
	var stderr bytes.Buffer

	if err := run([]string{"new", "--list"}, &out, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "code-review") {
		t.Fatalf("unexpected template list:\n%s", out.String())
	}

	out.Reset()
	if err := run([]string{"new", "research", research}, &out, &stderr); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := run([]string{"new", "code-review", review}, &out, &stderr); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	if err := run([]string{"diff", research, review}, &out, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Pipeline Diff") || !strings.Contains(out.String(), "step added") {
		t.Fatalf("unexpected diff:\n%s", out.String())
	}

	out.Reset()
	if err := run([]string{"replay", "-f", review, "--input", "repository_path=.", "--json", "--output-dir", t.TempDir()}, &out, &stderr); err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(out.Bytes(), &parsed); err != nil {
		t.Fatalf("replay --json did not emit valid json: %v\n%s", err, out.String())
	}
	summary, ok := parsed["summary"].(map[string]any)
	if !ok || summary["attempts"] == nil {
		t.Fatalf("missing summary in replay json: %#v", parsed)
	}

	bundle := filepath.Join(dir, "run.mcpipebundle")
	out.Reset()
	if err := run([]string{"bundle", "-f", review, "--input", "repository_path=.", "--out", bundle}, &out, &stderr); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(bundle); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	if err := run([]string{"providers", "list"}, &out, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "ollama") || !strings.Contains(out.String(), "anthropic") {
		t.Fatalf("unexpected providers list:\n%s", out.String())
	}

	out.Reset()
	if err := run([]string{"mcp", "list", "-f", research}, &out, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "MCP Servers") || !strings.Contains(out.String(), "filesystem") {
		t.Fatalf("unexpected mcp list:\n%s", out.String())
	}
}

func TestCLIVersion(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"version"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != "mcpipe 0.1.0" {
		t.Fatalf("unexpected version output: %q", out.String())
	}
}

func TestCLICompletion(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"completion", "powershell"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Register-ArgumentCompleter") || !strings.Contains(out.String(), "schema-docs") {
		t.Fatalf("unexpected completion output:\n%s", out.String())
	}
}

func TestCLIInputFileWithFlagOverride(t *testing.T) {
	dir := t.TempDir()
	pipeline := filepath.Join(dir, "pipeline.json")
	inputs := filepath.Join(dir, "inputs.json")
	var out bytes.Buffer
	var stderr bytes.Buffer
	if err := run([]string{"init", pipeline}, &out, &stderr); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inputs, []byte(`{"topic":"From File","output_lang":"pt"}`), 0644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := run([]string{"dry-run", "-f", pipeline, "--input-file", inputs, "--input", "topic=From Flag"}, &out, &stderr); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "topic = From Flag") {
		t.Fatalf("expected flag to override input file:\n%s", got)
	}
	if !strings.Contains(got, "output_lang = pt") {
		t.Fatalf("expected input file value:\n%s", got)
	}
}

func TestRunRejectsUnsupportedOutputBeforeExecution(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pipeline.json")
	body := `{
		"version": "1.0.0",
		"defaults": {"llm": {"backend": "ollama", "model": "qwen"}},
		"steps": [
			{"id": "a", "prompt": {"user": "a"}, "outputs": {"out": "{{ response.text }}"}}
		],
		"output": {"destination": "file", "fields": ["steps.a.outputs.out"]}
	}`
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	err := run([]string{"run", "-f", path, "--mock"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected unsupported output error")
	}
	if !strings.Contains(err.Error(), "output destination") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCLIExplainGraphAndRunJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pipeline.json")
	var out bytes.Buffer
	var stderr bytes.Buffer
	if err := run([]string{"init", path}, &out, &stderr); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	if err := run([]string{"explain", "-f", path}, &out, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Pipeline") || !strings.Contains(out.String(), "Execution") {
		t.Fatalf("unexpected explain output:\n%s", out.String())
	}

	out.Reset()
	if err := run([]string{"graph", "-f", path}, &out, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "flowchart TD") || !strings.Contains(out.String(), "web_search") {
		t.Fatalf("unexpected mermaid graph:\n%s", out.String())
	}

	out.Reset()
	if err := run([]string{"graph", "-f", path, "--format", "dot"}, &out, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "digraph mcpipe") || !strings.Contains(out.String(), `"web_search"`) {
		t.Fatalf("unexpected dot graph:\n%s", out.String())
	}

	out.Reset()
	if err := run([]string{"run", "-f", path, "--input", "topic=Quantum Computing", "--mock", "--json", "--no-audit", "--output-dir", t.TempDir()}, &out, &stderr); err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(out.Bytes(), &parsed); err != nil {
		t.Fatalf("run --json did not emit valid json: %v\n%s", err, out.String())
	}
	if parsed["run_id"] == "" {
		t.Fatalf("missing run_id in json output: %#v", parsed)
	}
}

func TestCLIVetLockAndSignature(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pipeline.json")
	var out bytes.Buffer
	var stderr bytes.Buffer
	if err := run([]string{"init", path}, &out, &stderr); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := run([]string{"vet", "-f", path}, &out, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "broad_tool_allow") {
		t.Fatalf("expected security lint finding:\n%s", out.String())
	}

	lockPath := filepath.Join(dir, "pipeline.lock")
	out.Reset()
	if err := run([]string{"lock", "-f", path, "--out", lockPath}, &out, &stderr); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := run([]string{"lock", "-f", path, "--out", lockPath, "--verify"}, &out, &stderr); err != nil {
		t.Fatal(err)
	}

	privateKey := filepath.Join(dir, "mcpipe.key")
	publicKey := filepath.Join(dir, "mcpipe.pub")
	sig := filepath.Join(dir, "pipeline.sig")
	if err := run([]string{"keygen", "--private", privateKey, "--public", publicKey}, &out, &stderr); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"sign", "-f", path, "--key", privateKey, "--out", sig}, &out, &stderr); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"verify", "-f", path, "--key", publicKey, "--sig", sig}, &out, &stderr); err != nil {
		t.Fatal(err)
	}
}

func TestCLIRequireConfirmation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pipeline.json")
	var out bytes.Buffer
	if err := run([]string{"init", path}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	err := run([]string{"run", "-f", path, "--input", "topic=Quantum Computing", "--mock", "--require-confirmation", "--no-audit", "--output-dir", t.TempDir()}, &out, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected confirmation error")
	}
	if !strings.Contains(out.String(), "mcpipe execution plan") {
		t.Fatalf("expected execution plan:\n%s", out.String())
	}
}

func TestCLIDoctor(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pipeline.json")
	body := `{
		"version": "1.0.0",
		"defaults": {"llm": {"backend": "ollama", "model": "qwen"}},
		"inputs": {"topic": {"type": "string", "required": true}},
		"steps": [
			{"id": "a", "prompt": {"user": "{{ inputs.topic }}"}, "outputs": {"out": "{{ response.text }}"}}
		],
		"output": {"fields": ["steps.a.outputs.out"]}
	}`
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := run([]string{"doctor", "-f", path, "--input", "topic=ok", "--mock"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "[OK] pipeline validation") || !strings.Contains(got, "[OK] llm providers") {
		t.Fatalf("unexpected doctor output:\n%s", got)
	}
}

func TestCLIToolsInspectAndSchemaDocs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pipeline.json")
	var out bytes.Buffer
	var stderr bytes.Buffer
	if err := run([]string{"init", path}, &out, &stderr); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	if err := run([]string{"tools", "-f", path, "--mock"}, &out, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Tools") || !strings.Contains(out.String(), "filesystem.write_file") {
		t.Fatalf("unexpected tools output:\n%s", out.String())
	}

	audit := filepath.Join(dir, "run.jsonl")
	events := strings.Join([]string{
		`{"ts":"2026-05-10T10:00:00Z","kind":"run_start","run_id":"run_test"}`,
		`{"ts":"2026-05-10T10:00:01Z","kind":"step_start","step_id":"a"}`,
		`{"ts":"2026-05-10T10:00:02Z","kind":"step_end","step_id":"a","attempts":1}`,
		`{"ts":"2026-05-10T10:00:03Z","kind":"run_end","status":"success"}`,
	}, "\n")
	if err := os.WriteFile(audit, []byte(events), 0644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := run([]string{"inspect", "run", audit}, &out, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Run Inspection") || !strings.Contains(out.String(), "a: success") {
		t.Fatalf("unexpected inspect output:\n%s", out.String())
	}

	out.Reset()
	if err := run([]string{"schema-docs", "--schema", filepath.Join("..", "..", "schemas", "pipeline", "v1.json")}, &out, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Top-Level Fields") || !strings.Contains(out.String(), "`steps`") {
		t.Fatalf("unexpected schema docs:\n%s", out.String())
	}
}

func TestRunRedactsSensitiveOutput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pipeline.json")
	body := `{
		"version": "1.0.0",
		"defaults": {"llm": {"backend": "ollama", "model": "qwen"}},
		"inputs": {"api_key": {"type": "string", "required": true}},
		"steps": [
			{"id": "a", "prompt": {"user": "return {{ inputs.api_key }}"}, "outputs": {"out": "{{ response.text }}"}}
		],
		"output": {"fields": ["steps.a.outputs.out"]}
	}`
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := run([]string{"run", "-f", path, "--input", "api_key=secret-value-12345", "--mock", "--json", "--no-audit", "--output-dir", t.TempDir()}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "secret-value-12345") {
		t.Fatalf("secret leaked in run output:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "[REDACTED]") {
		t.Fatalf("expected redaction marker:\n%s", out.String())
	}
}
