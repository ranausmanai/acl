// Package parser converts a flat token stream (from the lexer) into an ACL AST.
//
// The parser is line-oriented: each statement starts with a keyword token and
// the rest of the tokens on that line are its arguments.  NEWLINE tokens mark
// statement boundaries.  The parser never looks across NEWLINE except for
// sub-structures (STEP → CHECK → ONFAIL sequences within an agent body).
package parser

import (
	"fmt"
	"strconv"
	"strings"

	"acl/internal/ast"
	"acl/internal/evidence"
	"acl/internal/lexer"
)

// ParseError carries a source-location-aware parse failure.
type ParseError struct {
	Line int
	Msg  string
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("parse error (L%d): %s", e.Line, e.Msg)
}

func pe(line int, format string, args ...any) error {
	return &ParseError{Line: line, Msg: fmt.Sprintf(format, args...)}
}

// Parse converts a token slice into a program (slice of AST nodes).
func Parse(tokens []lexer.Token) ([]ast.Node, error) {
	p := &parser{tokens: tokens, pos: 0}
	return p.parseProgram()
}

// ── parser ────────────────────────────────────────────────────────────────────

// topLevelKeywords marks tokens that can only start a top-level statement.
var topLevelKws = map[lexer.TokenType]bool{
	lexer.KWINTENT: true, lexer.KWALLOW: true, lexer.KWLIMIT: true,
	lexer.KWAGENT: true, lexer.KWTEMPLATE: true, lexer.KWMAKE: true,
	lexer.KWGROUP: true, lexer.KWREMOTE: true, lexer.KWSCHEDULE: true,
}

// agentBodyKws are valid inside an AGENT or TEMPLATE body.
var agentBodyKws = map[lexer.TokenType]bool{
	lexer.KWIN: true, lexer.KWOUT: true, lexer.KWTOOLS: true,
	lexer.KWMUST: true, lexer.KWSTEP: true, lexer.KWRESULT: true,
	lexer.KWPARALLEL: true,
}

// stepTrailKws follow a STEP line immediately.
var stepTrailKws = map[lexer.TokenType]bool{
	lexer.KWCHECK: true, lexer.KWONFAIL: true,
}

type parser struct {
	tokens         []lexer.Token
	pos            int
	parallelCounter int // incremented each time a PARALLEL block is opened
}

func (p *parser) cur() lexer.Token {
	if p.pos < len(p.tokens) {
		return p.tokens[p.pos]
	}
	return lexer.Token{Type: lexer.EOF}
}

func (p *parser) peek(n int) lexer.Token {
	i := p.pos + n
	if i < len(p.tokens) {
		return p.tokens[i]
	}
	return lexer.Token{Type: lexer.EOF}
}

func (p *parser) advance() lexer.Token {
	t := p.cur()
	p.pos++
	return t
}

// skipNewlines consumes consecutive NEWLINE tokens.
func (p *parser) skipNewlines() {
	for p.cur().Type == lexer.NEWLINE {
		p.pos++
	}
}

// consumeNewline expects and consumes exactly one NEWLINE (or EOF).
func (p *parser) consumeNewline() {
	if p.cur().Type == lexer.NEWLINE || p.cur().Type == lexer.EOF {
		p.pos++
	}
}

// lineTokens returns all tokens on the current line (up to but not including
// NEWLINE / EOF) without advancing the parser position.
func (p *parser) lineTokens() []lexer.Token {
	var toks []lexer.Token
	i := p.pos
	for i < len(p.tokens) && p.tokens[i].Type != lexer.NEWLINE && p.tokens[i].Type != lexer.EOF {
		toks = append(toks, p.tokens[i])
		i++
	}
	return toks
}

// consumeLine advances past all tokens on the current line including the NEWLINE.
func (p *parser) consumeLine() []lexer.Token {
	toks := p.lineTokens()
	p.pos += len(toks)
	p.consumeNewline()
	return toks
}

