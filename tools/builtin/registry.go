// Package builtin registers all built-in ACL tools into a protocol.Registry.
//
// Built-in tools are pure Go functions; they bypass the subprocess protocol
// and are called directly by the runtime.
//
// Available tools:
//
//	http.get(url)                         → {status, text, url}
//	llm.generate(prompt, data, format)    → {text, model, tokens, provider}
//	extract.table(text, columns)          → {rows, count}
//	sql.query(query, db)                  → {rows, count}
//	pdf.render(template, data, file)      → {file_id, path, page_count, size_bytes}
//	email.draft(to, subject, body, …)     → {message_id, to, subject, preview, sent_at}
//
// Environment variables (production):
//
//	ACL_DB_URL         — database connection (sqlite:./file.db or postgres://...)
//	ANTHROPIC_API_KEY  — enables Anthropic provider for llm.generate
//	OPENAI_API_KEY     — enables OpenAI provider
//	GROQ_API_KEY       — enables Groq provider
//	MISTRAL_API_KEY    — enables Mistral provider
//	OLLAMA_HOST        — enables local Ollama (default http://localhost:11434)
//	ACL_LLM_PROVIDER   — force a specific provider
//	ACL_LLM_MODEL      — override model name
//	ACL_SMTP_HOST      — SMTP server for email.draft
//	ACL_SMTP_PORT      — SMTP port (default 587)
//	ACL_SMTP_USER      — SMTP username
//	ACL_SMTP_PASS      — SMTP password / app-password
//	ACL_SMTP_FROM      — From address
//	ACL_PDF_DIR        — directory for generated PDF files (default: OS temp)
package builtin

import "acl/internal/protocol"

// Register adds all production built-in tools to reg.
func Register(reg *protocol.Registry) {
	reg.RegisterBuiltin("http.get", httpGet, "2")
	reg.RegisterBuiltin("llm.generate", llmGenerate, "2")
	reg.RegisterBuiltin("extract.table", extractTable, "1")
	reg.RegisterBuiltin("sql.query", sqlQuery, "2")
	reg.RegisterBuiltin("pdf.render", pdfRender, "2")
	reg.RegisterBuiltin("email.draft", emailDraft, "2")
}

// NewRegistry creates a protocol.Registry pre-loaded with all production tools.
// Each tool calls real external services; configure via environment variables.
func NewRegistry() *protocol.Registry {
	reg := protocol.NewRegistry()
	Register(reg)
	return reg
}

// NewMockRegistry creates a registry suitable for unit tests.
// It replaces network-dependent tools with offline equivalents:
//   - http.get    → URL pattern matching, no network
//   - llm.generate→ deterministic hash-based text, no API key needed
//   - sql.query   → in-memory demo tables (sales, incidents, pricing, agents_registry)
//   - pdf.render  → writes a real PDF to a temp file (no external service)
//   - email.draft → validates args and returns a fake message_id (no SMTP)
func NewMockRegistry() *protocol.Registry {
	reg := protocol.NewRegistry()
	reg.RegisterBuiltin("http.get", mockHTTPGet, "2")
	reg.RegisterBuiltin("llm.generate", mockLLMGenerate, "2")
	reg.RegisterBuiltin("extract.table", extractTable, "1")
	reg.RegisterBuiltin("sql.query", mockSQLQuery, "2")
	reg.RegisterBuiltin("pdf.render", pdfRender, "2")       // real PDF, no external service
	reg.RegisterBuiltin("email.draft", mockEmailDraft, "2") // validates args, no SMTP
	return reg
}
