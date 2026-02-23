// Package runtime is the ACL execution engine.
//
// Usage:
//
//	reg := builtin.NewRegistry()
//	cfg := runtime.Config{CacheDir: ".acl_cache", ReceiptPath: "receipt.json"}
//	receipt, err := runtime.RunFile(ctx, src, cfg, reg)
package runtime

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/ranausmanai/acl/internal/ast"
	"github.com/ranausmanai/acl/internal/cache"
	"github.com/ranausmanai/acl/internal/checker"
	"github.com/ranausmanai/acl/internal/evidence"
	"github.com/ranausmanai/acl/internal/lexer"
	"github.com/ranausmanai/acl/internal/parser"
	"github.com/ranausmanai/acl/internal/protocol"
	"github.com/ranausmanai/acl/internal/receipt"
)

// Config holds execution parameters.
type Config struct {
	CacheDir    string         // directory for the SHA-256 result cache (empty = disabled)
	ReceiptPath string         // path to write receipt.json (empty = skip)
	Vars        map[string]any // variables from --vars JSON
	MaxDepth    int            // max agent call depth (default 10)
}

// RunSource parses, expands, checks, and executes an ACL program from source.
func RunSource(ctx context.Context, src string, cfg Config, reg *protocol.Registry) (*receipt.Receipt, error) {
	if cfg.MaxDepth == 0 {
		cfg.MaxDepth = 10
	}
	if cfg.Vars == nil {
		cfg.Vars = map[string]any{}
	}

	// ── Parse ────────────────────────────────────────────────────────────────
	tokens, err := lexer.New(src).Tokenize()
	if err != nil {
		return failReceipt("", err), err
	}
	nodes, err := parser.Parse(tokens)
	if err != nil {
		return failReceipt("", err), err
	}

	// ── Intent ───────────────────────────────────────────────────────────────
	intent := ""
	for _, n := range nodes {
		if i, ok := n.(*ast.IntentNode); ok {
			intent = i.Text
			break
		}
	}

	rb := receipt.NewBuilder(intent)

	// ── Template expansion ───────────────────────────────────────────────────
	expanded, err := expand(nodes, cfg.Vars)
	if err != nil {
		rb.SetStatus("failed", err.Error())
		r := rb.Build()
		return &r, err
	}

	// ── Semantic check ───────────────────────────────────────────────────────
	checkErrs := checker.Check(expanded, reg.Names())
	if len(checkErrs) > 0 {
		msg := checkErrs[0].Error()
		rb.SetStatus("failed", "checker: "+msg)
		r := rb.Build()
		return &r, fmt.Errorf("checker: %s", msg)
	}

	// ── Extract limit / allow nodes ──────────────────────────────────────────
	var limitNode *ast.LimitNode
	var allowNode *ast.AllowNode
	for _, n := range expanded {
		switch x := n.(type) {
		case *ast.LimitNode:
			limitNode = x
		case *ast.AllowNode:
			allowNode = x
		}
	}

	retries := 2
	var timeS *float64
	var callsLimit *int
	if limitNode != nil {
		retries = limitNode.Retries
		timeS = limitNode.TimeS
		callsLimit = limitNode.Calls
	}
	allow := []string{}
	if allowNode != nil {
		allow = allowNode.Tools
	}
	rb.SetPolicy(allow, timeS, callsLimit, retries)

	// ── Context with optional timeout ────────────────────────────────────────
	runCtx := ctx
	if timeS != nil {
		// Timeout is handled by caller or OS; we just note it in the receipt.
		_ = runCtx
	}

	// ── Set up cache ─────────────────────────────────────────────────────────
	var c *cache.Cache
	if cfg.CacheDir != "" {
		c, err = cache.New(cfg.CacheDir)
		if err != nil {
			return failReceipt(intent, err), err
		}
	}

	// ── Collect agents and remote declarations ───────────────────────────────
	agents := make(map[string]*ast.AgentDef)
	remotes := make(map[string]string)
	var entryAgent *ast.AgentDef
	for _, n := range expanded {
		switch x := n.(type) {
		case *ast.AgentDef:
			agents[x.Name] = x
			entryAgent = x // last one = orchestrator
		case *ast.RemoteDecl:
			remotes[x.Name] = x.BaseURL
		}
	}
	if entryAgent == nil {
		err = fmt.Errorf("no AGENT defined")
		rb.SetStatus("failed", err.Error())
		r := rb.Build()
		return &r, err
	}

	// ── Run ──────────────────────────────────────────────────────────────────
	var calls int64
	maxCalls := int64(0)
	if callsLimit != nil {
		maxCalls = int64(*callsLimit)
	}

	run := &runner{
		reg:      reg,
		cache:    c,
		agents:   agents,
		remotes:  remotes,
		vars:     cfg.Vars,
		retries:  retries,
		maxCalls: maxCalls,
		calls:    &calls,
		maxDepth: cfg.MaxDepth,
	}

	atrace, agentErr := run.runAgent(runCtx, entryAgent, cfg.Vars, 0)
	rb.AddAgent(*atrace)

	if agentErr != nil {
		rb.SetStatus("failed", agentErr.Error())
	} else if atrace.Status == "needs_human" {
		rb.SetStatus("needs_human", atrace.Error)
	} else {
		rb.SetStatus("success", "")
	}

	r := rb.Build()
	if cfg.ReceiptPath != "" {
		_ = rb.Write(cfg.ReceiptPath)
	}
	return &r, agentErr
}