// consumeBalancedLine reads tokens until a NEWLINE that sits at paren/bracket
// depth 0.  This enables multi-line step calls:
//
//	STEP x = TOOL foo(
//	  a="hello",
//	  b=42)
func (p *parser) consumeBalancedLine() []lexer.Token {
	var toks []lexer.Token
	depth := 0
	for {
		t := p.cur()
		if t.Type == lexer.EOF {
			break
		}
		if t.Type == lexer.NEWLINE {
			if depth == 0 {
				p.pos++ // consume the newline
				break
			}
			// Inside open parens: skip the newline and continue.
			p.pos++
			continue
		}
		toks = append(toks, t)
		switch t.Type {
		case lexer.LPAREN, lexer.LBRACKET:
			depth++
		case lexer.RPAREN, lexer.RBRACKET:
			depth--
		}
		p.pos++
	}
	return toks
}

// ── top-level ─────────────────────────────────────────────────────────────────

func (p *parser) parseProgram() ([]ast.Node, error) {
	var nodes []ast.Node
	for {
		p.skipNewlines()
		if p.cur().Type == lexer.EOF {
			break
		}
		node, err := p.parseStmt()
		if err != nil {
			return nil, err
		}
		if node != nil {
			nodes = append(nodes, node)
		}
	}
	return nodes, nil
}

func (p *parser) parseStmt() (ast.Node, error) {
	t := p.cur()
	switch t.Type {
	case lexer.KWINTENT:
		return p.parseIntent()
	case lexer.KWALLOW:
		return p.parseAllow()
	case lexer.KWLIMIT:
		return p.parseLimit()
	case lexer.KWAGENT:
		return p.parseAgent()
	case lexer.KWTEMPLATE:
		return p.parseTemplate()
	case lexer.KWMAKE:
		return p.parseMake()
	case lexer.KWGROUP:
		return p.parseGroup()
	case lexer.KWREMOTE:
		return p.parseRemote()
	case lexer.KWSCHEDULE:
		return p.parseSchedule()
	default:
		// skip unknown top-level line
		p.consumeLine()
		return nil, nil
	}
}

// ── directives ────────────────────────────────────────────────────────────────

func (p *parser) parseIntent() (*ast.IntentNode, error) {
	line := p.cur().Line
	toks := p.consumeLine() // [INTENT, STRING]
	if len(toks) < 2 || toks[1].Type != lexer.STRING {
		return nil, pe(line, `INTENT requires a quoted string, e.g. INTENT "goal"`)
	}
	return &ast.IntentNode{Text: toks[1].Literal}, nil
}

func (p *parser) parseAllow() (*ast.AllowNode, error) {
	toks := p.consumeLine() // [ALLOW, id, COMMA, id, ...]
	node := &ast.AllowNode{}
	for _, t := range toks[1:] {
		if t.Type == lexer.IDENT || t.Type == lexer.DOT {
			// handled below with joinDotted
		}
		_ = t
	}
	// Re-parse dotted names from raw tokens
	node.Tools = parseDottedList(toks[1:])
	return node, nil
}

func (p *parser) parseLimit() (*ast.LimitNode, error) {
	toks := p.consumeLine() // [LIMIT, k=v, ...]
	node := &ast.LimitNode{Retries: 2}
	// Reconstruct raw text and extract k=v pairs
	raw := tokensToString(toks[1:])
	for _, kv := range splitKV(raw) {
		switch kv[0] {
		case "time":
			d, err := parseDuration(kv[1])
			if err != nil {
				return nil, err
			}
			node.TimeS = &d
		case "calls":
			v, err := strconv.Atoi(kv[1])
			if err != nil {
				return nil, err
			}
			node.Calls = &v
		case "retries":
			v, err := strconv.Atoi(kv[1])
			if err != nil {
				return nil, err
			}
			node.Retries = v
		case "cost":
			v, err := strconv.ParseFloat(kv[1], 64)
			if err != nil {
				return nil, err
			}
			node.Cost = &v
		}
	}
	return node, nil
}

