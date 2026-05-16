// Package main is the acl CLI binary.
//
// Verbs:
//
//	acl run <file.acl>      [--var k=v]... [--vars f.json] [--agent Name]
//	                        [--no-cache] [--receipt path] [--quiet]
//	acl serve <file.acl>    [--port 8080]
//	acl init <project>
//	acl history list        [--limit 20]
//	acl history show <id>
//	acl history purge       [--days 30]
//	acl cli <file.acl>      [--name <name>] [--out <path>]
//	acl version
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ranausmanai/acl/internal/cligen"
	"github.com/ranausmanai/acl/internal/lexer"
	"github.com/ranausmanai/acl/internal/parser"
	"github.com/ranausmanai/acl/internal/protocol"
	"github.com/ranausmanai/acl/internal/receipt"
	"github.com/ranausmanai/acl/internal/runtime"
	"github.com/ranausmanai/acl/internal/schema"
	"github.com/ranausmanai/acl/internal/server"
	"github.com/ranausmanai/acl/internal/store"
	"github.com/ranausmanai/acl/tools/builtin"
)

// Version is overridable via -ldflags "-X main.Version=..."
var Version = "0.1.0"

func main() {
	if len(os.Args) < 2 {
		printUsage(os.Stderr)
		os.Exit(2)
	}
	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "run":
		os.Exit(cmdRun(args))
	case "serve":
		os.Exit(cmdServe(args))
	case "init":
		os.Exit(cmdInit(args))
	case "history":
		os.Exit(cmdHistory(args))
	case "cli":
		os.Exit(cmdCLI(args))
	case "version", "--version", "-v":
		fmt.Println("acl", Version)
	case "help", "--help", "-h":
		printUsage(os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "acl: unknown command %q\n\n", cmd)
		printUsage(os.Stderr)
		os.Exit(2)
	}
}

func printUsage(w *os.File) {
	fmt.Fprintf(w, `acl — Agent Contract Language

USAGE
  acl run <file.acl>      Execute an ACL program
  acl serve <file.acl>    Expose every AGENT as an HTTP endpoint
  acl init <project>      Scaffold a new ACL project
  acl history list|show|purge   Inspect run history
  acl cli <file.acl>      Generate a bash wrapper that calls each AGENT
  acl version             Print the version

Run any command with --help to see its flags.
Docs: https://acl.fyi
`)
}

// ---------------------------------------------------------------------------
// run
// ---------------------------------------------------------------------------

