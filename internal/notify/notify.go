package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"mcpipe/internal/config"
)

type Payload struct {
	Event       string    `json:"event"`
	RunID       string    `json:"run_id,omitempty"`
	Pipeline    string    `json:"pipeline"`
	FailedStep  string    `json:"failed_step,omitempty"`
	Error       string    `json:"error"`
	Timestamp   string    `json:"timestamp"`
	DurationMS  int64     `json:"duration_ms,omitempty"`
	Attempts    int       `json:"attempts,omitempty"`
	ToolCalls   int       `json:"tool_calls,omitempty"`
	InputTokens int       `json:"input_tokens,omitempty"`
	OutputTokens int      `json:"output_tokens,omitempty"`
}

func Send(ctx context.Context, cfg config.Notify, pipelineFile, runID, failedStep, errMsg string, startedAt, endedAt time.Time, attempts, toolCalls, inputTokens, outputTokens int) error {
	if cfg.Channel == "" && cfg.URL == "" {
		return nil
	}
	url := cfg.URL
	if url == "" {
		return nil
	}
	payload := Payload{
		Event:        "pipeline_failure",
		RunID:        runID,
		Pipeline:     pipelineFile,
		Timestamp:    time.Now().UTC().Format(time.RFC3339),
		DurationMS:   endedAt.Sub(startedAt).Milliseconds(),
		Attempts:     attempts,
		ToolCalls:    toolCalls,
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
	}
	if cfg.IncludeRunID {
		payload.RunID = runID
	}
	if cfg.IncludeFailedStep {
		payload.FailedStep = failedStep
	}
	payload.Error = errMsg

	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal notification payload: %w", err)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(data))
		if err != nil {
			return fmt.Errorf("create notification request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		for key, value := range cfg.Headers {
			req.Header.Set(key, value)
		}

		resp, err := client.Do(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return nil
			}
			lastErr = fmt.Errorf("notification returned HTTP %d", resp.StatusCode)
		} else {
			lastErr = fmt.Errorf("notification request: %w", err)
		}

		if attempt < 3 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt) * time.Second):
			}
		}
	}
	return fmt.Errorf("notification failed after 3 attempts: %w", lastErr)
}