// ── AGENT ─────────────────────────────────────────────────────────────────────

func (p *parser) parseAgent() (*ast.AgentDef, error) {
	line := p.cur().Line
	toks := p.consumeLine() // [AGENT, name]
	if len(toks) < 2 || toks[1].Type != lexer.IDENT {
		return nil, pe(line, "AGENT requires a name")
	}
	name := toks[1].Literal
	return p.parseAgentBody(name, line)
}

// parseAgentBody parses IN/OUT/TOOLS/MUST/STEP/RESULT lines until we hit a
// top-level keyword or EOF.
func (p *parser) parseAgentBody(name string, line int) (*ast.AgentDef, error) {
	agent := &ast.AgentDef{Name: name, Line: line}
	for {
		p.skipNewlines()
		if p.cur().Type == lexer.EOF {
			break
		}
		kw := p.cur().Type
		// Stop at any top-level keyword that is NOT an agent-body keyword
		if topLevelKws[kw] && !agentBodyKws[kw] {
			break
		}
		switch kw {
		case lexer.KWIN:
			toks := p.consumeLine()
			agent.Inputs = parseDottedList(toks[1:])
		case lexer.KWOUT:
			toks := p.consumeLine()
			agent.Outputs = parseDottedList(toks[1:])
		case lexer.KWTOOLS:
			toks := p.consumeLine()
			agent.Tools = parseDottedList(toks[1:])
		case lexer.KWMUST:
			toks := p.consumeLine()
			exprStr := tokensToString(toks[1:])
			expr, err := evidence.Parse(exprStr)
			if err != nil {
				return nil, pe(p.cur().Line, "MUST expression: %v", err)
			}
			agent.MustExpr = expr
		case lexer.KWSTEP:
			step, err := p.parseStep()
			if err != nil {
				return nil, err
			}
			agent.Steps = append(agent.Steps, step)
		case lexer.KWPARALLEL:
			// PARALLEL block: consume the "PARALLEL" line, then parse all
			// immediately following STEP lines, tagging them with the same group ID.
			p.consumeLine() // consume the PARALLEL keyword line itself
			p.parallelCounter++
			gid := p.parallelCounter
			p.skipNewlines()
			for p.cur().Type == lexer.KWSTEP {
				step, err := p.parseStep()
				if err != nil {
					return nil, err
				}
				step.ParallelGroup = gid
				agent.Steps = append(agent.Steps, step)
				p.skipNewlines()
			}
		case lexer.KWRESULT:
			toks := p.consumeLine()
			exprStr := tokensToString(toks[1:])
			expr, err := evidence.Parse(exprStr)
			if err != nil {
				return nil, pe(p.cur().Line, "RESULT expression: %v", err)
			}
			agent.ResultExpr = expr
		default:
			p.consumeLine() // skip unknown
		}
	}
	return agent, nil
}

// ── STEP ──────────────────────────────────────────────────────────────────────

func (p *parser) parseStep() (*ast.StepDef, error) {
	line := p.cur().Line
	toks := p.consumeBalancedLine() // [STEP, name, =, TOOL|AGENT, call...] (multi-line safe)

	if len(toks) < 4 {
		return nil, pe(line, "STEP: too short")
	}
	if toks[1].Type != lexer.IDENT {
		return nil, pe(line, "STEP requires a name")
	}
	stepName := toks[1].Literal

	if toks[2].Type != lexer.ASSIGN {
		return nil, pe(line, "STEP: expected '=' after name")
	}

	var kind ast.StepKind
	switch toks[3].Type {
	case lexer.KWTOOL:
		kind = ast.SKTool
	case lexer.KWAGENT:
		kind = ast.SKAgent
	default:
		return nil, pe(line, "STEP: expected TOOL or AGENT, got %q", toks[3].Literal)
	}

	// Everything after TOOL/AGENT is the call expression
	callToks := toks[4:]
	target, args, err := parseCallTokens(callToks, line)
	if err != nil {
		return nil, err
	}

	step := &ast.StepDef{
		Name:   stepName,
		Kind:   kind,
		Target: target,
		Args:   args,
		Line:   line,
	}

	// Collect optional CHECK and ONFAIL lines immediately following
	for {
		p.skipNewlines()
		kw := p.cur().Type
		if kw != lexer.KWCHECK && kw != lexer.KWONFAIL {
			break
		}
		trail := p.consumeLine()
		switch kw {
		case lexer.KWCHECK:
			exprStr := tokensToString(trail[1:])
			expr, err := evidence.Parse(exprStr)
			if err != nil {
				return nil, pe(line, "CHECK expression: %v", err)
			}
			step.Check = expr
		case lexer.KWONFAIL:
			of, err := parseOnFail(trail[1:], line)
			if err != nil {
				return nil, err
			}
			step.OnFail = of
		}
	}
	return step, nil
}

