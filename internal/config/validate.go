package config

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var stepIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

type ValidationError struct {
	Problems []string
}

func (e *ValidationError) Error() string {
	if len(e.Problems) == 1 {
		return e.Problems[0]
	}
	return fmt.Sprintf("%d validation problems:\n- %s", len(e.Problems), strings.Join(e.Problems, "\n- "))
}

func (p *Pipeline) Validate() error {
	var problems []string
	if strings.TrimSpace(p.Version) == "" {
		problems = append(problems, "version is required")
	}
	if len(p.Steps) == 0 {
		problems = append(problems, "at least one step is required")
	}

	stepIndex := map[string]Step{}
	for i, step := range p.Steps {
		if strings.TrimSpace(step.ID) == "" {
			problems = append(problems, fmt.Sprintf("steps[%d].id is required", i))
			continue
		}
		if !stepIDPattern.MatchString(step.ID) {
			problems = append(problems, fmt.Sprintf("step %q id must match %s", step.ID, stepIDPattern.String()))
		}
		if _, exists := stepIndex[step.ID]; exists {
			problems = append(problems, fmt.Sprintf("duplicate step id %q", step.ID))
		}
		stepIndex[step.ID] = step
		if strings.TrimSpace(step.Prompt.User) == "" {
			problems = append(problems, fmt.Sprintf("step %q prompt.user is required", step.ID))
		}
		if len(step.Outputs) == 0 {
			problems = append(problems, fmt.Sprintf("step %q must declare at least one output", step.ID))
		}
	}

	problems = append(problems, p.validateInputs()...)
	problems = append(problems, p.validateMCPServers()...)
	problems = append(problems, p.validateDependencies(stepIndex)...)
	problems = append(problems, p.validateStepReferences(stepIndex)...)
	problems = append(problems, p.validateStepRuntimeConfig()...)
	problems = append(problems, p.validateToolRules()...)
	problems = append(problems, p.validatePolicyRules()...)
	problems = append(problems, p.validateOutputFields(stepIndex)...)
	problems = append(problems, p.validateFallbacks(stepIndex)...)

	if len(problems) > 0 {
		sort.Strings(problems)
		return &ValidationError{Problems: problems}
	}
	return nil
}

func (p *Pipeline) validateInputs() []string {
	var problems []string
	for name, spec := range p.Inputs {
		if spec.Type != "string" && spec.Type != "enum" {
			problems = append(problems, fmt.Sprintf("input %q has unsupported type %q", name, spec.Type))
		}
		if spec.Type == "enum" {
			if len(spec.Values) == 0 {
				problems = append(problems, fmt.Sprintf("input %q enum must declare values", name))
			}
			if spec.Default != nil {
				value := fmt.Sprint(spec.Default)
				if !contains(spec.Values, value) {
					problems = append(problems, fmt.Sprintf("input %q default %q is not in enum values", name, value))
				}
			}
		}
		if spec.Pattern != "" {
			if _, err := regexp.Compile(spec.Pattern); err != nil {
				problems = append(problems, fmt.Sprintf("input %q pattern is invalid: %v", name, err))
			}
		}
	}
	return problems
}

func (p *Pipeline) validateMCPServers() []string {
	var problems []string
	for name, server := range p.MCPServers {
		switch server.Transport {
		case "stdio":
			if strings.TrimSpace(server.Command) == "" {
				problems = append(problems, fmt.Sprintf("mcp server %q stdio command is required", name))
			}
		case "sse":
			if strings.TrimSpace(server.URL) == "" {
				problems = append(problems, fmt.Sprintf("mcp server %q sse url is required", name))
			}
		default:
			problems = append(problems, fmt.Sprintf("mcp server %q has unsupported transport %q", name, server.Transport))
		}
	}
	return problems
}

