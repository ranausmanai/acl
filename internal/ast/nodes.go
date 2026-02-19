// Package ast defines the Abstract Syntax Tree for ACL programs.
//
// An ACL program is a sequence of top-level statements.  After template
// expansion (performed by the runtime), only IntentNode, AllowNode,
// LimitNode, and AgentDef remain.
package ast

import "acl/internal/evidence"

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

type StepDef struct {
	Name          string
	Kind          StepKind // SKTool | SKAgent
	Target        string   // tool name or agent name
	Args          map[string]*Arg
	Check         *evidence.Expr // nil = no CHECK
	OnFail        *OnFail
	Line          int
	ParallelGroup int // 0 = sequential; same non-zero value = run concurrently
}

// StepKind distinguishes TOOL and AGENT steps.
type StepKind int

const (
	SKTool  StepKind = iota // STEP x = TOOL tool(...)
	SKAgent                 // STEP x = AGENT agent(...)
)

// Arg is a step argument value: either a static literal or a runtime expr.
type Arg struct {
	Literal  any            // non-nil for string/int/float/bool/[]any
	ExprRef  *evidence.Expr // non-nil when value must be evaluated at runtime
	IsLiteral bool
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
