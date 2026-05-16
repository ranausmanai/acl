package parser_test

import (
	"strings"
	"testing"

	"github.com/ranausmanai/acl/internal/ast"
	"github.com/ranausmanai/acl/internal/lexer"
	"github.com/ranausmanai/acl/internal/parser"
)

// parseSrc is the shared lex+parse helper.
func parseSrc(t *testing.T, src string) []ast.Node {
	t.Helper()
	toks, err := lexer.New(src).Tokenize()
	if err != nil {
		t.Fatalf("lex: %v", err)
	}
	nodes, err := parser.Parse(toks)
	if err != nil {
		t.Fatalf("parse: %v\nsrc:\n%s", err, src)
	}
	return nodes
}

func parseExpectError(t *testing.T, src, wantSubstr string) {
	t.Helper()
	toks, err := lexer.New(src).Tokenize()
	if err != nil {
		// Some malformed inputs fail at lex; that's still a rejection.
		if wantSubstr == "" || strings.Contains(err.Error(), wantSubstr) {
			return
		}
		t.Fatalf("lex failed with %q, want substr %q", err.Error(), wantSubstr)
	}
	_, err = parser.Parse(toks)
	if err == nil {
		t.Fatalf("expected parse error for src:\n%s", src)
	}
	if wantSubstr != "" && !strings.Contains(err.Error(), wantSubstr) {
		t.Fatalf("parse error %q does not contain %q", err.Error(), wantSubstr)
	}
}

// findAgent returns the first AgentDef with the given name, or fails.
func findAgent(t *testing.T, nodes []ast.Node, name string) *ast.AgentDef {
	t.Helper()
	for _, n := range nodes {
		if a, ok := n.(*ast.AgentDef); ok && a.Name == name {
			return a
		}
	}
	t.Fatalf("no AGENT %q in parsed nodes", name)
	return nil
}

// ─── Basic structure ─────────────────────────────────────────────────────────

func TestParse_BasicProgram(t *testing.T) {
	src := `
INTENT "Smoke test"
ALLOW  http.get

AGENT Smoke
  TOOLS http.get
  STEP page = TOOL http.get(url="https://example.com")
    CHECK page.status == 200
  RESULT page
`
	nodes := parseSrc(t, src)

	var sawIntent, sawAllow bool
	for _, n := range nodes {
		switch x := n.(type) {
		case *ast.IntentNode:
			sawIntent = true
			if x.Text != "Smoke test" {
				t.Errorf("intent text = %q, want %q", x.Text, "Smoke test")
			}
		case *ast.AllowNode:
			sawAllow = true
			if len(x.Tools) != 1 || x.Tools[0] != "http.get" {
				t.Errorf("ALLOW tools = %v, want [http.get]", x.Tools)
			}
		}
	}
	if !sawIntent {
		t.Error("INTENT node missing")
	}
	if !sawAllow {
		t.Error("ALLOW node missing")
	}

	a := findAgent(t, nodes, "Smoke")
	if len(a.Steps) != 1 {
		t.Fatalf("Smoke has %d steps, want 1", len(a.Steps))
	}
	if a.Steps[0].Kind != ast.SKTool {
		t.Errorf("step kind = %v, want SKTool", a.Steps[0].Kind)
	}
	if a.Steps[0].Target != "http.get" {
		t.Errorf("step target = %q, want http.get", a.Steps[0].Target)
	}
	if a.Steps[0].Check == nil {
		t.Error("step CHECK missing")
	}
	if a.ResultExpr == nil {
		t.Error("RESULT missing")
	}
}

