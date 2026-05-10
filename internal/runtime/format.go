package runtime

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"mcpipe/internal/config"
)

func FormatOutput(p *config.Pipeline, result *RunResult) string {
	var b strings.Builder
	if p.Output.IncludeRunMetadata {
		summary := Summarize(result)
		b.WriteString("run_id: ")
		b.WriteString(result.RunID)
		b.WriteByte('\n')
		b.WriteString("step_count: ")
		b.WriteString(fmt.Sprint(summary.Steps))
		b.WriteByte('\n')
		b.WriteString("started_at: ")
		b.WriteString(result.StartedAt.Format(time.RFC3339))
		b.WriteByte('\n')
		b.WriteString("completed_at: ")
		b.WriteString(result.CompletedAt.Format(time.RFC3339))
		b.WriteByte('\n')
		b.WriteString("duration_ms: ")
		b.WriteString(fmt.Sprint(summary.DurationMS))
		b.WriteByte('\n')
		b.WriteString("attempts: ")
		b.WriteString(fmt.Sprint(summary.Attempts))
		b.WriteByte('\n')
		b.WriteString("tool_calls: ")
		b.WriteString(fmt.Sprint(summary.ToolCalls))
		b.WriteByte('\n')
		b.WriteString("input_tokens: ")
		b.WriteString(fmt.Sprint(summary.InputTokens))
		b.WriteByte('\n')
		b.WriteString("output_tokens: ")
		b.WriteString(fmt.Sprint(summary.OutputTokens))
		b.WriteString("\n\n")
	}
	for i, output := range result.Outputs {
		if len(result.Outputs) > 1 {
			b.WriteString("## ")
			b.WriteString(output.Field)
			b.WriteString("\n\n")
		}
		b.WriteString(output.Value)
		if i < len(result.Outputs)-1 {
			b.WriteString("\n\n")
		}
	}
	if !strings.HasSuffix(b.String(), "\n") {
		b.WriteByte('\n')
	}
	return b.String()
}

func FormatJSONOutput(result *RunResult) (string, error) {
	body := struct {
		RunID       string                `json:"run_id"`
		StartedAt   time.Time             `json:"started_at"`
		CompletedAt time.Time             `json:"completed_at"`
		DurationMS  int64                 `json:"duration_ms"`
		Summary     UsageSummary          `json:"summary"`
		StepOrder   []string              `json:"step_order"`
		Outputs     []FieldOutput         `json:"outputs"`
		Steps       map[string]StepResult `json:"steps"`
	}{
		RunID:       result.RunID,
		StartedAt:   result.StartedAt,
		CompletedAt: result.CompletedAt,
		DurationMS:  result.CompletedAt.Sub(result.StartedAt).Milliseconds(),
		Summary:     Summarize(result),
		StepOrder:   result.StepOrder,
		Outputs:     result.Outputs,
		Steps:       result.Steps,
	}
	data, err := json.MarshalIndent(body, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data) + "\n", nil
}
