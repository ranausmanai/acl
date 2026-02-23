// Package schema generates machine-readable capability descriptions from ACL programs.
//
// An ACL schema describes every AGENT in a program — its input parameters,
// output fields, allowed tools, HTTP endpoint, and CLI command name.  It is
// the contract that lets an LLM (or any agent) discover and call an ACL
// service without reading documentation.
//
// Output format (GET /schema, acl schema <file>):
//
//	{
//	  "acl_version": "0.1.0",
//	  "schema_version": "1",
//	  "intent": "Expense splitting service",
//	  "allow":  ["sql.query", "llm.generate"],
//	  "agents": [
//	    {
//	      "name":     "CreateGroup",
//	      "cli":      "create-group",
//	      "inputs":   [{"name":"name","required":false}, ...],
//	      "outputs":  [],
//	      "tools":    ["sql.query"],
//	      "schedule": "",
//	      "http": {
//	        "method": "POST",
//	        "path":   "/run/CreateGroup",
//	        "body":   {"vars": {"name": "<name>"}}
//	      }
//	    }
//	  ],
//	  "generated_at": "2026-02-20T00:00:00Z"
//	}
package schema

import (
	"bytes"
	"fmt"
	"time"
	"unicode"

	"github.com/ranausmanai/acl/internal/ast"
	"github.com/ranausmanai/acl/internal/lexer"
	"github.com/ranausmanai/acl/internal/parser"
	"github.com/ranausmanai/acl/internal/runtime"
)

const (
	aclVersion    = "0.1.0"
	schemaVersion = "1"
)

// ServiceSchema is the top-level machine-readable description of an ACL service.
type ServiceSchema struct {
	ACLVersion    string         `json:"acl_version"`
	SchemaVersion string         `json:"schema_version"`
	Intent        string         `json:"intent"`
	Allow         []string       `json:"allow"`
	Agents        []*AgentSchema `json:"agents"`
	GeneratedAt   string         `json:"generated_at"`
}

// AgentSchema describes a single agent within the service.
type AgentSchema struct {
	Name     string       `json:"name"`
	CLI      string       `json:"cli"`                // kebab-case command name
	Inputs   []InputParam `json:"inputs"`
	Outputs  []string     `json:"outputs,omitempty"`
	Tools    []string     `json:"tools"`
	Schedule string       `json:"schedule,omitempty"` // cron expression if scheduled
	HTTP     HTTPSchema   `json:"http"`
}

// InputParam describes one IN parameter of an agent.
type InputParam struct {
	Name     string `json:"name"`
	Required bool   `json:"required"` // true when listed in IN (future type system)
}

// HTTPSchema describes how to call the agent over HTTP.
type HTTPSchema struct {
	Method string         `json:"method"`
	Path   string         `json:"path"`
	Body   map[string]any `json:"body"` // example request body
}

// FromSource parses src and returns the schema.  Does not require a tool
// registry — it only parses and expands the AST.
func FromSource(src string) (*ServiceSchema, error) {
	tokens, err := lexer.New(src).Tokenize()
	if err != nil {
		return nil, fmt.Errorf("schema: lex: %w", err)
	}
	nodes, err := parser.Parse(tokens)
	if err != nil {
		return nil, fmt.Errorf("schema: parse: %w", err)
	}
	expanded, err := runtime.Expand(nodes, nil)
	if err != nil {
		return nil, fmt.Errorf("schema: expand: %w", err)
	}
	return FromNodes(expanded), nil
}

// FromNodes builds a ServiceSchema from an already-expanded AST.
// Use this when you have pre-parsed nodes (e.g. inside acl serve).
func FromNodes(nodes []ast.Node) *ServiceSchema {
	s := &ServiceSchema{
		ACLVersion:    aclVersion,
		SchemaVersion: schemaVersion,
		Allow:         []string{},
		Agents:        []*AgentSchema{},
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
	}

	// First pass: collect schedules keyed by agent name.
	schedules := map[string]string{}
	for _, n := range nodes {
		if sd, ok := n.(*ast.ScheduleDecl); ok {
			schedules[sd.AgentName] = sd.Cron
		}
	}

	// Second pass: build the schema.
	for _, n := range nodes {
		switch x := n.(type) {
		case *ast.IntentNode:
			s.Intent = x.Text
		case *ast.AllowNode:
			s.Allow = x.Tools
		case *ast.AgentDef:
			a := &AgentSchema{
				Name:    x.Name,
				CLI:     toKebabCase(x.Name),
				Outputs: x.Outputs,
				Tools:   x.Tools,
			}
			if a.Outputs == nil {
				a.Outputs = []string{}
			}
			if a.Tools == nil {
				a.Tools = []string{}
			}

			for _, inp := range x.Inputs {
				a.Inputs = append(a.Inputs, InputParam{Name: inp, Required: false})
			}
			if a.Inputs == nil {
				a.Inputs = []InputParam{}
			}

			a.HTTP = HTTPSchema{
				Method: "POST",
				Path:   "/run/" + x.Name,
				Body:   buildExampleBody(x.Inputs),
			}

			if cron, ok := schedules[x.Name]; ok {
				a.Schedule = cron
			}

			s.Agents = append(s.Agents, a)
		}
	}

	return s
}

// ── helpers ───────────────────────────────────────────────────────────────────

// toKebabCase converts PascalCase or camelCase to kebab-case.
//
//	PriceMonitor  → price-monitor
//	CreateGroup   → create-group
//	ETLPipeline   → etl-pipeline
func toKebabCase(s string) string {
	runes := []rune(s)
	var buf bytes.Buffer
	for i, r := range runes {
		if i == 0 {
			buf.WriteRune(unicode.ToLower(r))
			continue
		}
		if unicode.IsUpper(r) {
			// Insert hyphen when transitioning lower→upper or when the next
			// char is lower (handles sequences like "ETL" → "etl").
			prev := runes[i-1]
			next := rune(0)
			if i+1 < len(runes) {
				next = runes[i+1]
			}
			if unicode.IsLower(prev) || (unicode.IsUpper(prev) && next != 0 && unicode.IsLower(next)) {
				buf.WriteByte('-')
			}
		}
		buf.WriteRune(unicode.ToLower(r))
	}
	return buf.String()
}

func buildExampleBody(inputs []string) map[string]any {
	vars := map[string]any{}
	for _, inp := range inputs {
		vars[inp] = "<" + inp + ">"
	}
	return map[string]any{"vars": vars}
}