func cmdRun(args []string) int {
	var (
		file        string
		vars        = map[string]any{}
		varsFile    string
		agentName   string
		noCache     bool
		receiptPath string
		quiet       bool
	)

	i := 0
	for i < len(args) {
		a := args[i]
		switch {
		case a == "--help" || a == "-h":
			fmt.Println(`acl run <file.acl> [flags]

Flags:
  --var key=value       Set an input variable (repeatable)
  --vars <file.json>    Load variables from a JSON file
  --agent <Name>        Run only this agent (otherwise: all agents in order)
  --no-cache            Disable the SHA-256 step-result cache
  --receipt <path>      Write the receipt JSON to this path
  --quiet, -q           Suppress progress output`)
			return 0
		case a == "--var":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "acl: --var needs key=value")
				return 2
			}
			k, v, ok := strings.Cut(args[i], "=")
			if !ok {
				fmt.Fprintln(os.Stderr, "acl: --var expects key=value")
				return 2
			}
			vars[k] = coerceScalar(v)
		case a == "--vars":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "acl: --vars needs a path")
				return 2
			}
			varsFile = args[i]
		case a == "--agent":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "acl: --agent needs a name")
				return 2
			}
			agentName = args[i]
		case a == "--no-cache":
			noCache = true
		case a == "--receipt":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "acl: --receipt needs a path")
				return 2
			}
			receiptPath = args[i]
		case a == "--quiet" || a == "-q":
			quiet = true
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(os.Stderr, "acl run: unknown flag %q\n", a)
			return 2
		default:
			if file != "" {
				fmt.Fprintln(os.Stderr, "acl run: only one file is supported")
				return 2
			}
			file = a
		}
		i++
	}

	if file == "" {
		fmt.Fprintln(os.Stderr, "acl run: missing <file.acl>")
		return 2
	}

	src, err := os.ReadFile(file)
	if err != nil {
		fmt.Fprintf(os.Stderr, "acl run: %v\n", err)
		return 1
	}
	if varsFile != "" {
		raw, err := os.ReadFile(varsFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "acl run: %v\n", err)
			return 1
		}
		extra := map[string]any{}
		if err := json.Unmarshal(raw, &extra); err != nil {
			fmt.Fprintf(os.Stderr, "acl run: --vars: %v\n", err)
			return 1
		}
		// Inline --var entries win over the file (set last).
		for k, v := range extra {
			if _, set := vars[k]; !set {
				vars[k] = v
			}
		}
	}

	cfg := runtime.Config{
		Vars:        vars,
		ReceiptPath: receiptPath,
	}
	if !noCache {
		cfg.CacheDir = defaultCacheDir()
	}

	reg := builtin.NewRegistry()

	// runProgram returns the receipt and (optionally) an error. The receipt
	// is the product — print and persist it regardless of error.
	r, runErr := runProgram(context.Background(), string(src), cfg, reg, agentName)
	if r == nil {
		fmt.Fprintf(os.Stderr, "acl run: %v\n", runErr)
		return 1
	}

	if os.Getenv("ACL_NO_HISTORY") == "" {
		if dbPath, err := store.DefaultPath(); err == nil {
			if st, err := store.Open(dbPath); err == nil {
				_, _ = st.Put(r)
				_ = st.Close()
			}
		}
	}

	if !quiet {
		fmt.Printf("acl > %s  (status=%s, agents=%d)\n", file, r.Status, len(r.Agents))
		if r.Error != "" {
			fmt.Fprintf(os.Stderr, "acl > error: %s\n", r.Error)
		}
		for _, a := range r.Agents {
			must := "n/a"
			if a.MustPassed != nil {
				if *a.MustPassed {
					must = "true"
				} else {
					must = "false"
				}
			}
			fmt.Printf("  - %s: %s  (must=%s, steps=%d)\n",
				a.Name, a.Status, must, len(a.Steps))
			// Surface the failing CHECK for any step that didn't pass.
			// This is what makes failure-mode runs useful: the receipt
			// quotes the exact expression that stopped the agent.
			for _, s := range a.Steps {
				if s.CheckPassed != nil && !*s.CheckPassed {
					fmt.Printf("      ↳ step %q failed: CHECK %s\n",
						s.Name, s.CheckExpr)
				}
			}
		}
		if receiptPath != "" {
			fmt.Printf("acl > receipt: %s\n", receiptPath)
		}
	}
	_ = runErr // already reflected in r.Status / r.Error

	if r.Status != "success" {
		return 1
	}
	return 0
}

// ---------------------------------------------------------------------------
// serve
// ---------------------------------------------------------------------------

func cmdServe(args []string) int {
	var (
		file string
		port = 8080
	)
	i := 0
	for i < len(args) {
		a := args[i]
		switch {
		case a == "--help" || a == "-h":
			fmt.Println(`acl serve <file.acl> [--port 8080]

Exposes every AGENT as POST /run/<Name>. SCHEDULE agents fire in the background.
Set ACL_SERVE_API_KEY to require a bearer token on /agents and /run/*.`)
			return 0
		case a == "--port":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "acl: --port needs a value")
				return 2
			}
			n, err := strconv.Atoi(args[i])
			if err != nil {
				fmt.Fprintf(os.Stderr, "acl: --port: %v\n", err)
				return 2
			}
			port = n
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(os.Stderr, "acl serve: unknown flag %q\n", a)
			return 2
		default:
			if file != "" {
				fmt.Fprintln(os.Stderr, "acl serve: only one file is supported")
				return 2
			}
			file = a
		}
		i++
	}
	if file == "" {
		fmt.Fprintln(os.Stderr, "acl serve: missing <file.acl>")
		return 2
	}

	src, err := os.ReadFile(file)
	if err != nil {
		fmt.Fprintf(os.Stderr, "acl serve: %v\n", err)
		return 1
	}
	reg := builtin.NewRegistry()
	srv, err := server.NewServer(string(src), reg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "acl serve: %v\n", err)
		return 1
	}
	if err := srv.Start(port); err != nil {
		fmt.Fprintf(os.Stderr, "acl serve: %v\n", err)
		return 1
	}
	return 0
}

