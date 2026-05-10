package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	goruntime "runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"mcpipe/internal/config"
)

type StdioManager struct {
	servers map[string]config.MCPServer
	clients map[string]*stdioClient
	options StdioOptions
	mu      sync.Mutex
}

func NewStdioManager(servers map[string]config.MCPServer) *StdioManager {
	return NewStdioManagerWithOptions(servers, SecureOptions())
}

type StdioOptions struct {
	StartupTimeout   time.Duration
	ToolTimeout      time.Duration
	MaxResponseBytes int
	MaxStderrBytes   int
	EnvAllowlist     []string
	WorkingDir       string
}

func SecureOptions() StdioOptions {
	return StdioOptions{
		StartupTimeout:   20 * time.Second,
		ToolTimeout:      60 * time.Second,
		MaxResponseBytes: 10 * 1024 * 1024,
		MaxStderrBytes:   256 * 1024,
		EnvAllowlist: []string{
			"PATH", "Path", "PATHEXT", "SYSTEMROOT", "SystemRoot", "TEMP", "TMP", "HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA",
		},
	}
}

func NewStdioManagerWithOptions(servers map[string]config.MCPServer, options StdioOptions) *StdioManager {
	if options.StartupTimeout <= 0 {
		options.StartupTimeout = 20 * time.Second
	}
	if options.ToolTimeout <= 0 {
		options.ToolTimeout = 60 * time.Second
	}
	if options.MaxResponseBytes <= 0 {
		options.MaxResponseBytes = 10 * 1024 * 1024
	}
	if options.MaxStderrBytes <= 0 {
		options.MaxStderrBytes = 256 * 1024
	}
	return &StdioManager{
		servers: servers,
		clients: map[string]*stdioClient{},
		options: options,
	}
}

func (m *StdioManager) AllowedTools(ctx context.Context, rules config.Tools) ([]Tool, error) {
	if len(rules.Allow) == 0 {
		return nil, nil
	}
	var available []Tool
	for _, serverName := range referencedServers(rules) {
		server, ok := m.servers[serverName]
		if !ok {
			return nil, fmt.Errorf("unknown mcp server %q", serverName)
		}
		if server.Transport != "stdio" {
			return nil, fmt.Errorf("mcp server %q uses %q transport, which is parsed but not executable in v1", serverName, server.Transport)
		}
		callCtx, cancel := context.WithTimeout(ctx, m.options.ToolTimeout)
		client, err := m.client(callCtx, serverName, server)
		if err != nil {
			cancel()
			return nil, err
		}
		tools, err := client.listTools(callCtx)
		cancel()
		if err != nil {
			return nil, err
		}
		for _, tool := range tools {
			tool.Name = serverName + "." + tool.Name
			available = append(available, tool)
		}
	}
	return FilterTools(available, rules), nil
}

func (m *StdioManager) Call(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
	serverName, toolName, ok := SplitToolName(name)
	if !ok {
		return ToolResult{}, fmt.Errorf("invalid tool name %q", name)
	}
	server, ok := m.servers[serverName]
	if !ok {
		return ToolResult{}, fmt.Errorf("unknown mcp server %q", serverName)
	}
	if server.Transport != "stdio" {
		return ToolResult{}, fmt.Errorf("mcp server %q uses %q transport, which is parsed but not executable in v1", serverName, server.Transport)
	}
	callCtx, cancel := context.WithTimeout(ctx, m.options.ToolTimeout)
	defer cancel()
	client, err := m.client(callCtx, serverName, server)
	if err != nil {
		return ToolResult{}, err
	}
	raw, err := client.callTool(callCtx, toolName, args)
	if err != nil {
		return ToolResult{}, err
	}
	return normalizeCallResult(name, args, raw), nil
}

func (m *StdioManager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var errs []string
	for name, client := range m.clients {
		if err := client.close(); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", name, err))
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func (m *StdioManager) client(ctx context.Context, name string, server config.MCPServer) (*stdioClient, error) {
	m.mu.Lock()
	if client, ok := m.clients[name]; ok {
		m.mu.Unlock()
		return client, nil
	}
	m.mu.Unlock()

	client, err := startStdioClient(ctx, name, server, m.options)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, ok := m.clients[name]; ok {
		_ = client.close()
		return existing, nil
	}
	m.clients[name] = client
	return client, nil
}

type stdioClient struct {
	name             string
	cmd              *exec.Cmd
	stdin            io.WriteCloser
	rawStdout        io.ReadCloser
	stdout           *bufio.Reader
	maxResponseBytes int
	mu               sync.Mutex
	nextID           int
}

func startStdioClient(ctx context.Context, name string, server config.MCPServer, options StdioOptions) (*stdioClient, error) {
	command := expandEnv(server.Command)
	args := make([]string, len(server.Args))
	for i, arg := range server.Args {
		args[i] = expandEnv(arg)
	}
	cmd := exec.Command(command, args...)
	if options.WorkingDir != "" {
		cmd.Dir = options.WorkingDir
	}
	cmd.Env = allowedEnv(options.EnvAllowlist)
	for key, value := range server.Env {
		cmd.Env = append(cmd.Env, key+"="+expandEnv(value))
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start mcp server %q: %w", name, err)
	}
	go copyLimit(io.Discard, stderr, int64(options.MaxStderrBytes))
	client := &stdioClient{name: name, cmd: cmd, stdin: stdin, rawStdout: stdout, stdout: bufio.NewReader(stdout), nextID: 1, maxResponseBytes: options.MaxResponseBytes}
	initCtx, cancel := context.WithTimeout(ctx, options.StartupTimeout)
	defer cancel()
	if err := client.initialize(initCtx); err != nil {
		_ = client.close()
		return nil, err
	}
	return client, nil
}

func (c *stdioClient) initialize(ctx context.Context) error {
	_, err := c.request(ctx, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "mcpipe",
			"version": "0.1.0",
		},
	})
	if err != nil {
		return fmt.Errorf("initialize mcp server %q: %w", c.name, err)
	}
	return c.notify("notifications/initialized", map[string]any{})
}