func failReceipt(intent string, err error) *receipt.Receipt {
	rb := receipt.NewBuilder(intent)
	rb.SetStatus("failed", err.Error())
	r := rb.Build()
	return &r
}

// Expand resolves TEMPLATE, MAKE, and GROUP statements into concrete AgentDefs.
// It is exported so the server package can pre-parse once at startup.
func Expand(nodes []ast.Node, vars map[string]any) ([]ast.Node, error) {
	return expand(nodes, vars)
}

// RunNamedAgent executes a single named agent from a pre-expanded AST.
// Use this when you have already called Expand; it skips re-parsing.
func RunNamedAgent(ctx context.Context, nodes []ast.Node, agentName string, cfg Config, reg *protocol.Registry) (*receipt.Receipt, error) {
	if cfg.MaxDepth == 0 {
		cfg.MaxDepth = 10
	}
	if cfg.Vars == nil {
		cfg.Vars = map[string]any{}
	}

	// Extract metadata for the receipt.
	intent := ""
	var limitNode *ast.LimitNode
	var allowNode *ast.AllowNode
	agents := make(map[string]*ast.AgentDef)
	remotes := make(map[string]string)

	for _, n := range nodes {
		switch x := n.(type) {
		case *ast.IntentNode:
			intent = x.Text
		case *ast.LimitNode:
			limitNode = x
		case *ast.AllowNode:
			allowNode = x
		case *ast.AgentDef:
			agents[x.Name] = x
		case *ast.RemoteDecl:
			remotes[x.Name] = x.BaseURL
		}
	}

	target, ok := agents[agentName]
	if !ok {
		// Remote-only agents can't be run locally; return an error.
		if _, isRemote := remotes[agentName]; isRemote {
			return failReceipt(intent, fmt.Errorf("agent %q is declared REMOTE and cannot be run directly on this server", agentName)), fmt.Errorf("agent %q is remote", agentName)
		}
		return failReceipt(intent, fmt.Errorf("agent %q not found", agentName)), fmt.Errorf("agent %q not found", agentName)
	}

	rb := receipt.NewBuilder(intent)

	retries := 2
	var timeS *float64
	var callsLimit *int
	if limitNode != nil {
		retries = limitNode.Retries
		timeS = limitNode.TimeS
		callsLimit = limitNode.Calls
	}
	allow := []string{}
	if allowNode != nil {
		allow = allowNode.Tools
	}
	rb.SetPolicy(allow, timeS, callsLimit, retries)

	var cacheInst *cache.Cache
	var err error
	if cfg.CacheDir != "" {
		cacheInst, err = cache.New(cfg.CacheDir)
		if err != nil {
			return failReceipt(intent, err), err
		}
	}

	var calls int64
	maxCalls := int64(0)
	if callsLimit != nil {
		maxCalls = int64(*callsLimit)
	}

	run := &runner{
		reg:      reg,
		cache:    cacheInst,
		agents:   agents,
		remotes:  remotes,
		vars:     cfg.Vars,
		retries:  retries,
		maxCalls: maxCalls,
		calls:    &calls,
		maxDepth: cfg.MaxDepth,
	}

	atrace, agentErr := run.runAgent(ctx, target, cfg.Vars, 0)
	rb.AddAgent(*atrace)
	if agentErr != nil {
		rb.SetStatus("failed", agentErr.Error())
	} else if atrace.Status == "needs_human" {
		rb.SetStatus("needs_human", atrace.Error)
	} else {
		rb.SetStatus("success", "")
	}

	r := rb.Build()
	if cfg.ReceiptPath != "" {
		_ = rb.Write(cfg.ReceiptPath)
	}
	return &r, agentErr
}

