package builtin

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"

	// SQLite driver — pure Go, no CGO required.
	_ "modernc.org/sqlite"

	// PostgreSQL driver.
	_ "github.com/lib/pq"
)

// sqlQuery executes a SQL query against a real database.
//
// Connection is resolved in this order:
//  1. "db" arg in the tool call
//  2. ACL_DB_URL environment variable
//
// URL formats:
//
//	sqlite:./path/to/file.db          — SQLite file
//	sqlite::memory:                   — in-memory SQLite (testing)
//	postgres://user:pass@host/dbname  — PostgreSQL
//	postgresql://user:pass@host/dbname
//
// Returns {"rows": [...], "count": int}
func sqlQuery(ctx context.Context, args map[string]any) (any, error) {
	query, _ := args["query"].(string)
	if query == "" {
		return nil, fmt.Errorf("sql.query: query argument required")
	}

	dsn := ""
	if d, _ := args["db"].(string); d != "" {
		dsn = d
	} else if d := os.Getenv("ACL_DB_URL"); d != "" {
		dsn = d
	}
	if dsn == "" {
		return nil, fmt.Errorf(
			"sql.query: no database configured — set ACL_DB_URL or pass db= argument.\n" +
				"  Examples:\n" +
				"    ACL_DB_URL=sqlite:./mydata.db\n" +
				"    ACL_DB_URL=postgres://user:pass@localhost/mydb\n" +
				"    db=\"sqlite::memory:\"   (in-memory, for testing)")
	}

	driver, connStr, err := parseDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("sql.query: %w", err)
	}

	db, err := sql.Open(driver, connStr)
	if err != nil {
		return nil, fmt.Errorf("sql.query: open(%s): %w", driver, err)
	}
	defer db.Close()

	db.SetMaxOpenConns(5)

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("sql.query: execute: %w", err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("sql.query: columns: %w", err)
	}

	var result []any
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, fmt.Errorf("sql.query: scan: %w", err)
		}
		row := make(map[string]any, len(cols))
		for i, col := range cols {
			v := vals[i]
			// Convert []byte to string for readability.
			if b, ok := v.([]byte); ok {
				v = string(b)
			}
			row[col] = v
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sql.query: rows: %w", err)
	}
	if result == nil {
		result = []any{}
	}

	return map[string]any{
		"rows":  result,
		"count": int64(len(result)),
	}, nil
}

// parseDSN splits a dsn string into (driverName, driverConnStr).
func parseDSN(dsn string) (string, string, error) {
	switch {
	case strings.HasPrefix(dsn, "sqlite:"):
		// Strip the "sqlite:" prefix; the rest is the SQLite file path or ":memory:".
		path := strings.TrimPrefix(dsn, "sqlite:")
		return "sqlite", path, nil
	case strings.HasPrefix(dsn, "postgres://"), strings.HasPrefix(dsn, "postgresql://"):
		return "postgres", dsn, nil
	default:
		return "", "", fmt.Errorf("unsupported DB URL scheme %q (supported: sqlite:, postgres://)", dsn)
	}
}
