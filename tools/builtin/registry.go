// Package builtin registers all built-in ACL tools into a protocol.Registry.
//
// Available tools:
//
// Network & APIs
//
//	http.get(url)                                  → {status, text, url}
//	http.request(url, method, body, headers, …)   → {status, text, json, url, method}
//	search.web(query, limit)                       → {hits:[{title,url,snippet}], count, provider}
//
// AI & LLM
//
//	llm.generate(prompt, data, format, provider)   → {text, model, tokens, provider}
//	vision.describe(url|path, prompt)              → {description, model, provider}
//	audio.transcribe(file, language)               → {text, language, duration_s, segments}
//
// Data
//
//	extract.table(text, columns)                   → {rows, count}
//	json.parse(text)                               → {data, ok}
//	json.stringify(data, indent)                   → {text}
//	csv.parse(text, has_header, delimiter)         → {rows, columns, count}
//	csv.write(rows, columns)                       → {text, count}
//	text.chunk(text, size, overlap)                → {chunks, count}
//
// Storage & DB
//
//	sql.query(query, db)                           → {rows, count}
//	file.read(path)                                → {text, path, size_bytes}
//	file.write(path, content)                      → {path, size_bytes, written}
//	file.append(path, content)                     → {path, written, total_bytes}
//	file.delete(path)                              → {path, deleted}
//	file.list(path)                                → {files, path, count}
//	memory.set(key, value, namespace, ttl)         → {key, namespace, stored_at}
//	memory.get(key, namespace)                     → {key, value, found, stored_at}
//	memory.delete(key, namespace)                  → {key, namespace, deleted}
//	memory.list(namespace, limit)                  → {entries, namespace, count}
//	vector.upsert(id, text, collection, metadata)  → {id, collection, model, dimensions}
//	vector.search(query, collection, limit)        → {results:[{id,text,score}], count}
//	vector.delete(id, collection)                  → {deleted, id, collection}
//
// Output
//
//	pdf.render(template, data, file)               → {file_id, path, page_count, size_bytes}
//	email.draft(to, subject, body, …)             → {message_id, to, subject, preview, sent_at}
//	code.run(language, code, timeout, env)         → {stdout, stderr, exit_code, duration_ms}
//
// Environment variables:
//
//	ACL_DB_URL           database connection (sqlite:./file.db or postgres://...)
//	ANTHROPIC_API_KEY    Anthropic (llm, vision)
//	OPENAI_API_KEY       OpenAI (llm, vision, embeddings)
//	GROQ_API_KEY         Groq (llm, vision, audio)
//	MISTRAL_API_KEY      Mistral (llm)
//	OLLAMA_HOST          local Ollama (llm, embeddings)
//	ACL_LLM_PROVIDER     force LLM provider
//	ACL_LLM_MODEL        override LLM model
//	BRAVE_API_KEY        Brave Search (search.web)
//	SERPER_API_KEY       Serper.dev / Google (search.web)
//	ACL_SMTP_HOST        SMTP server
//	ACL_SMTP_PORT        SMTP port (default 587)
//	ACL_SMTP_USER        SMTP username
//	ACL_SMTP_PASS        SMTP password
//	ACL_SMTP_FROM        From address
//	ACL_PDF_DIR          directory for PDF output (default: OS temp)
//	ACL_FILE_DIR         sandbox root for file.* tools
//	ACL_CODE_RUN_ENABLED set to "1" to enable code.run
package builtin

import (
	"context"
	"fmt"

	"github.com/ranausmanai/acl/internal/protocol"
)

