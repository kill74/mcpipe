package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"mcpipe/internal/config"
	"mcpipe/internal/llm"
	"mcpipe/internal/mcp"
	"mcpipe/internal/notify"
	"mcpipe/internal/security"
	pipetemplate "mcpipe/internal/template"
)

type Engine struct {
	Pipeline       *config.Pipeline
	Inputs         map[string]any
	LLM            llm.Client
	MCP            mcp.Manager
	Now            func() time.Time
	Security       *security.Policy
	ProgressWriter io.Writer
}

type RunResult struct {
	RunID       string                `json:"run_id"`
	StartedAt   time.Time             `json:"started_at"`
	CompletedAt time.Time             `json:"completed_at"`
	Steps       map[string]StepResult `json:"steps"`
	StepOrder   []string              `json:"step_order"`
	Outputs     []FieldOutput         `json:"outputs"`
}

type StepResult struct {
	ID         string            `json:"id"`
	Name       string            `json:"name,omitempty"`
	Outputs    map[string]string `json:"outputs"`
	Response   StepResponse      `json:"response"`
	Attempts   int               `json:"attempts"`
	DurationMS int64             `json:"duration_ms"`
	ToolCalls  int               `json:"tool_calls"`
	Skipped    bool              `json:"skipped"`
	Error      string            `json:"error,omitempty"`
}

type StepResponse struct {
	Text        string           `json:"text"`
	ToolResults []map[string]any `json:"tool_results,omitempty"`
	Usage       llm.Usage        `json:"usage"`
}

type FieldOutput struct {
	Field string `json:"field"`
	Value string `json:"value"`
}

type UsageSummary struct {
	DurationMS   int64 `json:"duration_ms"`
	Steps        int   `json:"steps"`
	Attempts     int   `json:"attempts"`
	ToolCalls    int   `json:"tool_calls"`
	InputTokens  int   `json:"input_tokens"`
	OutputTokens int   `json:"output_tokens"`
}

func Summarize(result *RunResult) UsageSummary {
	summary := UsageSummary{
		DurationMS: result.CompletedAt.Sub(result.StartedAt).Milliseconds(),
		Steps:      len(result.Steps),
	}
	for _, step := range result.Steps {
		summary.Attempts += step.Attempts
		summary.ToolCalls += step.ToolCalls
		summary.InputTokens += step.Response.Usage.InputTokens
		summary.OutputTokens += step.Response.Usage.OutputTokens
	}
	return summary
}