// ── TEMPLATE ──────────────────────────────────────────────────────────────────

func (p *parser) parseTemplate() (*ast.TemplateDef, error) {
	line := p.cur().Line
	toks := p.consumeLine() // [TEMPLATE, name, (, p1, COMMA, p2, ..., )]
	if len(toks) < 2 {
		return nil, pe(line, "TEMPLATE requires a name and parameters")
	}
	// Reconstruct raw and parse Name(params)
	raw := tokensToString(toks[1:])
	name, params, err := parseNameParams(raw, line)
	if err != nil {
		return nil, err
	}
	body, err := p.parseAgentBody(name, line)
	if err != nil {
		return nil, err
	}
	return &ast.TemplateDef{Name: name, Params: params, Body: body, Line: line}, nil
}

// ── MAKE ──────────────────────────────────────────────────────────────────────

func (p *parser) parseMake() (*ast.MakeStmt, error) {
	line := p.cur().Line
	toks := p.consumeLine() // [MAKE, Template, (, ..., ), AS, name]
	raw := tokensToString(toks[1:])

	// Split on " AS "
	parts := strings.SplitN(raw, " AS ", 2)
	if len(parts) != 2 {
		return nil, pe(line, "MAKE requires 'AS AgentName'")
	}
	callPart := strings.TrimSpace(parts[0])
	agentName := strings.TrimSpace(parts[1])

	// Parse Template(args...)
	lparen := strings.Index(callPart, "(")
	if lparen < 0 {
		return nil, pe(line, "MAKE: expected Template(args...)")
	}
	templateName := strings.TrimSpace(callPart[:lparen])
	argsRaw := strings.TrimSuffix(strings.TrimSpace(callPart[lparen+1:]), ")")

	args, err := parseKWArgs(argsRaw, line)
	if err != nil {
		return nil, err
	}
	return &ast.MakeStmt{
		Template:  templateName,
		Args:      args,
		AgentName: agentName,
		Line:      line,
	}, nil
}

// ── GROUP ─────────────────────────────────────────────────────────────────────

func (p *parser) parseGroup() (*ast.GroupStmt, error) {
	line := p.cur().Line
	toks := p.consumeLine()
	// GROUP Name = FOR var IN source : MAKE Template(args...)
	raw := tokensToString(toks[1:])

	// Split on " = "
	eqIdx := strings.Index(raw, "=")
	if eqIdx < 0 {
		return nil, pe(line, "GROUP requires '= FOR ...'")
	}
	groupName := strings.TrimSpace(raw[:eqIdx])
	rest := strings.TrimSpace(raw[eqIdx+1:])

	if !strings.HasPrefix(rest, "FOR ") {
		return nil, pe(line, "GROUP: expected FOR after '='")
	}
	rest = rest[4:]

	// var IN source : MAKE Template(args)
	colonIdx := strings.Index(rest, ":")
	if colonIdx < 0 {
		return nil, pe(line, "GROUP: missing ':' before MAKE")
	}
	forPart := strings.TrimSpace(rest[:colonIdx])
	makePart := strings.TrimSpace(rest[colonIdx+1:])

	forParts := strings.SplitN(forPart, " IN ", 2)
	if len(forParts) != 2 {
		return nil, pe(line, "GROUP: expected 'var IN source'")
	}
	varName := strings.TrimSpace(forParts[0])
	source := strings.TrimSpace(forParts[1])

	if !strings.HasPrefix(makePart, "MAKE ") {
		return nil, pe(line, "GROUP: expected MAKE after ':'")
	}
	makePart = makePart[5:]
	lparen := strings.Index(makePart, "(")
	if lparen < 0 {
		return nil, pe(line, "GROUP: MAKE requires Template(args...)")
	}
	templateName := strings.TrimSpace(makePart[:lparen])
	argsRaw := strings.TrimSuffix(strings.TrimSpace(makePart[lparen+1:]), ")")
	args, err := parseKWArgs(argsRaw, line)
	if err != nil {
		return nil, err
	}

	return &ast.GroupStmt{
		GroupName:    groupName,
		Var:          varName,
		Source:       source,
		MakeTemplate: templateName,
		MakeArgs:     args,
		Line:         line,
	}, nil
}