func (c *stdioClient) listTools(ctx context.Context) ([]Tool, error) {
	raw, err := c.request(ctx, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	object, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("tools/list returned %T", raw)
	}
	items, _ := object["tools"].([]any)
	tools := make([]Tool, 0, len(items))
	for _, item := range items {
		toolObj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name, _ := toolObj["name"].(string)
		if name == "" {
			continue
		}
		description, _ := toolObj["description"].(string)
		tools = append(tools, Tool{Name: name, Description: description, InputSchema: toolObj["inputSchema"]})
	}
	return tools, nil
}

func (c *stdioClient) callTool(ctx context.Context, name string, args map[string]any) (any, error) {
	return c.request(ctx, "tools/call", map[string]any{
		"name":      name,
		"arguments": args,
	})
}

func (c *stdioClient) request(ctx context.Context, method string, params any) (any, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	id := c.nextID
	c.nextID++
	msg := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}
	if err := c.write(msg); err != nil {
		return nil, err
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		raw, err := c.read(ctx)
		if err != nil {
			return nil, err
		}
		var resp rpcResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			return nil, err
		}
		if resp.ID == nil {
			continue
		}
		var respID int
		if err := json.Unmarshal(resp.ID, &respID); err != nil || respID != id {
			continue
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("mcp error %d: %s", resp.Error.Code, resp.Error.Message)
		}
		var result any
		if len(resp.Result) > 0 {
			if err := json.Unmarshal(resp.Result, &result); err != nil {
				return nil, err
			}
		}
		return result, nil
	}
}

func (c *stdioClient) notify(method string, params any) error {
	return c.write(map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	})
}

func (c *stdioClient) write(msg any) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(data))
	if _, err := c.stdin.Write([]byte(header)); err != nil {
		return err
	}
	_, err = c.stdin.Write(data)
	return err
}

func (c *stdioClient) read(ctx context.Context) ([]byte, error) {
	type readResult struct {
		data []byte
		err  error
	}
	done := make(chan readResult, 1)
	go func() {
		data, err := c.readFrame()
		done <- readResult{data: data, err: err}
	}()
	select {
	case <-ctx.Done():
		if c.rawStdout != nil {
			_ = c.rawStdout.Close()
		}
		return nil, ctx.Err()
	case result := <-done:
		return result.data, result.err
	}
}

func (c *stdioClient) readFrame() ([]byte, error) {
	contentLength := -1
	for {
		line, err := c.stdout.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(key), "Content-Length") {
			parsed, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return nil, err
			}
			contentLength = parsed
		}
	}
	if contentLength < 0 {
		return nil, errors.New("missing Content-Length header")
	}
	if c.maxResponseBytes > 0 && contentLength > c.maxResponseBytes {
		return nil, fmt.Errorf("mcp response exceeds max frame size of %d bytes", c.maxResponseBytes)
	}
	buf := make([]byte, contentLength)
	_, err := io.ReadFull(c.stdout, buf)
	return buf, err
}

func (c *stdioClient) close() error {
	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	if c.rawStdout != nil {
		_ = c.rawStdout.Close()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		_ = killProcessTree(c.cmd.Process.Pid)
	}
	if c.cmd != nil {
		_ = c.cmd.Wait()
	}
	return nil
}

func allowedEnv(allowlist []string) []string {
	if len(allowlist) == 0 {
		return nil
	}
	var out []string
	for _, key := range allowlist {
		if value, ok := os.LookupEnv(key); ok {
			out = append(out, key+"="+value)
		}
	}
	return out
}

func copyLimit(dst io.Writer, src io.Reader, limit int64) {
	if limit <= 0 {
		_, _ = io.Copy(io.Discard, src)
		return
	}
	_, _ = io.Copy(dst, io.LimitReader(src, limit))
	_, _ = io.Copy(io.Discard, src)
}

func killProcessTree(pid int) error {
	if pid <= 0 {
		return nil
	}
	if goruntime.GOOS == "windows" {
		cmd := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid))
		return cmd.Run()
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return process.Kill()
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

var envPattern = regexp.MustCompile(`\$\{env:([A-Za-z_][A-Za-z0-9_]*)\}`)

func expandEnv(input string) string {
	return envPattern.ReplaceAllStringFunc(input, func(match string) string {
		name := strings.TrimSuffix(strings.TrimPrefix(match, "${env:"), "}")
		return os.Getenv(name)
	})
}