func (e *Engine) Run(ctx context.Context) (*RunResult, error) {
	if e.Pipeline == nil {
		return nil, errors.New("pipeline is required")
	}
	if e.LLM == nil {
		return nil, errors.New("llm client is required")
	}
	if e.MCP == nil {
		return nil, errors.New("mcp manager is required")
	}
	now := e.now()
	if e.Security == nil {
		policy := security.DefaultPolicy()
		e.Security = &policy
	}
	e.Security.Normalize()
	result := &RunResult{
		RunID:     fmt.Sprintf("run_%d", now.UnixNano()),
		StartedAt: now,
		Steps:     map[string]StepResult{},
	}
	auditor, err := security.NewAuditor(e.Security.AuditDir, result.RunID, e.Security.Redactor, e.Security.NoAudit)
	if err != nil {
		return nil, err
	}
	defer auditor.Close()
	auditor.Event("run_start", map[string]any{
		"run_id": result.RunID,
		"inputs": e.Inputs,
	})
	levels, err := Levels(e.Pipeline.Steps)
	if err != nil {
		return nil, err
	}
	stepOutputs := map[string]map[string]string{}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	for _, level := range levels {
		type stepOutcome struct {
			step   config.Step
			result StepResult
			err    error
		}
		outcomes := make(chan stepOutcome, len(level))
		var wg sync.WaitGroup
		sem := make(chan struct{}, e.Security.MaxConcurrentSteps)
		for _, step := range level {
			step := step
			wg.Add(1)
			go func() {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				auditor.Event("step_start", map[string]any{"step_id": step.ID})
				res, err := e.executeStep(runCtx, step, snapshotOutputs(stepOutputs))
				fields := map[string]any{"step_id": step.ID, "attempts": res.Attempts, "duration_ms": res.DurationMS, "skipped": res.Skipped}
				if err != nil {
					fields["error"] = err.Error()
				}
				for name, value := range res.Outputs {
					fields["output_"+name+"_sha256"] = security.HashString(value)
				}
				auditor.Event("step_end", fields)
				outcomes <- stepOutcome{step: step, result: res, err: err}
			}()
		}
		wg.Wait()
		close(outcomes)

		var levelErr error
		collected := make([]stepOutcome, 0, len(level))
		for outcome := range outcomes {
			collected = append(collected, outcome)
		}
		sort.Slice(collected, func(i, j int) bool { return collected[i].step.ID < collected[j].step.ID })
		for _, outcome := range collected {
			res := outcome.result
			if outcome.err != nil {
				if fallback, ok := e.Pipeline.ErrorHandling.FallbackSteps[outcome.step.ID]; ok && fallback.SkipIfError {
					res = skippedResult(outcome.step, outcome.err)
				} else if levelErr == nil {
					levelErr = fmt.Errorf("step %q failed: %w", outcome.step.ID, outcome.err)
				}
			}
			result.Steps[outcome.step.ID] = res
			result.StepOrder = append(result.StepOrder, outcome.step.ID)
		}
		for _, outcome := range sortedLevelOutcomes(result.Steps, level) {
			stepOutputs[outcome.ID] = outcome.Outputs
		}
		if levelErr != nil {
			cancel()
			result.CompletedAt = e.now()
			auditor.Event("run_error", map[string]any{"error": levelErr.Error()})
			e.sendFailureNotification(result, "", levelErr.Error())
			return result, levelErr
		}
	}

	result.CompletedAt = e.now()
	outputs, err := ResolveOutputFields(e.Pipeline, stepOutputs)
	if err != nil {
		auditor.Event("run_error", map[string]any{"error": err.Error()})
		e.sendFailureNotification(result, "", err.Error())
		return result, err
	}
	result.Outputs = outputs
	outputHashes := map[string]string{}
	for _, output := range outputs {
		outputHashes[output.Field] = security.HashString(output.Value)
	}
	auditor.Event("run_end", map[string]any{"duration_ms": result.CompletedAt.Sub(result.StartedAt).Milliseconds(), "output_hashes": outputHashes})
	return result, nil
}

func (e *Engine) sendFailureNotification(result *RunResult, failedStep, errMsg string) {
	n := e.Pipeline.ErrorHandling.OnPipelineFailure
	if n == nil || n.Notify == nil {
		return
	}
	cfg := *n.Notify
	if cfg.URL == "" && cfg.Channel == "" {
		return
	}
	usage := Summarize(result)
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	_ = notify.Send(ctx, cfg, e.Pipeline.Schema, result.RunID, failedStep, errMsg, result.StartedAt, result.CompletedAt, usage.Attempts, usage.ToolCalls, usage.InputTokens, usage.OutputTokens)
}

func (e *Engine) executeStep(ctx context.Context, step config.Step, stepOutputs map[string]map[string]string) (StepResult, error) {
	effective := e.Pipeline.EffectiveStep(step)
	attempts := effective.Retry.MaxAttempts
	if attempts <= 0 {
		attempts = 1
	}
	started := e.now()
	var lastErr error
	usedAttempts := 0
	for attempt := 1; attempt <= attempts; attempt++ {
		usedAttempts = attempt
		attemptCtx, cancel := context.WithTimeout(ctx, time.Duration(effective.TimeoutMS)*time.Millisecond)
		res, err := e.executeStepOnce(attemptCtx, effective, stepOutputs)
		cancel()
		res.Attempts = attempt
		res.DurationMS = e.now().Sub(started).Milliseconds()
		if err == nil {
			return res, nil
		}
		lastErr = err
		if attempt < attempts && shouldRetry(lastErr, effective.Retry) {
			sleepBackoff(ctx, effective.Retry, attempt)
			continue
		}
		break
	}
	return StepResult{
		ID:         step.ID,
		Name:       step.Name,
		Outputs:    emptyOutputs(step),
		Attempts:   usedAttempts,
		DurationMS: e.now().Sub(started).Milliseconds(),
		Error:      lastErr.Error(),
	}, lastErr
}