func TestParse_LimitClause(t *testing.T) {
	src := `
INTENT "limit"
ALLOW http.get
LIMIT time=60s calls=20 retries=3

AGENT A
  TOOLS http.get
  STEP page = TOOL http.get(url="https://x")
  RESULT page
`
	nodes := parseSrc(t, src)
	var lim *ast.LimitNode
	for _, n := range nodes {
		if l, ok := n.(*ast.LimitNode); ok {
			lim = l
			break
		}
	}
	if lim == nil {
		t.Fatal("LIMIT node missing")
	}
	if lim.Retries != 3 {
		t.Errorf("retries = %d, want 3", lim.Retries)
	}
	if lim.TimeS == nil || *lim.TimeS != 60 {
		t.Errorf("time_s = %v, want 60", lim.TimeS)
	}
	if lim.Calls == nil || *lim.Calls != 20 {
		t.Errorf("calls = %v, want 20", lim.Calls)
	}
}

// ─── PARALLEL block ───────────────────────────────────────────────────────────

func TestParse_ParallelBlock(t *testing.T) {
	src := `
INTENT "parallel"
ALLOW http.get

AGENT HealthCheck
  TOOLS http.get
  PARALLEL
    STEP api  = TOOL http.get(url="https://api.example.com/health")
    STEP db   = TOOL http.get(url="https://db.example.com/health")
    STEP auth = TOOL http.get(url="https://auth.example.com/health")
  END
  CHECK api.status == 200
  RESULT api
`
	nodes := parseSrc(t, src)
	a := findAgent(t, nodes, "HealthCheck")
	if len(a.Steps) != 3 {
		t.Fatalf("HealthCheck has %d steps, want 3", len(a.Steps))
	}
	groups := map[int]int{}
	for _, s := range a.Steps {
		groups[s.ParallelGroup]++
	}
	if len(groups) != 1 {
		t.Errorf("expected all 3 steps in one ParallelGroup, got %+v", groups)
	}
	for _, s := range a.Steps {
		if s.ParallelGroup == 0 {
			t.Errorf("step %q has ParallelGroup=0; want non-zero", s.Name)
		}
	}
}

func TestParse_ParallelMixedWithSequential(t *testing.T) {
	src := `
INTENT "mix"
ALLOW http.get

AGENT Mix
  TOOLS http.get
  STEP first = TOOL http.get(url="https://1")
  PARALLEL
    STEP a = TOOL http.get(url="https://a")
    STEP b = TOOL http.get(url="https://b")
  END
  STEP last = TOOL http.get(url="https://z")
  RESULT last
`
	a := findAgent(t, parseSrc(t, src), "Mix")
	if len(a.Steps) != 4 {
		t.Fatalf("want 4 steps, got %d", len(a.Steps))
	}
	if a.Steps[0].ParallelGroup != 0 {
		t.Error("first should be sequential")
	}
	if a.Steps[1].ParallelGroup == 0 || a.Steps[2].ParallelGroup == 0 {
		t.Error("a,b should be parallel")
	}
	if a.Steps[1].ParallelGroup != a.Steps[2].ParallelGroup {
		t.Error("a,b should share the same parallel group id")
	}
	if a.Steps[3].ParallelGroup != 0 {
		t.Error("last should be sequential")
	}
}

// ─── REMOTE ───────────────────────────────────────────────────────────────────

func TestParse_RemoteDeclaration(t *testing.T) {
	src := `
INTENT "remote"
ALLOW http.get

REMOTE AnalyticsAgent "http://analytics-service:8080"

AGENT Caller
  TOOLS http.get
  STEP r = AGENT AnalyticsAgent()
  RESULT r
`
	nodes := parseSrc(t, src)
	var rd *ast.RemoteDecl
	for _, n := range nodes {
		if r, ok := n.(*ast.RemoteDecl); ok {
			rd = r
			break
		}
	}
	if rd == nil {
		t.Fatal("REMOTE node missing")
	}
	if rd.Name != "AnalyticsAgent" {
		t.Errorf("remote name = %q, want AnalyticsAgent", rd.Name)
	}
	if rd.BaseURL != "http://analytics-service:8080" {
		t.Errorf("remote url = %q", rd.BaseURL)
	}
}

// ─── SCHEDULE ─────────────────────────────────────────────────────────────────

