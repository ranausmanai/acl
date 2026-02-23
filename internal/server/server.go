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
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ranausmanai/acl/internal/ast"
	"github.com/ranausmanai/acl/internal/lexer"
	"github.com/ranausmanai/acl/internal/parser"
	"github.com/ranausmanai/acl/internal/protocol"
	"github.com/ranausmanai/acl/internal/receipt"
	"github.com/ranausmanai/acl/internal/runtime"
	"github.com/ranausmanai/acl/internal/schema"
	"github.com/ranausmanai/acl/internal/store"
	"github.com/ranausmanai/acl/internal/ui"
)

// Server hosts a parsed ACL program over HTTP.
type Server struct {
	src       string
	nodes     []ast.Node // expanded AST (read-only after NewServer)
	agents    map[string]*ast.AgentDef
	remotes   map[string]string // agentName → base URL
	schedules []*ast.ScheduleDecl
	reg       *protocol.Registry
	apiKey    string
	mu        sync.RWMutex // protects nodes/agents/remotes during hot-reload (future)
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
// Routes:
//
//	GET  /           → web dashboard (open)
//	GET  /health     → {"status":"ok"} (open)
//	GET  /schema     → machine-readable schema (open)
//	GET  /agents     → agent list (auth)
//	GET  /history    → last 20 run receipts (auth)
//	POST /run/{name} → run an agent (auth)
//
// The cron scheduler is NOT started by this method; use Start for that.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Open routes.
	mux.Handle("GET /", ui.Handler())
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /schema", s.handleSchema)

	// Auth-protected routes — each registered with its exact method so they
	// don't conflict with the catch-all "GET /" pattern.
	mux.Handle("GET /agents", s.authMiddleware(http.HandlerFunc(s.handleAgents)))
	mux.Handle("GET /history", s.authMiddleware(http.HandlerFunc(s.handleHistory)))
	mux.Handle("POST /run/{name}", s.authMiddleware(http.HandlerFunc(s.handleRun)))

	// Playground — UI + API (rate-limited).
	mux.Handle("GET /playground", ui.Handler())
	mux.HandleFunc("POST /playground", s.handlePlayground)

	// Natural language agent — chat UI + API.
	mux.Handle("GET /agent", ui.AgentHandler())
	mux.HandleFunc("POST /agent", s.handleAgent)
	mux.Handle("GET /agenticflow", ui.AgenticFlowHandler())
	mux.HandleFunc("POST /agenticflow", s.handleAgenticFlow)
	mux.Handle("GET /quickstart", ui.QuickstartHandler())
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

func (s *Server) handleSchema(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	nodes := s.nodes
	s.mu.RUnlock()
	svc := schema.FromNodes(nodes)
	writeJSON(w, http.StatusOK, svc)
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

// HistoryItem is one entry returned by GET /history.
type HistoryItem struct {
	ID         int64            `json:"id"`
	AgentName  string           `json:"agent_name"`
	Status     string           `json:"status"`
	Timestamp  string           `json:"timestamp"`
	DurationMS int64            `json:"duration_ms"`
	Receipt    *receipt.Receipt `json:"receipt"`
}

func (s *Server) handleHistory(w http.ResponseWriter, _ *http.Request) {
	dbPath, err := store.DefaultPath()
	if err != nil {
		writeJSON(w, http.StatusOK, []HistoryItem{})
		return
	}
	st, err := store.Open(dbPath)
	if err != nil {
		writeJSON(w, http.StatusOK, []HistoryItem{})
		return
	}
	defer st.Close()

	summaries, err := st.List(20)
	if err != nil {
		writeJSON(w, http.StatusOK, []HistoryItem{})
		return
	}

	items := make([]HistoryItem, 0, len(summaries))
	for _, sum := range summaries {
		rec, err := st.Get(sum.ID)
		if err != nil {
			continue
		}
		agentName := ""
		if len(rec.Agents) > 0 {
			agentName = rec.Agents[0].Name
		}
		items = append(items, HistoryItem{
			ID:         sum.ID,
			AgentName:  agentName,
			Status:     sum.Status,
			Timestamp:  sum.Timestamp,
			DurationMS: sum.DurationMS,
			Receipt:    rec,
		})
	}
	writeJSON(w, http.StatusOK, items)
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

// ── Playground ────────────────────────────────────────────────────────────────

// Simple per-IP rate limiter: max 10 runs per minute.
var (
	playLimiter   = map[string][]time.Time{}
	playLimiterMu sync.Mutex
)

func rateLimitOK(ip string) bool {
	playLimiterMu.Lock()
	defer playLimiterMu.Unlock()
	now := time.Now()
	cutoff := now.Add(-1 * time.Minute)
	// Remove old entries.
	var recent []time.Time
	for _, t := range playLimiter[ip] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}
	if len(recent) >= 10 {
		playLimiter[ip] = recent
		return false
	}
	playLimiter[ip] = append(recent, now)
	return true
}

func (s *Server) handlePlayground(w http.ResponseWriter, r *http.Request) {
	// Rate limit by IP.
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	if ip == "" {
		ip = r.RemoteAddr
	}
	if !rateLimitOK(ip) {
		writeError(w, http.StatusTooManyRequests, "rate limit: max 10 runs per minute")
		return
	}

	var body struct {
		Code string         `json:"code"`
		Vars map[string]any `json:"vars"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if body.Code == "" {
		writeError(w, http.StatusBadRequest, "code is required")
		return
	}
	if body.Vars == nil {
		body.Vars = map[string]any{}
	}

	// Parse and run the ACL code.
	tokens, err := lexer.New(body.Code).Tokenize()
	if err != nil {
		writeError(w, http.StatusBadRequest, "parse error: "+err.Error())
		return
	}
	nodes, err := parser.Parse(tokens)
	if err != nil {
		writeError(w, http.StatusBadRequest, "parse error: "+err.Error())
		return
	}
	expanded, err := runtime.Expand(nodes, nil)
	if err != nil {
		writeError(w, http.StatusBadRequest, "expand error: "+err.Error())
		return
	}

	// Find the first agent to run.
	agentName := ""
	for _, n := range expanded {
		if a, ok := n.(*ast.AgentDef); ok {
			agentName = a.Name
			break
		}
	}
	if agentName == "" {
		writeError(w, http.StatusBadRequest, "no AGENT found in code")
		return
	}

	// Run with 30s timeout.
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	rec, err := runtime.RunNamedAgent(ctx, expanded, agentName, runtime.Config{Vars: body.Vars}, s.reg)
	if err != nil && rec == nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// ── Natural language agent ────────────────────────────────────────────────────

func (s *Server) handleAgent(w http.ResponseWriter, r *http.Request) {
	// Rate limit.
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	if ip == "" {
		ip = r.RemoteAddr
	}
	if !rateLimitOK(ip) {
		writeError(w, http.StatusTooManyRequests, "rate limit: max 10 runs per minute")
		return
	}

	var body struct {
		Request string `json:"request"` // natural language
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if body.Request == "" {
		writeError(w, http.StatusBadRequest, "request is required")
		return
	}

	// Build the list of available tools (excluding llm.generate itself to avoid recursion confusion).
	toolNames := s.reg.Names()
	var toolList string
	for _, name := range toolNames {
		if name != "llm.generate" && name != "react.run" && !strings.HasPrefix(name, "demo.") {
			toolList += "  " + name + "\n"
		}
	}

	// Step 1: Ask LLM to generate ACL code from the natural language request.
	today := time.Now().Format("2006-01-02")
	monthStart := time.Now().Format("2006-01") + "-01"

	systemPrompt := `You are an ACL code generator. ACL is a language for AI agent workflows.

Given a user's natural language request about their finances, generate valid ACL code that fulfills the request.

TODAY'S DATE: ` + today + `
CURRENT MONTH START: ` + monthStart + `

AVAILABLE TOOLS (from Monarch Money + built-in):
` + toolList + `
TOOL: llm.generate(prompt="...") - Use to generate human-readable responses

TOOL RETURN SCHEMAS (use these field names with {step.field}):
- monarch.get_accounts → list of account objects (access entire result as {step})
- monarch.get_transactions(category, start_date, end_date, limit) → {step.transactions}, {step.count}
- monarch.get_categories → {step.categories}
- monarch.get_budgets → {step.budgets}, {step.total_budgeted}, {step.total_spent}, {step.total_remaining}
- monarch.get_recurring → {step.recurring}, {step.monthly_total}
- monarch.get_goals → {step.goals}
- monarch.get_investments → {step.holdings}, {step.total_value}
- monarch.get_cashflow → {step.income}, {step.expenses}, {step.savings}, {step.savings_rate}, {step.by_category}
- monarch.get_net_worth → {step.current}, {step.assets}, {step.liabilities}, {step.history}
- monarch.create_transaction(amount, description, category) → {step.status}, {step.transaction}, {step.message}
- llm.generate(prompt="...") → {step.text}

ACL SYNTAX RULES:
- AGENT Name ... END
- STEP name = TOOL toolname(arg="value", arg2="value")
- Use {step_name.field} to reference previous step results in strings
- MUST expression - required check (agent fails if false)
- CHECK expression - optional evidence check
- RESULT step_name - final output
- All strings must be on ONE line (no multiline strings)
- OUT field_name - declare output
- TOOLS tool1, tool2, tool3 - declare which tools agent uses

CRITICAL RULES:
- NEVER use template variables like {current_date} or {today} — use LITERAL date strings like "2026-02-01"
- {step.field} is ONLY for referencing results from previous STEPs, not for dates or other values
- All arg values must be literal strings/numbers, NOT computed expressions
- Always include llm.generate as the LAST step to produce a human-readable response
- In the llm.generate prompt, ALWAYS end with: "Respond like a friendly financial assistant in 1-3 sentences. Give the direct answer with key numbers. No step-by-step calculations."
- Use monarch.* tools to read/write financial data
- Use MUST to verify critical actions succeeded
- Keep it simple - minimal steps needed
- For "this month" queries, use start_date="` + monthStart + `"
- For category filters, just use the category name (e.g. category="Groceries") — no date args needed if you want all-time
- MUST expressions should use fields that actually exist in the tool return schema above

Example for "how much did I spend on food?":
AGENT FoodSpending
  OUT answer
  TOOLS monarch.get_transactions, llm.generate

  STEP txns = TOOL monarch.get_transactions(category="Groceries")
  STEP answer = TOOL llm.generate(prompt="Based on these transactions: {txns.transactions}, give a concise answer about how much the user spent on food. Just state the total and list the transactions briefly. No calculations or explanations.")

  MUST has(answer, "text")
  RESULT answer
END

Example for "log $50 dinner at Olive Garden":
AGENT LogExpense
  OUT result
  TOOLS monarch.create_transaction, monarch.get_budgets, llm.generate

  STEP log = TOOL monarch.create_transaction(amount=-50, description="Olive Garden", category="Restaurants")
  STEP budgets = TOOL monarch.get_budgets()
  STEP result = TOOL llm.generate(prompt="Transaction logged: {log.transaction}. Budget status: {budgets.budgets}. Briefly confirm the transaction and mention the Restaurants budget status. Be concise.")

  MUST has(log, "status")
  MUST has(result, "text")
  RESULT result
END

Now generate ACL code for the user's request. Output ONLY the ACL code, nothing else. No markdown, no explanation.`

	generateArgs := map[string]any{
		"prompt": body.Request,
		"data":   systemPrompt,
	}

	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()

	// Call LLM to generate ACL code.
	genResult, err := s.reg.Call(ctx, "llm.generate", generateArgs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate ACL: "+err.Error())
		return
	}

	genMap, ok := genResult.(map[string]any)
	if !ok {
		writeError(w, http.StatusInternalServerError, "unexpected LLM result type")
		return
	}
	aclCode, _ := genMap["text"].(string)
	if aclCode == "" {
		writeError(w, http.StatusInternalServerError, "LLM returned empty ACL code")
		return
	}

	// Clean up: remove markdown fences if present.
	aclCode = cleanACLCode(aclCode)

	// Step 2: Parse and execute the generated ACL code.
	tokens, err := lexer.New(aclCode).Tokenize()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"request":       body.Request,
			"generated_acl": aclCode,
			"error":         "parse error: " + err.Error(),
		})
		return
	}
	nodes, err := parser.Parse(tokens)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"request":       body.Request,
			"generated_acl": aclCode,
			"error":         "parse error: " + err.Error(),
		})
		return
	}
	expanded, err := runtime.Expand(nodes, nil)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"request":       body.Request,
			"generated_acl": aclCode,
			"error":         "expand error: " + err.Error(),
		})
		return
	}

	// Find first agent.
	agentName := ""
	for _, n := range expanded {
		if a, ok := n.(*ast.AgentDef); ok {
			agentName = a.Name
			break
		}
	}
	if agentName == "" {
		writeJSON(w, http.StatusOK, map[string]any{
			"request":       body.Request,
			"generated_acl": aclCode,
			"error":         "no AGENT found in generated code",
		})
		return
	}

	// Execute.
	rec, err := runtime.RunNamedAgent(ctx, expanded, agentName, runtime.Config{}, s.reg)

	// Build response.
	response := map[string]any{
		"request":       body.Request,
		"generated_acl": aclCode,
		"receipt":       rec,
	}

	// Extract the human-readable message from the last successful step.
	if rec != nil && len(rec.Agents) > 0 {
		response["status"] = rec.Status
		agent := rec.Agents[len(rec.Agents)-1]
		for i := len(agent.Steps) - 1; i >= 0; i-- {
			st := agent.Steps[i]
			if st.Result != nil {
				if m, ok := st.Result.(map[string]any); ok {
					if text, ok := m["text"].(string); ok && text != "" {
						response["message"] = text
						break
					}
				}
			}
		}
	}

	if err != nil && rec == nil {
		response["error"] = err.Error()
	}

	writeJSON(w, http.StatusOK, response)
}

