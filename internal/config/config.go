package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
)

type Pipeline struct {
	Schema        string                  `json:"$schema,omitempty"`
	Version       string                  `json:"version"`
	Metadata      *Metadata               `json:"metadata,omitempty"`
	Schedule      *Schedule               `json:"schedule,omitempty"`
	Defaults      Defaults                `json:"defaults,omitempty"`
	Inputs        map[string]InputSpec    `json:"inputs,omitempty"`
	Plugins       map[string]Plugin       `json:"plugins,omitempty"`
	Agents        map[string]AgentProfile `json:"agents,omitempty"`
	MCPServers    map[string]MCPServer    `json:"mcp_servers,omitempty"`
	Steps         []Step                  `json:"steps"`
	ErrorHandling ErrorHandling           `json:"error_handling,omitempty"`
	Policy        map[string]ToolPolicy   `json:"policy,omitempty"`
	Output        Output                  `json:"output,omitempty"`
	Observability *Observability          `json:"observability,omitempty"`
}

type Metadata struct {
	ID          string   `json:"id,omitempty"`
	Name        string   `json:"name,omitempty"`
	Description string   `json:"description,omitempty"`
	Author      string   `json:"author,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	CreatedAt   string   `json:"created_at,omitempty"`
	UpdatedAt   string   `json:"updated_at,omitempty"`
}

type Schedule struct {
	Enabled   bool   `json:"enabled,omitempty"`
	Cron      string `json:"cron,omitempty"`
	Timezone  string `json:"timezone,omitempty"`
	OnFailure string `json:"on_failure,omitempty"`
}

type Defaults struct {
	TimeoutMS int          `json:"timeout_ms,omitempty"`
	Retry     *RetryPolicy `json:"retry,omitempty"`
	LLM       *LLMConfig   `json:"llm,omitempty"`
}

type RetryPolicy struct {
	MaxAttempts     int      `json:"max_attempts,omitempty"`
	Backoff         string   `json:"backoff,omitempty"`
	BackoffBaseMS   int      `json:"backoff_base_ms,omitempty"`
	RetryableErrors []string `json:"retryable_errors,omitempty"`
}

type LLMConfig struct {
	Backend     string   `json:"backend,omitempty"`
	Model       string   `json:"model,omitempty"`
	Temperature *float64 `json:"temperature,omitempty"`
	MaxTokens   *int     `json:"max_tokens,omitempty"`
	Stream      *bool    `json:"stream,omitempty"`
}

type InputSpec struct {
	Type        string   `json:"type"`
	Description string   `json:"description,omitempty"`
	Required    bool     `json:"required,omitempty"`
	Example     any      `json:"example,omitempty"`
	Values      []string `json:"values,omitempty"`
	Default     any      `json:"default,omitempty"`
	Pattern     string   `json:"pattern,omitempty"`
}

type MCPServer struct {
	Transport   string            `json:"transport"`
	Command     string            `json:"command,omitempty"`
	Args        []string          `json:"args,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	URL         string            `json:"url,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	HealthCheck *HealthCheck      `json:"health_check,omitempty"`
	Reconnect   *Reconnect        `json:"reconnect,omitempty"`
}

type Plugin struct {
	Description string                `json:"description,omitempty"`
	MCPServers  map[string]MCPServer  `json:"mcp_servers,omitempty"`
	Tools       Tools                 `json:"tools,omitempty"`
	Policy      map[string]ToolPolicy `json:"policy,omitempty"`
}

type AgentProfile struct {
	Description string       `json:"description,omitempty"`
	LLM         *LLMConfig   `json:"llm,omitempty"`
	Prompt      *Prompt      `json:"prompt,omitempty"`
	Tools       Tools        `json:"tools,omitempty"`
	Agent       *AgentConfig `json:"agent,omitempty"`
}

type HealthCheck struct {
	Enabled    bool `json:"enabled,omitempty"`
	IntervalMS int  `json:"interval_ms,omitempty"`
}

type Reconnect struct {
	Enabled     bool `json:"enabled,omitempty"`
	MaxAttempts int  `json:"max_attempts,omitempty"`
	DelayMS     int  `json:"delay_ms,omitempty"`
}

