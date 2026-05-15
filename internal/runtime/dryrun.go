package runtime

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"mcpipe/internal/config"
	pipetemplate "mcpipe/internal/template"
)

func DryRun(p *config.Pipeline, inputs map[string]any, now time.Time) (string, error) {
	levels, err := Levels(p.Steps)
	if err != nil {
		return "", err
	}
	stepOutputs := map[string]map[string]string{}
	var b strings.Builder
	title := p.Version
	if p.Metadata != nil && p.Metadata.Name != "" {
		title = p.Metadata.Name
	}
	b.WriteString("Pipeline: ")
	b.WriteString(title)
	b.WriteString("\n\n")

	b.WriteString("Inputs:\n")
	inputNames := make([]string, 0, len(inputs))
	for name := range inputs {
		inputNames = append(inputNames, name)
	}
	sort.Strings(inputNames)
	for _, name := range inputNames {
		b.WriteString("  ")
		b.WriteString(name)
		b.WriteString(" = ")
		b.WriteString(fmt.Sprint(inputs[name]))
		b.WriteByte('\n')
	}
	b.WriteByte('\n')

	b.WriteString("Run order:\n")
	for i, level := range levels {
		ids := make([]string, 0, len(level))
		for _, step := range level {
			ids = append(ids, step.ID)
		}
		b.WriteString(fmt.Sprintf("  Level %d: %s\n", i+1, strings.Join(ids, ", ")))
	}
	b.WriteByte('\n')

	for _, level := range levels {
		for _, step := range level {
			ctx := pipetemplate.Context{Inputs: inputs, StepOutputs: stepOutputs, Now: now, Env: envMap()}
			system, systemErr := pipetemplate.RenderString(step.Prompt.System, ctx)
			user, userErr := pipetemplate.RenderString(step.Prompt.User, ctx)
			b.WriteString("Step: ")
			b.WriteString(step.ID)
			if step.Name != "" {
				b.WriteString(" (")
				b.WriteString(step.Name)
				b.WriteString(")")
			}
			b.WriteByte('\n')
			effective := p.EffectiveStep(step)
			b.WriteString(fmt.Sprintf("  llm: %s/%s\n", effective.LLM.Backend, effective.LLM.Model))
			if len(step.Tools.Allow) > 0 || len(step.Tools.Deny) > 0 {
				b.WriteString("  tools:\n")
				if len(step.Tools.Allow) > 0 {
					b.WriteString("    allow: ")
					b.WriteString(strings.Join(step.Tools.Allow, ", "))
					b.WriteByte('\n')
				}
				if len(step.Tools.Deny) > 0 {
					b.WriteString("    deny: ")
					b.WriteString(strings.Join(step.Tools.Deny, ", "))
					b.WriteByte('\n')
				}
			}
			if systemErr != nil {
				b.WriteString("  system: <render error: ")
				b.WriteString(systemErr.Error())
				b.WriteString(">\n")
			} else if system != "" {
				b.WriteString("  system: ")
				b.WriteString(indentPreview(system))
				b.WriteByte('\n')
			}
			if userErr != nil {
				b.WriteString("  user: <render error: ")
				b.WriteString(userErr.Error())
				b.WriteString(">\n")
			} else {
				b.WriteString("  user: ")
				b.WriteString(indentPreview(user))
				b.WriteByte('\n')
			}
			stepOutputs[step.ID] = placeholders(step)
			b.WriteByte('\n')
		}
	}
	return b.String(), nil
}

func placeholders(step config.Step) map[string]string {
	out := map[string]string{}
	for name := range step.Outputs {
		out[name] = fmt.Sprintf("<%s.outputs.%s>", step.ID, name)
	}
	return out
}

func indentPreview(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 500 {
		s = s[:497] + "..."
	}
	return strings.ReplaceAll(s, "\n", "\n        ")
}