// ── Shared sub-parsers ────────────────────────────────────────────────────────

// parseCallTokens parses  "tool.name(k=v, k=v)" from tokens.
// Returns (targetName, args, error).
func parseCallTokens(toks []lexer.Token, line int) (string, map[string]*ast.Arg, error) {
	raw := tokensToString(toks)
	lparen := strings.Index(raw, "(")
	if lparen < 0 {
		// No args
		return raw, map[string]*ast.Arg{}, nil
	}
	target := strings.TrimSpace(raw[:lparen])
	argsRaw := strings.TrimSuffix(strings.TrimSpace(raw[lparen+1:]), ")")
	args, err := parseKWArgs(argsRaw, line)
	if err != nil {
		return "", nil, err
	}
	return target, args, nil
}

// parseKWArgs parses "k=v, k=v" into a map of Arg values.
// Values that are Python literals become Arg.Literal; others become Arg.ExprRef.
func parseKWArgs(raw string, line int) (map[string]*ast.Arg, error) {
	args := map[string]*ast.Arg{}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return args, nil
	}
	// Split on commas that are not inside brackets/parens/quotes
	pairs := splitTopLevelCommas(raw)
	for _, pair := range pairs {
		pair = strings.TrimSpace(pair)
		eq := strings.Index(pair, "=")
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(pair[:eq])
		valRaw := strings.TrimSpace(pair[eq+1:])

		arg, err := parseArgValue(valRaw, line)
		if err != nil {
			return nil, err
		}
		args[key] = arg
	}
	return args, nil
}

// parseArgValue tries to parse valRaw as a literal; falls back to ExprRef.
func parseArgValue(raw string, line int) (*ast.Arg, error) {
	// String literal
	if strings.HasPrefix(raw, `"`) {
		s := strings.Trim(raw, `"`)
		return &ast.Arg{Literal: s, IsLiteral: true}, nil
	}
	// List literal
	if strings.HasPrefix(raw, "[") {
		// parse list of strings
		inner := strings.TrimSuffix(strings.TrimPrefix(raw, "["), "]")
		items := splitTopLevelCommas(inner)
		var list []any
		for _, item := range items {
			item = strings.TrimSpace(item)
			item = strings.Trim(item, `"`)
			list = append(list, item)
		}
		return &ast.Arg{Literal: list, IsLiteral: true}, nil
	}
	// Bool
	if raw == "True" || raw == "true" {
		return &ast.Arg{Literal: true, IsLiteral: true}, nil
	}
	if raw == "False" || raw == "false" {
		return &ast.Arg{Literal: false, IsLiteral: true}, nil
	}
	// Null
	if raw == "None" || raw == "null" {
		return &ast.Arg{Literal: nil, IsLiteral: true}, nil
	}
	// Number
	if iv, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return &ast.Arg{Literal: iv, IsLiteral: true}, nil
	}
	if fv, err := strconv.ParseFloat(raw, 64); err == nil {
		return &ast.Arg{Literal: fv, IsLiteral: true}, nil
	}
	// Expression reference
	expr, err := evidence.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("L%d: invalid argument value %q: %v", line, raw, err)
	}
	return &ast.Arg{ExprRef: expr, IsLiteral: false}, nil
}

