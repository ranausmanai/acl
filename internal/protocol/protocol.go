// Package protocol defines the ACL tool protocol.
//
// Any process that implements this protocol can serve as an ACL tool.
// The protocol is newline-delimited JSON over stdin/stdout:
//
//	→  {"id":"<uuid>","tool":"<name>","args":{...},"version":"1"}\n
//	←  {"id":"<uuid>","ok":true,"result":{...}}\n
//	←  {"id":"<uuid>","ok":false,"error":"<message>"}\n
//
// Built-in tools bypass the protocol and are called directly as Go functions.
// External tools are spawned as subprocesses; ACL writes the request to their
// stdin and reads the response from their stdout.
package protocol

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sync"
	"sync/atomic"
)

// ToolFn is the Go-native tool signature used for built-in tools.
// args values are one of: nil, bool, int64, float64, string, []any, map[string]any.
type ToolFn func(ctx context.Context, args map[string]any) (any, error)

// ── Wire types ────────────────────────────────────────────────────────────────

type Request struct {
	ID      string         `json:"id"`
	Tool    string         `json:"tool"`
	Args    map[string]any `json:"args"`
	Version string         `json:"version"`
}

type Response struct {
	ID     string `json:"id"`
	OK     bool   `json:"ok"`
	Result any    `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

// ── External tool client ──────────────────────────────────────────────────────

// ExternalTool wraps a subprocess that speaks the ACL tool protocol.
// The subprocess is kept alive between calls (persistent process model).
type ExternalTool struct {
	cmd    *exec.Cmd
	enc    *json.Encoder
	dec    *json.Decoder
	mu     sync.Mutex
	seq    atomic.Uint64
}

// NewExternalTool starts the tool process.
// cmdLine is the command to execute (e.g. ["python", "my_tool.py"]).
func NewExternalTool(ctx context.Context, cmdLine []string) (*ExternalTool, error) {
	if len(cmdLine) == 0 {
		return nil, fmt.Errorf("empty command")
	}
	cmd := exec.CommandContext(ctx, cmdLine[0], cmdLine[1:]...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting tool process: %w", err)
	}
	return &ExternalTool{
		cmd: cmd,
		enc: json.NewEncoder(stdin),
		dec: json.NewDecoder(bufio.NewReader(stdout)),
	}, nil
}

// Call sends a request and waits for the response.
func (t *ExternalTool) Call(ctx context.Context, toolName string, args map[string]any, version string) (any, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	id := fmt.Sprintf("%d", t.seq.Add(1))
	req := Request{ID: id, Tool: toolName, Args: args, Version: version}
	if err := t.enc.Encode(req); err != nil {
		return nil, fmt.Errorf("sending request: %w", err)
	}

	var resp Response
	if err := t.dec.Decode(&resp); err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}
	if resp.ID != id {
		return nil, fmt.Errorf("response ID mismatch: want %s got %s", id, resp.ID)
	}
	if !resp.OK {
		return nil, fmt.Errorf("tool error: %s", resp.Error)
	}
	return resp.Result, nil
}

// Close terminates the subprocess.
func (t *ExternalTool) Close() error {
	return t.cmd.Process.Kill()
}

// ── Tool registry ─────────────────────────────────────────────────────────────

// Registry maps tool names to callable implementations.
type Registry struct {
	mu       sync.RWMutex
	builtins map[string]ToolFn
	versions map[string]string
	external map[string]*ExternalTool
}

func NewRegistry() *Registry {
	return &Registry{
		builtins: make(map[string]ToolFn),
		versions: make(map[string]string),
		external: make(map[string]*ExternalTool),
	}
}

// RegisterBuiltin registers a Go-native tool.
func (r *Registry) RegisterBuiltin(name string, fn ToolFn, version string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.builtins[name] = fn
	r.versions[name] = version
}

// RegisterExternal registers an external tool process.
func (r *Registry) RegisterExternal(name string, tool *ExternalTool, version string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.external[name] = tool
	r.versions[name] = version
}

// Has reports whether a tool is registered.
func (r *Registry) Has(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok1 := r.builtins[name]
	_, ok2 := r.external[name]
	return ok1 || ok2
}

// Version returns the version string for a tool.
func (r *Registry) Version(name string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if v, ok := r.versions[name]; ok {
		return v
	}
	return "1"
}

// Call invokes a tool by name.
func (r *Registry) Call(ctx context.Context, name string, args map[string]any) (any, error) {
	r.mu.RLock()
	fn, isBuiltin := r.builtins[name]
	ext, isExternal := r.external[name]
	ver := r.versions[name]
	r.mu.RUnlock()

	if isBuiltin {
		return fn(ctx, args)
	}
	if isExternal {
		return ext.Call(ctx, name, args, ver)
	}
	return nil, fmt.Errorf("tool %q not registered", name)
}

// Names returns all registered tool names.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var names []string
	for k := range r.builtins {
		names = append(names, k)
	}
	for k := range r.external {
		names = append(names, k)
	}
	return names
}