func TestParse_ScheduleDeclaration(t *testing.T) {
	src := `
INTENT "schedule"
ALLOW http.get

SCHEDULE HealthMonitor "*/5 * * * *"

AGENT HealthMonitor
  TOOLS http.get
  STEP page = TOOL http.get(url="https://x")
    CHECK page.status == 200
  RESULT page
`
	nodes := parseSrc(t, src)
	var sd *ast.ScheduleDecl
	for _, n := range nodes {
		if s, ok := n.(*ast.ScheduleDecl); ok {
			sd = s
			break
		}
	}
	if sd == nil {
		t.Fatal("SCHEDULE node missing")
	}
	if sd.AgentName != "HealthMonitor" {
		t.Errorf("schedule agent = %q", sd.AgentName)
	}
	if sd.Cron != "*/5 * * * *" {
		t.Errorf("schedule cron = %q", sd.Cron)
	}
}

// ─── TEMPLATE / MAKE / GROUP ──────────────────────────────────────────────────

func TestParse_TemplateMakeGroup(t *testing.T) {
	src := `
INTENT "template"
ALLOW http.get

TEMPLATE SiteChecker(target_url)
  TOOLS http.get
  STEP page = TOOL http.get(url=target_url)
    CHECK page.status == 200
  RESULT page

MAKE SiteChecker(target_url="https://a") AS ACheck

GROUP AllChecks = FOR site IN sites : MAKE SiteChecker(target_url=site.url)
`
	nodes := parseSrc(t, src)

	var tmpl *ast.TemplateDef
	var mk *ast.MakeStmt
	var grp *ast.GroupStmt
	for _, n := range nodes {
		switch x := n.(type) {
		case *ast.TemplateDef:
			tmpl = x
		case *ast.MakeStmt:
			mk = x
		case *ast.GroupStmt:
			grp = x
		}
	}

	if tmpl == nil {
		t.Fatal("TemplateDef missing")
	}
	if tmpl.Name != "SiteChecker" {
		t.Errorf("template name = %q", tmpl.Name)
	}
	if len(tmpl.Params) != 1 || tmpl.Params[0] != "target_url" {
		t.Errorf("template params = %v", tmpl.Params)
	}

	if mk == nil {
		t.Fatal("MakeStmt missing")
	}
	if mk.AgentName != "ACheck" {
		t.Errorf("make agent name = %q", mk.AgentName)
	}
	if mk.Template != "SiteChecker" {
		t.Errorf("make template = %q", mk.Template)
	}

	if grp == nil {
		t.Fatal("GroupStmt missing")
	}
	if grp.GroupName != "AllChecks" {
		t.Errorf("group name = %q", grp.GroupName)
	}
	if grp.Var != "site" {
		t.Errorf("group var = %q", grp.Var)
	}
	if grp.Source != "sites" {
		t.Errorf("group source = %q", grp.Source)
	}
	if grp.MakeTemplate != "SiteChecker" {
		t.Errorf("group make template = %q", grp.MakeTemplate)
	}
}

// ─── ONFAIL policies ──────────────────────────────────────────────────────────

func TestParse_OnFailPolicies(t *testing.T) {
	cases := []struct {
		name     string
		policy   string
		fallback string
	}{
		{"retry", "retry", ""},
		{"stop", "stop", ""},
		{"askhuman", "askhuman", ""},
		{"fallback recover", "fallback", "recover"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := `
INTENT "onfail"
ALLOW http.get

AGENT A
  TOOLS http.get
  STEP page = TOOL http.get(url="https://x")
    CHECK page.status == 200
    ONFAIL ` + c.name + `
  STEP recover = TOOL http.get(url="https://fallback")
  RESULT page
`
			a := findAgent(t, parseSrc(t, src), "A")
			if a.Steps[0].OnFail == nil {
				t.Fatal("OnFail missing")
			}
			if a.Steps[0].OnFail.Policy != c.policy {
				t.Errorf("policy = %q, want %q", a.Steps[0].OnFail.Policy, c.policy)
			}
			if c.fallback != "" && a.Steps[0].OnFail.FallbackStep != c.fallback {
				t.Errorf("fallback = %q, want %q", a.Steps[0].OnFail.FallbackStep, c.fallback)
			}
		})
	}
}

