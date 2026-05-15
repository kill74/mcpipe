package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOllamaToolMapping(t *testing.T) {
	var providerName string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		tools := body["tools"].([]any)
		fn := tools[0].(map[string]any)["function"].(map[string]any)
		providerName = fn["name"].(string)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message": map[string]any{
				"role":    "assistant",
				"content": "call",
				"tool_calls": []map[string]any{{
					"function": map[string]any{"name": providerName, "arguments": map[string]any{"path": "x.md"}},
				}},
			},
			"done": true,
		})
	}))
	defer server.Close()

	router := NewRouter(false)
	router.OllamaURL = server.URL
	resp, err := router.Complete(context.Background(), Request{
		Backend: "ollama",
		Model:   "qwen",
		User:    "write",
		Tools:   []ToolDefinition{{Name: "filesystem.write_file", InputSchema: map[string]any{"type": "object"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "filesystem.write_file" {
		t.Fatalf("unexpected tool calls: %#v", resp.ToolCalls)
	}
}

func TestAnthropicToolMapping(t *testing.T) {
	var providerName string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "test-key" {
			t.Fatalf("missing api key")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		tools := body["tools"].([]any)
		providerName = tools[0].(map[string]any)["name"].(string)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": "hi"},
				{"type": "tool_use", "name": providerName, "input": map[string]any{"query": "x"}},
			},
			"usage": map[string]any{"input_tokens": 1, "output_tokens": 2},
		})
	}))
	defer server.Close()

	router := NewRouter(false)
	router.AnthropicURL = server.URL
	router.AnthropicKey = "test-key"
	resp, err := router.Complete(context.Background(), Request{
		Backend:   "anthropic",
		Model:     "claude",
		MaxTokens: 100,
		User:      "search",
		Tools:     []ToolDefinition{{Name: "brave_search.search", InputSchema: map[string]any{"type": "object"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "hi" {
		t.Fatalf("unexpected text %q", resp.Text)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "brave_search.search" {
		t.Fatalf("unexpected tool calls: %#v", resp.ToolCalls)
	}
	if resp.Usage.InputTokens != 1 || resp.Usage.OutputTokens != 2 {
		t.Fatalf("unexpected usage: %#v", resp.Usage)
	}
}

func TestOpenAIToolMapping(t *testing.T) {
	var providerName string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("missing api key, got %s", r.Header.Get("Authorization"))
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		tools := body["tools"].([]any)
		providerName = tools[0].(map[string]any)["function"].(map[string]any)["name"].(string)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"role":    "assistant",
						"content": "hi",
						"tool_calls": []map[string]any{{
							"id": "call_1",
							"function": map[string]any{
								"name":      providerName,
								"arguments": `{"query":"x"}`,
							},
						}},
					},
					"finish_reason": "tool_calls",
				},
			},
			"usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 5},
		})
	}))
	defer server.Close()

	router := NewRouter(false)
	router.OpenAIURL = server.URL
	router.OpenAIKey = "test-key"
	resp, err := router.Complete(context.Background(), Request{
		Backend:   "openai",
		Model:     "gpt-4o",
		MaxTokens: 100,
		User:      "search",
		Tools:     []ToolDefinition{{Name: "brave_search.search", InputSchema: map[string]any{"type": "object"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "hi" {
		t.Fatalf("unexpected text %q", resp.Text)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "brave_search.search" {
		t.Fatalf("unexpected tool calls: %#v", resp.ToolCalls)
	}
	if resp.ToolCalls[0].Arguments["query"] != "x" {
		t.Fatalf("unexpected tool args: %#v", resp.ToolCalls[0].Arguments)
	}
	if resp.Usage.InputTokens != 10 || resp.Usage.OutputTokens != 5 {
		t.Fatalf("unexpected usage: %#v", resp.Usage)
	}
}

func TestOpenAIRequiresKey(t *testing.T) {
	router := NewRouter(false)
	router.OpenAIKey = ""
	_, err := router.Complete(context.Background(), Request{Backend: "openai", Model: "gpt-4o", User: "hi"})
	if err == nil {
		t.Fatal("expected error for missing key")
	}
}

func TestOpenAIRequiresModel(t *testing.T) {
	router := NewRouter(false)
	router.OpenAIKey = "test"
	_, err := router.Complete(context.Background(), Request{Backend: "openai", Model: "", User: "hi"})
	if err == nil {
		t.Fatal("expected error for missing model")
	}
}
