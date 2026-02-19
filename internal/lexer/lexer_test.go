package lexer_test

import (
	"testing"

	"github.com/ranausmanai/acl/internal/lexer"
)

func tok(tt lexer.TokenType, lit string) lexer.Token {
	return lexer.Token{Type: tt, Literal: lit}
}

func assertTokens(t *testing.T, src string, want []lexer.Token) {
	t.Helper()
	tokens, err := lexer.New(src).Tokenize()
	if err != nil {
		t.Fatalf("Tokenize error: %v", err)
	}
	// Strip trailing EOF
	if len(tokens) > 0 && tokens[len(tokens)-1].Type == lexer.EOF {
		tokens = tokens[:len(tokens)-1]
	}
	if len(tokens) != len(want) {
		t.Fatalf("token count: got %d want %d\ntokens: %v", len(tokens), len(want), tokens)
	}
	for i, w := range want {
		got := tokens[i]
		if got.Type != w.Type || got.Literal != w.Literal {
			t.Errorf("token[%d]: got {%v %q} want {%v %q}", i, got.Type, got.Literal, w.Type, w.Literal)
		}
	}
}

func TestKeywords(t *testing.T) {
	src := `INTENT "hello"`
	assertTokens(t, src, []lexer.Token{
		tok(lexer.KWINTENT, "INTENT"),
		tok(lexer.STRING, "hello"),
		tok(lexer.NEWLINE, "\n"),
	})
}

func TestDottedToolName(t *testing.T) {
	src := "http.get"
	tokens, _ := lexer.New(src).Tokenize()
	if len(tokens) < 3 {
		t.Fatalf("expected at least 3 tokens, got %d", len(tokens))
	}
	if tokens[0].Literal != "http" || tokens[1].Type != lexer.DOT || tokens[2].Literal != "get" {
		t.Errorf("unexpected dotted token sequence: %v", tokens[:3])
	}
}

func TestNumbers(t *testing.T) {
	assertTokens(t, "42 3.14", []lexer.Token{
		tok(lexer.INT, "42"),
		tok(lexer.FLOAT, "3.14"),
		tok(lexer.NEWLINE, "\n"),
	})
}

func TestComment(t *testing.T) {
	src := "# this is a comment\nINTENT \"x\""
	tokens, err := lexer.New(src).Tokenize()
	if err != nil {
		t.Fatal(err)
	}
	if tokens[0].Type != lexer.KWINTENT {
		t.Errorf("comment not stripped: first token %v", tokens[0])
	}
}

func TestConsecutiveNewlines(t *testing.T) {
	src := "INTENT \"a\"\n\n\nAGENT Foo"
	tokens, err := lexer.New(src).Tokenize()
	if err != nil {
		t.Fatal(err)
	}
	newlineCount := 0
	for _, tk := range tokens {
		if tk.Type == lexer.NEWLINE {
			newlineCount++
		}
	}
	// One NEWLINE after the INTENT line + one trailing NEWLINE = 2 total.
	// The blank lines between the two statements are collapsed (no extra NEWLINEs).
	if newlineCount != 2 {
		t.Errorf("expected 2 newlines (1 per statement boundary), got %d", newlineCount)
	}
}

func TestOperators(t *testing.T) {
	src := "== != <= >= < > + - * /"
	tokens, err := lexer.New(src).Tokenize()
	if err != nil {
		t.Fatal(err)
	}
	want := []lexer.TokenType{
		lexer.EQEQ, lexer.NEQ, lexer.LTE, lexer.GTE,
		lexer.LT, lexer.GT, lexer.PLUS, lexer.MINUS, lexer.STAR, lexer.SLASH,
	}
	j := 0
	for _, tk := range tokens {
		if tk.Type == lexer.NEWLINE || tk.Type == lexer.EOF {
			continue
		}
		if j >= len(want) {
			t.Fatalf("too many tokens")
		}
		if tk.Type != want[j] {
			t.Errorf("op[%d]: got %v want %v", j, tk.Type, want[j])
		}
		j++
	}
}

func TestStringEscaping(t *testing.T) {
	src := `"hello world"`
	tokens, err := lexer.New(src).Tokenize()
	if err != nil {
		t.Fatal(err)
	}
	if tokens[0].Type != lexer.STRING || tokens[0].Literal != "hello world" {
		t.Errorf("string token: %v", tokens[0])
	}
}

func TestAllKeywords(t *testing.T) {
	kws := []string{"INTENT", "ALLOW", "LIMIT", "AGENT", "TEMPLATE", "MAKE", "GROUP",
		"IN", "OUT", "TOOLS", "MUST", "STEP", "CHECK", "ONFAIL", "RESULT", "FOR", "AS", "TOOL"}
	for _, kw := range kws {
		tokens, err := lexer.New(kw).Tokenize()
		if err != nil {
			t.Fatalf("keyword %q: %v", kw, err)
		}
		if tokens[0].Type <= lexer.IDENT {
			t.Errorf("keyword %q not recognised, got type %v", kw, tokens[0].Type)
		}
	}
}