// NewRegistry creates a protocol.Registry with all production built-in tools.
func NewRegistry() *protocol.Registry {
	reg := protocol.NewRegistry()

	// Network & APIs
	reg.RegisterBuiltin("http.get", httpGet, "2")
	reg.RegisterBuiltin("http.request", httpRequest, "1")
	reg.RegisterBuiltin("search.web", searchWeb, "1")

	// AI & LLM
	reg.RegisterBuiltin("llm.generate", llmGenerate, "2")
	reg.RegisterBuiltin("vision.describe", visionDescribe, "1")
	reg.RegisterBuiltin("audio.transcribe", audioTranscribe, "1")

	// Data
	reg.RegisterBuiltin("extract.table", extractTable, "1")
	reg.RegisterBuiltin("json.parse", jsonParse, "1")
	reg.RegisterBuiltin("json.stringify", jsonStringify, "1")
	reg.RegisterBuiltin("csv.parse", csvParse, "1")
	reg.RegisterBuiltin("csv.write", csvWrite, "1")
	reg.RegisterBuiltin("text.chunk", textChunk, "1")

	// Storage & DB
	reg.RegisterBuiltin("sql.query", sqlQuery, "2")
	reg.RegisterBuiltin("file.read", fileRead, "1")
	reg.RegisterBuiltin("file.write", fileWrite, "1")
	reg.RegisterBuiltin("file.append", fileAppend, "1")
	reg.RegisterBuiltin("file.delete", fileDelete, "1")
	reg.RegisterBuiltin("file.list", fileList, "1")
	reg.RegisterBuiltin("memory.set", memorySet, "1")
	reg.RegisterBuiltin("memory.get", memoryGet, "1")
	reg.RegisterBuiltin("memory.delete", memoryDelete, "1")
	reg.RegisterBuiltin("memory.list", memoryList, "1")
	reg.RegisterBuiltin("vector.upsert", vectorUpsert, "1")
	reg.RegisterBuiltin("vector.search", vectorSearch, "1")
	reg.RegisterBuiltin("vector.delete", vectorDelete, "1")

	// Output
	reg.RegisterBuiltin("pdf.render", pdfRender, "2")
	reg.RegisterBuiltin("email.draft", emailDraft, "2")
	reg.RegisterBuiltin("code.run", codeRun, "1")

	return reg
}

// NewMockRegistry creates a registry for unit tests — no network, no API keys needed.
// Pure computation tools (json, csv, text, file, memory) use their real implementations.
// Network-dependent tools use offline mocks.
func NewMockRegistry() *protocol.Registry {
	reg := protocol.NewRegistry()

	// Network & APIs — mocked
	reg.RegisterBuiltin("http.get", mockHTTPGet, "2")
	reg.RegisterBuiltin("http.request", mockHTTPRequest, "1")
	reg.RegisterBuiltin("search.web", mockSearchWeb, "1")

	// AI & LLM — mocked
	reg.RegisterBuiltin("llm.generate", mockLLMGenerate, "2")
	reg.RegisterBuiltin("vision.describe", mockVisionDescribe, "1")
	reg.RegisterBuiltin("audio.transcribe", mockAudioTranscribe, "1")

	// Data — real implementations (pure computation, no I/O)
	reg.RegisterBuiltin("extract.table", extractTable, "1")
	reg.RegisterBuiltin("json.parse", jsonParse, "1")
	reg.RegisterBuiltin("json.stringify", jsonStringify, "1")
	reg.RegisterBuiltin("csv.parse", csvParse, "1")
	reg.RegisterBuiltin("csv.write", csvWrite, "1")
	reg.RegisterBuiltin("text.chunk", textChunk, "1")

	// Storage & DB — real implementations (local I/O, no external services)
	reg.RegisterBuiltin("sql.query", mockSQLQuery, "2")
	reg.RegisterBuiltin("file.read", fileRead, "1")
	reg.RegisterBuiltin("file.write", fileWrite, "1")
	reg.RegisterBuiltin("file.append", fileAppend, "1")
	reg.RegisterBuiltin("file.delete", fileDelete, "1")
	reg.RegisterBuiltin("file.list", fileList, "1")
	reg.RegisterBuiltin("memory.set", memorySet, "1")
	reg.RegisterBuiltin("memory.get", memoryGet, "1")
	reg.RegisterBuiltin("memory.delete", memoryDelete, "1")
	reg.RegisterBuiltin("memory.list", memoryList, "1")
	reg.RegisterBuiltin("vector.upsert", mockVectorUpsert, "1")
	reg.RegisterBuiltin("vector.search", mockVectorSearch, "1")
	reg.RegisterBuiltin("vector.delete", vectorDelete, "1")

	// Output
	reg.RegisterBuiltin("pdf.render", pdfRender, "2")
	reg.RegisterBuiltin("email.draft", mockEmailDraft, "2")
	reg.RegisterBuiltin("code.run", codeRun, "1") // real — uses subprocess, safe for tests

	return reg
}

