package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

type openaiRequest struct {
	Model       string          `json:"model"`
	Messages    []openaiMessage `json:"messages"`
	Tools       []openaiTool    `json:"tools,omitempty"`
	Temperature *float64        `json:"temperature,omitempty"`
	MaxTokens   int             `json:"max_tokens,omitempty"`
	Stream      bool            `json:"stream,omitempty"`
}

type openaiMessage struct {
	Role      string           `json:"role"`
	Content   string           `json:"content"`
	ToolCalls []openaiToolCall `json:"tool_calls,omitempty"`
}

type openaiTool struct {
	Type     string         `json:"type"`
	Function openaiFunction `json:"function"`
}

type openaiFunction struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Parameters  any    `json:"parameters,omitempty"`
}

type openaiToolCall struct {
	ID       string `json:"id"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type openaiResponse struct {
	Choices []struct {
		Message      openaiMessage `json:"message"`
		FinishReason string        `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

type openaiStreamChunk struct {
	ID      string `json:"id"`
	Choices []struct {
		Delta struct {
			Content    string           `json:"content,omitempty"`
			ToolCalls  []openaiToolCall `json:"tool_calls,omitempty"`
			Role       string           `json:"role,omitempty"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage,omitempty"`
}

func (r *Router) completeOpenAI(ctx context.Context, req Request) (Response, error) {
	if strings.TrimSpace(req.Model) == "" {
		return Response{}, errors.New("openai model is required")
	}
	if strings.TrimSpace(r.OpenAIKey) == "" {
		return Response{}, errors.New("OPENAI_API_KEY is required for openai backend")
	}
	nameMap := providerToolMap(req.Tools)
	tools := make([]openaiTool, 0, len(req.Tools))
	for _, tool := range req.Tools {
		schema := tool.InputSchema
		if schema == nil {
			schema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		tools = append(tools, openaiTool{
			Type: "function",
			Function: openaiFunction{
				Name:        providerToolName(tool.Name),
				Description: tool.Description,
				Parameters:  schema,
			},
		})
	}

	messages := []openaiMessage{}
	if req.System != "" {
		messages = append(messages, openaiMessage{Role: "system", Content: req.System})
	}
	messages = append(messages, openaiMessage{Role: "user", Content: appendToolResults(req.User, req.ToolResults)})

	body := openaiRequest{
		Model:       req.Model,
		Messages:    messages,
		Tools:       tools,
		Temperature: req.Temperature,
		Stream:      req.Stream && req.Progress != nil,
	}
	if req.MaxTokens > 0 {
		body.MaxTokens = req.MaxTokens
	}

	headers := map[string]string{
		"Authorization": "Bearer " + r.OpenAIKey,
	}
	url := strings.TrimRight(r.OpenAIURL, "/") + "/v1/chat/completions"

	if body.Stream {
		return r.completeOpenAIStream(ctx, req, body, headers, nameMap)
	}

	var out openaiResponse
	if err := requestJSON(ctx, r.HTTP, "POST", url, headers, body, &out); err != nil {
		return Response{}, err
	}
	if len(out.Choices) == 0 {
		return Response{}, errors.New("openai returned no choices")
	}
	msg := out.Choices[0].Message
	resp := Response{
		Usage: Usage{
			InputTokens:  out.Usage.PromptTokens,
			OutputTokens: out.Usage.CompletionTokens,
		},
	}
	resp.Text = msg.Content
	for _, call := range msg.ToolCalls {
		args := map[string]any{}
		if call.Function.Arguments != "" {
			if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
				return Response{}, fmt.Errorf("decode openai tool arguments: %w", err)
			}
		}
		resp.ToolCalls = append(resp.ToolCalls, ToolCall{
			Name:      decodeProviderTool(call.Function.Name, nameMap),
			Arguments: args,
		})
	}
	return resp, nil
}

func (r *Router) completeOpenAIStream(ctx context.Context, req Request, body openaiRequest, headers map[string]string, nameMap map[string]string) (Response, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return Response{}, err
	}
	httpReq, err := newRequest(ctx, "POST", strings.TrimRight(r.OpenAIURL, "/")+"/v1/chat/completions", headers, bytes.NewReader(data))
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
		return Response{}, fmt.Errorf("openai http %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
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
		var chunk openaiStreamChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			continue
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		delta := chunk.Choices[0].Delta
		if delta.Content != "" {
			text.WriteString(delta.Content)
			if req.Progress != nil {
				req.Progress(delta.Content)
			}
		}
		if chunk.Usage != nil {
			usage.InputTokens = chunk.Usage.PromptTokens
			usage.OutputTokens = chunk.Usage.CompletionTokens
		}
	}
	return Response{Text: text.String(), Usage: usage}, scanner.Err()
}
