package runtime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mcpipe/internal/config"
	"mcpipe/internal/llm"
	"mcpipe/internal/mcp"
	"mcpipe/internal/sample"
	"mcpipe/internal/security"
)

func TestRunSampleWithMocks(t *testing.T) {
	p, err := config.Load(strings.NewReader(sample.ResearchDigestPipeline))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	inputs, err := p.ResolveInputs(map[string]string{"topic": "Quantum Computing"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 5, 9, 8, 0, 0, 0, time.UTC)
	policy := security.DefaultPolicy()
	policy.OutputDir = t.TempDir()
	policy.AuditDir = t.TempDir()
	engine := &Engine{
		Pipeline: p,
		Inputs:   inputs,
		LLM:      llm.NewRouter(true),
		MCP:      mcp.NewMockManager(p.MCPServers),
		Now:      func() time.Time { return now },
		Security: &policy,
	}
	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(result.Steps["persist"].Outputs["file_path"], "digest-quantum-computing-20260509.md") {
		t.Fatalf("unexpected file path: %#v", result.Steps["persist"].Outputs)
	}
	if result.Steps["persist"].ToolCalls != 1 {
		t.Fatalf("expected persist tool call count, got %d", result.Steps["persist"].ToolCalls)
	}
	out := FormatOutput(p, result)
	if !strings.Contains(out, "step_count: 6") || !strings.Contains(out, "steps.synthesis.outputs.digest") || !strings.Contains(out, "digest-quantum-computing-20260509.md") {
		t.Fatalf("unexpected formatted output:\n%s", out)
	}
}

func TestFallbackSkipProvidesEmptyOutputs(t *testing.T) {
	p := &config.Pipeline{
		Version:  "1.0.0",
		Defaults: config.Defaults{LLM: &config.LLMConfig{Backend: "mock", Model: "mock"}},
		Steps: []config.Step{
			{ID: "bad", Prompt: config.Prompt{User: "fail"}, Outputs: map[string]string{"out": "{{ response.text }}"}},
			{ID: "next", DependsOn: []string{"bad"}, Prompt: config.Prompt{User: "next {{ steps.bad.outputs.out }}"}, Outputs: map[string]string{"out": "{{ response.text }}"}},
		},
		ErrorHandling: config.ErrorHandling{FallbackSteps: map[string]config.FallbackStep{"bad": {SkipIfError: true}}},
		Output:        config.Output{Fields: []string{"steps.next.outputs.out"}},
	}
	engine := &Engine{
		Pipeline: p,
		Inputs:   map[string]any{},
		LLM:      failOnceClient{},
		MCP:      mcp.NewMockManager(nil),
		Now:      time.Now,
		Security: testPolicy(t),
	}
	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Steps["bad"].Skipped {
		t.Fatal("expected bad step to be skipped")
	}
	if result.Steps["next"].Outputs["out"] == "" {
		t.Fatal("expected downstream step to run")
	}
}

type failOnceClient struct{}

func (failOnceClient) Complete(ctx context.Context, req llm.Request) (llm.Response, error) {
	if req.User == "fail" {
		return llm.Response{}, errors.New("boom")
	}
	return llm.Response{Text: "ok: " + req.User}, nil
}

func TestRetryHonorsRetryableErrors(t *testing.T) {
	p := &config.Pipeline{
		Version: "1.0.0",
		Defaults: config.Defaults{
			LLM:   &config.LLMConfig{Backend: "mock", Model: "mock"},
			Retry: &config.RetryPolicy{MaxAttempts: 3, Backoff: "none", RetryableErrors: []string{"timeout"}},
		},
		Steps: []config.Step{
			{ID: "a", Prompt: config.Prompt{User: "a"}, Outputs: map[string]string{"out": "{{ response.text }}"}},
		},
		Output: config.Output{Fields: []string{"steps.a.outputs.out"}},
	}
	client := &countingClient{err: errors.New("http 400: bad request")}
	engine := &Engine{Pipeline: p, Inputs: map[string]any{}, LLM: client, MCP: mcp.NewMockManager(nil), Now: time.Now}
	engine.Security = testPolicy(t)
	_, err := engine.Run(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if client.calls != 1 {
		t.Fatalf("expected one attempt for non-retryable error, got %d", client.calls)
	}
	result, _ := engine.Run(context.Background())
	if result != nil && result.Steps["a"].Attempts > 1 {
		t.Fatalf("expected failed result to report actual attempts, got %d", result.Steps["a"].Attempts)
	}
}

func TestRetryRetriesClassifiedErrors(t *testing.T) {
	p := &config.Pipeline{
		Version: "1.0.0",
		Defaults: config.Defaults{
			LLM:   &config.LLMConfig{Backend: "mock", Model: "mock"},
			Retry: &config.RetryPolicy{MaxAttempts: 3, Backoff: "none", RetryableErrors: []string{"rate_limit"}},
		},
		Steps: []config.Step{
			{ID: "a", Prompt: config.Prompt{User: "a"}, Outputs: map[string]string{"out": "{{ response.text }}"}},
		},
		Output: config.Output{Fields: []string{"steps.a.outputs.out"}},
	}
	client := &countingClient{err: errors.New("http 429: slow down")}
	engine := &Engine{Pipeline: p, Inputs: map[string]any{}, LLM: client, MCP: mcp.NewMockManager(nil), Now: time.Now}
	engine.Security = testPolicy(t)
	_, err := engine.Run(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if client.calls != 3 {
		t.Fatalf("expected three attempts for retryable error, got %d", client.calls)
	}
}

func TestAgentErrorsWhenMaxIterationsNeverStops(t *testing.T) {
	p := &config.Pipeline{
		Version:  "1.0.0",
		Defaults: config.Defaults{LLM: &config.LLMConfig{Backend: "mock", Model: "mock"}},
		MCPServers: map[string]config.MCPServer{
			"fake": {Transport: "stdio", Command: "unused"},
		},
		Steps: []config.Step{
			{
				ID:     "a",
				Prompt: config.Prompt{User: "a"},
				Tools:  config.Tools{Allow: []string{"fake.*"}},
				Agent:  &config.AgentConfig{Enabled: true, MaxIterations: 2, StopOn: "no_tool_call"},
				Outputs: map[string]string{
					"out": "{{ response.text }}",
				},
			},
		},
		Output: config.Output{Fields: []string{"steps.a.outputs.out"}},
	}
	engine := &Engine{
		Pipeline: p,
		Inputs:   map[string]any{},
		LLM:      endlessToolClient{},
		MCP:      mcp.NewMockManager(p.MCPServers),
		Now:      time.Now,
		Security: testPolicy(t),
	}
	_, err := engine.Run(context.Background())
	if err == nil {
		t.Fatal("expected max iteration error")
	}
	if !strings.Contains(err.Error(), "max_iterations") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFilesystemSandboxRejectsEscapes(t *testing.T) {
	p := &config.Pipeline{
		Version:  "1.0.0",
		Defaults: config.Defaults{LLM: &config.LLMConfig{Backend: "mock", Model: "mock"}},
		MCPServers: map[string]config.MCPServer{
			"filesystem": {Transport: "stdio", Command: "unused"},
		},
		Steps: []config.Step{{
			ID:      "persist",
			Prompt:  config.Prompt{User: "write"},
			Tools:   config.Tools{Allow: []string{"filesystem.write_file"}},
			Agent:   &config.AgentConfig{Enabled: true, MaxIterations: 2, StopOn: "no_tool_call"},
			Outputs: map[string]string{"file_path": "{{ response.tool_results[0].path }}"},
		}},
		Output: config.Output{Fields: []string{"steps.persist.outputs.file_path"}},
	}
	policy := testPolicy(t)
	policy.ToolPolicies["filesystem.write_file"] = security.ToolPolicy{AllowedPaths: []string{policy.OutputDir}, MaxCalls: 2}
	engine := &Engine{
		Pipeline: p,
		Inputs:   map[string]any{},
		LLM:      fixedToolClient{path: "../escape.md"},
		MCP:      mcp.NewMockManager(p.MCPServers),
		Now:      time.Now,
		Security: policy,
	}
	_, err := engine.Run(context.Background())
	if err == nil {
		t.Fatal("expected sandbox escape error")
	}
	if !strings.Contains(err.Error(), "escapes allowed roots") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAuditRunEndStoresOutputHashesOnly(t *testing.T) {
	auditDir := t.TempDir()
	p := &config.Pipeline{
		Version:  "1.0.0",
		Defaults: config.Defaults{LLM: &config.LLMConfig{Backend: "mock", Model: "mock"}},
		Steps: []config.Step{
			{ID: "a", Prompt: config.Prompt{User: "secret final"}, Outputs: map[string]string{"out": "{{ response.text }}"}},
		},
		Output: config.Output{Fields: []string{"steps.a.outputs.out"}},
	}
	policy := testPolicy(t)
	policy.AuditDir = auditDir
	engine := &Engine{
		Pipeline: p,
		Inputs:   map[string]any{},
		LLM:      llm.NewRouter(true),
		MCP:      mcp.NewMockManager(nil),
		Now:      time.Now,
		Security: policy,
	}
	if _, err := engine.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(auditDir, "*.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("expected one audit log, got %d", len(files))
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "Mock response") {
		t.Fatalf("audit log leaked final output:\n%s", string(data))
	}
	if !strings.Contains(string(data), "output_hashes") {
		t.Fatalf("audit log missing output hashes:\n%s", string(data))
	}
}

type fixedToolClient struct {
	path string
}

func (c fixedToolClient) Complete(ctx context.Context, req llm.Request) (llm.Response, error) {
	if len(req.ToolResults) > 0 {
		return llm.Response{Text: "done"}, nil
	}
	return llm.Response{Text: "call", ToolCalls: []llm.ToolCall{{
		Name:      "filesystem.write_file",
		Arguments: map[string]any{"path": c.path, "content": "hello"},
	}}}, nil
}

func testPolicy(t *testing.T) *security.Policy {
	t.Helper()
	policy := security.DefaultPolicy()
	policy.OutputDir = t.TempDir()
	policy.AuditDir = t.TempDir()
	return &policy
}

type countingClient struct {
	calls int
	err   error
}

func (c *countingClient) Complete(ctx context.Context, req llm.Request) (llm.Response, error) {
	c.calls++
	if c.err != nil {
		return llm.Response{}, c.err
	}
	return llm.Response{Text: fmt.Sprintf("call %d", c.calls)}, nil
}

type endlessToolClient struct{}

func (endlessToolClient) Complete(ctx context.Context, req llm.Request) (llm.Response, error) {
	return llm.Response{
		Text: "again",
		ToolCalls: []llm.ToolCall{{
			Name:      req.Tools[0].Name,
			Arguments: map[string]any{"query": "x"},
		}},
	}, nil
}

func TestDryRunRendersPlaceholders(t *testing.T) {
	p, err := config.Load(strings.NewReader(sample.ResearchDigestPipeline))
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := p.ResolveInputs(map[string]string{"topic": "Quantum Computing"})
	if err != nil {
		t.Fatal(err)
	}
	out, err := DryRun(p, inputs, time.Date(2026, 5, 9, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Level 1: academic_search, web_search") {
		t.Fatalf("missing level output:\n%s", out)
	}
	if !strings.Contains(out, "<web_search.outputs.web_findings>") {
		t.Fatalf("missing placeholder output:\n%s", out)
	}
}

func TestGraphFormats(t *testing.T) {
	p, err := config.Load(strings.NewReader(sample.ResearchDigestPipeline))
	if err != nil {
		t.Fatal(err)
	}
	mermaid, err := Graph(p, "mermaid")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mermaid, "flowchart TD") || !strings.Contains(mermaid, "web_search --> web_summarize") {
		t.Fatalf("unexpected mermaid graph:\n%s", mermaid)
	}
	dot, err := Graph(p, "dot")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dot, "digraph mcpipe") || !strings.Contains(dot, `"web_search" -> "web_summarize"`) {
		t.Fatalf("unexpected dot graph:\n%s", dot)
	}
}
