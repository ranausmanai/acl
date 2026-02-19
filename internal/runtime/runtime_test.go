package runtime_test

import (
	"context"
	"testing"

	"github.com/ranausmanai/acl/internal/receipt"
	"github.com/ranausmanai/acl/internal/runtime"
	"github.com/ranausmanai/acl/tools/builtin"
)

func run(t *testing.T, src string, vars map[string]any) *receipt.Receipt {
	t.Helper()
	reg := builtin.NewMockRegistry()
	cfg := runtime.Config{Vars: vars}
	r, err := runtime.RunSource(context.Background(), src, cfg, reg)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	return r
}

func runExpectFail(t *testing.T, src string, vars map[string]any) *receipt.Receipt {
	t.Helper()
	reg := builtin.NewMockRegistry()
	cfg := runtime.Config{Vars: vars}
	r, _ := runtime.RunSource(context.Background(), src, cfg, reg)
	return r
}

func TestSimpleToolCall(t *testing.T) {
	src := `
INTENT "simple test"
ALLOW http.get
AGENT TestAgent
  TOOLS http.get
  STEP page = TOOL http.get(url="https://pricing.example.com")
    CHECK page.status == 200
  RESULT page
`
	r := run(t, src, nil)
	if r.Status != "success" {
		t.Errorf("status: %s (err: %s)", r.Status, r.Error)
	}
	if len(r.Agents) == 0 {
		t.Fatal("no agents in receipt")
	}
	if r.Agents[0].Result == nil {
		t.Error("no result")
	}
}

func TestMustGate(t *testing.T) {
	src := `
INTENT "must gate"
ALLOW sql.query
AGENT TestAgent
  TOOLS sql.query
  STEP rows = TOOL sql.query(query="SELECT * FROM pricing")
  MUST count(rows.rows) > 100
  RESULT rows
`
	r := runExpectFail(t, src, nil)
	if r.Status != "failed" {
		t.Errorf("expected failed, got %s", r.Status)
	}
	if len(r.Agents) > 0 && r.Agents[0].MustPassed != nil && *r.Agents[0].MustPassed {
		t.Error("MUST should have failed")
	}
}

func TestCheckPassed(t *testing.T) {
	src := `
INTENT "check test"
ALLOW sql.query
AGENT TestAgent
  TOOLS sql.query
  STEP rows = TOOL sql.query(query="SELECT * FROM pricing")
    CHECK count(rows.rows) >= 1
  RESULT rows
`
	r := run(t, src, nil)
	if r.Status != "success" {
		t.Errorf("status: %s", r.Status)
	}
}

func TestLLMGenerate(t *testing.T) {
	src := `
INTENT "llm test"
ALLOW llm.generate
AGENT TestAgent
  TOOLS llm.generate
  STEP out = TOOL llm.generate(prompt="Write a summary", data=null, format="json")
    CHECK len(out.text) > 5
  RESULT out
`
	r := run(t, src, nil)
	if r.Status != "success" {
		t.Fatalf("llm: %s %s", r.Status, r.Error)
	}
}

func TestFallback(t *testing.T) {
	src := `
INTENT "fallback test"
ALLOW http.get, sql.query
AGENT TestAgent
  TOOLS http.get, sql.query
  STEP live = TOOL http.get(url="https://status.example.com/health")
    CHECK live.status == 999
    ONFAIL fallback backup

  STEP backup = TOOL sql.query(query="SELECT * FROM pricing")

  RESULT backup
`
	r := run(t, src, nil)
	if r.Status != "success" {
		t.Errorf("fallback: %s %s", r.Status, r.Error)
	}
	// The backup step should appear in the trace.
	found := false
	for _, s := range r.Agents[0].Steps {
		if s.Name == "backup" {
			found = true
		}
	}
	if !found {
		t.Error("backup step not in trace")
	}
}

func TestOnFailStop(t *testing.T) {
	src := `
INTENT "stop test"
ALLOW sql.query
AGENT TestAgent
  TOOLS sql.query
  STEP rows = TOOL sql.query(query="SELECT * FROM nonexistent_table")
    ONFAIL stop
  RESULT rows
`
	r := runExpectFail(t, src, nil)
	if r.Status != "failed" {
		t.Errorf("expected failed, got %s", r.Status)
	}
}

func TestTemplateExpansion(t *testing.T) {
	src := `
INTENT "template test"
ALLOW http.get
TEMPLATE FetchAgent(target_url)
  TOOLS http.get
  STEP page = TOOL http.get(url=target_url)
    CHECK page.status == 200
  RESULT page

MAKE FetchAgent(target_url="https://pricing.example.com") AS MyFetcher

AGENT Orchestrator
  TOOLS http.get
  STEP r = AGENT MyFetcher()
  RESULT r
`
	r := run(t, src, nil)
	if r.Status != "success" {
		t.Fatalf("template: %s %s", r.Status, r.Error)
	}
}

