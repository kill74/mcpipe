package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"mcpipe/internal/config"
)

func TestFilterToolsDenyWins(t *testing.T) {
	tools := []Tool{
		{Name: "fs.write_file"},
		{Name: "fs.read_file"},
	}
	got := FilterTools(tools, config.Tools{Allow: []string{"fs.*"}, Deny: []string{"fs.write_file"}})
	if len(got) != 1 || got[0].Name != "fs.read_file" {
		t.Fatalf("unexpected tools: %#v", got)
	}
}

func TestStdioManagerWithFakeServer(t *testing.T) {
	if os.Getenv("MCPIPE_MCP_HELPER") == "1" {
		runFakeMCPServer()
		os.Exit(0)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	manager := NewStdioManager(map[string]config.MCPServer{
		"fake": {
			Transport: "stdio",
			Command:   os.Args[0],
			Args:      []string{"-test.run=TestStdioManagerWithFakeServer", "--"},
			Env:       map[string]string{"MCPIPE_MCP_HELPER": "1"},
		},
	})
	defer manager.Close()

	tools, err := manager.AllowedTools(ctx, config.Tools{Allow: []string{"fake.*"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "fake.echo" {
		t.Fatalf("unexpected tools: %#v", tools)
	}
	result, err := manager.Call(ctx, "fake.echo", map[string]any{"path": "out.md", "message": "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Data["path"] != "out.md" {
		t.Fatalf("expected normalized path, got %#v", result.Data)
	}
	if !strings.Contains(fmt.Sprint(result.Data["content"]), "hello") {
		t.Fatalf("expected content text, got %#v", result.Data)
	}
}

func TestStdioManagerToolTimeout(t *testing.T) {
	if os.Getenv("MCPIPE_MCP_SLEEP_HELPER") == "1" {
		select {}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	manager := NewStdioManagerWithOptions(map[string]config.MCPServer{
		"sleep": {
			Transport: "stdio",
			Command:   os.Args[0],
			Args:      []string{"-test.run=TestStdioManagerToolTimeout", "--"},
			Env:       map[string]string{"MCPIPE_MCP_SLEEP_HELPER": "1"},
		},
	}, StdioOptions{
		StartupTimeout:   100 * time.Millisecond,
		ToolTimeout:      100 * time.Millisecond,
		MaxResponseBytes: 1024,
		MaxStderrBytes:   1024,
		EnvAllowlist:     []string{"PATH", "Path", "PATHEXT", "SYSTEMROOT", "SystemRoot", "TEMP", "TMP"},
	})
	defer manager.Close()
	_, err := manager.AllowedTools(ctx, config.Tools{Allow: []string{"sleep.*"}})
	if err == nil {
		t.Fatal("expected timeout")
	}
}

func runFakeMCPServer() {
	in := bufio.NewReader(os.Stdin)
	for {
		msg, err := readFrame(in)
		if err != nil {
			return
		}
		var req struct {
			ID     json.RawMessage        `json:"id,omitempty"`
			Method string                 `json:"method"`
			Params map[string]interface{} `json:"params"`
		}
		if err := json.Unmarshal(msg, &req); err != nil {
			return
		}
		if len(req.ID) == 0 {
			continue
		}
		switch req.Method {
		case "initialize":
			writeFrame(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(req.ID), "result": map[string]any{"protocolVersion": "2024-11-05"}})
		case "tools/list":
			writeFrame(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(req.ID), "result": map[string]any{"tools": []map[string]any{{
				"name":        "echo",
				"description": "echo input",
				"inputSchema": map[string]any{"type": "object"},
			}}}})
		case "tools/call":
			args, _ := req.Params["arguments"].(map[string]any)
			writeFrame(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(req.ID), "result": map[string]any{
				"content": []map[string]any{{"type": "text", "text": "echo " + fmt.Sprint(args["message"])}},
			}})
		}
	}
}

func readFrame(r *bufio.Reader) ([]byte, error) {
	length := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		key, value, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(strings.TrimSpace(key), "Content-Length") {
			length, err = strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return nil, err
			}
		}
	}
	if length < 0 {
		return nil, fmt.Errorf("missing length")
	}
	buf := make([]byte, length)
	_, err := io.ReadFull(r, buf)
	return buf, err
}

func writeFrame(v any) {
	data, _ := json.Marshal(v)
	fmt.Fprintf(os.Stdout, "Content-Length: %d\r\n\r\n%s", len(data), data)
}
