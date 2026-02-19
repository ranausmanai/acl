// Package server implements the ACL HTTP server for `acl serve`.
//
// Every AGENT in an ACL file becomes an endpoint:
//
//	GET  /health               → {"status":"ok","agents":[...]}
//	GET  /agents               → agent descriptors with IN/OUT/TOOLS
//	POST /run/{AgentName}      → {"vars":{...}}  →  receipt JSON
//
// Authentication: if ACL_SERVE_API_KEY is set, all routes except /health
// require the header  Authorization: Bearer <key>.
//
// SCHEDULE declarations activate background cron execution automatically
// when Start() is called.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"acl/internal/ast"
	"acl/internal/lexer"
	"acl/internal/parser"
	"acl/internal/receipt"
	"acl/internal/runtime"
	"acl/internal/store"
	"acl/internal/protocol"
)

// Server hosts a parsed ACL program over HTTP.
type Server struct {
	src       string
	nodes     []ast.Node          // expanded AST (read-only after NewServer)
	agents    map[string]*ast.AgentDef
	remotes   map[string]string   // agentName → base URL
	schedules []*ast.ScheduleDecl
	reg       *protocol.Registry
	apiKey    string
	mu        sync.RWMutex        // protects nodes/agents/remotes during hot-reload (future)
}

// NewServer parses and expands src, returning a ready-to-Start server.
func NewServer(src string, reg *protocol.Registry) (*Server, error) {
	tokens, err := lexer.New(src).Tokenize()
	if err != nil {
		return nil, fmt.Errorf("lex: %w", err)
	}
	nodes, err := parser.Parse(tokens)
	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	expanded, err := runtime.Expand(nodes, nil)
	if err != nil {
		return nil, fmt.Errorf("expand: %w", err)
	}

	s := &Server{
		src:     src,
		nodes:   expanded,
		agents:  make(map[string]*ast.AgentDef),
		remotes: make(map[string]string),
		reg:     reg,
		apiKey:  os.Getenv("ACL_SERVE_API_KEY"),
	}

	for _, n := range expanded {
		switch x := n.(type) {
		case *ast.AgentDef:
			s.agents[x.Name] = x
		case *ast.RemoteDecl:
			s.remotes[x.Name] = x.BaseURL
		case *ast.ScheduleDecl:
			s.schedules = append(s.schedules, x)
		}
	}
	return s, nil
}

// AgentNames returns a sorted list of all locally-defined and remote agent names.
func (s *Server) AgentNames() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var names []string
	for n := range s.agents {
		names = append(names, n)
	}
	for n := range s.remotes {
		if _, already := s.agents[n]; !already {
			names = append(names, n)
		}
	}
	return names
}

// Handler builds and returns the HTTP handler for this server.
// Routes: GET /health (open), GET /agents (auth), POST /run/{name} (auth).
// The cron scheduler is NOT started by this method; use Start for that.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)

	// Routes that require auth.
	protected := http.NewServeMux()
	protected.HandleFunc("GET /agents",     s.handleAgents)
	protected.HandleFunc("POST /run/{name}", s.handleRun)

	mux.Handle("/agents", s.authMiddleware(protected))
	mux.Handle("/run/",   s.authMiddleware(protected))
	return mux
}

// Start binds the HTTP server and blocks until it exits.
// It also activates any SCHEDULE declarations as background cron jobs.
func (s *Server) Start(port int) error {
	// Start cron scheduler.
	sched := NewCronScheduler()
	for _, sd := range s.schedules {
		spec, err := ParseCron(sd.Cron)
		if err != nil {
			return fmt.Errorf("SCHEDULE %s: %w", sd.AgentName, err)
		}
		agentName := sd.AgentName // capture
		sched.Add(spec, agentName, func() {
			s.runScheduled(agentName)
		})
		log.Printf("[schedule] %s  cron=%q", agentName, sd.Cron)
	}
	if len(s.schedules) > 0 {
		sched.Start()
		defer sched.Stop()
	}

	addr := fmt.Sprintf(":%d", port)
	log.Printf("[acl serve] listening on %s", addr)
	return http.ListenAndServe(addr, s.Handler())
}