// ── Mock implementations for test registry ────────────────────────────────────

func mockHTTPRequest(_ context.Context, args map[string]any) (any, error) {
	url, _ := args["url"].(string)
	method := "GET"
	if m, ok := args["method"].(string); ok && m != "" {
		method = m
	}
	return map[string]any{
		"status": 200,
		"text":   fmt.Sprintf(`{"mock":true,"url":%q,"method":%q}`, url, method),
		"json":   map[string]any{"mock": true, "url": url, "method": method},
		"url":    url,
		"method": method,
	}, nil
}

func mockSearchWeb(_ context.Context, args map[string]any) (any, error) {
	query, _ := args["query"].(string)
	return map[string]any{
		"hits": []any{
			map[string]any{"title": "Mock Result 1", "url": "https://example.com/1", "snippet": "First mock search result for: " + query},
			map[string]any{"title": "Mock Result 2", "url": "https://example.com/2", "snippet": "Second mock search result for: " + query},
		},
		"count":    2,
		"query":    query,
		"provider": "mock",
	}, nil
}

func mockVisionDescribe(_ context.Context, args map[string]any) (any, error) {
	src := args["url"]
	if src == nil {
		src = args["path"]
	}
	return map[string]any{
		"description": fmt.Sprintf("Mock image description for: %v", src),
		"model":       "mock-vision-1",
		"provider":    "mock",
	}, nil
}

func mockAudioTranscribe(_ context.Context, args map[string]any) (any, error) {
	file, _ := args["file"].(string)
	return map[string]any{
		"text":       fmt.Sprintf("Mock transcription of %s", file),
		"language":   "en",
		"duration_s": 10.0,
		"segments":   []any{},
		"model":      "mock-whisper",
		"provider":   "mock",
	}, nil
}

// mockVectorUpsert — in-memory vector store for tests (no embeddings API needed).
var mockVectors = map[string]map[string]any{}

func mockVectorUpsert(_ context.Context, args map[string]any) (any, error) {
	id, _ := args["id"].(string)
	text, _ := args["text"].(string)
	collection := "default"
	if c, ok := args["collection"].(string); ok && c != "" {
		collection = c
	}
	key := collection + "/" + id
	mockVectors[key] = map[string]any{"id": id, "text": text, "collection": collection, "metadata": args["metadata"]}
	return map[string]any{"id": id, "collection": collection, "model": "mock", "dimensions": 8}, nil
}

func mockVectorSearch(_ context.Context, args map[string]any) (any, error) {
	query, _ := args["query"].(string)
	collection := "default"
	if c, ok := args["collection"].(string); ok && c != "" {
		collection = c
	}
	var hits []any
	for key, v := range mockVectors {
		if len(hits) >= 5 {
			break
		}
		if vc, _ := v["collection"].(string); vc == collection {
			_ = key
			hits = append(hits, map[string]any{
				"id":    v["id"],
				"text":  v["text"],
				"score": 0.9,
			})
		}
	}
	if hits == nil {
		hits = []any{}
	}
	return map[string]any{
		"results":    hits,
		"count":      len(hits),
		"query":      query,
		"collection": collection,
		"model":      "mock",
	}, nil
}
