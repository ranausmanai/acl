package evidence

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// Parse parses an ACL expression string into an Expr AST.
// This is a hand-written Pratt / recursive-descent parser.
//
// Grammar (precedence low → high):
//
//	expr     := or
//	or       := and ('or' and)*
//	and      := not ('and' not)*
//	not      := 'not' not | compare
//	compare  := add (cmpOp add)*   cmpOp := == != < <= > >= in
//	add      := mul (('+' | '-') mul)*
//	mul      := unary (('*' | '/') unary)*
//	unary    := '-' unary | postfix
//	postfix  := primary ('.' IDENT | '[' expr ']' | '(' args ')' )*
//	primary  := NULL | BOOL | INT | FLOAT | STRING | IDENT | '(' expr ')' | '[' list ']'
func Parse(src string) (*Expr, error) {
	p := &exprParser{tokens: tokeniseExpr(src), pos: 0}
	expr, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if p.pos < len(p.tokens) && p.cur().kind != eTOKEOF {
		return nil, fmt.Errorf("unexpected token %q after expression", p.cur().lit)
	}
	return &expr, nil
}

// ── Minimal expression tokeniser ─────────────────────────────────────────────

type eTokKind int

const (
	eTOKEOF eTokKind = iota
	eTOKNULL
	eTOKBOOL
	eTOKINT
	eTOKFLOAT
	eTOKSTRING
	eTOKIDENT
	eTOKLPAREN
	eTOKRPAREN
	eTOKLBRACKET
	eTOKRBRACKET
	eTOKCOMMA
	eTOKDOT
	eTOKPLUS
	eTOKMINUS
	eTOKSTAR
	eTOKSLASH
	eTOKEQ  // ==
	eTOKNEQ // !=
	eTOKLT
	eTOKLTE
	eTOKGT
	eTOKGTE
	eTOKAND
	eTOKOR
	eTOKNOT
	eTOKIN
)

type eTok struct {
	kind eTokKind
	lit  string
}

func tokeniseExpr(src string) []eTok {
	runes := []rune(strings.TrimSpace(src))
	var tokens []eTok
	i := 0
	for i < len(runes) {
		ch := runes[i]
		if ch == ' ' || ch == '\t' {
			i++
			continue
		}
		if ch == '"' {
			j := i + 1
			var sb strings.Builder
			for j < len(runes) && runes[j] != '"' {
				if runes[j] == '\\' && j+1 < len(runes) {
					j++
					switch runes[j] {
					case 'n':
						sb.WriteByte('\n')
					case 't':
						sb.WriteByte('\t')
					default:
						sb.WriteRune(runes[j])
					}
				} else {
					sb.WriteRune(runes[j])
				}
				j++
			}
			tokens = append(tokens, eTok{eTOKSTRING, sb.String()})
			i = j + 1
			continue
		}
		if unicode.IsDigit(ch) {
			j := i
			isFloat := false
			for j < len(runes) && (unicode.IsDigit(runes[j]) || runes[j] == '.') {
				if runes[j] == '.' {
					isFloat = true
				}
				j++
			}
			lit := string(runes[i:j])
			if isFloat {
				tokens = append(tokens, eTok{eTOKFLOAT, lit})
			} else {
				tokens = append(tokens, eTok{eTOKINT, lit})
			}
			i = j
			continue
		}
		if unicode.IsLetter(ch) || ch == '_' {
			j := i
			for j < len(runes) && (unicode.IsLetter(runes[j]) || unicode.IsDigit(runes[j]) || runes[j] == '_') {
				j++
			}
			lit := string(runes[i:j])
			switch lit {
			case "True", "true":
				tokens = append(tokens, eTok{eTOKBOOL, "True"})
			case "False", "false":
				tokens = append(tokens, eTok{eTOKBOOL, "False"})
			case "None", "null", "nil":
				tokens = append(tokens, eTok{eTOKNULL, "None"})
			case "and":
				tokens = append(tokens, eTok{eTOKAND, "and"})
			case "or":
				tokens = append(tokens, eTok{eTOKOR, "or"})
			case "not":
				tokens = append(tokens, eTok{eTOKNOT, "not"})
			case "in":
				tokens = append(tokens, eTok{eTOKIN, "in"})
			default:
				tokens = append(tokens, eTok{eTOKIDENT, lit})
			}
			i = j
			continue
		}
		// Two-char operators
		if i+1 < len(runes) {
			two := string(runes[i : i+2])
			switch two {
			case "==":
				tokens = append(tokens, eTok{eTOKEQ, "=="})
				i += 2
				continue
			case "!=":
				tokens = append(tokens, eTok{eTOKNEQ, "!="})
				i += 2
				continue
			case "<=":
				tokens = append(tokens, eTok{eTOKLTE, "<="})
				i += 2
				continue
			case ">=":
				tokens = append(tokens, eTok{eTOKGTE, ">="})
				i += 2
				continue
			}
		}
		switch ch {
		case '(':
			tokens = append(tokens, eTok{eTOKLPAREN, "("})
		case ')':
			tokens = append(tokens, eTok{eTOKRPAREN, ")"})
		case '[':
			tokens = append(tokens, eTok{eTOKLBRACKET, "["})
		case ']':
			tokens = append(tokens, eTok{eTOKRBRACKET, "]"})
		case ',':
			tokens = append(tokens, eTok{eTOKCOMMA, ","})
		case '.':
			tokens = append(tokens, eTok{eTOKDOT, "."})
		case '+':
			tokens = append(tokens, eTok{eTOKPLUS, "+"})
		case '-':
			tokens = append(tokens, eTok{eTOKMINUS, "-"})
		case '*':
			tokens = append(tokens, eTok{eTOKSTAR, "*"})
		case '/':
			tokens = append(tokens, eTok{eTOKSLASH, "/"})
		case '<':
			tokens = append(tokens, eTok{eTOKLT, "<"})
		case '>':
			tokens = append(tokens, eTok{eTOKGT, ">"})
		}
		i++
	}
	tokens = append(tokens, eTok{eTOKEOF, ""})
	return tokens
}

