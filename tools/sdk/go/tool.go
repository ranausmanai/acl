// Package tool provides a lightweight SDK for writing ACL-compatible external tools in Go.
//
// Usage:
//
//	func main() {
//	    srv := tool.NewServer()
//	    srv.Handle("my.greet", func(ctx context.Context, args map[string]any) (any, error) {
//	        name, _ := args["name"].(string)
//	        return map[string]any{"greeting": "Hello, " + name + "!"}, nil
//	    })
//	    srv.Run() // reads from stdin, writes to stdout
//	}
package tool

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
)

// HandlerFunc is the function signature for a tool implementation.
type HandlerFunc func(ctx context.Context, args map[string]any) (any, error)

// Request is the wire request from ACL.
type Request struct {
	ID      string         `json:"id"`
	Tool    string         `json:"tool"`
	Args    map[string]any `json:"args"`
	Version string         `json:"version"`
}

// Response is the wire response to ACL.
type Response struct {
	ID     string `json:"id"`
	OK     bool   `json:"ok"`
	Result any    `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

// Server dispatches ACL tool protocol requests to registered handlers.
type Server struct {
	handlers map[string]HandlerFunc
}

// NewServer creates an empty tool server.
func NewServer() *Server {
	return &Server{handlers: make(map[string]HandlerFunc)}
}

// Handle registers a tool handler.
// name is the dot-namespaced tool name (e.g. "my.greet").
func (s *Server) Handle(name string, fn HandlerFunc) {
	s.handlers[name] = fn
}

// Run starts the request/response loop on stdin/stdout.
// It blocks until stdin is closed or an unrecoverable I/O error occurs.
func (s *Server) Run() {
	scanner := bufio.NewScanner(os.Stdin)
	enc := json.NewEncoder(os.Stdout)

	for scanner.Scan() {
		line := scanner.Bytes()
		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			// Write error response without ID (best-effort).
			_ = enc.Encode(Response{OK: false, Error: fmt.Sprintf("invalid request JSON: %v", err)})
			continue
		}

		resp := s.dispatch(req)
		if err := enc.Encode(resp); err != nil {
			// stdout write failure is unrecoverable.
			fmt.Fprintf(os.Stderr, "acl-tool: stdout write error: %v\n", err)
			return
		}
	}
}

func (s *Server) dispatch(req Request) Response {
	fn, ok := s.handlers[req.Tool]
	if !ok {
		return Response{ID: req.ID, OK: false, Error: fmt.Sprintf("unknown tool %q", req.Tool)}
	}
	result, err := fn(context.Background(), req.Args)
	if err != nil {
		return Response{ID: req.ID, OK: false, Error: err.Error()}
	}
	return Response{ID: req.ID, OK: true, Result: result}
}