func parseOnFail(toks []lexer.Token, line int) (*ast.OnFail, error) {
	raw := tokensToString(toks)
	raw = strings.TrimSpace(raw)
	switch {
	case raw == "retry":
		return &ast.OnFail{Policy: "retry"}, nil
	case raw == "askhuman":
		return &ast.OnFail{Policy: "askhuman"}, nil
	case raw == "stop":
		return &ast.OnFail{Policy: "stop"}, nil
	case strings.HasPrefix(raw, "fallback"):
		parts := strings.Fields(raw)
		fb := ""
		if len(parts) > 1 {
			fb = parts[1]
		}
		return &ast.OnFail{Policy: "fallback", FallbackStep: fb}, nil
	}
	return nil, pe(line, "unknown ONFAIL policy %q", raw)
}

func parseNameParams(raw string, line int) (string, []string, error) {
	lparen := strings.Index(raw, "(")
	if lparen < 0 {
		return strings.TrimSpace(raw), nil, nil
	}
	name := strings.TrimSpace(raw[:lparen])
	paramsRaw := strings.TrimSuffix(strings.TrimSpace(raw[lparen+1:]), ")")
	var params []string
	for _, p := range strings.Split(paramsRaw, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			params = append(params, p)
		}
	}
	return name, params, nil
}

// ── Token-level utilities ─────────────────────────────────────────────────────

// tokensToString reconstructs the source text from a token slice.
func tokensToString(toks []lexer.Token) string {
	var sb strings.Builder
	for i, t := range toks {
		if i > 0 {
			// Add space between tokens except around DOT
			prev := toks[i-1]
			if prev.Type != lexer.DOT && t.Type != lexer.DOT &&
				prev.Type != lexer.LPAREN && t.Type != lexer.RPAREN &&
				prev.Type != lexer.LBRACKET && t.Type != lexer.RBRACKET &&
				t.Type != lexer.COMMA {
				sb.WriteByte(' ')
			}
		}
		switch t.Type {
		case lexer.STRING:
			sb.WriteRune('"')
			sb.WriteString(t.Literal)
			sb.WriteRune('"')
		default:
			sb.WriteString(t.Literal)
		}
	}
	return sb.String()
}

// parseDottedList extracts a comma-separated list of dotted names.
func parseDottedList(toks []lexer.Token) []string {
	var result []string
	var current strings.Builder
	for _, t := range toks {
		switch t.Type {
		case lexer.COMMA:
			if s := strings.TrimSpace(current.String()); s != "" {
				result = append(result, s)
			}
			current.Reset()
		case lexer.DOT:
			current.WriteByte('.')
		default:
			current.WriteString(t.Literal)
		}
	}
	if s := strings.TrimSpace(current.String()); s != "" {
		result = append(result, s)
	}
	return result
}

// splitKV parses "time=30s calls=10" → [["time","30s"],["calls","10"]]
func splitKV(raw string) [][2]string {
	var result [][2]string
	for _, field := range strings.Fields(raw) {
		eq := strings.Index(field, "=")
		if eq > 0 {
			result = append(result, [2]string{field[:eq], field[eq+1:]})
		}
	}
	return result
}

// parseDuration converts "30s", "2m", "1h" to seconds.
func parseDuration(s string) (float64, error) {
	if strings.HasSuffix(s, "h") {
		v, err := strconv.ParseFloat(s[:len(s)-1], 64)
		return v * 3600, err
	}
	if strings.HasSuffix(s, "m") {
		v, err := strconv.ParseFloat(s[:len(s)-1], 64)
		return v * 60, err
	}
	if strings.HasSuffix(s, "s") {
		return strconv.ParseFloat(s[:len(s)-1], 64)
	}
	return strconv.ParseFloat(s, 64)
}