// ---------------------------------------------------------------------------
// init
// ---------------------------------------------------------------------------

func cmdInit(args []string) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Println(`acl init <project>

Creates ./<project>/main.acl and a stub vars.json so you can run:
  cd <project>
  acl run main.acl`)
		if len(args) == 0 {
			return 2
		}
		return 0
	}
	name := args[0]
	if err := os.MkdirAll(name, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "acl init: %v\n", err)
		return 1
	}
	mainPath := filepath.Join(name, "main.acl")
	if _, err := os.Stat(mainPath); err == nil {
		fmt.Fprintf(os.Stderr, "acl init: %s already exists\n", mainPath)
		return 1
	}
	if err := os.WriteFile(mainPath, []byte(scaffoldACL), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "acl init: %v\n", err)
		return 1
	}
	_ = os.WriteFile(filepath.Join(name, "vars.json"), []byte("{\n}\n"), 0o644)
	fmt.Printf("acl > created %s\n", mainPath)
	fmt.Printf("acl > next:  cd %s && acl run main.acl\n", name)
	return 0
}

const scaffoldACL = `INTENT "Hello from ACL"
ALLOW  http.get

AGENT Hello
  TOOLS http.get
  STEP page = TOOL http.get(url="https://example.com")
    CHECK page.status == 200
  RESULT page
`

// ---------------------------------------------------------------------------
// history
// ---------------------------------------------------------------------------

func cmdHistory(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "acl history: missing subcommand (list|show|purge)")
		return 2
	}
	sub := args[0]
	rest := args[1:]

	dbPath, err := store.DefaultPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "acl history: %v\n", err)
		return 1
	}
	st, err := store.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "acl history: %v\n", err)
		return 1
	}
	defer st.Close()

	switch sub {
	case "list":
		n := 20
		for i := 0; i < len(rest); i++ {
			if rest[i] == "--limit" && i+1 < len(rest) {
				if v, err := strconv.Atoi(rest[i+1]); err == nil {
					n = v
				}
				i++
			}
		}
		rows, err := st.List(n)
		if err != nil {
			fmt.Fprintf(os.Stderr, "acl history list: %v\n", err)
			return 1
		}
		if len(rows) == 0 {
			fmt.Println("no runs yet")
			return 0
		}
		fmt.Printf("%-5s  %-10s  %-25s  %-8s  %s\n", "ID", "STATUS", "WHEN", "DUR(ms)", "INTENT")
		for _, r := range rows {
			intent := r.Intent
			if len(intent) > 60 {
				intent = intent[:57] + "..."
			}
			fmt.Printf("%-5d  %-10s  %-25s  %-8d  %s\n",
				r.ID, r.Status, r.Timestamp, r.DurationMS, intent)
		}
		return 0

	case "show":
		if len(rest) == 0 {
			fmt.Fprintln(os.Stderr, "acl history show: missing <id>")
			return 2
		}
		id, err := strconv.ParseInt(rest[0], 10, 64)
		if err != nil {
			fmt.Fprintf(os.Stderr, "acl history show: %v\n", err)
			return 2
		}
		rec, err := st.Get(id)
		if err != nil {
			fmt.Fprintf(os.Stderr, "acl history show: %v\n", err)
			return 1
		}
		b, _ := json.MarshalIndent(rec, "", "  ")
		fmt.Println(string(b))
		return 0

	case "purge":
		days := 30
		for i := 0; i < len(rest); i++ {
			if rest[i] == "--days" && i+1 < len(rest) {
				if v, err := strconv.Atoi(rest[i+1]); err == nil {
					days = v
				}
				i++
			}
		}
		n, err := st.Purge(days)
		if err != nil {
			fmt.Fprintf(os.Stderr, "acl history purge: %v\n", err)
			return 1
		}
		fmt.Printf("purged %d run(s) older than %d days\n", n, days)
		return 0

	default:
		fmt.Fprintf(os.Stderr, "acl history: unknown subcommand %q\n", sub)
		return 2
	}
}