// ── Recursive descent parser ──────────────────────────────────────────────────

type exprParser struct {
	tokens []eTok
	pos    int
}

func (p *exprParser) cur() eTok {
	if p.pos < len(p.tokens) {
		return p.tokens[p.pos]
	}
	return eTok{eTOKEOF, ""}
}

func (p *exprParser) peek(offset int) eTok {
	i := p.pos + offset
	if i < len(p.tokens) {
		return p.tokens[i]
	}
	return eTok{eTOKEOF, ""}
}

func (p *exprParser) advance() eTok {
	t := p.cur()
	p.pos++
	return t
}

func (p *exprParser) expect(k eTokKind) (eTok, error) {
	t := p.cur()
	if t.kind != k {
		return t, fmt.Errorf("expected %v got %q", k, t.lit)
	}
	p.pos++
	return t, nil
}

func (p *exprParser) parseOr() (Expr, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.cur().kind == eTOKOR {
		p.advance()
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = &BinOpExpr{Op: "or", Left: left, Right: right}
	}
	return left, nil
}

func (p *exprParser) parseAnd() (Expr, error) {
	left, err := p.parseNot()
	if err != nil {
		return nil, err
	}
	for p.cur().kind == eTOKAND {
		p.advance()
		right, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		left = &BinOpExpr{Op: "and", Left: left, Right: right}
	}
	return left, nil
}

func (p *exprParser) parseNot() (Expr, error) {
	if p.cur().kind == eTOKNOT {
		p.advance()
		operand, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		return &UnaryExpr{Op: "not", Operand: operand}, nil
	}
	return p.parseCompare()
}

func (p *exprParser) parseCompare() (Expr, error) {
	left, err := p.parseAdd()
	if err != nil {
		return nil, err
	}
	for {
		op := ""
		switch p.cur().kind {
		case eTOKEQ:
			op = "=="
		case eTOKNEQ:
			op = "!="
		case eTOKLT:
			op = "<"
		case eTOKLTE:
			op = "<="
		case eTOKGT:
			op = ">"
		case eTOKGTE:
			op = ">="
		case eTOKIN:
			op = "in"
		}
		if op == "" {
			// Check for "not in"
			if p.cur().kind == eTOKNOT && p.peek(1).kind == eTOKIN {
				p.advance()
				p.advance()
				right, err := p.parseAdd()
				if err != nil {
					return nil, err
				}
				left = &BinOpExpr{Op: "not in", Left: left, Right: right}
				continue
			}
			break
		}
		p.advance()
		right, err := p.parseAdd()
		if err != nil {
			return nil, err
		}
		left = &BinOpExpr{Op: op, Left: left, Right: right}
	}
	return left, nil
}