// splitTopLevelCommas splits on commas that are not inside brackets/parens/quotes.
func splitTopLevelCommas(s string) []string {
	var parts []string
	depth := 0
	inStr := false
	start := 0
	runes := []rune(s)
	for i, ch := range runes {
		if inStr {
			if ch == '"' {
				inStr = false
			}
			continue
		}
		switch ch {
		case '"':
			inStr = true
		case '(', '[':
			depth++
		case ')', ']':
			depth--
		case ',':
			if depth == 0 {
				parts = append(parts, string(runes[start:i]))
				start = i + 1
			}
		}
	}
	if start < len(runes) {
		parts = append(parts, string(runes[start:]))
	}
	return parts
}

// ── Phase 3: REMOTE and SCHEDULE ─────────────────────────────────────────────

// parseRemote handles: REMOTE AgentName "http://host:8080"
func (p *parser) parseRemote() (*ast.RemoteDecl, error) {
	line := p.cur().Line
	toks := p.consumeLine()
	if len(toks) < 3 {
		return nil, pe(line, `REMOTE requires a name and URL: REMOTE AgentName "http://host:8080"`)
	}
	if toks[1].Type != lexer.IDENT {
		return nil, pe(line, "REMOTE: expected agent name, got %q", toks[1].Literal)
	}
	if toks[2].Type != lexer.STRING {
		return nil, pe(line, "REMOTE: expected quoted URL, got %q", toks[2].Literal)
	}
	return &ast.RemoteDecl{
		Name:    toks[1].Literal,
		BaseURL: toks[2].Literal,
		Line:    line,
	}, nil
}

// parseSchedule handles: SCHEDULE AgentName "5-field-cron"
func (p *parser) parseSchedule() (*ast.ScheduleDecl, error) {
	line := p.cur().Line
	toks := p.consumeLine()
	if len(toks) < 3 {
		return nil, pe(line, `SCHEDULE requires an agent name and cron expression: SCHEDULE AgentName "0 9 * * 1"`)
	}
	if toks[1].Type != lexer.IDENT {
		return nil, pe(line, "SCHEDULE: expected agent name, got %q", toks[1].Literal)
	}
	if toks[2].Type != lexer.STRING {
		return nil, pe(line, "SCHEDULE: expected quoted cron expression, got %q", toks[2].Literal)
	}
	if err := validateCronExpr(toks[2].Literal); err != nil {
		return nil, pe(line, "SCHEDULE: invalid cron expression %q: %v", toks[2].Literal, err)
	}
	return &ast.ScheduleDecl{
		AgentName: toks[1].Literal,
		Cron:      toks[2].Literal,
		Line:      line,
	}, nil
}

// validateCronExpr validates a 5-field cron expression.
// Supported: * | */N | integer (single value).
// Fields: minute(0-59) hour(0-23) dom(1-31) month(1-12) dow(0-7)
func validateCronExpr(expr string) error {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return fmt.Errorf("expected 5 fields (got %d): \"minute hour dom month dow\"", len(fields))
	}
	limits := [5][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 7}}
	names := []string{"minute", "hour", "day-of-month", "month", "day-of-week"}
	for i, f := range fields {
		lo, hi := limits[i][0], limits[i][1]
		if f == "*" {
			continue
		}
		if strings.HasPrefix(f, "*/") {
			n, err := strconv.Atoi(f[2:])
			if err != nil || n < 1 {
				return fmt.Errorf("field %s: invalid step %q (must be */N where N≥1)", names[i], f)
			}
			continue
		}
		// Allow comma-separated values.
		for _, part := range strings.Split(f, ",") {
			n, err := strconv.Atoi(part)
			if err != nil {
				return fmt.Errorf("field %s: %q is not a number", names[i], part)
			}
			if n < lo || n > hi {
				return fmt.Errorf("field %s: %d out of range [%d, %d]", names[i], n, lo, hi)
			}
		}
	}
	return nil
}