type Step struct {
	ID            string            `json:"id"`
	Name          string            `json:"name,omitempty"`
	Description   string            `json:"description,omitempty"`
	ParallelGroup string            `json:"parallel_group,omitempty"`
	DependsOn     []string          `json:"depends_on,omitempty"`
	TimeoutMS     int               `json:"timeout_ms,omitempty"`
	Retry         *RetryPolicy      `json:"retry,omitempty"`
	LLM           *LLMConfig        `json:"llm,omitempty"`
	Prompt        Prompt            `json:"prompt"`
	Tools         Tools             `json:"tools,omitempty"`
	Plugins       []string          `json:"plugins,omitempty"`
	AgentRef      string            `json:"agent_ref,omitempty"`
	Agent         *AgentConfig      `json:"agent,omitempty"`
	Outputs       map[string]string `json:"outputs"`
}

type Prompt struct {
	System string `json:"system,omitempty"`
	User   string `json:"user"`
}

type Tools struct {
	Allow []string `json:"allow,omitempty"`
	Deny  []string `json:"deny,omitempty"`
}

type AgentConfig struct {
	Enabled       bool   `json:"enabled,omitempty"`
	MaxIterations int    `json:"max_iterations,omitempty"`
	StopOn        string `json:"stop_on,omitempty"`
}

type ErrorHandling struct {
	OnStepFailure     string                  `json:"on_step_failure,omitempty"`
	FallbackSteps     map[string]FallbackStep `json:"fallback_steps,omitempty"`
	OnPipelineFailure *OnPipelineFailure      `json:"on_pipeline_failure,omitempty"`
}

