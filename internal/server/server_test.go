package server_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"acl/internal/server"
	"acl/tools/builtin"
)

// testACL is a minimal program with two agents that work with the mock registry.
const testACL = `
INTENT "server test"
ALLOW http.get, sql.query

AGENT HealthAgent
  TOOLS http.get
  STEP page = TOOL http.get(url="https://api.example.com")
    CHECK page.status == 200
  RESULT page

AGENT DataAgent
  IN  query_filter
  TOOLS sql.query
  STEP rows = TOOL sql.query(query="SELECT * FROM incidents WHERE status = 'open'")
    CHECK count(rows.rows) >= 1
  RESULT rows
`

func newTestHTTPServer(t *testing.T) *httptest.Server {
	t.Helper()
	t.Setenv("ACL_NO_HISTORY", "1")
	reg := builtin.NewMockRegistry()
	srv, err := server.NewServer(testACL, reg)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

// ── /health ───────────────────────────────────────────────────────────────────

func TestHealthEndpoint(t *testing.T) {
	ts := newTestHTTPServer(t)

	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status: got %d, want 200", resp.StatusCode)
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("status field: %v", body["status"])
	}
	agents, ok := body["agents"].([]any)
	if !ok {
		t.Fatalf("agents field missing or wrong type: %T", body["agents"])
	}
	if len(agents) != 2 {
		t.Errorf("expected 2 agents, got %d: %v", len(agents), agents)
	}
}

func TestHealthDoesNotRequireAuth(t *testing.T) {
	t.Setenv("ACL_NO_HISTORY", "1")
	t.Setenv("ACL_SERVE_API_KEY", "secret")
	reg := builtin.NewMockRegistry()
	srv, err := server.NewServer(testACL, reg)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// No auth header — /health should still return 200.
	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("/health with API key set: expected 200, got %d", resp.StatusCode)
	}
}

// ── /agents ───────────────────────────────────────────────────────────────────

func TestAgentsEndpoint(t *testing.T) {
	ts := newTestHTTPServer(t)

	resp, err := http.Get(ts.URL + "/agents")
	if err != nil {
		t.Fatalf("GET /agents: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status: got %d, want 200", resp.StatusCode)
	}

	var body []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body) != 2 {
		t.Errorf("expected 2 agent descriptors, got %d", len(body))
	}
	names := map[string]bool{}
	for _, a := range body {
		name, _ := a["name"].(string)
		kind, _ := a["kind"].(string)
		names[name] = true
		if kind != "local" {
			t.Errorf("agent %q: expected kind=local, got %q", name, kind)
		}
	}
	for _, want := range []string{"HealthAgent", "DataAgent"} {
		if !names[want] {
			t.Errorf("agent %q not found in /agents response", want)
		}
	}
}

// ── /run/{name} ───────────────────────────────────────────────────────────────

func postRun(t *testing.T, ts *httptest.Server, agentName string, vars map[string]any) *http.Response {
	t.Helper()
	body := map[string]any{"vars": vars}
	data, _ := json.Marshal(body)
	resp, err := http.Post(ts.URL+"/run/"+agentName, "application/json", bytes.NewReader(data))
	if err != nil {
		t.Fatalf("POST /run/%s: %v", agentName, err)
	}
	return resp
}

func TestRunHealthAgent(t *testing.T) {
	ts := newTestHTTPServer(t)

	resp := postRun(t, ts, "HealthAgent", nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status: got %d, want 200", resp.StatusCode)
	}

	var rec map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&rec); err != nil {
		t.Fatalf("decode receipt: %v", err)
	}
	if rec["status"] != "success" {
		t.Errorf("receipt status: %v (error: %v)", rec["status"], rec["error"])
	}
	if rec["intent"] != "server test" {
		t.Errorf("intent: %v", rec["intent"])
	}
}