// ── runner ────────────────────────────────────────────────────────────────────

type runner struct {
	reg      *protocol.Registry
	cache    *cache.Cache
	agents   map[string]*ast.AgentDef
	remotes  map[string]string // agentName → base URL for remote dispatch
	vars     map[string]any
	retries  int
	maxCalls int64 // 0 = unlimited
	calls    *int64
	maxDepth int
}

func (r *runner) runAgent(ctx context.Context, agent *ast.AgentDef, inputs map[string]any, depth int) (*receipt.AgentTrace, error) {
	if depth > r.maxDepth {
		return errorAgentTrace(agent, "maximum agent recursion depth exceeded"), fmt.Errorf("max recursion depth")
	}

	atrace := &receipt.AgentTrace{
		Name:    agent.Name,
		Inputs:  agent.Inputs,
		Outputs: agent.Outputs,
		Tools:   agent.Tools,
		Status:  "running",
	}
	if agent.FromTemplate != "" {
		atrace.FromTemplate = agent.FromTemplate
		atrace.TemplateArgs = agent.TemplateArgs
	}
	if agent.MustExpr != nil {
		atrace.MustExpr = evidence.ExprString(*agent.MustExpr)
	}

	// ── Environment ──────────────────────────────────────────────────────────
	env := make(evidence.Env, len(r.vars)+len(inputs))
	for k, v := range r.vars {
		env[k] = v
	}
	for k, v := range inputs {
		env[k] = v
	}

	// ── Build step queue (exclude fallback-target steps from main flow) ───────
	fallbackTargets := map[string]bool{}
	for _, s := range agent.Steps {
		if s.OnFail != nil && s.OnFail.Policy == "fallback" && s.OnFail.FallbackStep != "" {
			fallbackTargets[s.OnFail.FallbackStep] = true
		}
	}
	stepMap := make(map[string]*ast.StepDef, len(agent.Steps))
	for _, s := range agent.Steps {
		stepMap[s.Name] = s
	}
	queue := make([]*ast.StepDef, 0, len(agent.Steps))
	for _, s := range agent.Steps {
		if !fallbackTargets[s.Name] {
			queue = append(queue, s)
		}
	}

	// ── Execute steps ─────────────────────────────────────────────────────────
	i := 0
	for i < len(queue) {
		// Collect a run-set: consecutive steps with the same non-zero ParallelGroup,
		// or a single sequential step (ParallelGroup == 0).
		s := queue[i]
		runSet := []*ast.StepDef{s}
		if s.ParallelGroup != 0 {
			for j := i + 1; j < len(queue) && queue[j].ParallelGroup == s.ParallelGroup; j++ {
				runSet = append(runSet, queue[j])
			}
		}

		if len(runSet) == 1 {
			// ── Sequential step ───────────────────────────────────────────────
			step := runSet[0]
			start := receipt.Now()

			// Control-flow steps (IF/FOREACH) run sub-steps and update env directly.
			if step.Kind == ast.SKIf || step.Kind == ast.SKForeach {
				stepErr := r.runControlStep(ctx, step, env)
				strace := receipt.StepTrace{
					Name:      step.Name,
					Kind:      step.Kind.String(),
					StartedAt: start,
					EndedAt:   receipt.Now(),
					DurationMS: receipt.DurationMS(start),
				}
				if stepErr != nil {
					strace.Error = stepErr.Error()
					atrace.Steps = append(atrace.Steps, strace)
					atrace.Status = "failed"
					atrace.Error = stepErr.Error()
					return atrace, stepErr
				}
				atrace.Steps = append(atrace.Steps, strace)
				i++
				continue
			}

			val, hit, retriesUsed, checkPassed, stepErr := r.execStep(ctx, step, env)

			strace := r.buildStepTrace(step, env, start, val, hit, retriesUsed, checkPassed, stepErr)
			atrace.Steps = append(atrace.Steps, strace)

			if stepErr != nil {
				if step.OnFail == nil {
					atrace.Status = "failed"
					atrace.Error = stepErr.Error()
					return atrace, stepErr
				}
				switch step.OnFail.Policy {
				case "retry":
					atrace.Status = "failed"
					atrace.Error = stepErr.Error()
					return atrace, stepErr
				case "fallback":
					if fb, ok := stepMap[step.OnFail.FallbackStep]; ok {
						newQueue := make([]*ast.StepDef, 0, len(queue)+1)
						newQueue = append(newQueue, queue[:i+1]...)
						newQueue = append(newQueue, fb)
						for _, sq := range queue[i+1:] {
							if sq.Name != fb.Name {
								newQueue = append(newQueue, sq)
							}
						}
						queue = newQueue
					}
				case "askhuman":
					atrace.Status = "needs_human"
					atrace.Error = stepErr.Error()
					return atrace, nil
				case "stop":
					atrace.Status = "failed"
					atrace.Error = stepErr.Error()
					return atrace, stepErr
				}
			} else {
				env[step.Name] = val
			}
			i++

		} else {
			// ── Parallel run-set ──────────────────────────────────────────────
			type pResult struct {
				step        *ast.StepDef
				val         any
				hit         bool
				retriesUsed int
				checkPassed *bool
				err         error
				start       string
			}
			results := make([]pResult, len(runSet))
			envSnap := copyEnv(env) // all parallel steps share a read-only snapshot

			var wg sync.WaitGroup
			for pi, ps := range runSet {
				wg.Add(1)
				go func(idx int, step *ast.StepDef) {
					defer wg.Done()
					start := receipt.Now()
					val, hit, ret, cp, err := r.execStep(ctx, step, envSnap)
					results[idx] = pResult{step, val, hit, ret, cp, err, start}
				}(pi, ps)
			}
			wg.Wait()

			// Merge results into env and traces; fail-fast on first error.
			var firstErr error
			var firstErrStep *ast.StepDef
			for _, res := range results {
				strace := r.buildStepTrace(res.step, envSnap, res.start, res.val, res.hit, res.retriesUsed, res.checkPassed, res.err)
				atrace.Steps = append(atrace.Steps, strace)
				if res.err != nil && firstErr == nil {
					firstErr = res.err
					firstErrStep = res.step
				} else if res.err == nil {
					env[res.step.Name] = res.val
				}
			}
			if firstErr != nil {
				_ = firstErrStep
				atrace.Status = "failed"
				atrace.Error = firstErr.Error()
				return atrace, firstErr
			}
			i += len(runSet)
		}
	}

	// ── MUST gate ─────────────────────────────────────────────────────────────
	if agent.MustExpr != nil {
		passed, mustErr := evidence.Check(agent.MustExpr, env)
		mustOK := passed && mustErr == nil
		atrace.MustPassed = receipt.BoolPtr(mustOK)
		if !mustOK {
			msg := "MUST expression failed"
			if mustErr != nil {
				msg = mustErr.Error()
			}
			atrace.Status = "failed"
			atrace.Error = msg
			return atrace, fmt.Errorf("%s", msg)
		}
	}

	// ── RESULT ────────────────────────────────────────────────────────────────
	if agent.ResultExpr != nil {
		val, evalErr := evidence.Evaluate(agent.ResultExpr, env)
		if evalErr != nil {
			atrace.Status = "failed"
			atrace.Error = evalErr.Error()
			return atrace, evalErr
		}
		atrace.Result = val
	}

	atrace.Status = "success"
	return atrace, nil
}