type FallbackStep struct {
	SkipIfError bool   `json:"skip_if_error,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

type ToolPolicy struct {
	AllowedPaths []string `json:"allowed_paths,omitempty"`
	MaxBytes     int      `json:"max_bytes,omitempty"`
	MaxCalls     int      `json:"max_calls,omitempty"`
}

type OnPipelineFailure struct {
	Notify *Notify `json:"notify,omitempty"`
}

type Notify struct {
	Channel           string `json:"channel,omitempty"`
	IncludeRunID      bool   `json:"include_run_id,omitempty"`
	IncludeFailedStep bool   `json:"include_failed_step,omitempty"`
}

type Output struct {
	Format             string   `json:"format,omitempty"`
	Destination        string   `json:"destination,omitempty"`
	IncludeRunMetadata bool     `json:"include_run_metadata,omitempty"`
	Fields             []string `json:"fields,omitempty"`
}

type Observability struct {
	RunHistory *RunHistory `json:"run_history,omitempty"`
	Metrics    *Metrics    `json:"metrics,omitempty"`
	DryRun     *DryRun     `json:"dry_run,omitempty"`
}

type RunHistory struct {
	Enabled       bool   `json:"enabled,omitempty"`
	Storage       string `json:"storage,omitempty"`
	Path          string `json:"path,omitempty"`
	RetentionDays int    `json:"retention_days,omitempty"`
}

type Metrics struct {
	Enabled bool     `json:"enabled,omitempty"`
	Emit    []string `json:"emit,omitempty"`
}

type DryRun struct {
	ShowPromptPreviews   bool `json:"show_prompt_previews,omitempty"`
	ShowToolCallPlan     bool `json:"show_tool_call_plan,omitempty"`
	ShowVariableBindings bool `json:"show_variable_bindings,omitempty"`
}

type EffectiveStep struct {
	Step      Step
	TimeoutMS int
	Retry     RetryPolicy
	LLM       LLMConfig
}

func LoadFile(path string) (*Pipeline, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Load(f)
}

func Load(r io.Reader) (*Pipeline, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var p Pipeline
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("decode pipeline: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err == nil {
		return nil, errors.New("decode pipeline: unexpected trailing JSON")
	} else if !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("decode pipeline: %w", err)
	}
	normalize(&p)
	return &p, nil
}

func normalize(p *Pipeline) {
	if p.Inputs == nil {
		p.Inputs = map[string]InputSpec{}
	}
	if p.MCPServers == nil {
		p.MCPServers = map[string]MCPServer{}
	}
	if p.Plugins == nil {
		p.Plugins = map[string]Plugin{}
	}
	if p.Agents == nil {
		p.Agents = map[string]AgentProfile{}
	}
	if p.ErrorHandling.FallbackSteps == nil {
		p.ErrorHandling.FallbackSteps = map[string]FallbackStep{}
	}
	if p.Policy == nil {
		p.Policy = map[string]ToolPolicy{}
	}
	if p.Output.Format == "" {
		p.Output.Format = "markdown"
	}
	if p.Output.Destination == "" {
		p.Output.Destination = "stdout"
	}
	if p.ErrorHandling.OnStepFailure == "" {
		p.ErrorHandling.OnStepFailure = "fail_fast"
	}
	for i := range p.Steps {
		if p.Steps[i].Outputs == nil {
			p.Steps[i].Outputs = map[string]string{}
		}
	}
	applyExtensions(p)
}

func (p *Pipeline) StepByID(id string) (Step, bool) {
	for _, step := range p.Steps {
		if step.ID == id {
			return step, true
		}
	}
	return Step{}, false
}

func (p *Pipeline) EffectiveStep(step Step) EffectiveStep {
	step = p.ResolveStep(step)
	timeout := p.Defaults.TimeoutMS
	if timeout == 0 {
		timeout = 30000
	}
	if step.TimeoutMS > 0 {
		timeout = step.TimeoutMS
	}

	retry := RetryPolicy{MaxAttempts: 1, Backoff: "none"}
	if p.Defaults.Retry != nil {
		retry = *p.Defaults.Retry
	}
	if step.Retry != nil {
		retry = mergeRetry(retry, *step.Retry)
	}
	if retry.MaxAttempts <= 0 {
		retry.MaxAttempts = 1
	}
	if retry.Backoff == "" {
		retry.Backoff = "none"
	}

	llm := LLMConfig{}
	if p.Defaults.LLM != nil {
		llm = *p.Defaults.LLM
	}
	if step.LLM != nil {
		llm = mergeLLM(llm, *step.LLM)
	}
	return EffectiveStep{Step: step, TimeoutMS: timeout, Retry: retry, LLM: llm}
}

func (p *Pipeline) ResolveStep(step Step) Step {
	resolved := step
	for _, name := range step.Plugins {
		plugin, ok := p.Plugins[name]
		if !ok {
			continue
		}
		resolved.Tools = mergeTools(plugin.Tools, resolved.Tools)
	}
	if step.AgentRef != "" {
		profile, ok := p.Agents[step.AgentRef]
		if ok {
			if resolved.LLM == nil && profile.LLM != nil {
				copy := *profile.LLM
				resolved.LLM = &copy
			} else if resolved.LLM != nil && profile.LLM != nil {
				merged := mergeLLM(*profile.LLM, *resolved.LLM)
				resolved.LLM = &merged
			}
			if profile.Prompt != nil {
				if resolved.Prompt.System == "" {
					resolved.Prompt.System = profile.Prompt.System
				}
				if resolved.Prompt.User == "" {
					resolved.Prompt.User = profile.Prompt.User
				}
			}
			resolved.Tools = mergeTools(profile.Tools, resolved.Tools)
			if resolved.Agent == nil && profile.Agent != nil {
				copy := *profile.Agent
				resolved.Agent = &copy
			} else if resolved.Agent != nil && profile.Agent != nil {
				merged := mergeAgent(*profile.Agent, *resolved.Agent)
				resolved.Agent = &merged
			}
		}
	}
	return resolved
}

func applyExtensions(p *Pipeline) {
	for _, plugin := range p.Plugins {
		for name, server := range plugin.MCPServers {
			if _, exists := p.MCPServers[name]; !exists {
				p.MCPServers[name] = server
			}
		}
		for rule, policy := range plugin.Policy {
			if _, exists := p.Policy[rule]; !exists {
				p.Policy[rule] = policy
			}
		}
	}
	for i := range p.Steps {
		p.Steps[i] = p.ResolveStep(p.Steps[i])
	}
}

func mergeTools(base, override Tools) Tools {
	out := Tools{}
	out.Allow = append(out.Allow, base.Allow...)
	out.Allow = append(out.Allow, override.Allow...)
	out.Deny = append(out.Deny, base.Deny...)
	out.Deny = append(out.Deny, override.Deny...)
	out.Allow = uniqueStrings(out.Allow)
	out.Deny = uniqueStrings(out.Deny)
	return out
}

func mergeAgent(base, override AgentConfig) AgentConfig {
	out := base
	if override.Enabled {
		out.Enabled = true
	}
	if override.MaxIterations > 0 {
		out.MaxIterations = override.MaxIterations
	}
	if override.StopOn != "" {
		out.StopOn = override.StopOn
	}
	return out
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func mergeRetry(base, override RetryPolicy) RetryPolicy {
	out := base
	if override.MaxAttempts > 0 {
		out.MaxAttempts = override.MaxAttempts
	}
	if override.Backoff != "" {
		out.Backoff = override.Backoff
	}
	if override.BackoffBaseMS > 0 {
		out.BackoffBaseMS = override.BackoffBaseMS
	}
	if len(override.RetryableErrors) > 0 {
		out.RetryableErrors = override.RetryableErrors
	}
	return out
}

func mergeLLM(base, override LLMConfig) LLMConfig {
	out := base
	if override.Backend != "" {
		out.Backend = override.Backend
	}
	if override.Model != "" {
		out.Model = override.Model
	}
	if override.Temperature != nil {
		out.Temperature = override.Temperature
	}
	if override.MaxTokens != nil {
		out.MaxTokens = override.MaxTokens
	}
	if override.Stream != nil {
		out.Stream = override.Stream
	}
	return out
}

func (p *Pipeline) ResolveInputs(raw map[string]string) (map[string]any, error) {
	resolved := make(map[string]any, len(p.Inputs))
	names := make([]string, 0, len(p.Inputs))
	for name := range p.Inputs {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		spec := p.Inputs[name]
		value, ok := raw[name]
		if !ok && spec.Default != nil {
			value = fmt.Sprint(spec.Default)
			ok = true
		}
		if !ok {
			if spec.Required {
				return nil, fmt.Errorf("input %q is required", name)
			}
			continue
		}
		if err := validateInputValue(name, spec, value); err != nil {
			return nil, err
		}
		resolved[name] = value
	}

	for name := range raw {
		if _, ok := p.Inputs[name]; !ok {
			return nil, fmt.Errorf("unknown input %q", name)
		}
	}
	return resolved, nil
}

func (p *Pipeline) RuntimeWarnings() []string {
	var warnings []string
	if p.Schedule != nil && p.Schedule.Enabled {
		warnings = append(warnings, "schedule.enabled is parsed but no scheduler daemon runs in v1")
	}
	for name, server := range p.MCPServers {
		if server.Transport == "sse" {
			warnings = append(warnings, fmt.Sprintf("mcp server %q uses sse transport, which is parsed but not executable in v1", name))
		}
	}
	if p.Observability != nil {
		if p.Observability.RunHistory != nil && p.Observability.RunHistory.Enabled {
			warnings = append(warnings, "observability.run_history is parsed but SQLite history is not written in v1")
		}
		if p.Observability.Metrics != nil && p.Observability.Metrics.Enabled {
			warnings = append(warnings, "observability.metrics is parsed; metrics are retained in memory but not emitted in v1")
		}
	}
	if p.Output.Destination != "" && p.Output.Destination != "stdout" {
		warnings = append(warnings, fmt.Sprintf("output.destination %q is parsed but not executable in v1", p.Output.Destination))
	}
	sort.Strings(warnings)
	return warnings
}

func validateInputValue(name string, spec InputSpec, value string) error {
	switch spec.Type {
	case "string":
	case "enum":
		if len(spec.Values) == 0 {
			return fmt.Errorf("input %q enum must declare values", name)
		}
		found := false
		for _, allowed := range spec.Values {
			if value == allowed {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("input %q must be one of %s", name, strings.Join(spec.Values, ", "))
		}
	default:
		return fmt.Errorf("input %q has unsupported type %q", name, spec.Type)
	}
	if spec.Pattern != "" {
		re, err := regexp.Compile(spec.Pattern)
		if err != nil {
			return fmt.Errorf("input %q pattern is invalid: %w", name, err)
		}
		if !re.MatchString(value) {
			return fmt.Errorf("input %q does not match pattern %q", name, spec.Pattern)
		}
	}
	return nil
}