func (p *exprParser) parseAdd() (Expr, error) {
	left, err := p.parseMul()
	if err != nil {
		return nil, err
	}
	for p.cur().kind == eTOKPLUS || p.cur().kind == eTOKMINUS {
		op := p.advance().lit
		right, err := p.parseMul()
		if err != nil {
			return nil, err
		}
		left = &BinOpExpr{Op: op, Left: left, Right: right}
	}
	return left, nil
}

func (p *exprParser) parseMul() (Expr, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for p.cur().kind == eTOKSTAR || p.cur().kind == eTOKSLASH {
		op := p.advance().lit
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		left = &BinOpExpr{Op: op, Left: left, Right: right}
	}
	return left, nil
}

func (p *exprParser) parseUnary() (Expr, error) {
	if p.cur().kind == eTOKMINUS {
		p.advance()
		operand, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &UnaryExpr{Op: "-", Operand: operand}, nil
	}
	return p.parsePostfix()
}

func (p *exprParser) parsePostfix() (Expr, error) {
	expr, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	for {
		switch p.cur().kind {
		case eTOKDOT:
			p.advance()
			t, err := p.expect(eTOKIDENT)
			if err != nil {
				return nil, err
			}
			expr = &AttrExpr{Obj: expr, Field: t.lit}
		case eTOKLBRACKET:
			p.advance()
			idx, err := p.parseOr()
			if err != nil {
				return nil, err
			}
			if _, err := p.expect(eTOKRBRACKET); err != nil {
				return nil, err
			}
			expr = &IndexExpr{Obj: expr, Idx: idx}
		case eTOKLPAREN:
			// function call: expr must be an IdentExpr
			ident, ok := expr.(*IdentExpr)
			if !ok {
				return nil, fmt.Errorf("call on non-identifier")
			}
			p.advance()
			args, err := p.parseArgList()
			if err != nil {
				return nil, err
			}
			expr = &CallExpr{Func: ident.Name, Args: args}
		default:
			return expr, nil
		}
	}
}

func (p *exprParser) parsePrimary() (Expr, error) {
	t := p.cur()
	switch t.kind {
	case eTOKNULL:
		p.advance()
		return &NullExpr{}, nil
	case eTOKBOOL:
		p.advance()
		return &BoolExpr{Val: t.lit == "True"}, nil
	case eTOKINT:
		p.advance()
		v, err := strconv.ParseInt(t.lit, 10, 64)
		if err != nil {
			return nil, err
		}
		return &IntExpr{Val: v}, nil
	case eTOKFLOAT:
		p.advance()
		v, err := strconv.ParseFloat(t.lit, 64)
		if err != nil {
			return nil, err
		}
		return &FloatExpr{Val: v}, nil
	case eTOKSTRING:
		p.advance()
		return &StringExpr{Val: t.lit}, nil
	case eTOKIDENT:
		p.advance()
		return &IdentExpr{Name: t.lit}, nil
	case eTOKLPAREN:
		p.advance()
		inner, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(eTOKRPAREN); err != nil {
			return nil, err
		}
		return inner, nil
	case eTOKLBRACKET:
		p.advance()
		items, err := p.parseArgList()
		if err != nil {
			return nil, err
		}
		return &ListExpr{Items: items}, nil
	}
	return nil, fmt.Errorf("unexpected token %q in expression", t.lit)
}

func (p *exprParser) parseArgList() ([]Expr, error) {
	var args []Expr
	for p.cur().kind != eTOKRPAREN && p.cur().kind != eTOKRBRACKET && p.cur().kind != eTOKEOF {
		arg, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		args = append(args, arg)
		if p.cur().kind == eTOKCOMMA {
			p.advance()
		} else {
			break
		}
	}
	// consume closing ) or ]
	if p.cur().kind == eTOKRPAREN || p.cur().kind == eTOKRBRACKET {
		p.advance()
	}
	return args, nil
}
