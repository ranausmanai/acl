// Package ast defines the Abstract Syntax Tree for ACL programs.
//
// An ACL program is a sequence of top-level statements.  After template
// expansion (performed by the runtime), only IntentNode, AllowNode,
// LimitNode, and AgentDef remain.
package ast

import "github.com/ranausmanai/acl/internal/evidence"

// ── Top-level nodes ──────────────────────────────────────────────────────────

// Node is the marker interface for all AST nodes.
type Node interface{ nodeTag() }

type IntentNode struct{ Text string }
type AllowNode struct{ Tools []string }
type LimitNode struct {
	TimeS   *float64 // nil = no limit
	Calls   *int     // nil = no limit
	Retries int      // default 2
	Cost    *float64 // nil = no limit (future)
}

// ── Agent definition ─────────────────────────────────────────────────────────

type AgentDef struct {
	Name         string
	Inputs       []string
	Outputs      []string
	Tools        []string
	MustExpr     *evidence.Expr // nil = no MUST gate
	Steps        []*StepDef
	ResultExpr   *evidence.Expr // nil = no RESULT (returns nil)
	FromTemplate string         // set when expanded from a TEMPLATE
	TemplateArgs map[string]any // binding used during expansion
	Line         int
}

// ── Step definition ──────────────────────────────────────────────────────────

// StepDef defines one step within an agent body.
// For SKTool/SKAgent the usual fields apply.
// For SKIf the IfCond/IfThen/IfElse fields are used.
// For SKForeach the ForeachVar/ForeachOver/ForeachBody fields are used.
type StepDef struct {
	Name          string
	Kind          StepKind // SKTool | SKAgent | SKIf | SKForeach
	Target        string   // tool name or agent name (SKTool / SKAgent)
	Args          map[string]*Arg
	Check         *evidence.Expr // nil = no CHECK
	OnFail        *OnFail
	Line          int
	ParallelGroup int // 0 = sequential; same non-zero value = run concurrently

	// SKIf — IF/ELSE block
	IfCond *evidence.Expr
	IfThen []*StepDef
	IfElse []*StepDef // nil when there is no ELSE branch

	// SKForeach — FOREACH var IN expr … END
	ForeachVar  string
	ForeachOver *evidence.Expr
	ForeachBody []*StepDef
}

// StepKind distinguishes step types.
type StepKind int

const (
	SKTool    StepKind = iota // STEP x = TOOL tool(...)
	SKAgent                   // STEP x = AGENT agent(...)
	SKIf                      // IF expr … ELSE … END
	SKForeach                 // FOREACH var IN expr … END
)

func (k StepKind) String() string {
	switch k {
	case SKTool:
		return "tool"
	case SKAgent:
		return "agent"
	case SKIf:
		return "if"
	case SKForeach:
		return "foreach"
	default:
		return "unknown"
	}
}

// Arg is a step argument value: a static literal, a runtime expr, or an
// interpolated string containing one or more {expr} segments.
type Arg struct {
	Literal   any            // non-nil for plain string/int/float/bool/[]any
	ExprRef   *evidence.Expr // non-nil when value is a bare step reference
	IsLiteral bool
	Parts     []ArgPart // non-nil for interpolated strings: "Hello {user.name}"
}

// ArgPart is one segment of an interpolated string.
// Exactly one of Text or Expr is set.
type ArgPart struct {
	Text string         // literal text segment
	Expr *evidence.Expr // {expr} segment — evaluated at runtime
}

// OnFail holds the ONFAIL policy for a step.
type OnFail struct {
	Policy       string // retry | fallback | askhuman | stop
	FallbackStep string // set when Policy == "fallback"
}

// ── Template / MAKE / GROUP ──────────────────────────────────────────────────

type TemplateDef struct {
	Name   string
	Params []string
	Body   *AgentDef // prototype (may contain ExprRef param references)
	Line   int
}

type MakeStmt struct {
	Template  string
	Args      map[string]*Arg
	AgentName string
	Line      int
}

type GroupStmt struct {
	GroupName    string
	Var          string       // loop variable
	Source       string       // variable name from --vars
	MakeTemplate string
	MakeArgs     map[string]*Arg
	Line         int
}

// ── Phase 3: network layer ────────────────────────────────────────────────────

// RemoteDecl registers an agent that lives on another ACL server.
// Syntax: REMOTE AgentName "http://host:8080"
// Once declared, STEP x = AGENT AgentName(args) POSTs to the remote server
// instead of running locally.
type RemoteDecl struct {
	Name    string // agent name (must be unique across local + remote)
	BaseURL string // e.g. "http://host:8080" — /run/{Name} is appended at call time
	Line    int
}

// ScheduleDecl triggers an agent on a cron schedule when `acl serve` is running.
// Syntax: SCHEDULE AgentName "5-field-cron"
// Example: SCHEDULE WeeklyReport "0 9 * * 1"   (every Monday at 09:00)
type ScheduleDecl struct {
	AgentName string // must refer to a local AGENT or REMOTE declaration
	Cron      string // 5-field cron expression: minute hour dom month dow
	Line      int
}

// ── nodeTag markers ──────────────────────────────────────────────────────────

func (*IntentNode) nodeTag()    {}
func (*AllowNode) nodeTag()     {}
func (*LimitNode) nodeTag()     {}
func (*AgentDef) nodeTag()      {}
func (*TemplateDef) nodeTag()   {}
func (*MakeStmt) nodeTag()      {}
func (*GroupStmt) nodeTag()     {}
func (*RemoteDecl) nodeTag()    {}
func (*ScheduleDecl) nodeTag()  {}