// buildStepTrace constructs a StepTrace from execution results.
func (r *runner) buildStepTrace(s *ast.StepDef, env evidence.Env, start string, val any, hit bool, retriesUsed int, checkPassed *bool, stepErr error) receipt.StepTrace {
	st := receipt.StepTrace{
		Name:        s.Name,
		Target:      s.Target,
		StartedAt:   start,
		EndedAt:     receipt.Now(),
		DurationMS:  receipt.DurationMS(start),
		CacheHit:    hit,
		RetriesUsed: retriesUsed,
		CheckPassed: checkPassed,
	}
	switch s.Kind {
	case ast.SKTool:
		st.Kind = "tool"
	case ast.SKAgent:
		st.Kind = "agent"
	case ast.SKIf:
		st.Kind = "if"
	case ast.SKForeach:
		st.Kind = "foreach"
	}
	st.Args = flattenArgs(s.Args, env)
	if s.Check != nil {
		st.CheckExpr = evidence.ExprString(*s.Check)
	}
	if s.OnFail != nil {
		st.OnFailPolicy = s.OnFail.Policy
	}
	if s.ParallelGroup != 0 {
		st.Parallel = true
	}
	if stepErr != nil {
		st.Error = stepErr.Error()
	} else {
		st.OutputHash = receipt.HashValue(val)
		st.OutputPreview = receipt.PreviewValue(val, 200)
		st.Result = val
	}
	return st
}