// ---------------------------------------------------------------------------
// cli (bash wrapper generator)
// ---------------------------------------------------------------------------

func cmdCLI(args []string) int {
	var (
		file    string
		name    string
		outPath string
	)
	i := 0
	for i < len(args) {
		a := args[i]
		switch {
		case a == "--help" || a == "-h":
			fmt.Println(`acl cli <file.acl> [--name <name>] [--out <path>]

Generate a self-contained bash CLI wrapper that exposes every AGENT
in the file as a kebab-case sub-command. The output script calls
"acl run <file> --agent <Name>" under the hood.`)
			return 0
		case a == "--name":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "acl: --name needs a value")
				return 2
			}
			name = args[i]
		case a == "--out":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "acl: --out needs a path")
				return 2
			}
			outPath = args[i]
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(os.Stderr, "acl cli: unknown flag %q\n", a)
			return 2
		default:
			if file != "" {
				fmt.Fprintln(os.Stderr, "acl cli: only one file is supported")
				return 2
			}
			file = a
		}
		i++
	}
	if file == "" {
		fmt.Fprintln(os.Stderr, "acl cli: missing <file.acl>")
		return 2
	}
	src, err := os.ReadFile(file)
	if err != nil {
		fmt.Fprintf(os.Stderr, "acl cli: %v\n", err)
		return 1
	}
	svc, err := schema.FromSource(string(src))
	if err != nil {
		fmt.Fprintf(os.Stderr, "acl cli: %v\n", err)
		return 1
	}
	if name == "" {
		name = strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
	}
	script, err := cligen.Generate(svc, name, file)
	if err != nil {
		fmt.Fprintf(os.Stderr, "acl cli: %v\n", err)
		return 1
	}
	if outPath == "" {
		fmt.Print(script)
		return 0
	}
	if err := os.WriteFile(outPath, []byte(script), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "acl cli: %v\n", err)
		return 1
	}
	fmt.Printf("acl > wrote %s\n", outPath)
	return 0
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// runProgram dispatches to RunNamedAgent when --agent is set, otherwise to
// RunSource. Both return a *receipt.Receipt that the caller must surface
// regardless of error — the receipt IS the audit log.
func runProgram(
	ctx context.Context,
	src string,
	cfg runtime.Config,
	reg *protocol.Registry,
	agentName string,
) (*receipt.Receipt, error) {
	if agentName == "" {
		return runtime.RunSource(ctx, src, cfg, reg)
	}
	toks, err := lexer.New(src).Tokenize()
	if err != nil {
		return nil, fmt.Errorf("lex: %w", err)
	}
	nodes, err := parser.Parse(toks)
	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	return runtime.RunNamedAgent(ctx, nodes, agentName, cfg, reg)
}

// coerceScalar turns a --var key=value RHS into a typed Go value.
// Booleans and numbers are auto-coerced so contracts that compare
// numerically don't have to handle string inputs.
func coerceScalar(s string) any {
	switch strings.ToLower(s) {
	case "true":
		return true
	case "false":
		return false
	case "null":
		return nil
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	return s
}

func defaultCacheDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".acl_cache"
	}
	return filepath.Join(home, ".acl", "cache")
}