func (e *Engine) executeStepOnce(ctx context.Context, effective config.EffectiveStep, stepOutputs map[string]map[string]string) (StepResult, error) {
	step := effective.Step
	tplCtx := pipetemplate.Context{
		Inputs:      e.Inputs,
		StepOutputs: stepOutputs,
		Now:         e.now(),
		Env:         envMap(),
	}
	system, err := pipetemplate.RenderString(step.Prompt.System, tplCtx)
	if err != nil {
		return StepResult{}, fmt.Errorf("render system prompt: %w", err)
	}
	user, err := pipetemplate.RenderString(step.Prompt.User, tplCtx)
	if err != nil {
		return StepResult{}, fmt.Errorf("render user prompt: %w", err)
	}
	if err := e.Security.AuthorizePrompt(step.ID, system, user); err != nil {
		return StepResult{}, err
	}

	allowedTools, err := e.MCP.AllowedTools(ctx, step.Tools)
	if err != nil {
		return StepResult{}, err
	}
	reqTools := make([]llm.ToolDefinition, 0, len(allowedTools))
	for _, tool := range allowedTools {
		reqTools = append(reqTools, llm.ToolDefinition{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: tool.InputSchema,
		})
	}

	req := llm.Request{
		Backend:     effective.LLM.Backend,
		Model:       effective.LLM.Model,
		Temperature: effective.LLM.Temperature,
		MaxTokens:   intValue(effective.LLM.MaxTokens),
		Stream:      boolValue(effective.LLM.Stream),
		System:      system,
		User:        user,
	}
	if req.Stream && e.ProgressWriter != nil {
		req.Progress = func(chunk string) {
			fmt.Fprint(e.ProgressWriter, chunk)
		}
	}

	var response llm.Response
	var toolResults []llm.ToolResult
	var renderedToolResults []map[string]any
	if step.Agent != nil && step.Agent.Enabled {
		maxIterations := step.Agent.MaxIterations
		if maxIterations <= 0 {
			maxIterations = 1
		}
		stopped := false
		for i := 0; i < maxIterations; i++ {
			req.Tools = reqTools
			req.ToolResults = toolResults
			response, err = e.LLM.Complete(ctx, req)
			if err != nil {
				return StepResult{}, err
			}
			if err := e.Security.AuthorizeResponse(step.ID, response.Text); err != nil {
				return StepResult{}, err
			}
			if len(response.ToolCalls) == 0 {
				stopped = true
				break
			}
			for _, call := range response.ToolCalls {
				if !toolInList(call.Name, allowedTools) {
					return StepResult{}, fmt.Errorf("llm requested disallowed tool %q", call.Name)
				}
				authorizedArgs, authErr := e.Security.AuthorizeToolCall(call.Name, call.Arguments)
				if authErr != nil {
					return StepResult{}, authErr
				}
				toolResult, err := e.MCP.Call(ctx, call.Name, authorizedArgs)
				data := map[string]any{}
				if err != nil {
					data["error"] = err.Error()
					toolResults = append(toolResults, llm.ToolResult{Name: call.Name, Result: data, Error: err.Error()})
					renderedToolResults = append(renderedToolResults, data)
					continue
				}
				for key, value := range toolResult.Data {
					data[key] = value
				}
				if err := e.Security.AuthorizeToolResult(call.Name, data); err != nil {
					return StepResult{}, err
				}
				toolResults = append(toolResults, llm.ToolResult{Name: call.Name, Result: data})
				renderedToolResults = append(renderedToolResults, data)
			}
		}
		if !stopped {
			return StepResult{}, fmt.Errorf("agent max_iterations reached before stop condition %q", step.Agent.StopOn)
		}
	} else {
		response, err = e.LLM.Complete(ctx, req)
		if err != nil {
			return StepResult{}, err
		}
		if err := e.Security.AuthorizeResponse(step.ID, response.Text); err != nil {
			return StepResult{}, err
		}
	}

	responseView := pipetemplate.Response{Text: response.Text, ToolResults: renderedToolResults}
	outputs, err := evaluateStepOutputs(step, e.Inputs, stepOutputs, responseView, e.now())
	if err != nil {
		return StepResult{}, err
	}
	return StepResult{
		ID:      step.ID,
		Name:    step.Name,
		Outputs: outputs,
		Response: StepResponse{
			Text:        response.Text,
			ToolResults: renderedToolResults,
			Usage:       response.Usage,
		},
		ToolCalls: len(renderedToolResults),
	}, nil
}