// execStep executes a single step (tool call or agent call), handling retries.
// Returns (value, cacheHit, retriesUsed, checkPassed, error).
func (r *runner) execStep(
	ctx context.Context,
	s *ast.StepDef,
	env evidence.Env,
) (any, bool, int, *bool, error) {
	maxRetries := 0
	if s.OnFail != nil && s.OnFail.Policy == "retry" {
		maxRetries = r.retries
	}

	var lastErr error
	var lastVal any
	var lastHit bool

	for attempt := 0; attempt <= maxRetries; attempt++ {
		val, hit, callErr := r.callTarget(ctx, s, env)
		if callErr != nil {
			lastErr = callErr
			if attempt < maxRetries {
				continue
			}
			f := false
			return nil, false, attempt, &f, callErr
		}

		// Evaluate CHECK.
		if s.Check != nil {
			testEnv := copyEnv(env)
			testEnv[s.Name] = val
			passed, checkErr := evidence.Check(s.Check, testEnv)
			if checkErr != nil {
				lastErr = checkErr
				lastVal = val
				lastHit = hit
				if attempt < maxRetries {
					continue
				}
				f := false
				return val, hit, attempt, &f, checkErr
			}
			if !passed {
				lastErr = fmt.Errorf("CHECK failed for step %q", s.Name)
				lastVal = val
				lastHit = hit
				if attempt < maxRetries {
					continue
				}
				f := false
				return val, hit, attempt, &f, lastErr
			}
			t := true
			return val, hit, attempt, &t, nil
		}

		// No CHECK — success.
		_ = lastErr
		_ = lastVal
		_ = lastHit
		return val, hit, attempt, nil, nil
	}

	f := false
	return nil, false, maxRetries, &f, lastErr
}

// callTarget dispatches to callTool or callAgent.
func (r *runner) callTarget(ctx context.Context, s *ast.StepDef, env evidence.Env) (any, bool, error) {
	args, err := resolveArgs(s.Args, env)
	if err != nil {
		return nil, false, fmt.Errorf("step %q: arg resolution: %w", s.Name, err)
	}
	if s.Kind == ast.SKTool {
		return r.callTool(ctx, s.Target, args)
	}
	val, err := r.callAgent(ctx, s.Target, args)
	return val, false, err
}

// callTool executes a tool, checking the cache first.
func (r *runner) callTool(ctx context.Context, name string, args map[string]any) (any, bool, error) {
	// Cache lookup.
	var cacheKey string
	if r.cache != nil {
		ver := r.reg.Version(name)
		cacheKey = r.cache.Key(name, args, ver)
		if v, ok := r.cache.Get(cacheKey); ok {
			return v, true, nil
		}
	}

	// Call budget.
	if r.maxCalls > 0 {
		n := atomic.AddInt64(r.calls, 1)
		if n > r.maxCalls {
			atomic.AddInt64(r.calls, -1)
			return nil, false, fmt.Errorf("call budget exhausted (%d/%d)", n-1, r.maxCalls)
		}
	}

	val, err := r.reg.Call(ctx, name, args)
	if err != nil {
		return nil, false, err
	}

	if r.cache != nil && cacheKey != "" {
		_ = r.cache.Set(cacheKey, val)
	}
	return val, false, nil
}

// callAgent runs a named agent — local or remote.
func (r *runner) callAgent(ctx context.Context, name string, inputs map[string]any) (any, error) {
	// Remote declaration takes precedence over any local agent with the same name.
	if baseURL, ok := r.remotes[name]; ok {
		return callRemoteAgent(ctx, name, baseURL, inputs)
	}
	agent, ok := r.agents[name]
	if !ok {
		return nil, fmt.Errorf("agent %q not found", name)
	}
	atrace, err := r.runAgent(ctx, agent, inputs, 1)
	if err != nil {
		return nil, err
	}
	return atrace.Result, nil
}

// ── Control-flow step execution ───────────────────────────────────────────────

// runControlStep executes an IF or FOREACH step, updating env in place.
func (r *runner) runControlStep(ctx context.Context, s *ast.StepDef, env evidence.Env) error {
	switch s.Kind {
	case ast.SKIf:
		return r.runIfStep(ctx, s, env)
	case ast.SKForeach:
		return r.runForeachStep(ctx, s, env)
	}
	return nil
}

