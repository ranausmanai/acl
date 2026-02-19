package builtin

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// Vector store — embeddings in SQLite, cosine similarity in pure Go.
// Embedding providers: OpenAI text-embedding-3-small, Ollama nomic-embed-text.
// DB location: ~/.acl/vectors.db

var (
	vecDB    *sql.DB
	vecMu    sync.Mutex
	vecReady bool
)

func openVectorDB() (*sql.DB, error) {
	vecMu.Lock()
	defer vecMu.Unlock()
	if vecReady {
		return vecDB, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(home, ".acl")
	os.MkdirAll(dir, 0o750) //nolint
	path := filepath.Join(dir, "vectors.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("vector: open db: %w", err)
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS vectors (
			id          TEXT NOT NULL,
			collection  TEXT NOT NULL DEFAULT 'default',
			text        TEXT NOT NULL,
			embedding   TEXT NOT NULL,
			metadata    TEXT,
			stored_at   TEXT NOT NULL,
			PRIMARY KEY (id, collection)
		);
		CREATE INDEX IF NOT EXISTS vectors_coll ON vectors(collection);
	`)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("vector: schema: %w", err)
	}
	vecDB = db
	vecReady = true
	return vecDB, nil
}

// ── Embedding providers ────────────────────────────────────────────────────────

func generateEmbedding(ctx context.Context, text string) ([]float64, string, error) {
	if key := os.Getenv("OPENAI_API_KEY"); key != "" {
		emb, err := openAIEmbedding(ctx, text, key)
		if err == nil {
			return emb, "openai/text-embedding-3-small", nil
		}
	}
	ollamaHost := os.Getenv("OLLAMA_HOST")
	if ollamaHost == "" {
		ollamaHost = "http://localhost:11434"
	}
	emb, err := ollamaEmbedding(ctx, text, ollamaHost)
	if err == nil {
		return emb, "ollama/nomic-embed-text", nil
	}
	return nil, "", fmt.Errorf("vector: no embedding provider — set OPENAI_API_KEY or OLLAMA_HOST")
}

func openAIEmbedding(ctx context.Context, text, apiKey string) ([]float64, error) {
	payload, _ := json.Marshal(map[string]any{
		"model": "text-embedding-3-small",
		"input": text,
	})
	req, err := http.NewRequestWithContext(ctx, "POST",
		"https://api.openai.com/v1/embeddings", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	if len(result.Data) == 0 {
		return nil, fmt.Errorf("openai: no embedding returned")
	}
	return result.Data[0].Embedding, nil
}

func ollamaEmbedding(ctx context.Context, text, host string) ([]float64, error) {
	payload, _ := json.Marshal(map[string]any{
		"model":  "nomic-embed-text",
		"prompt": text,
	})
	req, err := http.NewRequestWithContext(ctx, "POST", host+"/api/embeddings", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var result struct {
		Embedding []float64 `json:"embedding"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	if len(result.Embedding) == 0 {
		return nil, fmt.Errorf("ollama: no embedding returned")
	}
	return result.Embedding, nil
}

// ── Math ──────────────────────────────────────────────────────────────────────

func cosineSimilarity(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

// ── Tools ─────────────────────────────────────────────────────────────────────

// vector.upsert — embed text and store it in the vector DB.
//
// Args:
//
//	id          string  unique identifier (required)
//	text        string  content to embed (required)
//	collection  string  (default "default")
//	metadata    any     arbitrary JSON metadata
//
// Returns: {id, collection, model, dimensions, stored_at}
func vectorUpsert(ctx context.Context, args map[string]any) (any, error) {
	id, _ := args["id"].(string)
	if id == "" {
		return nil, fmt.Errorf("vector.upsert: id is required")
	}
	text, _ := args["text"].(string)
	if text == "" {
		return nil, fmt.Errorf("vector.upsert: text is required")
	}
	collection := "default"
	if c, ok := args["collection"].(string); ok && c != "" {
		collection = c
	}

	metaJSON, _ := json.Marshal(args["metadata"])

	emb, model, err := generateEmbedding(ctx, text)
	if err != nil {
		return nil, err
	}
	embJSON, _ := json.Marshal(emb)

	db, err := openVectorDB()
	if err != nil {
		return nil, err
	}

	vecMu.Lock()
	defer vecMu.Unlock()

	now := time.Now().UTC().Format(time.RFC3339)
	_, err = db.Exec(
		`INSERT INTO vectors(id, collection, text, embedding, metadata, stored_at) VALUES(?,?,?,?,?,?)
		 ON CONFLICT(id, collection) DO UPDATE SET
		   text=excluded.text, embedding=excluded.embedding,
		   metadata=excluded.metadata, stored_at=excluded.stored_at`,
		id, collection, text, string(embJSON), string(metaJSON), now,
	)
	if err != nil {
		return nil, fmt.Errorf("vector.upsert: %w", err)
	}
	return map[string]any{
		"id":         id,
		"collection": collection,
		"model":      model,
		"dimensions": len(emb),
		"stored_at":  now,
	}, nil
}

// vector.search — find the most similar documents to a query.
//
// Args:
//
//	query       string  (required)
//	collection  string  (default "default")
//	limit       int     (default 5)
//	threshold   float   min similarity score 0-1 (default 0.0)
//
// Returns: {results: [{id, text, score, metadata}], count, query, collection, model}
func vectorSearch(ctx context.Context, args map[string]any) (any, error) {
	query, _ := args["query"].(string)
	if query == "" {
		return nil, fmt.Errorf("vector.search: query is required")
	}
	collection := "default"
	if c, ok := args["collection"].(string); ok && c != "" {
		collection = c
	}
	limit := 5
	if l, ok := args["limit"].(float64); ok && l > 0 {
		limit = int(l)
	}
	threshold := 0.0
	if t, ok := args["threshold"].(float64); ok {
		threshold = t
	}

	queryEmb, model, err := generateEmbedding(ctx, query)
	if err != nil {
		return nil, err
	}

	db, err := openVectorDB()
	if err != nil {
		return nil, err
	}

	vecMu.Lock()
	rows, err := db.Query(
		`SELECT id, text, embedding, metadata FROM vectors WHERE collection=?`, collection)
	vecMu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("vector.search: %w", err)
	}
	defer rows.Close()

	type scored struct {
		id       string
		text     string
		metadata any
		score    float64
	}
	var candidates []scored

	for rows.Next() {
		var id, text, embJSON, metaJSON string
		if err := rows.Scan(&id, &text, &embJSON, &metaJSON); err != nil {
			continue
		}
		var emb []float64
		if err := json.Unmarshal([]byte(embJSON), &emb); err != nil {
			continue
		}
		score := cosineSimilarity(queryEmb, emb)
		if score >= threshold {
			var meta any
			json.Unmarshal([]byte(metaJSON), &meta) //nolint
			candidates = append(candidates, scored{id, text, meta, score})
		}
	}

	sort.Slice(candidates, func(i, j int) bool { return candidates[i].score > candidates[j].score })
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}

	var hits []any
	for _, c := range candidates {
		hits = append(hits, map[string]any{
			"id":       c.id,
			"text":     c.text,
			"score":    c.score,
			"metadata": c.metadata,
		})
	}
	if hits == nil {
		hits = []any{}
	}
	return map[string]any{
		"results":    hits,
		"count":      len(hits),
		"query":      query,
		"collection": collection,
		"model":      model,
	}, nil
}

// vector.delete — remove one document or an entire collection.
//
// Args:
//
//	id          string  if empty, deletes the whole collection
//	collection  string  (default "default")
//
// Returns: {deleted: int, id, collection}
func vectorDelete(_ context.Context, args map[string]any) (any, error) {
	id, _ := args["id"].(string)
	collection := "default"
	if c, ok := args["collection"].(string); ok && c != "" {
		collection = c
	}

	db, err := openVectorDB()
	if err != nil {
		return nil, err
	}

	vecMu.Lock()
	defer vecMu.Unlock()

	var res sql.Result
	if id == "" {
		res, err = db.Exec(`DELETE FROM vectors WHERE collection=?`, collection)
	} else {
		res, err = db.Exec(`DELETE FROM vectors WHERE id=? AND collection=?`, id, collection)
	}
	if err != nil {
		return nil, fmt.Errorf("vector.delete: %w", err)
	}
	n, _ := res.RowsAffected()
	return map[string]any{"deleted": n, "id": id, "collection": collection}, nil
}
