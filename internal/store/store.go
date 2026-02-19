// Package store persists ACL run receipts to a local SQLite database.
//
// Default location: ~/.acl/history.db (created automatically).
// Disable by setting ACL_NO_HISTORY=1.
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"acl/internal/receipt"
	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS runs (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    intent       TEXT    NOT NULL DEFAULT '',
    status       TEXT    NOT NULL DEFAULT '',
    timestamp    TEXT    NOT NULL,
    duration_ms  INTEGER NOT NULL DEFAULT 0,
    receipt_json TEXT    NOT NULL
);
CREATE INDEX IF NOT EXISTS runs_timestamp ON runs(timestamp DESC);
`

// Store is an append-only receipt journal.
type Store struct {
	db *sql.DB
}

// RunSummary is a lightweight row for listing history.
type RunSummary struct {
	ID         int64  `json:"id"`
	Intent     string `json:"intent"`
	Status     string `json:"status"`
	Timestamp  string `json:"timestamp"`
	DurationMS int64  `json:"duration_ms"`
}

// DefaultPath returns ~/.acl/history.db, creating the directory if needed.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".acl")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	return filepath.Join(dir, "history.db"), nil
}

// Open opens (or creates) the store at path.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	db.SetMaxOpenConns(1) // SQLite is single-writer
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: schema: %w", err)
	}
	return &Store{db: db}, nil
}

// Close closes the underlying database.
func (s *Store) Close() error { return s.db.Close() }

// Put saves a receipt. Returns the auto-assigned run ID.
func (s *Store) Put(r *receipt.Receipt) (int64, error) {
	data, err := json.Marshal(r)
	if err != nil {
		return 0, fmt.Errorf("store: marshal: %w", err)
	}

	// Compute total duration across all agent steps.
	var durMS int64
	for _, a := range r.Agents {
		for _, st := range a.Steps {
			durMS += st.DurationMS
		}
	}

	ts := r.Timestamp
	if ts == "" {
		ts = time.Now().UTC().Format(time.RFC3339)
	}

	res, err := s.db.Exec(
		`INSERT INTO runs(intent, status, timestamp, duration_ms, receipt_json) VALUES(?,?,?,?,?)`,
		r.Intent, r.Status, ts, durMS, string(data),
	)
	if err != nil {
		return 0, fmt.Errorf("store: insert: %w", err)
	}
	return res.LastInsertId()
}

// List returns the n most recent run summaries (newest first).
func (s *Store) List(n int) ([]RunSummary, error) {
	if n <= 0 {
		n = 20
	}
	rows, err := s.db.Query(
		`SELECT id, intent, status, timestamp, duration_ms FROM runs ORDER BY id DESC LIMIT ?`, n)
	if err != nil {
		return nil, fmt.Errorf("store: list: %w", err)
	}
	defer rows.Close()

	var out []RunSummary
	for rows.Next() {
		var r RunSummary
		if err := rows.Scan(&r.ID, &r.Intent, &r.Status, &r.Timestamp, &r.DurationMS); err != nil {
			return nil, fmt.Errorf("store: scan: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Get returns the full receipt for a specific run ID.
func (s *Store) Get(id int64) (*receipt.Receipt, error) {
	var raw string
	err := s.db.QueryRow(`SELECT receipt_json FROM runs WHERE id = ?`, id).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("store: run #%d not found", id)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get: %w", err)
	}
	var r receipt.Receipt
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		return nil, fmt.Errorf("store: unmarshal: %w", err)
	}
	return &r, nil
}

// Count returns the total number of runs stored.
func (s *Store) Count() (int64, error) {
	var n int64
	err := s.db.QueryRow(`SELECT COUNT(*) FROM runs`).Scan(&n)
	return n, err
}

// Purge deletes all runs older than n days.
func (s *Store) Purge(days int) (int64, error) {
	cutoff := time.Now().UTC().AddDate(0, 0, -days).Format(time.RFC3339)
	res, err := s.db.Exec(`DELETE FROM runs WHERE timestamp < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
