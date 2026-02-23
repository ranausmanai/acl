// Package mcp implements an MCP (Model Context Protocol) client that connects
// to any MCP server over stdio and registers its tools into an ACL registry.
//
// Usage:
//
//	client, err := mcp.Connect(ctx, []string{"uv", "run", "server.py"}, nil)
//	client.RegisterAll(reg, "monarch")  // registers as monarch.get_accounts, etc.
//	defer client.Close()
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sync"
	"sync/atomic"

	"github.com/ranausmanai/acl/internal/protocol"
)

// ── JSON-RPC 2.0 types ──────────────────────────────────────────────────────

type jsonRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ── MCP types ────────────────────────────────────────────────────────────────

// ToolDef is one tool advertised by the MCP server.
type ToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

type toolsListResult struct {
	Tools []ToolDef `json:"tools"`
}

type callToolResult struct {
	Content []contentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// ── Client ───────────────────────────────────────────────────────────────────

// Client is a live connection to an MCP server subprocess.
type Client struct {
	cmd   *exec.Cmd
	enc   *json.Encoder
	dec   *json.Decoder
	mu    sync.Mutex
	seq   atomic.Int64
	tools []ToolDef
}

// Connect spawns an MCP server, performs the initialize handshake, and
// discovers all available tools.
func Connect(ctx context.Context, cmdLine []string, env []string) (*Client, error) {
	if len(cmdLine) == 0 {
		return nil, fmt.Errorf("mcp: empty command")
	}

	cmd := exec.CommandContext(ctx, cmdLine[0], cmdLine[1:]...)
	if len(env) > 0 {
		cmd.Env = append(cmd.Environ(), env...)
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp: stdout pipe: %w", err)
	}
	// Discard stderr so the server's logs don't block.
	cmd.Stderr = nil

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("mcp: start server: %w", err)
	}

	c := &Client{
		cmd: cmd,
		enc: json.NewEncoder(stdin),
		dec: json.NewDecoder(bufio.NewReader(stdout)),
	}

	// MCP initialize handshake.
	initParams := map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "acl",
			"version": "0.1.0",
		},
	}
	if _, err := c.call("initialize", initParams); err != nil {
		c.Close()
		return nil, fmt.Errorf("mcp: initialize: %w", err)
	}

	// Send initialized notification (no id — it's a notification).
	c.notify("notifications/initialized", nil)

	// Discover tools.
	raw, err := c.call("tools/list", map[string]any{})
	if err != nil {
		c.Close()
		return nil, fmt.Errorf("mcp: tools/list: %w", err)
	}
	var tl toolsListResult
	if err := json.Unmarshal(raw, &tl); err != nil {
		c.Close()
		return nil, fmt.Errorf("mcp: parse tools: %w", err)
	}
	c.tools = tl.Tools

	return c, nil
}

// Tools returns the tools advertised by the server.
func (c *Client) Tools() []ToolDef {
	return c.tools
}

// CallTool invokes a single tool on the MCP server.
func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (any, error) {
	params := map[string]any{
		"name":      name,
		"arguments": args,
	}
	raw, err := c.call("tools/call", params)
	if err != nil {
		return nil, err
	}

	var result callToolResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("mcp: parse tool result: %w", err)
	}

	if result.IsError {
		msg := "unknown error"
		if len(result.Content) > 0 {
			msg = result.Content[0].Text
		}
		return nil, fmt.Errorf("mcp tool %s: %s", name, msg)
	}

	// Extract text content and try to parse as JSON.
	if len(result.Content) == 0 {
		return map[string]any{"text": ""}, nil
	}

	text := result.Content[0].Text

	// Try JSON first.
	var parsed any
	if err := json.Unmarshal([]byte(text), &parsed); err == nil {
		// If it's a map, return as-is (structured data).
		if m, ok := parsed.(map[string]any); ok {
			return m, nil
		}
		// If it's an array or primitive, wrap it.
		return map[string]any{"data": parsed, "text": text}, nil
	}

	// Plain text.
	return map[string]any{"text": text}, nil
}

// RegisterAll registers every tool from this MCP server into the ACL registry.
// If prefix is non-empty, tools are registered as "prefix.toolname".
func (c *Client) RegisterAll(reg *protocol.Registry, prefix string) {
	for _, t := range c.tools {
		toolName := t.Name
		aclName := toolName
		if prefix != "" {
			aclName = prefix + "." + toolName
		}
		// Capture for closure.
		captured := toolName
		fn := func(ctx context.Context, args map[string]any) (any, error) {
			return c.CallTool(ctx, captured, args)
		}
		reg.RegisterBuiltin(aclName, fn, "1")
	}
}

// Close terminates the MCP server process.
func (c *Client) Close() error {
	if c.cmd != nil && c.cmd.Process != nil {
		return c.cmd.Process.Kill()
	}
	return nil
}

// ── Internal JSON-RPC helpers ────────────────────────────────────────────────

func (c *Client) call(method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	id := c.seq.Add(1)
	req := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}
	if err := c.enc.Encode(req); err != nil {
		return nil, fmt.Errorf("send %s: %w", method, err)
	}

	// Read responses, skipping notifications (no id or id=0) until we get ours.
	for {
		var resp jsonRPCResponse
		if err := c.dec.Decode(&resp); err != nil {
			return nil, fmt.Errorf("recv %s: %w", method, err)
		}
		if resp.ID == id {
			if resp.Error != nil {
				return nil, fmt.Errorf("rpc error %d: %s", resp.Error.Code, resp.Error.Message)
			}
			return resp.Result, nil
		}
		// Skip notifications / other messages.
	}
}

func (c *Client) notify(method string, params any) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Notifications have no id field.
	msg := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
	}
	if params != nil {
		msg["params"] = params
	}
	c.enc.Encode(msg) //nolint
}
