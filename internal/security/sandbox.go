package security

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	DefaultOutputDir          = ".mcpipe/outputs"
	DefaultAuditDir           = ".mcpipe/runs"
	DefaultMaxPromptChars     = 200000
	DefaultMaxResponseChars   = 1000000
	DefaultMaxToolResultBytes = 5000000
	DefaultMaxWriteBytes      = 10000000
	DefaultMaxToolCalls       = 25
	DefaultMaxConcurrentSteps = 8
)

type ToolPolicy struct {
	AllowedPaths []string `json:"allowed_paths,omitempty"`
	MaxBytes     int      `json:"max_bytes,omitempty"`
	MaxCalls     int      `json:"max_calls,omitempty"`
}

type Policy struct {
	OutputDir          string
	AuditDir           string
	NoAudit            bool
	MaxPromptChars     int
	MaxResponseChars   int
	MaxToolResultBytes int
	MaxConcurrentSteps int
	ToolPolicies       map[string]ToolPolicy
	Redactor           Redactor

	mu        sync.Mutex
	callCount map[string]int
}

func DefaultPolicy() Policy {
	return Policy{
		OutputDir:          DefaultOutputDir,
		AuditDir:           DefaultAuditDir,
		MaxPromptChars:     DefaultMaxPromptChars,
		MaxResponseChars:   DefaultMaxResponseChars,
		MaxToolResultBytes: DefaultMaxToolResultBytes,
		MaxConcurrentSteps: DefaultMaxConcurrentSteps,
		ToolPolicies:       map[string]ToolPolicy{},
		Redactor:           NewRedactor(),
		callCount:          map[string]int{},
	}
}

func (p *Policy) Normalize() {
	if p.OutputDir == "" {
		p.OutputDir = DefaultOutputDir
	}
	if p.AuditDir == "" {
		p.AuditDir = DefaultAuditDir
	}
	if p.MaxPromptChars <= 0 {
		p.MaxPromptChars = DefaultMaxPromptChars
	}
	if p.MaxResponseChars <= 0 {
		p.MaxResponseChars = DefaultMaxResponseChars
	}
	if p.MaxToolResultBytes <= 0 {
		p.MaxToolResultBytes = DefaultMaxToolResultBytes
	}
	if p.MaxConcurrentSteps <= 0 {
		p.MaxConcurrentSteps = DefaultMaxConcurrentSteps
	}
	if p.ToolPolicies == nil {
		p.ToolPolicies = map[string]ToolPolicy{}
	}
	if p.callCount == nil {
		p.callCount = map[string]int{}
	}
}

func (p *Policy) AuthorizePrompt(stepID, system, user string) error {
	p.Normalize()
	if len(system)+len(user) > p.MaxPromptChars {
		return fmt.Errorf("step %q prompt exceeds max prompt size of %d chars", stepID, p.MaxPromptChars)
	}
	return nil
}

func (p *Policy) AuthorizeResponse(stepID, text string) error {
	p.Normalize()
	if len(text) > p.MaxResponseChars {
		return fmt.Errorf("step %q response exceeds max response size of %d chars", stepID, p.MaxResponseChars)
	}
	return nil
}

func (p *Policy) AuthorizeToolCall(name string, args map[string]any) (map[string]any, error) {
	p.Normalize()
	if err := p.incrementToolCall(name); err != nil {
		return nil, err
	}
	copied := copyArgs(args)
	if strings.EqualFold(name, "filesystem.write_file") || strings.HasSuffix(strings.ToLower(name), ".write_file") {
		pathValue, _ := copied["path"].(string)
		if pathValue == "" {
			pathValue, _ = copied["filename"].(string)
		}
		if pathValue == "" {
			return nil, fmt.Errorf("%s requires a path argument", name)
		}
		resolved, err := p.SandboxPath(name, pathValue)
		if err != nil {
			return nil, err
		}
		content := fmt.Sprint(copied["content"])
		limit := p.policyFor(name).MaxBytes
		if limit <= 0 {
			limit = DefaultMaxWriteBytes
		}
		if len([]byte(content)) > limit {
			return nil, fmt.Errorf("%s content exceeds max write size of %d bytes", name, limit)
		}
		if err := os.MkdirAll(filepath.Dir(resolved), 0755); err != nil {
			return nil, err
		}
		copied["path"] = resolved
	}
	return copied, nil
}

func (p *Policy) AuthorizeToolResult(name string, result map[string]any) error {
	p.Normalize()
	data, err := json.Marshal(result)
	if err != nil {
		return err
	}
	if len(data) > p.MaxToolResultBytes {
		return fmt.Errorf("%s result exceeds max tool result size of %d bytes", name, p.MaxToolResultBytes)
	}
	return nil
}

func (p *Policy) SandboxPath(toolName, requested string) (string, error) {
	p.Normalize()
	policy := p.policyFor(toolName)
	roots := policy.AllowedPaths
	if len(roots) == 0 {
		roots = []string{p.OutputDir}
	}
	absRoots := make([]string, 0, len(roots))
	for _, root := range roots {
		abs, err := filepath.Abs(root)
		if err != nil {
			return "", err
		}
		absRoots = append(absRoots, filepath.Clean(abs))
	}

	candidate := requested
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(absRoots[0], candidate)
	}
	absCandidate, err := filepath.Abs(candidate)
	if err != nil {
		return "", err
	}
	absCandidate = filepath.Clean(absCandidate)
	resolvedCandidate, err := resolveExistingPath(absCandidate)
	if err != nil {
		return "", err
	}
	for _, root := range absRoots {
		resolvedRoot, err := resolveExistingPath(root)
		if err != nil {
			return "", err
		}
		if isWithin(resolvedRoot, resolvedCandidate) {
			return absCandidate, nil
		}
	}
	return "", fmt.Errorf("path %q escapes allowed roots %s", requested, strings.Join(absRoots, ", "))
}

func (p *Policy) incrementToolCall(name string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.callCount[name]++
	limit := p.policyFor(name).MaxCalls
	if limit <= 0 {
		limit = DefaultMaxToolCalls
	}
	if p.callCount[name] > limit {
		return fmt.Errorf("%s exceeded max call count of %d", name, limit)
	}
	return nil
}

func (p *Policy) policyFor(toolName string) ToolPolicy {
	if p.ToolPolicies == nil {
		return ToolPolicy{}
	}
	if policy, ok := p.ToolPolicies[toolName]; ok {
		return policy
	}
	for pattern, policy := range p.ToolPolicies {
		if strings.HasSuffix(pattern, ".*") && strings.HasPrefix(toolName, strings.TrimSuffix(pattern, ".*")+".") {
			return policy
		}
	}
	return ToolPolicy{}
}

func isWithin(root, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	return rel == "." || (rel != "" && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel))
}

func resolveExistingPath(path string) (string, error) {
	path = filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return filepath.Clean(resolved), nil
	}
	current := path
	var missing []string
	for {
		if current == "." || current == string(filepath.Separator) || current == filepath.VolumeName(current)+string(filepath.Separator) {
			break
		}
		if _, err := os.Lstat(current); err == nil {
			resolved, err := filepath.EvalSymlinks(current)
			if err != nil {
				return "", err
			}
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return filepath.Clean(resolved), nil
		}
		missing = append(missing, filepath.Base(current))
		next := filepath.Dir(current)
		if next == current {
			break
		}
		current = next
	}
	return path, nil
}

func copyArgs(args map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range args {
		out[key] = value
	}
	return out
}