func (r *runner) runIfStep(ctx context.Context, s *ast.StepDef, env evidence.Env) error {
	val, err := evidence.Evaluate(s.IfCond, env)
	if err != nil {
		return fmt.Errorf("IF condition: %w", err)
	}
	var branch []*ast.StepDef
	if isTruthy(val) {
		branch = s.IfThen
	} else if s.IfElse != nil {
		branch = s.IfElse
	}
	return r.runSubSteps(ctx, branch, env)
}

func (r *runner) runForeachStep(ctx context.Context, s *ast.StepDef, env evidence.Env) error {
	listVal, err := evidence.Evaluate(s.ForeachOver, env)
	if err != nil {
		return fmt.Errorf("FOREACH expression: %w", err)
	}
	items, ok := toSlice(listVal)
	if !ok {
		return fmt.Errorf("FOREACH: expression must evaluate to a list, got %T", listVal)
	}
	for _, item := range items {
		env[s.ForeachVar] = item
		if err := r.runSubSteps(ctx, s.ForeachBody, env); err != nil {
			return err
		}
	}
	return nil
}

// runSubSteps executes a slice of steps (inside IF/FOREACH bodies).
// Results are written into env so subsequent sub-steps can reference them.
func (r *runner) runSubSteps(ctx context.Context, steps []*ast.StepDef, env evidence.Env) error {
	for _, step := range steps {
		if step.Kind == ast.SKIf || step.Kind == ast.SKForeach {
			if err := r.runControlStep(ctx, step, env); err != nil {
				return err
			}
		} else {
			val, _, _, _, err := r.execStep(ctx, step, env)
			if err != nil {
				return err
			}
			if step.Name != "" {
				env[step.Name] = val
			}
		}
	}
	return nil
}

func isTruthy(v any) bool {
	if v == nil {
		return false
	}
	switch x := v.(type) {
	case bool:
		return x
	case int64:
		return x != 0
	case float64:
		return x != 0
	case string:
		return x != ""
	case []any:
		return len(x) > 0
	}
	return true
}

func toSlice(v any) ([]any, bool) {
	if s, ok := v.([]any); ok {
		return s, true
	}
	return nil, false
}

// ── Template expansion ────────────────────────────────────────────────────────

// expand resolves TEMPLATE, MAKE, and GROUP statements into concrete AgentDefs.
func expand(nodes []ast.Node, vars map[string]any) ([]ast.Node, error) {
	templates := make(map[string]*ast.TemplateDef)
	for _, n := range nodes {
		if t, ok := n.(*ast.TemplateDef); ok {
			templates[t.Name] = t
		}
	}

	var result []ast.Node
	for _, n := range nodes {
		switch x := n.(type) {
		case *ast.TemplateDef:
			// Consumed; do not emit.
		case *ast.MakeStmt:
			agent, err := expandMake(x, templates, vars)
			if err != nil {
				return nil, err
			}
			result = append(result, agent)
		case *ast.GroupStmt:
			agents, err := expandGroup(x, templates, vars)
			if err != nil {
				return nil, err
			}
			for _, a := range agents {
				result = append(result, a)
			}
		default:
			result = append(result, n)
		}
	}
	return result, nil
}

func expandMake(m *ast.MakeStmt, templates map[string]*ast.TemplateDef, vars map[string]any) (*ast.AgentDef, error) {
	tmpl, ok := templates[m.Template]
	if !ok {
		return nil, fmt.Errorf("MAKE: template %q not found (line %d)", m.Template, m.Line)
	}

	env := envFrom(vars)
	bindings, err := evalArgs(m.Args, env)
	if err != nil {
		return nil, fmt.Errorf("MAKE %s: %w", m.AgentName, err)
	}

	// Verify all params are supplied.
	for _, p := range tmpl.Params {
		if _, ok := bindings[p]; !ok {
			return nil, fmt.Errorf("MAKE %s: missing param %q for template %q", m.AgentName, p, m.Template)
		}
	}

	agent := cloneAndSubstitute(tmpl.Body, bindings)
	agent.Name = m.AgentName
	agent.FromTemplate = tmpl.Name
	agent.TemplateArgs = bindings
	return agent, nil
}

