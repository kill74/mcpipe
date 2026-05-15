package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

type ollamaRequest struct {
	Model    string          `json:"model"`
	Stream   bool            `json:"stream"`
	Messages []ollamaMessage `json:"messages"`
	Tools    []ollamaTool    `json:"tools,omitempty"`
	Options  map[string]any  `json:"options,omitempty"`
}

type ollamaMessage struct {
	Role      string           `json:"role"`
	Content   string           `json:"content"`
	ToolCalls []ollamaToolCall `json:"tool_calls,omitempty"`
}

type ollamaTool struct {
	Type     string         `json:"type"`
	Function ollamaFunction `json:"function"`
}

type ollamaFunction struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Parameters  any    `json:"parameters,omitempty"`
}

type ollamaResponse struct {
	Message ollamaMessage `json:"message"`
	Done    bool          `json:"done"`
}

type ollamaToolCall struct {
	Function struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	} `json:"function"`
}

type ollamaStreamChunk struct {
	Model              string        `json:"model"`
	CreatedAt        string        `json:"created_at"`
	Message          ollamaMessage `json:"message"`
	Done             bool          `json:"done"`
	TotalDuration    int64         `json:"total_duration,omitempty"`
	LoadDuration     int64         `json:"load_duration,omitempty"`
	PromptEvalCount  int           `json:"prompt_eval_count,omitempty"`
	EvalCount        int           `json:"eval_count,omitempty"`
}

func (r *Router) completeOllama(ctx context.Context, req Request) (Response, error) {
	if strings.TrimSpace(req.Model) == "" {
		return Response{}, fmt.Errorf("ollama model is required")
	}
	nameMap := providerToolMap(req.Tools)
	tools := make([]ollamaTool, 0, len(req.Tools))
	for _, tool := range req.Tools {
		tools = append(tools, ollamaTool{
			Type: "function",
			Function: ollamaFunction{
				Name:        providerToolName(tool.Name),
				Description: tool.Description,
				Parameters:  tool.InputSchema,
			},
		})
	}

	messages := []ollamaMessage{}
	if req.System != "" {
		messages = append(messages, ollamaMessage{Role: "system", Content: req.System})
	}
	messages = append(messages, ollamaMessage{Role: "user", Content: appendToolResults(req.User, req.ToolResults)})

	options := map[string]any{}
	if req.Temperature != nil {
		options["temperature"] = *req.Temperature
	}
	if req.MaxTokens > 0 {
		options["num_predict"] = req.MaxTokens
	}
	streaming := req.Stream && req.Progress != nil
	body := ollamaRequest{
		Model:    req.Model,
		Stream:   streaming,
		Messages: messages,
		Tools:    tools,
		Options:  options,
	}
	url := strings.TrimRight(r.OllamaURL, "/") + "/api/chat"

	if streaming {
		return r.completeOllamaStream(ctx, req, body, nameMap, url)
	}

	var out ollamaResponse
	if err := requestJSON(ctx, r.HTTP, "POST", url, nil, body, &out); err != nil {
		return Response{}, err
	}
	resp := Response{Text: out.Message.Content}
	for _, call := range out.Message.ToolCalls {
		resp.ToolCalls = append(resp.ToolCalls, ToolCall{
			Name:      decodeProviderTool(call.Function.Name, nameMap),
			Arguments: call.Function.Arguments,
		})
	}
	return resp, nil
}

func (r *Router) completeOllamaStream(ctx context.Context, req Request, body ollamaRequest, nameMap map[string]string, url string) (Response, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return Response{}, err
	}
	httpReq, err := newRequest(ctx, "POST", url, nil, bytes.NewReader(data))
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
		return Response{}, fmt.Errorf("ollama http %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var text strings.Builder
	var usage Usage
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var chunk ollamaStreamChunk
		if err := json.Unmarshal([]byte(line), &chunk); err != nil {
			continue
		}
		if chunk.Message.Content != "" {
			text.WriteString(chunk.Message.Content)
			if req.Progress != nil {
				req.Progress(chunk.Message.Content)
			}
		}
		if chunk.Done {
			if chunk.PromptEvalCount > 0 {
				usage.InputTokens = chunk.PromptEvalCount
			}
			if chunk.EvalCount > 0 {
				usage.OutputTokens = chunk.EvalCount
			}
			break
		}
	}
	return Response{Text: text.String(), Usage: usage}, scanner.Err()
}
