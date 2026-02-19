package builtin

import (
	"context"
	"fmt"
	"strings"
)

// mockHTTPGet is the deterministic, offline version of http.get used in tests.
// It pattern-matches the URL and returns hardcoded HTML/JSON responses so
// tests never touch the network.
func mockHTTPGet(_ context.Context, args map[string]any) (any, error) {
	url, _ := args["url"].(string)
	if url == "" {
		return nil, fmt.Errorf("http.get: url argument required")
	}
	return map[string]any{
		"status": int64(200),
		"text":   mockHTTPBody(url),
		"url":    url,
	}, nil
}

func mockHTTPBody(url string) string {
	lower := strings.ToLower(url)
	switch {
	case strings.Contains(lower, "pricing") || strings.Contains(lower, "plan"):
		return `<html><body>
<table>
<tr><th>plan</th><th>price</th><th>features</th></tr>
<tr><td>starter</td><td>$29/mo</td><td>5 users</td></tr>
<tr><td>pro</td><td>$99/mo</td><td>50 users</td></tr>
<tr><td>enterprise</td><td>$499/mo</td><td>unlimited</td></tr>
</table>
</body></html>`
	case strings.Contains(lower, "news") || strings.Contains(lower, "blog"):
		return `{"articles":[{"title":"AI Breakthrough","source":"TechNews","date":"2026-02-19"},{"title":"Market Rally","source":"FinanceDaily","date":"2026-02-18"}]}`
	case strings.Contains(lower, "api") || strings.Contains(lower, "data"):
		return `{"status":"ok","data":{"value":42,"label":"test result","updated":"2026-02-19"}}`
	case strings.Contains(lower, "health") || strings.Contains(lower, "status"):
		return `{"healthy":true,"uptime":99.97,"latency_ms":42}`
	default:
		return fmt.Sprintf(`<html><body><h1>Response for %s</h1><p>Content placeholder for testing.</p></body></html>`, url)
	}
}
