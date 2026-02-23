package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// memory.messages — persistent conversation thread storage.
//
// Args:
//
//	session  string  (required) — unique thread identifier
//	op       string  — "get" (default) | "append" | "clear"
//	role     string  — required for append: "user" | "assistant" | "system"
//	content  string  — required for append
//	limit    float   — max messages to return (default 50)
//
// Returns (get):    {session, messages:[{role,content,created_at}], count}
// Returns (append): {session, role, appended, count}
// Returns (clear):  {session, cleared}
func memoryMessages(_ context.Context, args map[string]any) (any, error) {
	session, _ := args["session"].(string)
	if session == "" {
		return nil, fmt.Errorf("memory.messages: session is required")
	}

	op := "get"
	if o, ok := args["op"].(string); ok && o != "" {
		op = o
	}

	db, err := openMemDB()
	if err != nil {
		return nil, fmt.Errorf("memory.messages: %w", err)
	}

	// Ensure message_threads table exists.
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS message_threads (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		session    TEXT    NOT NULL,
		role       TEXT    NOT NULL,
		content    TEXT    NOT NULL,
		created_at TEXT    NOT NULL
	);
	CREATE INDEX IF NOT EXISTS mt_session ON message_threads(session, id);`)
	if err != nil {
		return nil, fmt.Errorf("memory.messages: migrate: %w", err)
	}

	switch op {
	case "get":
		limit := 50
		if l, ok := args["limit"].(float64); ok && l > 0 {
			limit = int(l)
		}
		rows, err := db.Query(
			`SELECT role, content, created_at FROM message_threads
			 WHERE session=? ORDER BY id DESC LIMIT ?`, session, limit)
		if err != nil {
			return nil, fmt.Errorf("memory.messages get: %w", err)
		}
		defer rows.Close()

		var msgs []any
		for rows.Next() {
			var role, content, createdAt string
			if err := rows.Scan(&role, &content, &createdAt); err == nil {
				msgs = append([]any{map[string]any{
					"role": role, "content": content, "created_at": createdAt,
				}}, msgs...) // reverse to chronological order
			}
		}
		if msgs == nil {
			msgs = []any{}
		}
		return map[string]any{"session": session, "messages": msgs, "count": len(msgs)}, nil

	case "append":
		role, _ := args["role"].(string)
		content, _ := args["content"].(string)
		if role == "" {
			return nil, fmt.Errorf("memory.messages append: role required (user|assistant|system)")
		}
		if content == "" {
			return nil, fmt.Errorf("memory.messages append: content required")
		}
		now := time.Now().UTC().Format(time.RFC3339)
		_, err := db.Exec(
			`INSERT INTO message_threads(session, role, content, created_at) VALUES(?,?,?,?)`,
			session, role, content, now)
		if err != nil {
			return nil, fmt.Errorf("memory.messages append: %w", err)
		}
		var count int
		db.QueryRow(`SELECT COUNT(*) FROM message_threads WHERE session=?`, session).Scan(&count) //nolint
		return map[string]any{"session": session, "role": role, "appended": true, "count": count}, nil

	case "clear":
		res, err := db.Exec(`DELETE FROM message_threads WHERE session=?`, session)
		if err != nil {
			return nil, fmt.Errorf("memory.messages clear: %w", err)
		}
		n, _ := res.RowsAffected()
		return map[string]any{"session": session, "cleared": true, "deleted": n}, nil

	default:
		return nil, fmt.Errorf("memory.messages: unknown op %q (want: get|append|clear)", op)
	}
}

// memoryMessagesToString formats a messages slice as a plain-text conversation
// suitable for passing to llm.generate as context.
func memoryMessagesToString(messages []any) string {
	var out string
	for _, m := range messages {
		msg, ok := m.(map[string]any)
		if !ok {
			continue
		}
		role, _ := msg["role"].(string)
		content, _ := msg["content"].(string)
		out += role + ": " + content + "\n"
	}
	return out
}

// ensure the helper is used (suppress unused warning)
var _ = json.Marshal
var _ = memoryMessagesToString
