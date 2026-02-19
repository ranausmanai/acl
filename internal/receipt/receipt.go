// Package receipt builds the structured execution trace (receipt) that ACL
// emits after every run — whether it succeeds or fails.
package receipt

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// ── Top-level receipt ─────────────────────────────────────────────────────────

type Receipt struct {
	ACLVersion string       `json:"acl_version"`
	Timestamp  string       `json:"timestamp"`
	Status     string       `json:"status"` // success | failed | needs_human
	Intent     string       `json:"intent"`
	Policy     Policy       `json:"policy"`
	Agents     []AgentTrace `json:"agents"`
	Error      string       `json:"error,omitempty"`
}

type Policy struct {
	Allow []string    `json:"allow"`
	Limit LimitPolicy `json:"limit"`
}

type LimitPolicy struct {
	TimeS   *float64 `json:"time_s"`
	Calls   *int     `json:"calls"`
	Retries int      `json:"retries"`
}

// ── Agent trace ───────────────────────────────────────────────────────────────

type AgentTrace struct {
	Name         string            `json:"name"`
	FromTemplate string            `json:"from_template,omitempty"`
	TemplateArgs map[string]any    `json:"template_args,omitempty"`
	Inputs       []string          `json:"inputs"`
	Outputs      []string          `json:"outputs"`
	Tools        []string          `json:"tools"`
	MustExpr     string            `json:"must_expr,omitempty"`
	MustPassed   *bool             `json:"must_passed"`
	Result       any               `json:"result"`
	Status       string            `json:"status"` // success | failed | needs_human | skipped
	Error        string            `json:"error,omitempty"`
	Steps        []StepTrace       `json:"steps"`
}

// ── Step trace ────────────────────────────────────────────────────────────────

type StepTrace struct {
	Name          string         `json:"name"`
	Kind          string         `json:"kind"` // tool | agent
	Target        string         `json:"target"`
	Args          map[string]any `json:"args"`
	OutputHash    string         `json:"output_hash"`
	OutputPreview string         `json:"output_preview"`
	CacheHit      bool           `json:"cache_hit"`
	CheckExpr     string         `json:"check_expr,omitempty"`
	CheckPassed   *bool          `json:"check_passed"`
	OnFailPolicy  string         `json:"onfail_policy,omitempty"`
	RetriesUsed   int            `json:"retries_used"`
	Parallel      bool           `json:"parallel,omitempty"` // true when step ran in a PARALLEL block
	StartedAt     string         `json:"started_at"`
	EndedAt       string         `json:"ended_at"`
	DurationMS    int64          `json:"duration_ms"`
	ToolCalls     []ToolCall     `json:"tool_calls"`
	Error         string         `json:"error,omitempty"`
}

type ToolCall struct {
	Tool       string `json:"tool"`
	CacheHit   bool   `json:"cache_hit"`
	DurationMS int64  `json:"duration_ms"`
}

// ── Builder ───────────────────────────────────────────────────────────────────

type Builder struct {
	r Receipt
}

func NewBuilder(intent string) *Builder {
	return &Builder{
		r: Receipt{
			ACLVersion: "0.1.0",
			Timestamp:  time.Now().UTC().Format(time.RFC3339Nano),
			Status:     "failed",
			Intent:     intent,
		},
	}
}

func (b *Builder) SetPolicy(allow []string, timeS *float64, calls *int, retries int) {
	b.r.Policy = Policy{
		Allow: allow,
		Limit: LimitPolicy{TimeS: timeS, Calls: calls, Retries: retries},
	}
}

func (b *Builder) SetStatus(status, errMsg string) {
	b.r.Status = status
	b.r.Error = errMsg
}

func (b *Builder) AddAgent(a AgentTrace) {
	b.r.Agents = append(b.r.Agents, a)
}

func (b *Builder) Build() Receipt {
	return b.r
}

func (b *Builder) Write(path string) error {
	data, err := json.MarshalIndent(b.r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func HashValue(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:16])
}

func PreviewValue(v any, maxLen int) string {
	b, err := json.Marshal(v)
	if err != nil {
		b = []byte(fmt.Sprintf("%v", v))
	}
	s := string(b)
	if len(s) > maxLen {
		return s[:maxLen] + "…"
	}
	return s
}

func BoolPtr(b bool) *bool { return &b }

func Now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func DurationMS(start string) int64 {
	t, err := time.Parse(time.RFC3339Nano, start)
	if err != nil {
		return 0
	}
	return time.Since(t).Milliseconds()
}