// runScheduled executes an agent on behalf of the cron scheduler.
func (s *Server) runScheduled(agentName string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	log.Printf("[schedule] firing %s", agentName)
	r, err := runtime.RunNamedAgent(ctx, s.nodes, agentName, runtime.Config{}, s.reg)
	if err != nil {
		log.Printf("[schedule] %s: error: %v", agentName, err)
		return
	}
	log.Printf("[schedule] %s: status=%s", agentName, r.Status)

	// Persist to history unless disabled.
	if os.Getenv("ACL_NO_HISTORY") == "" {
		if dbPath, err := store.DefaultPath(); err == nil {
			if st, err := store.Open(dbPath); err == nil {
				st.Put(r) //nolint
				st.Close()
			}
		}
	}
}

// ── Handlers ──────────────────────────────────────────────────────────────────

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	names := s.AgentNames()
	s.mu.RUnlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"version": "0.1.0",
		"agents":  names,
	})
}

type agentDescriptor struct {
	Name  string   `json:"name"`
	Kind  string   `json:"kind"` // "local" | "remote"
	In    []string `json:"in,omitempty"`
	Out   []string `json:"out,omitempty"`
	Tools []string `json:"tools,omitempty"`
	URL   string   `json:"url,omitempty"`
}

func (s *Server) handleAgents(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var descriptors []agentDescriptor
	for _, a := range s.agents {
		descriptors = append(descriptors, agentDescriptor{
			Name:  a.Name,
			Kind:  "local",
			In:    a.Inputs,
			Out:   a.Outputs,
			Tools: a.Tools,
		})
	}
	for name, url := range s.remotes {
		if _, isLocal := s.agents[name]; !isLocal {
			descriptors = append(descriptors, agentDescriptor{
				Name: name,
				Kind: "remote",
				URL:  url,
			})
		}
	}
	writeJSON(w, http.StatusOK, descriptors)
}

func (s *Server) handleRun(w http.ResponseWriter, r *http.Request) {
	agentName := r.PathValue("name")
	if agentName == "" {
		writeError(w, http.StatusBadRequest, "agent name required in path: /run/{name}")
		return
	}

	s.mu.RLock()
	_, isLocal := s.agents[agentName]
	_, isRemote := s.remotes[agentName]
	s.mu.RUnlock()

	if !isLocal && !isRemote {
		writeError(w, http.StatusNotFound, fmt.Sprintf("agent %q not found", agentName))
		return
	}

	// Decode request body.
	var body struct {
		Vars map[string]any `json:"vars"`
	}
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
			return
		}
	}
	if body.Vars == nil {
		body.Vars = map[string]any{}
	}

	ctx := r.Context()
	rec, err := runtime.RunNamedAgent(ctx, s.nodes, agentName, runtime.Config{Vars: body.Vars}, s.reg)
	if err != nil && rec == nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Persist to history.
	if os.Getenv("ACL_NO_HISTORY") == "" {
		if dbPath, histErr := store.DefaultPath(); histErr == nil {
			if st, openErr := store.Open(dbPath); openErr == nil {
				st.Put(rec) //nolint
				st.Close()
			}
		}
	}

	// Always return 200; receipt.status reflects success/failure.
	writeJSON(w, http.StatusOK, rec)
}

// ── Auth middleware ────────────────────────────────────────────────────────────

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.apiKey == "" {
			next.ServeHTTP(w, r)
			return
		}
		authHeader := r.Header.Get("Authorization")
		if authHeader != "Bearer "+s.apiKey {
			writeError(w, http.StatusUnauthorized, "unauthorized: invalid or missing Bearer token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(v) //nolint
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// ensure unused imports don't break build
var _ = receipt.Receipt{}