func TestRunDataAgent(t *testing.T) {
	ts := newTestHTTPServer(t)

	resp := postRun(t, ts, "DataAgent", map[string]any{"query_filter": "open"})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status: got %d, want 200", resp.StatusCode)
	}

	var rec map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&rec); err != nil {
		t.Fatalf("decode receipt: %v", err)
	}
	if rec["status"] != "success" {
		t.Errorf("receipt status: %v (error: %v)", rec["status"], rec["error"])
	}
}

func TestRunNoBody(t *testing.T) {
	ts := newTestHTTPServer(t)

	// Empty body (no vars) should be treated as empty map.
	resp, err := http.Post(ts.URL+"/run/HealthAgent", "application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status: %d", resp.StatusCode)
	}
}

func TestRunAgentNotFound(t *testing.T) {
	ts := newTestHTTPServer(t)

	resp := postRun(t, ts, "NoSuchAgent", nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 for unknown agent, got %d", resp.StatusCode)
	}

	var body map[string]any
	json.NewDecoder(resp.Body).Decode(&body)
	if _, ok := body["error"]; !ok {
		t.Error("expected error field in 404 response")
	}
}

func TestRunInvalidJSON(t *testing.T) {
	ts := newTestHTTPServer(t)

	resp, err := http.Post(ts.URL+"/run/HealthAgent", "application/json",
		bytes.NewBufferString("{not valid json"))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid JSON, got %d", resp.StatusCode)
	}
}

// ── Auth middleware ───────────────────────────────────────────────────────────

func TestAuthMiddlewareBlocks(t *testing.T) {
	t.Setenv("ACL_NO_HISTORY", "1")
	t.Setenv("ACL_SERVE_API_KEY", "my-secret")
	reg := builtin.NewMockRegistry()
	srv, err := server.NewServer(testACL, reg)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// No auth header → 401.
	resp, _ := http.Get(ts.URL + "/agents")
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("no token: expected 401, got %d", resp.StatusCode)
	}

	// Wrong token → 401.
	req, _ := http.NewRequest("GET", ts.URL+"/agents", nil)
	req.Header.Set("Authorization", "Bearer wrong-key")
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("wrong token: expected 401, got %d", resp.StatusCode)
	}
}

func TestAuthMiddlewareAllows(t *testing.T) {
	t.Setenv("ACL_NO_HISTORY", "1")
	t.Setenv("ACL_SERVE_API_KEY", "my-secret")
	reg := builtin.NewMockRegistry()
	srv, err := server.NewServer(testACL, reg)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// Correct token → 200.
	req, _ := http.NewRequest("GET", ts.URL+"/agents", nil)
	req.Header.Set("Authorization", "Bearer my-secret")
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("correct token: expected 200, got %d", resp.StatusCode)
	}
}

func TestNoAuthKeyOpenAccess(t *testing.T) {
	ts := newTestHTTPServer(t) // no ACL_SERVE_API_KEY set

	// Without any auth header, /agents should be accessible.
	resp, _ := http.Get(ts.URL + "/agents")
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("no API key configured: expected 200, got %d", resp.StatusCode)
	}
}

// ── SCHEDULE declaration ──────────────────────────────────────────────────────

func TestScheduleInACL(t *testing.T) {
	// Just verify that a SCHEDULE declaration parses and creates a valid server.
	t.Setenv("ACL_NO_HISTORY", "1")
	src := `
INTENT "scheduled"
ALLOW http.get

SCHEDULE HealthAgent "*/5 * * * *"

AGENT HealthAgent
  TOOLS http.get
  STEP p = TOOL http.get(url="https://api.example.com")
  RESULT p
`
	reg := builtin.NewMockRegistry()
	srv, err := server.NewServer(src, reg)
	if err != nil {
		t.Fatalf("NewServer with SCHEDULE: %v", err)
	}
	names := srv.AgentNames()
	found := false
	for _, n := range names {
		if n == "HealthAgent" {
			found = true
		}
	}
	if !found {
		t.Errorf("HealthAgent not found in agent names: %v", names)
	}
}