type agenticFlowMode struct {
	ID             string
	ToolPrefixes   []string
	ExtraToolNames []string
	PromptBuilder  func(today, monthStart, toolList string) string
}

func (s *Server) handleAgenticFlow(w http.ResponseWriter, r *http.Request) {
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	if ip == "" {
		ip = r.RemoteAddr
	}
	if !rateLimitOK(ip) {
		writeError(w, http.StatusTooManyRequests, "rate limit: max 10 runs per minute")
		return
	}

	var body struct {
		Request string `json:"request"`
		Mode    string `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if strings.TrimSpace(body.Request) == "" {
		writeError(w, http.StatusBadRequest, "request is required")
		return
	}

	mode := agenticFlowModes()[strings.ToLower(strings.TrimSpace(body.Mode))]
	if mode.ID == "" {
		mode = agenticFlowModes()["splitwise"]
	}

	today := time.Now().Format("2006-01-02")
	monthStart := time.Now().Format("2006-01") + "-01"
	toolList := s.filteredToolList(mode.ToolPrefixes, mode.ExtraToolNames)
	systemPrompt := mode.PromptBuilder(today, monthStart, toolList)

	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	genResult, err := s.reg.Call(ctx, "llm.generate", map[string]any{
		"prompt": body.Request,
		"data":   systemPrompt,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate ACL: "+err.Error())
		return
	}
	genMap, ok := genResult.(map[string]any)
	if !ok {
		writeError(w, http.StatusInternalServerError, "unexpected LLM result type")
		return
	}
	aclCode, _ := genMap["text"].(string)
	if aclCode == "" {
		writeError(w, http.StatusInternalServerError, "LLM returned empty ACL code")
		return
	}
	aclCode = cleanACLCode(aclCode)

	var nodes []ast.Node
	for attempt := 0; attempt < 2; attempt++ {
		tokens, tokErr := lexer.New(aclCode).Tokenize()
		if tokErr != nil {
			if attempt == 0 {
				if repaired, repErr := s.repairACLCode(ctx, body.Request, mode.ID, aclCode, "lex error: "+tokErr.Error()); repErr == nil && repaired != "" {
					aclCode = cleanACLCode(repaired)
					continue
				}
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"request":       body.Request,
				"mode":          mode.ID,
				"generated_acl": aclCode,
				"error":         "parse error: " + tokErr.Error(),
			})
			return
		}
		parseNodes, parseErr := parser.Parse(tokens)
		if parseErr != nil {
			if attempt == 0 {
				if repaired, repErr := s.repairACLCode(ctx, body.Request, mode.ID, aclCode, "parse error: "+parseErr.Error()); repErr == nil && repaired != "" {
					aclCode = cleanACLCode(repaired)
					continue
				}
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"request":       body.Request,
				"mode":          mode.ID,
				"generated_acl": aclCode,
				"error":         "parse error: " + parseErr.Error(),
			})
			return
		}
		nodes = parseNodes
		break
	}
	expanded, err := runtime.Expand(nodes, nil)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"request":       body.Request,
			"mode":          mode.ID,
			"generated_acl": aclCode,
			"error":         "expand error: " + err.Error(),
		})
		return
	}
	agentName := ""
	for _, n := range expanded {
		if a, ok := n.(*ast.AgentDef); ok {
			agentName = a.Name
			break
		}
	}
	if agentName == "" {
		writeJSON(w, http.StatusOK, map[string]any{
			"request":       body.Request,
			"mode":          mode.ID,
			"generated_acl": aclCode,
			"error":         "no AGENT found in generated code",
		})
		return
	}

	rec, runErr := runtime.RunNamedAgent(ctx, expanded, agentName, runtime.Config{}, s.reg)
	response := map[string]any{
		"request":       body.Request,
		"mode":          mode.ID,
		"generated_acl": aclCode,
		"receipt":       rec,
	}
	if rec != nil && len(rec.Agents) > 0 {
		response["status"] = rec.Status
		agent := rec.Agents[len(rec.Agents)-1]
		for i := len(agent.Steps) - 1; i >= 0; i-- {
			st := agent.Steps[i]
			if st.Result == nil {
				continue
			}
			if m, ok := st.Result.(map[string]any); ok {
				if text, ok := m["text"].(string); ok && text != "" {
					response["message"] = text
					break
				}
				if msg, ok := m["message"].(string); ok && msg != "" {
					response["message"] = msg
					break
				}
			}
		}
	}
	if _, ok := response["message"]; !ok && runErr == nil {
		response["message"] = "Done."
	}
	if runErr != nil && rec == nil {
		response["error"] = runErr.Error()
	}
	if hints := deriveAgenticFlowUIHints(mode.ID, body.Request, rec); hints != nil {
		response["ui_hints"] = hints
	}
	writeJSON(w, http.StatusOK, response)
}

func deriveAgenticFlowUIHints(modeID, originalRequest string, rec *receipt.Receipt) map[string]any {
	if rec == nil || len(rec.Agents) == 0 {
		return nil
	}
	var lastStepResult map[string]any
	agent := rec.Agents[len(rec.Agents)-1]
	for i := len(agent.Steps) - 1; i >= 0; i-- {
		if m, ok := agent.Steps[i].Result.(map[string]any); ok {
			lastStepResult = m
			break
		}
	}

	trimmed := strings.TrimSpace(originalRequest)
	lowerReq := strings.ToLower(trimmed)
	alreadyApproved := strings.Contains(lowerReq, "(approved)") ||
		strings.Contains(lowerReq, " approved") ||
		strings.Contains(lowerReq, "confirm") ||
		strings.Contains(lowerReq, "yes create") ||
		strings.Contains(lowerReq, "do it")

	// Strong signal: zapier preview tool result.
	if preview := findToolStepResult(agent.Steps, "zapier.invoke"); preview != nil {
		status, _ := preview["status"].(string)
		if status == "preview" {
			return map[string]any{
				"approval": map[string]any{
					"required":        true,
					"phase":           "preview",
					"label":           "Execute Zapier action",
					"confirm_request": appendApprovedMarker(trimmed),
					"cancel_label":    "Edit request",
				},
			}
		}
		if status == "accepted" || status == "not_configured" {
			return map[string]any{
				"approval": map[string]any{
					"required": false,
					"phase":    "executed",
					"label":    "Action executed",
				},
			}
		}
	}

	// Heuristic support for known demo modes using text conventions.
	if rec.Status == "success" && !alreadyApproved {
		switch modeID {
		case "support_refund", "calendar", "splitwise":
			text := ""
			if lastStepResult != nil {
				if t, ok := lastStepResult["text"].(string); ok {
					text = strings.ToLower(t)
				}
				if text == "" {
					if m, ok := lastStepResult["message"].(string); ok {
						text = strings.ToLower(m)
					}
				}
			}
			if strings.Contains(text, "confirm") || strings.Contains(text, "before creating") || strings.Contains(text, "do not execute") {
				return map[string]any{
					"approval": map[string]any{
						"required":        true,
						"phase":           "preview",
						"label":           "Confirmation required",
						"confirm_request": appendApprovedMarker(trimmed),
						"cancel_label":    "Edit request",
					},
				}
			}
		}
	}

	if alreadyApproved && rec.Status == "success" {
		return map[string]any{
			"approval": map[string]any{
				"required": false,
				"phase":    "executed",
				"label":    "Confirmed and executed",
			},
		}
	}

	return nil
}

func findToolStepResult(steps []receipt.StepTrace, target string) map[string]any {
	for i := len(steps) - 1; i >= 0; i-- {
		st := steps[i]
		if st.Target != target {
			continue
		}
		if m, ok := st.Result.(map[string]any); ok {
			return m
		}
	}
	return nil
}

func appendApprovedMarker(s string) string {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return "(approved)"
	}
	lower := strings.ToLower(trimmed)
	if strings.Contains(lower, "(approved)") {
		return trimmed
	}
	return trimmed + " (approved)"
}

func (s *Server) repairACLCode(ctx context.Context, userRequest, modeID, badACL, parseErr string) (string, error) {
	repairPrompt := "Repair the invalid ACL code and return ONLY corrected ACL."
	repairContext := `You are fixing ACL syntax for mode "` + modeID + `".

Original user request:
` + userRequest + `

Error:
` + parseErr + `

Invalid ACL:
` + badACL + `

ACL FIX RULES:
- Use: STEP name = TOOL tool.name(...)
- Use: CHECK expr and MUST expr (NO curly braces around expressions)
- Curly braces {step.field} are only allowed inside quoted strings for interpolation
- Use: OUT result_name (declare output variable name)
- Use: RESULT step_name (return the step result)
- Keep tool names and overall intent the same
- Output ONLY valid ACL code`

	res, err := s.reg.Call(ctx, "llm.generate", map[string]any{
		"prompt": repairPrompt,
		"data":   repairContext,
	})
	if err != nil {
		return "", err
	}
	m, ok := res.(map[string]any)
	if !ok {
		return "", fmt.Errorf("repair ACL: unexpected LLM result type")
	}
	out, _ := m["text"].(string)
	return out, nil
}

func (s *Server) filteredToolList(prefixes []string, extra []string) string {
	allowed := make(map[string]bool)
	for _, name := range extra {
		allowed[name] = true
	}
	for _, name := range s.reg.Names() {
		for _, p := range prefixes {
			if strings.HasPrefix(name, p) {
				allowed[name] = true
				break
			}
		}
	}
	var names []string
	for name := range allowed {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, name := range names {
		b.WriteString("  ")
		b.WriteString(name)
		b.WriteByte('\n')
	}
	return b.String()
}

func agenticFlowModes() map[string]agenticFlowMode {
	return map[string]agenticFlowMode{
		"splitwise": {
			ID:             "splitwise",
			ToolPrefixes:   []string{"demo.splitwise."},
			ExtraToolNames: []string{"llm.generate"},
			PromptBuilder:  buildSplitwiseAgenticPrompt,
		},
		"support_refund": {
			ID:             "support_refund",
			ToolPrefixes:   []string{"demo.support."},
			ExtraToolNames: []string{"llm.generate"},
			PromptBuilder:  buildSupportRefundPrompt,
		},
		"calendar": {
			ID:             "calendar",
			ToolPrefixes:   []string{"demo.calendar."},
			ExtraToolNames: []string{"llm.generate"},
			PromptBuilder:  buildCalendarPrompt,
		},
		"zapier_calendar": {
			ID:             "zapier_calendar",
			ToolPrefixes:   nil,
			ExtraToolNames: []string{"zapier.invoke", "llm.generate"},
			PromptBuilder:  buildZapierCalendarPrompt,
		},
		"monarch": {
			ID:             "monarch",
			ToolPrefixes:   []string{"monarch."},
			ExtraToolNames: []string{"llm.generate"},
			PromptBuilder:  buildMonarchLitePrompt,
		},
	}
}

func sharedACLPromptPreamble(toolList string) string {
	return `You are an ACL code generator. ACL is a language for auditable AI agent workflows.

Generate VALID ACL code for the user's request using ONLY these tools:
` + toolList + `

ACL SYNTAX RULES:
- Output ONLY ACL code (no markdown, no explanation)
- Use AGENT ... END
- Use STEP name = TOOL toolname(...)
- Use OUT result and RESULT result
- Use MUST for critical validations
- Use CHECK for non-critical evidence checks
- Use llm.generate as the LAST step for the human-readable answer (when available)
- All strings must be single-line
- Prefer simple, minimal steps

REFERENCE RULES:
- Use {step.field} only to reference previous step outputs
- Do not invent fields that are not in the schemas below
- Use literal values for dates/numbers/strings (except {step.field} references)
`
}

func buildSplitwiseAgenticPrompt(today, _ string, toolList string) string {
	return sharedACLPromptPreamble(toolList) + `
MODE: Splitwise-style shared expense assistant (sandbox demo tools).
TODAY: ` + today + `

TOOL SCHEMAS:
- demo.splitwise.find_contact(name) -> {query, matches, count}
- demo.splitwise.create_expense(description, total_amount OR amount_each, currency, paid_by, person) -> {status, expense, split, balances, message}
- demo.splitwise.get_balances() -> {balances, count}
- llm.generate(prompt="...") -> {text}

BEHAVIOR:
- If the request is about balances, use demo.splitwise.get_balances.
- If the request logs a shared expense and the payer is missing/ambiguous, DO NOT call create_expense. Instead ask a clarification question via llm.generate.
- If a person name is present, you may call demo.splitwise.find_contact(name="...") first.
- When you create an expense, verify success with MUST create.status == "ok".
- Keep responses friendly and concise (1-3 sentences).

EXAMPLE (clarify before write):
AGENT SplitExpenseClarify
  OUT answer
  TOOLS demo.splitwise.find_contact, llm.generate

  STEP people = TOOL demo.splitwise.find_contact(name="Sarah")
  STEP answer = TOOL llm.generate(prompt="The user said: Dinner with Sarah - 20 USD each. I need one clarification before creating a split expense: ask who paid the full bill. Respond in one sentence.")
  MUST has(answer, "text")
  RESULT answer
END
`
}

func buildSupportRefundPrompt(today, _ string, toolList string) string {
	return sharedACLPromptPreamble(toolList) + `
MODE: Support refund assistant (sandbox demo tools).
TODAY: ` + today + `

TOOL SCHEMAS:
- demo.support.get_order(order_id) -> {found, order_id, status, total_amount, currency, customer_email, customer_name, refund_eligible, reason_hint}
- demo.support.refund_order(order_id, amount, reason, approved) -> {status, ...}
- demo.support.send_email(to, subject, body) -> {status, message_id, preview, sent_at}
- llm.generate(prompt="...") -> {text}

BEHAVIOR:
- Always fetch the order first.
- If order not found, explain and stop.
- If the request says \"do not execute yet\" or does not include explicit approval, do NOT refund. Draft/prepare response text only.
- Only call demo.support.refund_order with approved=true when the user clearly requested execution/approval.
- If refund succeeds, optionally send demo.support.send_email and MUST refund.status == "refunded".
- Keep the final answer short and operational.

EXAMPLE (execute approved refund):
AGENT RefundOrder
  OUT answer
  TOOLS demo.support.get_order, demo.support.refund_order, demo.support.send_email, llm.generate

  STEP order = TOOL demo.support.get_order(order_id="10482")
  MUST order.found == true
  MUST order.refund_eligible == true
  STEP refund = TOOL demo.support.refund_order(order_id="10482", approved=true, reason="Customer requested refund")
  MUST refund.status == "refunded"
  STEP mail = TOOL demo.support.send_email(to=order.customer_email, subject="Your refund has been processed", body="Your refund for order #10482 has been processed.")
  STEP answer = TOOL llm.generate(prompt="Summarize this support action in 1-2 sentences using: order={order}, refund={refund}, email={mail}.")
  MUST has(answer, "text")
  RESULT answer
END
`
}

func buildCalendarPrompt(today, _ string, toolList string) string {
	return sharedACLPromptPreamble(toolList) + `
MODE: Calendar move + notify assistant (sandbox demo tools).
TODAY: ` + today + `

TOOL SCHEMAS:
- demo.calendar.find_event(query) -> {query, events, count}
- demo.calendar.move_event(event_id, new_starts_at) -> {status, event_id, title, old_starts_at, new_starts_at}
- demo.calendar.send_note(event_id, message) -> {status, note_id, attendee_count, preview}
- llm.generate(prompt="...") -> {text}

BEHAVIOR:
- Find the target event before moving it.
- If multiple events could match, ask a clarification question instead of moving.
- If moving an event, MUST move.status == "moved".
- If the user asks to notify attendees, call demo.calendar.send_note.
- Final response should confirm what changed.
- For read-only questions (e.g. "do I have any meetings?", "when is my meeting?"), do NOT fail the agent when count is 0. Answer gracefully instead.

ACL PITFALLS TO AVOID (important):
- DO NOT write CHECK {step.count} == 1  (wrong)
- DO write CHECK step.count == 1        (correct)
- For read-only queries, prefer no CHECK on count, or handle zero/multi results in llm.generate response text
- DO NOT write OUT {answer.text}        (wrong)
- DO write OUT answer followed by RESULT answer (correct)
- Every tool call must include TOOL keyword: STEP x = TOOL demo.calendar.find_event(...)

EXAMPLE (read-only question):
AGENT WhenIsMeeting
  OUT answer
  TOOLS demo.calendar.find_event, llm.generate

  STEP ev = TOOL demo.calendar.find_event(query="coffee")
  CHECK ev.count >= 1
  STEP answer = TOOL llm.generate(prompt="Using these events: {ev.events}, answer when the coffee meeting is in 1 sentence.")
  MUST has(answer, "text")
  RESULT answer
END
`
}

func buildZapierCalendarPrompt(today, _ string, toolList string) string {
	return sharedACLPromptPreamble(toolList) + `
MODE: Zapier Calendar bridge (MVP).
TODAY: ` + today + `

TOOL SCHEMAS:
- zapier.invoke(action, mode, approved, title, starts_at, location, attendee, attendee_email, notes, ...extra) -> {status, mode, action, approved, configured, request_id, payload, accepted, http_status?, response_text?, preview}
- llm.generate(prompt="...") -> {text}

GOAL:
- Convert natural-language scheduling requests into a safe Zapier action call.
- Use action="calendar.create_event" for create/schedule requests.
- Default to preview mode unless the user explicitly confirms execution ("approved", "confirm", "yes create it", "do it").

BEHAVIOR:
- If details are missing (title/date/time), ask a short clarification question via llm.generate and do not call zapier.invoke.
- For non-confirmed create requests:
  - Use STEP z = TOOL zapier.invoke(..., action="calendar.create_event", mode="preview", approved=false, ...)
  - MUST z.status == "preview"
  - Then llm.generate should summarize the preview and ask for confirmation.
- For explicitly confirmed create requests:
  - Use mode="execute" and approved=true
  - MUST (z.status == "accepted") or (z.status == "not_configured")
  - If z.status == "not_configured", clearly explain the flow is ready but webhook is not configured yet.
- Keep responses concise (1-3 sentences).

ACL PITFALLS TO AVOID:
- Every tool call must include TOOL keyword
- OUT declares a variable name, not a string expression
- Use {step.field} only inside quoted strings

EXAMPLE (preview):
AGENT SchedulePreview
  OUT answer
  TOOLS zapier.invoke, llm.generate

  STEP z = TOOL zapier.invoke(action="calendar.create_event", mode="preview", approved=false, title="Lunch with Ali", starts_at="2026-02-23T13:00:00", location="Home")
  MUST z.status == "preview"
  STEP answer = TOOL llm.generate(prompt="Using this preview payload: {z.preview}, confirm what would be created and ask the user to confirm execution in 1-2 sentences.")
  MUST has(answer, "text")
  RESULT answer
END
`
}

func buildMonarchLitePrompt(today, monthStart, toolList string) string {
	return sharedACLPromptPreamble(toolList) + `
MODE: Monarch finance assistant.
TODAY: ` + today + `
CURRENT MONTH START: ` + monthStart + `

TOOL SCHEMAS:
- monarch.get_transactions(category, start_date, end_date, limit) -> {transactions, count}
- monarch.get_budgets() -> {budgets, total_budgeted, total_spent, total_remaining}
- monarch.get_cashflow() -> {income, expenses, savings, savings_rate, by_category}
- monarch.get_net_worth() -> {current, assets, liabilities, history}
- monarch.create_transaction(amount, description, category) -> {status, transaction, message}
- llm.generate(prompt="...") -> {text}

BEHAVIOR:
- Use monarch.* tools for finance requests.
- For \"this month\", use start_date="` + monthStart + `".
- Use llm.generate as the final step.
- Keep the response concise and helpful.
`
}

// cleanACLCode removes markdown fences and leading/trailing whitespace.
func cleanACLCode(s string) string {
	// Remove ```acl ... ``` or ``` ... ```
	lines := make([]string, 0)
	inFence := false
	for _, line := range splitLines(s) {
		trimmed := trimSpace(line)
		if hasPrefix(trimmed, "```") {
			inFence = !inFence
			continue
		}
		lines = append(lines, line)
	}
	return joinLines(lines)
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

func trimSpace(s string) string {
	i, j := 0, len(s)
	for i < j && (s[i] == ' ' || s[i] == '\t' || s[i] == '\r') {
		i++
	}
	for j > i && (s[j-1] == ' ' || s[j-1] == '\t' || s[j-1] == '\r') {
		j--
	}
	return s[i:j]
}

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

func joinLines(lines []string) string {
	result := ""
	for i, l := range lines {
		if i > 0 {
			result += "\n"
		}
		result += l
	}
	return result
}

// ensure unused imports don't break build
var _ = receipt.Receipt{}
