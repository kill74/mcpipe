package security

import (
	"fmt"
	"path/filepath"
	"strings"

	"mcpipe/internal/config"
)

type Severity string

const (
	SeverityError Severity = "error"
	SeverityWarn  Severity = "warn"
	SeverityInfo  Severity = "info"
)

type Finding struct {
	Severity Severity `json:"severity"`
	Code     string   `json:"code"`
	Subject  string   `json:"subject"`
	Message  string   `json:"message"`
}

func LintPipeline(p *config.Pipeline, policy *Policy) []Finding {
	if policy == nil {
		defaultPolicy := DefaultPolicy()
		policy = &defaultPolicy
	}
	policy.Normalize()
	var findings []Finding
	add := func(sev Severity, code, subject, msg string) {
		findings = append(findings, Finding{Severity: sev, Code: code, Subject: subject, Message: msg})
	}
	for _, step := range p.Steps {
		effective := p.EffectiveStep(step)
		if effective.TimeoutMS <= 0 {
			add(SeverityError, "missing_timeout", step.ID, "step has no effective timeout")
		}
		if step.Agent != nil && step.Agent.Enabled && step.Agent.MaxIterations > 10 {
			add(SeverityWarn, "high_agent_iterations", step.ID, "agent max_iterations is high; cap loops for live tool use")
		}
		if len(step.Tools.Allow) > 0 && len(step.Tools.Deny) == 0 {
			for _, rule := range step.Tools.Allow {
				if strings.HasSuffix(rule, ".*") {
					add(SeverityWarn, "broad_tool_allow", step.ID, fmt.Sprintf("broad tool rule %q should be narrowed or paired with deny rules", rule))
				}
			}
		}
		if effective.LLM.Backend != "" && len(step.Tools.Allow) > 0 {
			add(SeverityInfo, "live_tool_surface", step.ID, "step can combine LLM output with live tool access; review prompts and tool policy")
		}
	}
	for name, server := range p.MCPServers {
		if server.Transport == "stdio" {
			if server.Command == "npx" && contains(server.Args, "-y") {
				pinned := false
				for _, arg := range server.Args {
					if strings.Contains(arg, "@") && !strings.HasPrefix(arg, "@modelcontextprotocol/") {
						pinned = true
					}
					if strings.Count(arg, "@") >= 2 {
						pinned = true
					}
				}
				if !pinned {
					add(SeverityWarn, "unpinned_npx", "mcp."+name, "npx -y is convenient but should pin package versions for trusted automation")
				}
			}
			for key, value := range server.Env {
				if strings.Contains(value, "${env:") && IsSensitiveName(key) {
					add(SeverityInfo, "secret_env_ref", "mcp."+name, fmt.Sprintf("%s is read from the environment and will be redacted in diagnostics", key))
				}
			}
		}
		if server.Transport == "sse" {
			add(SeverityWarn, "unsupported_sse", "mcp."+name, "SSE transport is accepted in config but not executable in v1")
		}
	}
	for pattern, toolPolicy := range policy.ToolPolicies {
		for _, path := range toolPolicy.AllowedPaths {
			if path == "/" || path == `C:\` || path == filepath.VolumeName(path)+`\` {
				add(SeverityError, "wide_allowed_path", "policy."+pattern, "tool policy allows a filesystem root")
			}
		}
		if toolPolicy.MaxCalls > 100 {
			add(SeverityWarn, "high_tool_call_limit", "policy."+pattern, "max_calls is high for a live tool")
		}
	}
	if len(p.Output.Fields) == 0 {
		add(SeverityInfo, "implicit_outputs", "output", "no output.fields set; all step outputs may be emitted")
	}
	return findings
}

func HasBlockingFindings(findings []Finding) bool {
	for _, finding := range findings {
		if finding.Severity == SeverityError {
			return true
		}
	}
	return false
}

func contains(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}