func TestGroupExpansion(t *testing.T) {
	src := `
INTENT "group test"
ALLOW http.get
TEMPLATE URLFetcher(fetch_url)
  TOOLS http.get
  STEP page = TOOL http.get(url=fetch_url)
  RESULT page

GROUP fetchers = FOR item IN urls : MAKE URLFetcher(fetch_url=item.url)

AGENT Orchestrator
  IN urls
  TOOLS http.get
  STEP r0 = AGENT fetchers_0()
  STEP r1 = AGENT fetchers_1()
  RESULT r0
`
	vars := map[string]any{
		"urls": []any{
			map[string]any{"url": "https://pricing.example.com"},
			map[string]any{"url": "https://api.example.com"},
		},
	}
	r := run(t, src, vars)
	if r.Status != "success" {
		t.Fatalf("group: %s %s", r.Status, r.Error)
	}
}

func TestNestedAgents(t *testing.T) {
	src := `
INTENT "nested agents"
ALLOW sql.query, llm.generate
AGENT DataAgent
  TOOLS sql.query
  STEP rows = TOOL sql.query(query="SELECT * FROM pricing")
  RESULT rows

AGENT AnalysisAgent
  TOOLS llm.generate
  STEP data = AGENT DataAgent()
  STEP out  = TOOL llm.generate(prompt="Summarize", data=data, format="json")
  RESULT out
`
	r := run(t, src, nil)
	if r.Status != "success" {
		t.Fatalf("nested: %s %s", r.Status, r.Error)
	}
}

func TestVarsInjection(t *testing.T) {
	src := `
INTENT "vars test"
ALLOW llm.generate
AGENT TestAgent
  IN  user_goal
  TOOLS llm.generate
  STEP out = TOOL llm.generate(prompt=user_goal, data=null, format="plain")
  RESULT out
`
	vars := map[string]any{"user_goal": "explain quantum computing simply"}
	r := run(t, src, vars)
	if r.Status != "success" {
		t.Fatalf("vars: %s %s", r.Status, r.Error)
	}
}

func TestReceiptStructure(t *testing.T) {
	src := `
INTENT "receipt test"
ALLOW http.get
AGENT TestAgent
  TOOLS http.get
  STEP page = TOOL http.get(url="https://pricing.example.com")
  RESULT page
`
	r := run(t, src, nil)
	if r.ACLVersion == "" {
		t.Error("ACLVersion empty")
	}
	if r.Intent != "receipt test" {
		t.Errorf("intent: %q", r.Intent)
	}
	if r.Timestamp == "" {
		t.Error("timestamp empty")
	}
	if len(r.Agents) != 1 {
		t.Errorf("agent count: %d", len(r.Agents))
	}
	agent := r.Agents[0]
	if len(agent.Steps) != 1 {
		t.Errorf("step count: %d", len(agent.Steps))
	}
	step := agent.Steps[0]
	if step.DurationMS < 0 {
		t.Error("negative duration")
	}
	if step.OutputHash == "" {
		t.Error("output hash empty")
	}
}

func TestCaching(t *testing.T) {
	src := `
INTENT "cache test"
ALLOW http.get
AGENT TestAgent
  TOOLS http.get
  STEP page = TOOL http.get(url="https://pricing.example.com")
  RESULT page
`
	dir := t.TempDir()
	reg := builtin.NewMockRegistry()
	cfg := runtime.Config{CacheDir: dir}

	// First run: cache miss.
	r1, err := runtime.RunSource(context.Background(), src, cfg, reg)
	if err != nil {
		t.Fatal(err)
	}
	if r1.Status != "success" {
		t.Fatal("first run failed")
	}

	// Second run: cache hit.
	r2, err := runtime.RunSource(context.Background(), src, cfg, reg)
	if err != nil {
		t.Fatal(err)
	}
	if r2.Status != "success" {
		t.Fatal("second run failed")
	}
	if !r2.Agents[0].Steps[0].CacheHit {
		t.Error("expected cache hit on second run")
	}
}

func TestExtractTable(t *testing.T) {
	src := `
INTENT "extract test"
ALLOW http.get, extract.table
AGENT TestAgent
  TOOLS http.get, extract.table
  STEP page = TOOL http.get(url="https://pricing.example.com")
  STEP rows = TOOL extract.table(text=page.text, columns=["plan","price"])
    CHECK count(rows.rows) >= 1
  RESULT rows
`
	r := run(t, src, nil)
	if r.Status != "success" {
		t.Fatalf("extract: %s %s", r.Status, r.Error)
	}
}

func TestSQLQuery(t *testing.T) {
	src := `
INTENT "sql test"
ALLOW sql.query
AGENT TestAgent
  TOOLS sql.query
  STEP r = TOOL sql.query(query="SELECT * FROM incidents WHERE status = 'open'")
    CHECK count(r.rows) >= 1
  RESULT r
`
	r := run(t, src, nil)
	if r.Status != "success" {
		t.Fatalf("sql: %s %s", r.Status, r.Error)
	}
}

func TestEmailDraft(t *testing.T) {
	src := `
INTENT "email test"
ALLOW llm.generate, email.draft
AGENT TestAgent
  TOOLS llm.generate, email.draft
  STEP body = TOOL llm.generate(prompt="Write email body", data=null, format="plain")
  STEP draft = TOOL email.draft(to="user@example.com", subject="Hello", body=body.text)
    CHECK has(draft, "message_id")
  RESULT draft
`
	r := run(t, src, nil)
	if r.Status != "success" {
		t.Fatalf("email: %s %s", r.Status, r.Error)
	}
}