func expandGroup(g *ast.GroupStmt, templates map[string]*ast.TemplateDef, vars map[string]any) ([]*ast.AgentDef, error) {
	source, ok := vars[g.Source]
	if !ok {
		return nil, fmt.Errorf("GROUP %s: source var %q not found in --vars", g.GroupName, g.Source)
	}
	items, ok := source.([]any)
	if !ok {
		return nil, fmt.Errorf("GROUP %s: source %q must be a JSON array", g.GroupName, g.Source)
	}

	tmpl, ok := templates[g.MakeTemplate]
	if !ok {
		return nil, fmt.Errorf("GROUP %s: template %q not found", g.GroupName, g.MakeTemplate)
	}

	var agents []*ast.AgentDef
	for i, item := range items {
		loopVars := make(map[string]any, len(vars)+1)
		for k, v := range vars {
			loopVars[k] = v
		}
		loopVars[g.Var] = item
		env := envFrom(loopVars)

		bindings, err := evalArgs(g.MakeArgs, env)
		if err != nil {
			return nil, fmt.Errorf("GROUP %s[%d]: %w", g.GroupName, i, err)
		}

		agent := cloneAndSubstitute(tmpl.Body, bindings)
		agent.Name = fmt.Sprintf("%s_%d", g.GroupName, i)
		agent.FromTemplate = tmpl.Name
		agent.TemplateArgs = bindings
		agents = append(agents, agent)
	}
	return agents, nil
}

// ── AST helpers ───────────────────────────────────────────────────────────────

// cloneAndSubstitute deep-copies an AgentDef and substitutes template params.
func cloneAndSubstitute(body *ast.AgentDef, bindings map[string]any) *ast.AgentDef {
	agent := &ast.AgentDef{
		Name:    body.Name,
		Inputs:  append([]string(nil), body.Inputs...),
		Outputs: append([]string(nil), body.Outputs...),
		Tools:   append([]string(nil), body.Tools...),
		Line:    body.Line,
	}
	if body.MustExpr != nil {
		e := subExpr(*body.MustExpr, bindings)
		agent.MustExpr = &e
	}
	if body.ResultExpr != nil {
		e := subExpr(*body.ResultExpr, bindings)
		agent.ResultExpr = &e
	}
	agent.Steps = make([]*ast.StepDef, len(body.Steps))
	for i, s := range body.Steps {
		agent.Steps[i] = cloneStep(s, bindings)
	}
	return agent
}

func cloneStep(s *ast.StepDef, bindings map[string]any) *ast.StepDef {
	step := &ast.StepDef{
		Name:          s.Name,
		Kind:          s.Kind,
		Target:        s.Target,
		Line:          s.Line,
		ParallelGroup: s.ParallelGroup,
		ForeachVar:    s.ForeachVar,
	}
	step.Args = make(map[string]*ast.Arg, len(s.Args))
	for k, arg := range s.Args {
		if arg.IsLiteral {
			step.Args[k] = &ast.Arg{Literal: arg.Literal, IsLiteral: true}
		} else if len(arg.Parts) > 0 {
			parts := make([]ast.ArgPart, len(arg.Parts))
			copy(parts, arg.Parts)
			for i, p := range parts {
				if p.Expr != nil {
					e := subExpr(*p.Expr, bindings)
					parts[i].Expr = &e
				}
			}
			step.Args[k] = &ast.Arg{Parts: parts}
		} else if arg.ExprRef != nil {
			e := subExpr(*arg.ExprRef, bindings)
			step.Args[k] = &ast.Arg{ExprRef: &e}
		}
	}
	if s.Check != nil {
		e := subExpr(*s.Check, bindings)
		step.Check = &e
	}
	if s.OnFail != nil {
		of := *s.OnFail
		step.OnFail = &of
	}
	// IF block fields
	if s.IfCond != nil {
		e := subExpr(*s.IfCond, bindings)
		step.IfCond = &e
	}
	step.IfThen = cloneSteps(s.IfThen, bindings)
	step.IfElse = cloneSteps(s.IfElse, bindings)
	// FOREACH fields
	if s.ForeachOver != nil {
		e := subExpr(*s.ForeachOver, bindings)
		step.ForeachOver = &e
	}
	step.ForeachBody = cloneSteps(s.ForeachBody, bindings)
	return step
}

func cloneSteps(steps []*ast.StepDef, bindings map[string]any) []*ast.StepDef {
	if steps == nil {
		return nil
	}
	out := make([]*ast.StepDef, len(steps))
	for i, s := range steps {
		out[i] = cloneStep(s, bindings)
	}
	return out
}