func (p *Pipeline) validateDependencies(stepIndex map[string]Step) []string {
	var problems []string
	for _, step := range p.Steps {
		for _, dep := range step.DependsOn {
			if _, ok := stepIndex[dep]; !ok {
				problems = append(problems, fmt.Sprintf("step %q depends on unknown step %q", step.ID, dep))
			}
			if dep == step.ID {
				problems = append(problems, fmt.Sprintf("step %q cannot depend on itself", step.ID))
			}
		}
	}
	if cycle := findCycle(p.Steps); len(cycle) > 0 {
		problems = append(problems, fmt.Sprintf("step dependency cycle detected: %s", strings.Join(cycle, " -> ")))
	}
	return problems
}

func (p *Pipeline) validateStepReferences(stepIndex map[string]Step) []string {
	var problems []string
	transitive := transitiveDeps(p.Steps)
	for _, step := range p.Steps {
		refs := append(extractTemplateStepRefs(step.Prompt.System), extractTemplateStepRefs(step.Prompt.User)...)
		for _, outputExpr := range step.Outputs {
			refs = append(refs, extractTemplateStepRefs(outputExpr)...)
		}
		for _, ref := range refs {
			target, ok := stepIndex[ref.StepID]
			if !ok {
				problems = append(problems, fmt.Sprintf("step %q references unknown step %q", step.ID, ref.StepID))
				continue
			}
			if _, ok := target.Outputs[ref.OutputName]; !ok {
				problems = append(problems, fmt.Sprintf("step %q references unknown output %q on step %q", step.ID, ref.OutputName, ref.StepID))
			}
			if step.ID != ref.StepID && !transitive[step.ID][ref.StepID] {
				problems = append(problems, fmt.Sprintf("step %q references step %q but does not depend on it", step.ID, ref.StepID))
			}
		}
	}
	return problems
}

func (p *Pipeline) validateStepRuntimeConfig() []string {
	var problems []string
	for _, step := range p.Steps {
		effective := p.EffectiveStep(step)
		if strings.TrimSpace(effective.LLM.Backend) == "" {
			problems = append(problems, fmt.Sprintf("step %q effective llm.backend is required", step.ID))
		}
		if strings.TrimSpace(effective.LLM.Model) == "" {
			problems = append(problems, fmt.Sprintf("step %q effective llm.model is required", step.ID))
		}
		switch effective.Retry.Backoff {
		case "", "none", "fixed", "exponential":
		default:
			problems = append(problems, fmt.Sprintf("step %q retry.backoff %q is unsupported", step.ID, effective.Retry.Backoff))
		}
	}
	return problems
}

func (p *Pipeline) validateToolRules() []string {
	var problems []string
	for _, step := range p.Steps {
		for _, rule := range append(step.Tools.Allow, step.Tools.Deny...) {
			server, _, ok := splitToolRule(rule)
			if !ok {
				problems = append(problems, fmt.Sprintf("step %q has invalid tool rule %q", step.ID, rule))
				continue
			}
			if _, exists := p.MCPServers[server]; !exists {
				problems = append(problems, fmt.Sprintf("step %q references unknown mcp server %q in tool rule %q", step.ID, server, rule))
			}
		}
	}
	return problems
}

func (p *Pipeline) validatePolicyRules() []string {
	var problems []string
	for rule, policy := range p.Policy {
		if _, _, ok := splitToolRule(rule); !ok {
			problems = append(problems, fmt.Sprintf("policy key %q must use server.tool or server.*", rule))
		}
		if policy.MaxBytes < 0 {
			problems = append(problems, fmt.Sprintf("policy %q max_bytes cannot be negative", rule))
		}
		if policy.MaxCalls < 0 {
			problems = append(problems, fmt.Sprintf("policy %q max_calls cannot be negative", rule))
		}
	}
	return problems
}

