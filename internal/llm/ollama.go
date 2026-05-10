package llm

import (
	"context"
	"fmt"
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
	body := ollamaRequest{
		Model:    req.Model,
		Stream:   false,
		Messages: messages,
		Tools:    tools,
		Options:  options,
	}
	var out ollamaResponse
	if err := requestJSON(ctx, r.HTTP, "POST", strings.TrimRight(r.OllamaURL, "/")+"/api/chat", nil, body, &out); err != nil {
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
