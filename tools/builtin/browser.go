package builtin

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode"
)

var browserClient = &http.Client{Timeout: 20 * time.Second}

// browser.fetch — fetch a web page and return clean readable text.
//
// Args:
//
//	url      string  (required)
//	selector string  (optional) css hint — "main"|"article"|"body" (best-effort)
//
// Returns: {url, status, title, text, word_count}
func browserFetch(ctx context.Context, args map[string]any) (any, error) {
	rawURL, _ := args["url"].(string)
	if rawURL == "" {
		return nil, fmt.Errorf("browser.fetch: url is required")
	}

	req, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("browser.fetch: invalid url %q: %w", rawURL, err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := browserClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("browser.fetch: %w", err)
	}
	defer resp.Body.Close()

	const maxBytes = 512 * 1024 // 512 KB
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes))
	if err != nil {
		return nil, fmt.Errorf("browser.fetch: read: %w", err)
	}

	html := string(raw)
	title := extractTitle(html)
	text := htmlToText(html)

	wordCount := len(strings.Fields(text))

	// Truncate text to ~8 KB for the receipt preview
	const maxText = 8000
	if len(text) > maxText {
		text = text[:maxText] + "\n[truncated]"
	}

	return map[string]any{
		"url":        rawURL,
		"status":     resp.StatusCode,
		"title":      title,
		"text":       text,
		"word_count": wordCount,
	}, nil
}

// ── HTML helpers ──────────────────────────────────────────────────────────────

var (
	reScript  = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	reStyle   = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	reNav     = regexp.MustCompile(`(?is)<(nav|header|footer|aside)[^>]*>.*?</(nav|header|footer|aside)>`)
	reTag     = regexp.MustCompile(`<[^>]+>`)
	reEntAmp  = regexp.MustCompile(`&amp;`)
	reEntLt   = regexp.MustCompile(`&lt;`)
	reEntGt   = regexp.MustCompile(`&gt;`)
	reEntNbsp = regexp.MustCompile(`&nbsp;`)
	reEntQuot = regexp.MustCompile(`&quot;|&#34;`)
	reEntity  = regexp.MustCompile(`&[a-zA-Z0-9#]+;`)
	reSpaces  = regexp.MustCompile(`[ \t]+`)
	reLines   = regexp.MustCompile(`\n{3,}`)
	reTitle   = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
)

func extractTitle(html string) string {
	m := reTitle.FindStringSubmatch(html)
	if len(m) < 2 {
		return ""
	}
	return strings.TrimSpace(reTag.ReplaceAllString(m[1], ""))
}

func htmlToText(html string) string {
	// Remove blocks that are never useful
	s := reScript.ReplaceAllString(html, " ")
	s = reStyle.ReplaceAllString(s, " ")
	s = reNav.ReplaceAllString(s, " ")

	// Convert common block elements to newlines
	s = regexp.MustCompile(`(?i)<(br|p|div|h[1-6]|li|tr|blockquote|pre)[^>]*>`).ReplaceAllString(s, "\n")
	s = regexp.MustCompile(`(?i)</(p|div|h[1-6]|li|tr|blockquote|pre)>`).ReplaceAllString(s, "\n")

	// Strip remaining tags
	s = reTag.ReplaceAllString(s, "")

	// Decode common HTML entities
	s = reEntAmp.ReplaceAllString(s, "&")
	s = reEntLt.ReplaceAllString(s, "<")
	s = reEntGt.ReplaceAllString(s, ">")
	s = reEntNbsp.ReplaceAllString(s, " ")
	s = reEntQuot.ReplaceAllString(s, `"`)
	s = reEntity.ReplaceAllString(s, "")

	// Normalize whitespace
	s = reSpaces.ReplaceAllString(s, " ")

	// Clean up lines
	var lines []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimRightFunc(line, unicode.IsSpace)
		lines = append(lines, line)
	}
	s = strings.Join(lines, "\n")
	s = reLines.ReplaceAllString(s, "\n\n")

	return strings.TrimSpace(s)
}
