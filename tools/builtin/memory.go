package builtin

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// Persistent K/V memory store at ~/.acl/memory.db.
// Supports namespaces and optional TTL.

var (
	memDB    *sql.DB
	memMu    sync.Mutex
	memReady bool
)

func openMemDB() (*sql.DB, error) {
	memMu.Lock()
	defer memMu.Unlock()
	if memReady {
		return memDB, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(home, ".acl")
	os.MkdirAll(dir, 0o750) //nolint
	path := filepath.Join(dir, "memory.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("memory: open db: %w", err)
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS memory (
			key        TEXT NOT NULL,
			namespace  TEXT NOT NULL DEFAULT 'default',
			value_json TEXT NOT NULL,
			stored_at  TEXT NOT NULL,
			expires_at TEXT,
			PRIMARY KEY (key, namespace)
		);
		CREATE INDEX IF NOT EXISTS memory_ns ON memory(namespace);
	`)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("memory: schema: %w", err)
	}
	memDB = db
	memReady = true
	return memDB, nil
}

// memory.set — store a value under a key (creates or overwrites).
//
// Args:
//
//	key        string  (required)
//	value      any     (required)
//	namespace  string  (default "default")
//	ttl        float   seconds until expiry (0 = never)
//
// Returns: {key, namespace, stored_at}
func memorySet(_ context.Context, args map[string]any) (any, error) {
	key, _ := args["key"].(string)
	if key == "" {
		return nil, fmt.Errorf("memory.set: key is required")
	}
	ns := "default"
	if n, ok := args["namespace"].(string); ok && n != "" {
		ns = n
	}

	data, err := json.Marshal(args["value"])
	if err != nil {
		return nil, fmt.Errorf("memory.set: marshal: %w", err)
	}

	var expiresAt *string
	if ttl, ok := args["ttl"].(float64); ok && ttl > 0 {
		t := time.Now().UTC().Add(time.Duration(ttl) * time.Second).Format(time.RFC3339)
		expiresAt = &t
	}

	db, err := openMemDB()
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC().Format(time.RFC3339)
	_, err = db.Exec(
		`INSERT INTO memory(key, namespace, value_json, stored_at, expires_at) VALUES(?,?,?,?,?)
		 ON CONFLICT(key, namespace) DO UPDATE SET
		   value_json=excluded.value_json,
		   stored_at=excluded.stored_at,
		   expires_at=excluded.expires_at`,
		key, ns, string(data), now, expiresAt,
	)
	if err != nil {
		return nil, fmt.Errorf("memory.set: %w", err)
	}
	return map[string]any{"key": key, "namespace": ns, "stored_at": now}, nil
}

// memory.get — retrieve a value by key.
//
// Args: key string (required), namespace string (default "default")
// Returns: {key, namespace, value, found, stored_at}
// If not found or expired: {key, namespace, found: false, value: null}
func memoryGet(_ context.Context, args map[string]any) (any, error) {
	key, _ := args["key"].(string)
	if key == "" {
		return nil, fmt.Errorf("memory.get: key is required")
	}
	ns := "default"
	if n, ok := args["namespace"].(string); ok && n != "" {
		ns = n
	}

	db, err := openMemDB()
	if err != nil {
		return nil, err
	}

	var valueJSON, storedAt string
	var expiresAt *string
	err = db.QueryRow(
		`SELECT value_json, stored_at, expires_at FROM memory WHERE key=? AND namespace=?`,
		key, ns,
	).Scan(&valueJSON, &storedAt, &expiresAt)

	if err == sql.ErrNoRows {
		return map[string]any{"key": key, "namespace": ns, "found": false, "value": nil}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("memory.get: %w", err)
	}

	// Check TTL.
	if expiresAt != nil {
		exp, _ := time.Parse(time.RFC3339, *expiresAt)
		if time.Now().UTC().After(exp) {
			db.Exec(`DELETE FROM memory WHERE key=? AND namespace=?`, key, ns) //nolint
			return map[string]any{"key": key, "namespace": ns, "found": false, "value": nil, "expired": true}, nil
		}
	}

	var value any
	json.Unmarshal([]byte(valueJSON), &value) //nolint
	return map[string]any{
		"key":       key,
		"namespace": ns,
		"value":     value,
		"found":     true,
		"stored_at": storedAt,
	}, nil
}

// memory.delete — delete a key.
//
// Args: key string (required), namespace string (default "default")
// Returns: {key, namespace, deleted: bool}
func memoryDelete(_ context.Context, args map[string]any) (any, error) {
	key, _ := args["key"].(string)
	if key == "" {
		return nil, fmt.Errorf("memory.delete: key is required")
	}
	ns := "default"
	if n, ok := args["namespace"].(string); ok && n != "" {
		ns = n
	}

	db, err := openMemDB()
	if err != nil {
		return nil, err
	}

	res, err := db.Exec(`DELETE FROM memory WHERE key=? AND namespace=?`, key, ns)
	if err != nil {
		return nil, fmt.Errorf("memory.delete: %w", err)
	}
	n, _ := res.RowsAffected()
	return map[string]any{"key": key, "namespace": ns, "deleted": n > 0}, nil
}

// memory.list — list all keys in a namespace.
//
// Args: namespace string (default "default"), limit int (default 50)
// Returns: {entries: [{key, value, stored_at}], namespace, count}
func memoryList(_ context.Context, args map[string]any) (any, error) {
	ns := "default"
	if n, ok := args["namespace"].(string); ok && n != "" {
		ns = n
	}
	limit := 50
	if l, ok := args["limit"].(float64); ok && l > 0 {
		limit = int(l)
	}

	db, err := openMemDB()
	if err != nil {
		return nil, err
	}

	rows, err := db.Query(
		`SELECT key, value_json, stored_at FROM memory WHERE namespace=?
		 AND (expires_at IS NULL OR expires_at > ?)
		 ORDER BY stored_at DESC LIMIT ?`,
		ns, time.Now().UTC().Format(time.RFC3339), limit,
	)
	if err != nil {
		return nil, fmt.Errorf("memory.list: %w", err)
	}
	defer rows.Close()

	var entries []any
	for rows.Next() {
		var key, valueJSON, storedAt string
		if err := rows.Scan(&key, &valueJSON, &storedAt); err != nil {
			continue
		}
		var value any
		json.Unmarshal([]byte(valueJSON), &value) //nolint
		entries = append(entries, map[string]any{"key": key, "value": value, "stored_at": storedAt})
	}
	if entries == nil {
		entries = []any{}
	}
	return map[string]any{"entries": entries, "namespace": ns, "count": len(entries)}, nil
}
