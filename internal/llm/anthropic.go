package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type anthropicRequest struct {
	Model       string          `json:"model"`
	MaxTokens   int             `json:"max_tokens"`
	Temperature *float64        `json:"temperature,omitempty"`
	System      string          `json:"system,omitempty"`
	Messages    []anthropicMsg  `json:"messages"`
	Tools       []anthropicTool `json:"tools,omitempty"`
	Stream      bool            `json:"stream,omitempty"`
}

type anthropicMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicTool struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	InputSchema any    `json:"input_schema"`
}

type anthropicResponse struct {
	Content []anthropicContent `json:"content"`
	Usage   struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

type anthropicContent struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

type anthropicStreamEvent struct {
	Type  string         `json:"type"`
	Index int            `json:"index,omitempty"`
	Delta anthropicDelta `json:"delta,omitempty"`
	Usage anthropicUsage `json:"usage,omitempty"`
}

type anthropicDelta struct {
	Type string `json:"type,omitempty"`
	Text string `json:"text,omitempty"`
}

type anthropicUsage struct {
	OutputTokens int `json:"output_tokens,omitempty"`
}

func (r *Router) completeAnthropic(ctx context.Context, req Request) (Response, error) {
	if strings.TrimSpace(req.Model) == "" {
		return Response{}, errors.New("anthropic model is required")
	}
	if strings.TrimSpace(r.AnthropicKey) == "" {
		return Response{}, errors.New("ANTHROPIC_API_KEY is required for anthropic backend")
	}
	nameMap := providerToolMap(req.Tools)
	tools := make([]anthropicTool, 0, len(req.Tools))
	for _, tool := range req.Tools {
		schema := tool.InputSchema
		if schema == nil {
			schema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		tools = append(tools, anthropicTool{
			Name:        providerToolName(tool.Name),
			Description: tool.Description,
			InputSchema: schema,
		})
	}
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 1024
	}
	body := anthropicRequest{
		Model:       req.Model,
		MaxTokens:   maxTokens,
		Temperature: req.Temperature,
		System:      req.System,
		Messages: []anthropicMsg{{
			Role:    "user",
			Content: appendToolResults(req.User, req.ToolResults),
		}},
		Tools:  tools,
		Stream: req.Stream && req.Progress != nil,
	}
	headers := map[string]string{
		"x-api-key":         r.AnthropicKey,
		"anthropic-version": "2023-06-01",
	}

	if body.Stream {
		return r.completeAnthropicStream(ctx, req, body, headers, nameMap)
	}

	var out anthropicResponse
	if err := requestJSON(ctx, r.HTTP, "POST", r.AnthropicURL, headers, body, &out); err != nil {
		return Response{}, err
	}
	resp := Response{
		Usage: Usage{
			InputTokens:  out.Usage.InputTokens,
			OutputTokens: out.Usage.OutputTokens,
		},
	}
	for _, block := range out.Content {
		switch block.Type {
		case "text":
			if resp.Text != "" {
				resp.Text += "\n"
			}
			resp.Text += block.Text
		case "tool_use":
			args := map[string]any{}
			if len(block.Input) > 0 {
				if err := json.Unmarshal(block.Input, &args); err != nil {
					return Response{}, fmt.Errorf("decode anthropic tool input: %w", err)
				}
			}
			resp.ToolCalls = append(resp.ToolCalls, ToolCall{
				Name:      decodeProviderTool(block.Name, nameMap),
				Arguments: args,
			})
		}
	}
	return resp, nil
}

func (r *Router) completeAnthropicStream(ctx context.Context, req Request, body anthropicRequest, headers map[string]string, nameMap map[string]string) (Response, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return Response{}, err
	}
	httpReq, err := newRequest(ctx, "POST", r.AnthropicURL, headers, bytes.NewReader(data))
	if err != nil {
		return Response{}, err
	}
	resp, err := r.HTTP.Do(httpReq)
	if err != nil {
		return Response{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return Response{}, fmt.Errorf("anthropic http %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var text strings.Builder
	var usage Usage
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			break
		}
		var event anthropicStreamEvent
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			continue
		}
		switch event.Type {
		case "content_block_delta":
			if event.Delta.Text != "" {
				text.WriteString(event.Delta.Text)
				if req.Progress != nil {
					req.Progress(event.Delta.Text)
				}
			}
		case "message_delta":
			if event.Usage.OutputTokens > 0 {
				usage.OutputTokens = event.Usage.OutputTokens
			}
		}
	}
	return Response{Text: text.String(), Usage: usage}, scanner.Err()
}

func newRequest(ctx context.Context, method, url string, headers map[string]string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	return req, nil
}