// subExpr recursively replaces IdentExpr references to template params.
func subExpr(e evidence.Expr, bindings map[string]any) evidence.Expr {
	if e == nil {
		return nil
	}
	switch x := e.(type) {
	case *evidence.IdentExpr:
		if v, ok := bindings[x.Name]; ok {
			return anyToExpr(v)
		}
		return e
	case *evidence.AttrExpr:
		return &evidence.AttrExpr{Obj: subExpr(x.Obj, bindings), Field: x.Field}
	case *evidence.IndexExpr:
		return &evidence.IndexExpr{Obj: subExpr(x.Obj, bindings), Idx: subExpr(x.Idx, bindings)}
	case *evidence.BinOpExpr:
		return &evidence.BinOpExpr{Op: x.Op, Left: subExpr(x.Left, bindings), Right: subExpr(x.Right, bindings)}
	case *evidence.UnaryExpr:
		return &evidence.UnaryExpr{Op: x.Op, Operand: subExpr(x.Operand, bindings)}
	case *evidence.CallExpr:
		args := make([]evidence.Expr, len(x.Args))
		for i, a := range x.Args {
			args[i] = subExpr(a, bindings)
		}
		return &evidence.CallExpr{Func: x.Func, Args: args}
	case *evidence.ListExpr:
		items := make([]evidence.Expr, len(x.Items))
		for i, item := range x.Items {
			items[i] = subExpr(item, bindings)
		}
		return &evidence.ListExpr{Items: items}
	default:
		return e
	}
}

func anyToExpr(v any) evidence.Expr {
	switch x := v.(type) {
	case nil:
		return &evidence.NullExpr{}
	case bool:
		return &evidence.BoolExpr{Val: x}
	case int64:
		return &evidence.IntExpr{Val: x}
	case int:
		return &evidence.IntExpr{Val: int64(x)}
	case float64:
		return &evidence.FloatExpr{Val: x}
	case string:
		return &evidence.StringExpr{Val: x}
	default:
		return &evidence.StringExpr{Val: fmt.Sprintf("%v", x)}
	}
}

// ── Argument helpers ──────────────────────────────────────────────────────────

// resolveArgs evaluates all step args against the current environment.
func resolveArgs(args map[string]*ast.Arg, env evidence.Env) (map[string]any, error) {
	out := make(map[string]any, len(args))
	for k, arg := range args {
		v, err := resolveArg(arg, env)
		if err != nil {
			return nil, fmt.Errorf("arg %q: %w", k, err)
		}
		out[k] = v
	}
	return out, nil
}

func resolveArg(arg *ast.Arg, env evidence.Env) (any, error) {
	if arg.IsLiteral {
		return arg.Literal, nil
	}
	if len(arg.Parts) > 0 {
		var sb strings.Builder
		for _, part := range arg.Parts {
			if part.Expr != nil {
				v, err := evidence.Evaluate(part.Expr, env)
				if err != nil {
					return nil, fmt.Errorf("interpolation: %w", err)
				}
				sb.WriteString(fmt.Sprintf("%v", v))
			} else {
				sb.WriteString(part.Text)
			}
		}
		return sb.String(), nil
	}
	if arg.ExprRef != nil {
		return evidence.Evaluate(arg.ExprRef, env)
	}
	return nil, nil
}

// evalArgs resolves MAKE/GROUP args (may reference loop vars).
func evalArgs(args map[string]*ast.Arg, env evidence.Env) (map[string]any, error) {
	return resolveArgs(args, env)
}

// flattenArgs resolves step args for receipt storage (best-effort).
func flattenArgs(args map[string]*ast.Arg, env evidence.Env) map[string]any {
	out := make(map[string]any, len(args))
	for k, arg := range args {
		v, err := resolveArg(arg, env)
		if err != nil {
			out[k] = fmt.Sprintf("<expr: %v>", err)
		} else {
			out[k] = v
		}
	}
	return out
}

// ── Environment helpers ───────────────────────────────────────────────────────

func envFrom(vars map[string]any) evidence.Env {
	env := make(evidence.Env, len(vars))
	for k, v := range vars {
		env[k] = v
	}
	return env
}

func copyEnv(env evidence.Env) evidence.Env {
	cp := make(evidence.Env, len(env))
	for k, v := range env {
		cp[k] = v
	}
	return cp
}

// ── Receipt helpers ───────────────────────────────────────────────────────────

func errorAgentTrace(a *ast.AgentDef, msg string) *receipt.AgentTrace {
	return &receipt.AgentTrace{
		Name:   a.Name,
		Status: "failed",
		Error:  msg,
	}
}
