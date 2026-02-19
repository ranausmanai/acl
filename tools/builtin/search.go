package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// search.web — search the web and return structured results.
//
// Provider resolution (in order):
//  1. BRAVE_API_KEY  → Brave Search API
//  2. SERPER_API_KEY → Serper.dev (Google)
//
// Args:
//
//	query    string  (required)
//	limit    int     max results (default 5)
//	country  string  country code for Brave (default "us")
//
// Returns: {hits: [{title, url, snippet}], query, count, provider}
func searchWeb(ctx context.Context, args map[string]any) (any, error) {
	query, _ := args["query"].(string)
	if query == "" {
		return nil, fmt.Errorf("search.web: query is required")
	}
	limit := 5
	if l, ok := args["limit"].(float64); ok && l > 0 {
		limit = int(l)
	}
	country := "us"
	if c, ok := args["country"].(string); ok && c != "" {
		country = c
	}

	if key := os.Getenv("BRAVE_API_KEY"); key != "" {
		return braveSearch(ctx, query, limit, country, key)
	}
	if key := os.Getenv("SERPER_API_KEY"); key != "" {
		return serperSearch(ctx, query, limit, key)
	}
	return nil, fmt.Errorf("search.web: no provider configured — set BRAVE_API_KEY or SERPER_API_KEY")
}

func braveSearch(ctx context.Context, query string, limit int, country, apiKey string) (any, error) {
	endpoint := fmt.Sprintf(
		"https://api.search.brave.com/res/v1/web/search?q=%s&count=%d&country=%s",
		url.QueryEscape(query), limit, country,
	)
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("search.web (brave): %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("search.web (brave): %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var result struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("search.web (brave): parse: %w", err)
	}

	var hits []any
	for _, r := range result.Web.Results {
		hits = append(hits, map[string]any{
			"title":   r.Title,
			"url":     r.URL,
			"snippet": r.Description,
		})
	}
	if hits == nil {
		hits = []any{}
	}
	return map[string]any{
		"hits":     hits,
		"count":    len(hits),
		"query":    query,
		"provider": "brave",
	}, nil
}

func serperSearch(ctx context.Context, query string, limit int, apiKey string) (any, error) {
	payload := fmt.Sprintf(`{"q":%s,"num":%d}`, jsonQuote(query), limit)
	req, err := http.NewRequestWithContext(ctx, "POST", "https://google.serper.dev/search",
		strings.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("search.web (serper): %w", err)
	}
	req.Header.Set("X-API-KEY", apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("search.web (serper): %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Organic []struct {
			Title   string `json:"title"`
			Link    string `json:"link"`
			Snippet string `json:"snippet"`
		} `json:"organic"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("search.web (serper): parse: %w", err)
	}

	var hits []any
	for _, r := range result.Organic {
		hits = append(hits, map[string]any{
			"title":   r.Title,
			"url":     r.Link,
			"snippet": r.Snippet,
		})
	}
	if hits == nil {
		hits = []any{}
	}
	return map[string]any{
		"hits":     hits,
		"count":    len(hits),
		"query":    query,
		"provider": "serper",
	}, nil
}

func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
