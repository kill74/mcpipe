package runtime

import (
	"fmt"
	"sort"
	"strings"

	"mcpipe/internal/config"
)

func Graph(p *config.Pipeline, format string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", "mermaid":
		return MermaidGraph(p)
	case "dot":
		return DOTGraph(p)
	default:
		return "", fmt.Errorf("unsupported graph format %q", format)
	}
}

func MermaidGraph(p *config.Pipeline) (string, error) {
	levels, err := Levels(p.Steps)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("flowchart TD\n")
	for _, level := range levels {
		for _, step := range level {
			b.WriteString("  ")
			b.WriteString(nodeID(step.ID))
			b.WriteString("[\"")
			b.WriteString(escapeMermaid(stepLabel(step)))
			b.WriteString("\"]\n")
		}
	}
	for _, step := range sortedSteps(p.Steps) {
		for _, dep := range sortedStrings(step.DependsOn) {
			b.WriteString("  ")
			b.WriteString(nodeID(dep))
			b.WriteString(" --> ")
			b.WriteString(nodeID(step.ID))
			b.WriteByte('\n')
		}
	}
	groups := parallelGroups(p.Steps)
	for group, steps := range groups {
		if group == "" || len(steps) == 0 {
			continue
		}
		b.WriteString("  subgraph ")
		b.WriteString(nodeID("group_" + group))
		b.WriteString("[\"")
		b.WriteString(escapeMermaid(group))
		b.WriteString("\"]\n")
		for _, step := range steps {
			b.WriteString("    ")
			b.WriteString(nodeID(step.ID))
			b.WriteByte('\n')
		}
		b.WriteString("  end\n")
	}
	return b.String(), nil
}

func DOTGraph(p *config.Pipeline) (string, error) {
	if _, err := Levels(p.Steps); err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("digraph mcpipe {\n")
	b.WriteString("  rankdir=LR;\n")
	b.WriteString("  node [shape=box, style=\"rounded\"];\n")
	for _, step := range sortedSteps(p.Steps) {
		b.WriteString("  ")
		b.WriteString(dotQuote(step.ID))
		b.WriteString(" [label=")
		b.WriteString(dotQuote(stepLabel(step)))
		b.WriteString("];\n")
	}
	for _, step := range sortedSteps(p.Steps) {
		for _, dep := range sortedStrings(step.DependsOn) {
			b.WriteString("  ")
			b.WriteString(dotQuote(dep))
			b.WriteString(" -> ")
			b.WriteString(dotQuote(step.ID))
			b.WriteString(";\n")
		}
	}
	b.WriteString("}\n")
	return b.String(), nil
}

func stepLabel(step config.Step) string {
	label := step.ID
	if step.Name != "" {
		label += "\\n" + step.Name
	}
	return label
}

func nodeID(id string) string {
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := b.String()
	if out == "" || (out[0] >= '0' && out[0] <= '9') {
		out = "n_" + out
	}
	return out
}

func escapeMermaid(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	return s
}

func dotQuote(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	return "\"" + s + "\""
}

func parallelGroups(steps []config.Step) map[string][]config.Step {
	groups := map[string][]config.Step{}
	for _, step := range steps {
		if step.ParallelGroup != "" {
			groups[step.ParallelGroup] = append(groups[step.ParallelGroup], step)
		}
	}
	for group := range groups {
		sort.Slice(groups[group], func(i, j int) bool { return groups[group][i].ID < groups[group][j].ID })
	}
	return groups
}

func sortedSteps(steps []config.Step) []config.Step {
	out := append([]config.Step(nil), steps...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func sortedStrings(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}