// ─── Interpolated strings ─────────────────────────────────────────────────────

func TestParse_StringInterpolation(t *testing.T) {
	src := `
INTENT "interp"
ALLOW email.draft

AGENT A
  IN  name
  TOOLS email.draft
  STEP draft = TOOL email.draft(
    to="x@y.com",
    subject="Hello {name}",
    body="Hi {name}, your refund is ready.")
  RESULT draft
`
	a := findAgent(t, parseSrc(t, src), "A")
	subj := a.Steps[0].Args["subject"]
	if subj == nil {
		t.Fatal("subject arg missing")
	}
	if len(subj.Parts) == 0 {
		t.Fatalf("expected interpolated Parts for subject, got Literal=%v", subj.Literal)
	}
	// The interpolated arg must have at least one Expr part.
	sawExpr := false
	for _, p := range subj.Parts {
		if p.Expr != nil {
			sawExpr = true
		}
	}
	if !sawExpr {
		t.Error("expected at least one Expr part in interpolated subject")
	}

	// Non-interpolated string should be a plain literal.
	to := a.Steps[0].Args["to"]
	if !to.IsLiteral || to.Literal != "x@y.com" {
		t.Errorf("to should be a plain literal, got %+v", to)
	}
}

// ─── List literal argument ────────────────────────────────────────────────────

func TestParse_ListLiteralArg(t *testing.T) {
	src := `
INTENT "list"
ALLOW extract.table

AGENT A
  TOOLS extract.table
  STEP rows = TOOL extract.table(text="x", columns=["plan", "price"])
  RESULT rows
`
	a := findAgent(t, parseSrc(t, src), "A")
	cols := a.Steps[0].Args["columns"]
	if !cols.IsLiteral {
		t.Fatal("columns should be a literal list")
	}
	list, ok := cols.Literal.([]any)
	if !ok {
		t.Fatalf("columns literal type = %T, want []any", cols.Literal)
	}
	if len(list) != 2 || list[0] != "plan" || list[1] != "price" {
		t.Errorf("columns = %v, want [plan price]", list)
	}
}

// ─── AGENT-as-step ────────────────────────────────────────────────────────────

func TestParse_AgentStep(t *testing.T) {
	src := `
INTENT "compose"
ALLOW http.get

AGENT Inner
  TOOLS http.get
  STEP page = TOOL http.get(url="https://x")
  RESULT page

AGENT Outer
  TOOLS http.get
  STEP inner = AGENT Inner()
  RESULT inner
`
	a := findAgent(t, parseSrc(t, src), "Outer")
	if a.Steps[0].Kind != ast.SKAgent {
		t.Errorf("step kind = %v, want SKAgent", a.Steps[0].Kind)
	}
	if a.Steps[0].Target != "Inner" {
		t.Errorf("step target = %q, want Inner", a.Steps[0].Target)
	}
}

// ─── Error paths — must not panic, must report a useful message ──────────────

func TestParse_MissingClosingQuote(t *testing.T) {
	parseExpectError(t, `INTENT "unterminated`, "")
}

func TestParse_UnknownTopLevelKeyword(t *testing.T) {
	parseExpectError(t, `
INTENT "x"
ALLOW http.get

WIBBLE wobble
`, "")
}

func TestParse_BadOnFailPolicy(t *testing.T) {
	src := `
INTENT "bad"
ALLOW http.get

AGENT A
  TOOLS http.get
  STEP page = TOOL http.get(url="https://x")
    CHECK page.status == 200
    ONFAIL banana
  RESULT page
`
	parseExpectError(t, src, "ONFAIL")
}

func TestParse_StepInsideAgentRequiresAssignment(t *testing.T) {
	// Missing the "name =" prefix should not silently parse.
	src := `
INTENT "bad"
ALLOW http.get

AGENT A
  TOOLS http.get
  STEP TOOL http.get(url="https://x")
  RESULT 1
`
	parseExpectError(t, src, "")
}
