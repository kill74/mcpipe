package llm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

type Client interface {
	Complete(ctx context.Context, req Request) (Response, error)
}

type Request struct {
	Backend     string
	Model       string
	Temperature *float64
	MaxTokens   int
	Stream      bool
	System      string
	User        string
	Tools       []ToolDefinition
	ToolResults []ToolResult
	Progress    func(chunk string)
}

type ToolDefinition struct {
	Name        string
	Description string
	InputSchema any
}

type ToolCall struct {
	Name      string
	Arguments map[string]any
}

type ToolResult struct {
	Name   string
	Result map[string]any
	Error  string
}

type Response struct {
	Text      string
	ToolCalls []ToolCall
	Usage     Usage
}

type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type Router struct {
	Mock         bool
	HTTP         *http.Client
	OllamaURL    string
	AnthropicURL string
	AnthropicKey string
	OpenAIURL    string
	OpenAIKey    string
}

func NewRouter(mock bool) *Router {
	return &Router{
		Mock:         mock,
		HTTP:         &http.Client{Timeout: 2 * time.Minute},
		OllamaURL:    defaultOllamaURL(),
		AnthropicURL: "https://api.anthropic.com/v1/messages",
		AnthropicKey: os.Getenv("ANTHROPIC_API_KEY"),
		OpenAIURL:    "https://api.openai.com",
		OpenAIKey:    os.Getenv("OPENAI_API_KEY"),
	}
}

func (r *Router) Complete(ctx context.Context, req Request) (Response, error) {
	if r.Mock {
		return Mock{}.Complete(ctx, req)
	}
	switch strings.ToLower(req.Backend) {
	case "ollama":
		return r.completeOllama(ctx, req)
	case "anthropic":
		return r.completeAnthropic(ctx, req)
	case "openai":
		return r.completeOpenAI(ctx, req)
	case "":
		return Response{}, errors.New("llm backend is required")
	default:
		return Response{}, fmt.Errorf("unsupported llm backend %q", req.Backend)
	}
}

func defaultOllamaURL() string {
	host := strings.TrimRight(os.Getenv("OLLAMA_HOST"), "/")
	if host == "" {
		return "http://localhost:11434"
	}
	if !strings.Contains(host, "://") {
		host = "http://" + host
	}
	return host
}

func providerToolName(name string) string {
	return "tool_" + base64.RawURLEncoding.EncodeToString([]byte(name))
}

func providerToolMap(tools []ToolDefinition) map[string]string {
	out := make(map[string]string, len(tools))
	for _, tool := range tools {
		out[providerToolName(tool.Name)] = tool.Name
	}
	return out
}

func decodeProviderTool(name string, names map[string]string) string {
	if original, ok := names[name]; ok {
		return original
	}
	return name
}

func requestJSON(ctx context.Context, client *http.Client, method, url string, headers map[string]string, body any, target any) error {
	var payload io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		payload = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, payload)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("http %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if target == nil {
		return nil
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func appendToolResults(user string, results []ToolResult) string {
	if len(results) == 0 {
		return user
	}
	var b strings.Builder
	b.WriteString(user)
	b.WriteString("\n\nTool results:\n")
	for _, result := range results {
		b.WriteString("- ")
		b.WriteString(result.Name)
		b.WriteString(": ")
		if result.Error != "" {
			b.WriteString("ERROR: ")
			b.WriteString(result.Error)
		} else {
			data, _ := json.Marshal(result.Result)
			b.Write(data)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

type Mock struct{}

func (Mock) Complete(ctx context.Context, req Request) (Response, error) {
	select {
	case <-ctx.Done():
		return Response{}, ctx.Err()
	default:
	}
	if len(req.Tools) > 0 && len(req.ToolResults) == 0 {
		for _, tool := range req.Tools {
			if strings.HasSuffix(tool.Name, ".write_file") {
				path, content := parseWritePrompt(req.User)
				return Response{
					Text: "Preparing to write the requested file.",
					ToolCalls: []ToolCall{{
						Name: tool.Name,
						Arguments: map[string]any{
							"path":    path,
							"content": content,
						},
					}},
				}, nil
			}
		}
		tool := req.Tools[0]
		return Response{
			Text: "Looking up supporting material.",
			ToolCalls: []ToolCall{{
				Name: tool.Name,
				Arguments: map[string]any{
					"query": compactPrompt(req.User),
				},
			}},
		}, nil
	}

	if len(req.ToolResults) > 0 {
		return Response{Text: fmt.Sprintf("Mock synthesis from %d tool result(s): %s", len(req.ToolResults), compactPrompt(req.User))}, nil
	}
	return Response{Text: "Mock response: " + compactPrompt(req.User)}, nil
}

var writePromptPattern = regexp.MustCompile(`(?s)file named '([^']+)':\s*\n\n(.*)$`)

func parseWritePrompt(prompt string) (string, string) {
	match := writePromptPattern.FindStringSubmatch(prompt)
	if len(match) == 3 {
		return match[1], match[2]
	}
	return "mcpipe-output.md", prompt
}

func compactPrompt(prompt string) string {
	prompt = strings.TrimSpace(strings.Join(strings.Fields(prompt), " "))
	if len(prompt) > 220 {
		return prompt[:217] + "..."
	}
	return prompt
}
