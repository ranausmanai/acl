package builtin

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// mockSQLQuery is the test-time replacement for sql.query.
// It runs queries against hard-coded in-memory demo tables so tests never
// need a real database or ACL_DB_URL set.
//
// Available demo tables: sales, incidents, pricing, agents_registry.
func mockSQLQuery(_ context.Context, args map[string]any) (any, error) {
	query, _ := args["query"].(string)
	if query == "" {
		return nil, fmt.Errorf("sql.query: query argument required")
	}
	rows, err := runMockQuery(query)
	if err != nil {
		return nil, fmt.Errorf("sql.query: %w", err)
	}
	return map[string]any{
		"rows":  rows,
		"count": int64(len(rows)),
	}, nil
}

// ── Demo tables ───────────────────────────────────────────────────────────────

var demoTables = map[string][]map[string]any{
	"sales": {
		{"product": "Starter Plan", "region": "AMER", "amount": 29000.0, "quarter": "Q1"},
		{"product": "Pro Plan", "region": "EMEA", "amount": 99000.0, "quarter": "Q1"},
		{"product": "Enterprise Plan", "region": "APAC", "amount": 499000.0, "quarter": "Q1"},
		{"product": "Starter Plan", "region": "AMER", "amount": 31000.0, "quarter": "Q2"},
		{"product": "Pro Plan", "region": "AMER", "amount": 110000.0, "quarter": "Q2"},
		{"product": "Enterprise Plan", "region": "EMEA", "amount": 520000.0, "quarter": "Q2"},
	},
	"incidents": {
		{"id": int64(1), "severity": "critical", "status": "open", "description": "API latency spike", "created_at": "2026-02-19T08:00:00Z"},
		{"id": int64(2), "severity": "high", "status": "investigating", "description": "Auth service degraded", "created_at": "2026-02-19T09:15:00Z"},
		{"id": int64(3), "severity": "low", "status": "resolved", "description": "Cache miss rate high", "created_at": "2026-02-18T14:30:00Z"},
		{"id": int64(4), "severity": "critical", "status": "open", "description": "Database failover triggered", "created_at": "2026-02-19T10:45:00Z"},
	},
	"pricing": {
		{"plan": "starter", "price_usd": 29.0, "features": "5 users, 10GB storage, email support"},
		{"plan": "pro", "price_usd": 99.0, "features": "50 users, 100GB storage, priority support"},
		{"plan": "enterprise", "price_usd": 499.0, "features": "unlimited users, 1TB storage, SLA 99.99%"},
	},
	"agents_registry": {
		{"name": "PricingAgent", "status": "active", "last_run": "2026-02-19T06:00:00Z"},
		{"name": "ReportAgent", "status": "active", "last_run": "2026-02-18T23:00:00Z"},
		{"name": "IncidentAgent", "status": "idle", "last_run": "2026-02-17T12:00:00Z"},
		{"name": "OutreachAgent", "status": "active", "last_run": "2026-02-19T05:00:00Z"},
	},
}

var (
	sqlFromRe  = regexp.MustCompile(`(?i)\bFROM\s+(\w+)`)
	sqlWhereRe = regexp.MustCompile(`(?i)\bWHERE\s+(.+?)(?:\s*(?:ORDER|LIMIT|GROUP|$))`)
	sqlLimitRe = regexp.MustCompile(`(?i)\bLIMIT\s+(\d+)`)
)

func runMockQuery(query string) ([]any, error) {
	m := sqlFromRe.FindStringSubmatch(query)
	if m == nil {
		return []any{}, nil
	}
	tableName := strings.ToLower(m[1])
	table, ok := demoTables[tableName]
	if !ok {
		return nil, fmt.Errorf("unknown table %q (available: sales, incidents, pricing, agents_registry)", tableName)
	}

	rows := make([]map[string]any, len(table))
	for i, r := range table {
		cp := make(map[string]any, len(r))
		for k, v := range r {
			cp[k] = v
		}
		rows[i] = cp
	}

	if wm := sqlWhereRe.FindStringSubmatch(query); wm != nil {
		rows = mockApplyWhere(rows, wm[1])
	}

	if lm := sqlLimitRe.FindStringSubmatch(query); lm != nil {
		n := 0
		fmt.Sscanf(lm[1], "%d", &n)
		if n > 0 && n < len(rows) {
			rows = rows[:n]
		}
	}

	result := make([]any, len(rows))
	for i, r := range rows {
		result[i] = r
	}
	return result, nil
}

func mockApplyWhere(rows []map[string]any, clause string) []map[string]any {
	conditions := strings.Split(strings.TrimSpace(clause), " AND ")
	eqRe := regexp.MustCompile(`(?i)(\w+)\s*=\s*'?([^']+?)'?$`)
	filters := map[string]string{}
	for _, cond := range conditions {
		if m := eqRe.FindStringSubmatch(strings.TrimSpace(cond)); m != nil {
			filters[strings.ToLower(m[1])] = strings.ToLower(m[2])
		}
	}
	if len(filters) == 0 {
		return rows
	}
	var out []map[string]any
	for _, row := range rows {
		match := true
		for col, val := range filters {
			rv, ok := row[col]
			if !ok {
				match = false
				break
			}
			if !strings.EqualFold(fmt.Sprintf("%v", rv), val) {
				match = false
				break
			}
		}
		if match {
			out = append(out, row)
		}
	}
	return out
}