func (p *Pipeline) validateOutputFields(stepIndex map[string]Step) []string {
	var problems []string
	for _, field := range p.Output.Fields {
		ref, ok := parseOutputField(field)
		if !ok {
			problems = append(problems, fmt.Sprintf("output field %q must use steps.<id>.outputs.<name>", field))
			continue
		}
		step, exists := stepIndex[ref.StepID]
		if !exists {
			problems = append(problems, fmt.Sprintf("output field %q references unknown step %q", field, ref.StepID))
			continue
		}
		if _, exists := step.Outputs[ref.OutputName]; !exists {
			problems = append(problems, fmt.Sprintf("output field %q references unknown output %q on step %q", field, ref.OutputName, ref.StepID))
		}
	}
	return problems
}

func (p *Pipeline) validateFallbacks(stepIndex map[string]Step) []string {
	var problems []string
	for stepID := range p.ErrorHandling.FallbackSteps {
		if _, ok := stepIndex[stepID]; !ok {
			problems = append(problems, fmt.Sprintf("fallback step %q does not match any step", stepID))
		}
	}
	return problems
}

type stepOutputRef struct {
	StepID     string
	OutputName string
}

var templateExprPattern = regexp.MustCompile(`\{\{\s*([^}]+?)\s*\}\}`)
var stepRefPattern = regexp.MustCompile(`\bsteps\.([A-Za-z0-9_-]+)\.outputs\.([A-Za-z0-9_-]+)\b`)

func extractTemplateStepRefs(s string) []stepOutputRef {
	var refs []stepOutputRef
	for _, match := range templateExprPattern.FindAllStringSubmatch(s, -1) {
		expr := match[1]
		for _, ref := range stepRefPattern.FindAllStringSubmatch(expr, -1) {
			refs = append(refs, stepOutputRef{StepID: ref[1], OutputName: ref[2]})
		}
	}
	return refs
}

func parseOutputField(field string) (stepOutputRef, bool) {
	parts := strings.Split(field, ".")
	if len(parts) != 4 || parts[0] != "steps" || parts[2] != "outputs" {
		return stepOutputRef{}, false
	}
	return stepOutputRef{StepID: parts[1], OutputName: parts[3]}, true
}

func splitToolRule(rule string) (server string, tool string, ok bool) {
	parts := strings.Split(rule, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	if parts[1] == "*" {
		return parts[0], "*", true
	}
	if !stepIDPattern.MatchString(parts[0]) || !regexp.MustCompile(`^[A-Za-z0-9_-]+$`).MatchString(parts[1]) {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func findCycle(steps []Step) []string {
	graph := map[string][]string{}
	for _, step := range steps {
		graph[step.ID] = append([]string(nil), step.DependsOn...)
	}
	visiting := map[string]bool{}
	visited := map[string]bool{}
	var stack []string
	var visit func(string) []string
	visit = func(id string) []string {
		if visiting[id] {
			for i, item := range stack {
				if item == id {
					return append(append([]string(nil), stack[i:]...), id)
				}
			}
			return []string{id, id}
		}
		if visited[id] {
			return nil
		}
		visiting[id] = true
		stack = append(stack, id)
		for _, dep := range graph[id] {
			if _, known := graph[dep]; !known {
				continue
			}
			if cycle := visit(dep); len(cycle) > 0 {
				return cycle
			}
		}
		stack = stack[:len(stack)-1]
		visiting[id] = false
		visited[id] = true
		return nil
	}
	for _, step := range steps {
		if cycle := visit(step.ID); len(cycle) > 0 {
			return cycle
		}
	}
	return nil
}

func transitiveDeps(steps []Step) map[string]map[string]bool {
	direct := map[string][]string{}
	for _, step := range steps {
		direct[step.ID] = step.DependsOn
	}
	out := map[string]map[string]bool{}
	for _, step := range steps {
		seen := map[string]bool{}
		var walk func(string)
		walk = func(id string) {
			for _, dep := range direct[id] {
				if seen[dep] {
					continue
				}
				seen[dep] = true
				walk(dep)
			}
		}
		walk(step.ID)
		out[step.ID] = seen
	}
	return out
}