func evaluateStepOutputs(step config.Step, inputs map[string]any, stepOutputs map[string]map[string]string, response pipetemplate.Response, now time.Time) (map[string]string, error) {
	out := map[string]string{}
	ctx := pipetemplate.Context{Inputs: inputs, StepOutputs: stepOutputs, Response: response, Now: now, Env: envMap()}
	for name, expr := range step.Outputs {
		value, err := pipetemplate.RenderString(expr, ctx)
		if err != nil {
			return nil, fmt.Errorf("evaluate output %q: %w", name, err)
		}
		out[name] = value
	}
	return out, nil
}

func ResolveOutputFields(p *config.Pipeline, stepOutputs map[string]map[string]string) ([]FieldOutput, error) {
	fields := p.Output.Fields
	if len(fields) == 0 {
		for _, step := range p.Steps {
			for name := range step.Outputs {
				fields = append(fields, "steps."+step.ID+".outputs."+name)
			}
		}
		sort.Strings(fields)
	}
	out := make([]FieldOutput, 0, len(fields))
	for _, field := range fields {
		parts := strings.Split(field, ".")
		if len(parts) != 4 || parts[0] != "steps" || parts[2] != "outputs" {
			return nil, fmt.Errorf("invalid output field %q", field)
		}
		outputs, ok := stepOutputs[parts[1]]
		if !ok {
			return nil, fmt.Errorf("step %q outputs are not available", parts[1])
		}
		value, ok := outputs[parts[3]]
		if !ok {
			return nil, fmt.Errorf("step %q output %q is not available", parts[1], parts[3])
		}
		out = append(out, FieldOutput{Field: field, Value: value})
	}
	return out, nil
}

func skippedResult(step config.Step, err error) StepResult {
	return StepResult{
		ID:      step.ID,
		Name:    step.Name,
		Outputs: emptyOutputs(step),
		Skipped: true,
		Error:   err.Error(),
	}
}

func emptyOutputs(step config.Step) map[string]string {
	out := map[string]string{}
	for name := range step.Outputs {
		out[name] = ""
	}
	return out
}

func snapshotOutputs(in map[string]map[string]string) map[string]map[string]string {
	out := make(map[string]map[string]string, len(in))
	for stepID, outputs := range in {
		copyOutputs := make(map[string]string, len(outputs))
		for name, value := range outputs {
			copyOutputs[name] = value
		}
		out[stepID] = copyOutputs
	}
	return out
}

func sortedLevelOutcomes(results map[string]StepResult, level []config.Step) []StepResult {
	out := make([]StepResult, 0, len(level))
	for _, step := range level {
		out = append(out, results[step.ID])
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

func sleepBackoff(ctx context.Context, retry config.RetryPolicy, attempt int) {
	base := time.Duration(retry.BackoffBaseMS) * time.Millisecond
	if base <= 0 || retry.Backoff == "none" {
		return
	}
	delay := base
	if retry.Backoff == "exponential" {
		delay = base * time.Duration(1<<(attempt-1))
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

func shouldRetry(err error, retry config.RetryPolicy) bool {
	if err == nil {
		return false
	}
	if len(retry.RetryableErrors) == 0 {
		return true
	}
	kind := classifyError(err)
	for _, allowed := range retry.RetryableErrors {
		if strings.EqualFold(allowed, kind) {
			return true
		}
	}
	return false
}

func classifyError(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "timeout"
	}
	text := strings.ToLower(err.Error())
	switch {
	case strings.Contains(text, "rate_limit"), strings.Contains(text, "rate limit"), strings.Contains(text, "http 429"):
		return "rate_limit"
	case strings.Contains(text, "timeout"), strings.Contains(text, "deadline exceeded"):
		return "timeout"
	case strings.Contains(text, "server_error"), strings.Contains(text, "http 500"), strings.Contains(text, "http 502"), strings.Contains(text, "http 503"), strings.Contains(text, "http 504"):
		return "server_error"
	default:
		return "unknown"
	}
}

func toolInList(name string, tools []mcp.Tool) bool {
	for _, tool := range tools {
		if tool.Name == name {
			return true
		}
	}
	return false
}

func intValue(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}

func boolValue(v *bool) bool {
	if v == nil {
		return false
	}
	return *v
}

func envMap() map[string]string {
	out := make(map[string]string)
	for _, kv := range os.Environ() {
		if key, val, ok := strings.Cut(kv, "="); ok {
			out[key] = val
		}
	}
	return out
}
