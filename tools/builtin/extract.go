package builtin

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// extractTable extracts a table from HTML or text content.
// Returns {"rows": [{"col": "val", ...}], "count": int}.
func extractTable(_ context.Context, args map[string]any) (any, error) {
	text, _ := args["text"].(string)
	if text == "" {
		return nil, fmt.Errorf("extract.table: text argument required")
	}

	// Determine requested columns.
	var columns []string
	switch v := args["columns"].(type) {
	case []any:
		for _, c := range v {
			if s, ok := c.(string); ok {
				columns = append(columns, s)
			}
		}
	case string:
		if v != "" {
			columns = append(columns, v)
		}
	}

	rows := parseTable(text, columns)
	return map[string]any{
		"rows":  rows,
		"count": int64(len(rows)),
	}, nil
}

// parseTable extracts rows from HTML <table> or TSV/CSV-like text.
func parseTable(text string, wantCols []string) []any {
	// Try HTML table first.
	if strings.Contains(text, "<table") || strings.Contains(text, "<tr") {
		return parseHTMLTable(text, wantCols)
	}
	// Fall back to line-oriented parsing.
	return parseLineTable(text, wantCols)
}

func parseHTMLTable(text string, wantCols []string) []any {
	trRe := regexp.MustCompile(`(?i)<tr[^>]*>(.*?)</tr>`)
	tdRe := regexp.MustCompile(`(?i)<t[dh][^>]*>(.*?)</t[dh]>`)
	tagRe := regexp.MustCompile(`<[^>]+>`)

	matches := trRe.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return nil
	}

	// First row = headers.
	headers := extractCells(matches[0][1], tdRe, tagRe)

	if len(wantCols) > 0 {
		// Build header index.
		idx := map[string]int{}
		for i, h := range headers {
			idx[strings.ToLower(h)] = i
		}
		// Re-order headers to requested columns only.
		var filtered []string
		for _, c := range wantCols {
			if _, ok := idx[strings.ToLower(c)]; ok {
				filtered = append(filtered, c)
			} else {
				filtered = append(filtered, c) // include even if missing
			}
		}
		headers = filtered
	}

	var rows []any
	for _, match := range matches[1:] {
		cells := extractCells(match[1], tdRe, tagRe)
		row := make(map[string]any, len(headers))
		for i, h := range headers {
			if i < len(cells) {
				row[strings.ToLower(h)] = cells[i]
			} else {
				row[strings.ToLower(h)] = ""
			}
		}
		rows = append(rows, row)
	}
	return rows
}

func extractCells(rowHTML string, tdRe, tagRe *regexp.Regexp) []string {
	matches := tdRe.FindAllStringSubmatch(rowHTML, -1)
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		cell := tagRe.ReplaceAllString(m[1], "")
		out = append(out, strings.TrimSpace(cell))
	}
	return out
}

func parseLineTable(text string, wantCols []string) []any {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) < 2 {
		return nil
	}

	sep := "\t"
	if strings.Contains(lines[0], ",") && !strings.Contains(lines[0], "\t") {
		sep = ","
	}

	headers := splitFields(lines[0], sep)
	if len(wantCols) > 0 {
		headers = wantCols
	}

	var rows []any
	for _, line := range lines[1:] {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := splitFields(line, sep)
		row := make(map[string]any, len(headers))
		for i, h := range headers {
			if i < len(fields) {
				row[strings.ToLower(h)] = strings.TrimSpace(fields[i])
			} else {
				row[strings.ToLower(h)] = ""
			}
		}
		rows = append(rows, row)
	}
	return rows
}

func splitFields(s, sep string) []string {
	parts := strings.Split(s, sep)
	for i, p := range parts {
		parts[i] = strings.TrimSpace(p)
	}
	return parts
}
