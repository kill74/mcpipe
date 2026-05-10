package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"mcpipe/internal/config"
)

type Tool struct {
	Name        string
	Description string
	InputSchema any
}

type ToolResult struct {
	Name string
	Data map[string]any
	Raw  any
}

type Manager interface {
	AllowedTools(ctx context.Context, rules config.Tools) ([]Tool, error)
	Call(ctx context.Context, name string, args map[string]any) (ToolResult, error)
	Close() error
}

type MockManager struct {
	Servers map[string]config.MCPServer
}

func NewMockManager(servers map[string]config.MCPServer) *MockManager {
	return &MockManager{Servers: servers}
}

func (m *MockManager) AllowedTools(ctx context.Context, rules config.Tools) ([]Tool, error) {
	available := []Tool{}
	servers := referencedServers(rules)
	for _, server := range servers {
		switch server {
		case "filesystem":
			available = append(available, Tool{
				Name:        "filesystem.write_file",
				Description: "Mock file writer",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path":    map[string]any{"type": "string"},
						"content": map[string]any{"type": "string"},
					},
					"required": []string{"path", "content"},
				},
			})
		case "brave_search":
			available = append(available, Tool{Name: "brave_search.search", Description: "Mock web search", InputSchema: searchSchema()})
		case "arxiv":
			available = append(available, Tool{Name: "arxiv.search", Description: "Mock academic search", InputSchema: searchSchema()})
		default:
			available = append(available, Tool{Name: server + ".mock", Description: "Mock MCP tool", InputSchema: searchSchema()})
		}
	}
	return FilterTools(available, rules), nil
}

func (m *MockManager) Call(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
	select {
	case <-ctx.Done():
		return ToolResult{}, ctx.Err()
	default:
	}
	data := map[string]any{}
	for key, value := range args {
		data[key] = value
	}
	switch {
	case strings.HasSuffix(name, ".write_file"):
		if _, ok := data["path"]; !ok {
			data["path"] = "mcpipe-output.md"
		}
		data["content"] = fmt.Sprint(data["content"])
		data["status"] = "mock_written"
	case strings.Contains(name, "search"):
		data["content"] = "Mock results for query: " + fmt.Sprint(args["query"])
	default:
		data["content"] = "Mock tool result for " + name
	}
	return ToolResult{Name: name, Data: data, Raw: data}, nil
}

func (m *MockManager) Close() error {
	return nil
}

func searchSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{"type": "string"},
		},
		"required": []string{"query"},
	}
}

func FilterTools(tools []Tool, rules config.Tools) []Tool {
	out := []Tool{}
	for _, tool := range tools {
		if !matchesAny(tool.Name, rules.Allow) {
			continue
		}
		if matchesAny(tool.Name, rules.Deny) {
			continue
		}
		out = append(out, tool)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func matchesAny(name string, rules []string) bool {
	if len(rules) == 0 {
		return false
	}
	for _, rule := range rules {
		if rule == name {
			return true
		}
		if strings.HasSuffix(rule, ".*") && strings.HasPrefix(name, strings.TrimSuffix(rule, ".*")+".") {
			return true
		}
	}
	return false
}

func referencedServers(rules config.Tools) []string {
	seen := map[string]bool{}
	for _, rule := range rules.Allow {
		server, _, ok := SplitToolName(rule)
		if ok {
			seen[server] = true
		}
	}
	out := make([]string, 0, len(seen))
	for server := range seen {
		out = append(out, server)
	}
	sort.Strings(out)
	return out
}

func SplitToolName(name string) (server, tool string, ok bool) {
	parts := strings.Split(name, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func normalizeCallResult(name string, args map[string]any, raw any) ToolResult {
	data := map[string]any{}
	if object, ok := raw.(map[string]any); ok {
		for key, value := range object {
			data[key] = value
		}
		if content, ok := object["content"].([]any); ok {
			var texts []string
			for _, item := range content {
				block, ok := item.(map[string]any)
				if !ok {
					continue
				}
				if text, ok := block["text"].(string); ok {
					texts = append(texts, text)
				}
			}
			if len(texts) > 0 {
				data["content"] = strings.Join(texts, "\n")
			}
		}
	}
	for key, value := range args {
		if _, exists := data[key]; !exists {
			data[key] = value
		}
	}
	data["raw_json"] = mustJSON(raw)
	return ToolResult{Name: name, Data: data, Raw: raw}
}

func mustJSON(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(data)
}
