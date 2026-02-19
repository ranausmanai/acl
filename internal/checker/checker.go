// Package checker performs semantic validation on an expanded ACL program.
//
// It verifies that:
//   - Every tool referenced in TOOLS or STEP TOOL(...) is registered.
//   - Every agent referenced in STEP AGENT(...) is defined.
//   - Every ONFAIL fallback target names a step that exists in the same agent.
//   - No duplicate agent names exist.
package checker

import (
	"fmt"

	"acl/internal/ast"
)

// Error is a located semantic error.
type Error struct {
	Line int
	Msg  string
}

func (e *Error) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("line %d: %s", e.Line, e.Msg)
	}
	return e.Msg
}

// Check runs the full semantic pass.
// registeredTools is the list of tool names available in the registry.
// Returns nil if the program is valid.
func Check(nodes []ast.Node, registeredTools []string) []*Error {
	c := &checker{
		registered: make(map[string]bool),
		agents:     make(map[string]*ast.AgentDef),
	}
	for _, t := range registeredTools {
		c.registered[t] = true
	}
	c.run(nodes)
	return c.errs
}

// ── checker ───────────────────────────────────────────────────────────────────

type checker struct {
	registered    map[string]bool
	agents        map[string]*ast.AgentDef
	errs          []*Error
	groupPrefixes map[string]bool // GROUP-generated agent name prefixes

	// Global allowed tool set (from ALLOW node)
	globalAllowed map[string]bool
}

// agentKnown returns true if name is a declared agent, a MAKE-generated agent,
// or a name that matches a known GROUP prefix (e.g. "mygroup_3").
func (c *checker) agentKnown(name string) bool {
	if _, ok := c.agents[name]; ok {
		return true
	}
	// Check GROUP-generated prefix: "{prefix}_{digits}".
	for prefix := range c.groupPrefixes {
		if len(name) > len(prefix)+1 && name[:len(prefix)] == prefix && name[len(prefix)] == '_' {
			return true
		}
	}
	return false
}

func (c *checker) errf(line int, format string, args ...any) {
	c.errs = append(c.errs, &Error{
		Line: line,
		Msg:  fmt.Sprintf(format, args...),
	})
}

func (c *checker) run(nodes []ast.Node) {
	// Pass 1 – collect agent names, MAKE-generated names, GROUP prefixes, and allow list.
	var allowNode *ast.AllowNode
	groupPrefixes := map[string]bool{} // GroupStmt.GroupName → true
	var scheduledAgents []*ast.ScheduleDecl

	for _, n := range nodes {
		switch x := n.(type) {
		case *ast.AgentDef:
			if _, dup := c.agents[x.Name]; dup {
				c.errf(x.Line, "duplicate agent name %q", x.Name)
			}
			c.agents[x.Name] = x
		case *ast.AllowNode:
			allowNode = x
		case *ast.MakeStmt:
			// MAKE creates an agent with the declared name; treat it as defined.
			c.agents[x.AgentName] = &ast.AgentDef{Name: x.AgentName, Line: x.Line}
		case *ast.GroupStmt:
			groupPrefixes[x.GroupName] = true
		case *ast.RemoteDecl:
			// A REMOTE declaration counts as a defined agent for step-reference checks.
			// Warn if it shadows a local AGENT.
			if _, exists := c.agents[x.Name]; exists {
				c.errf(x.Line, "REMOTE %q shadows a local AGENT with the same name", x.Name)
			}
			c.agents[x.Name] = &ast.AgentDef{Name: x.Name, Line: x.Line}
		case *ast.ScheduleDecl:
			scheduledAgents = append(scheduledAgents, x)
		}
	}

	// Validate SCHEDULE agent references (after all agents/remotes collected).
	for _, sd := range scheduledAgents {
		if !c.agentKnown(sd.AgentName) {
			c.errf(sd.Line, "SCHEDULE references undefined agent %q", sd.AgentName)
		}
	}

	// Register a synthetic resolver for GROUP-generated names.
	// GROUP agents are named "{groupName}_{index}" at runtime; we can't
	// know the exact names without expanding, so we accept any name with a
	// known group prefix as potentially valid.
	c.groupPrefixes = groupPrefixes

	c.globalAllowed = make(map[string]bool)
	if allowNode != nil {
		for _, t := range allowNode.Tools {
			if !c.registered[t] {
				c.errf(0, "ALLOW references unknown tool %q", t)
			}
			c.globalAllowed[t] = true
		}
	}

	// Pass 2 – check each agent.
	for _, n := range nodes {
		if a, ok := n.(*ast.AgentDef); ok {
			c.checkAgent(a)
		}
	}
}

func (c *checker) checkAgent(a *ast.AgentDef) {
	// Build effective allowed-tool set for this agent.
	allowed := make(map[string]bool)
	for t := range c.globalAllowed {
		allowed[t] = true
	}
	for _, t := range a.Tools {
		if !c.registered[t] {
			c.errf(a.Line, "agent %q declares unknown tool %q", a.Name, t)
		}
		allowed[t] = true
	}

	// Collect step names for fallback validation.
	stepNames := make(map[string]bool)
	for _, s := range a.Steps {
		stepNames[s.Name] = true
	}

	// Check each step.
	for _, s := range a.Steps {
		switch s.Kind {
		case ast.SKTool:
			if !allowed[s.Target] {
				c.errf(s.Line, "step %q calls tool %q which is not in TOOLS list for agent %q",
					s.Name, s.Target, a.Name)
			}
		case ast.SKAgent:
			if !c.agentKnown(s.Target) {
				c.errf(s.Line, "step %q references undefined agent %q", s.Name, s.Target)
			}
		}

		if s.OnFail != nil && s.OnFail.Policy == "fallback" && s.OnFail.FallbackStep != "" {
			if !stepNames[s.OnFail.FallbackStep] {
				c.errf(s.Line, "step %q ONFAIL fallback target %q does not exist in agent %q",
					s.Name, s.OnFail.FallbackStep, a.Name)
			}
		}
	}
}
