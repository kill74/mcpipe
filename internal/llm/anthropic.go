package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type anthropicRequest struct {
	Model       string          `json:"model"`
	MaxTokens   int             `json:"max_tokens"`
	Temperature *float64        `json:"temperature,omitempty"`
	System      string          `json:"system,omitempty"`
	Messages    []anthropicMsg  `json:"messages"`
	Tools       []anthropicTool `json:"tools,omitempty"`
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
		Tools: tools,
	}
	headers := map[string]string{
		"x-api-key":         r.AnthropicKey,
		"anthropic-version": "2023-06-01",
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
