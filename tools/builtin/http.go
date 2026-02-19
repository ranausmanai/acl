package builtin

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

var httpClient = &http.Client{
	Timeout: 15 * time.Second,
}

// httpGet performs a real HTTP GET request.
// Returns {"status": int, "text": string, "url": string}.
func httpGet(ctx context.Context, args map[string]any) (any, error) {
	url, _ := args["url"].(string)
	if url == "" {
		return nil, fmt.Errorf("http.get: url argument required")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("http.get: bad url %q: %w", url, err)
	}
	req.Header.Set("User-Agent", "ACL/0.1 (+https://github.com/acl-lang/acl)")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http.get: request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20)) // 2 MB cap
	if err != nil {
		return nil, fmt.Errorf("http.get: read body: %w", err)
	}

	return map[string]any{
		"status": int64(resp.StatusCode),
		"text":   string(body),
		"url":    url,
	}, nil
}
