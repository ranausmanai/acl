// Package builtin — data tools: json.parse, json.stringify, csv.parse, csv.write, text.chunk
package builtin

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"strings"
)

// json.parse — convert a JSON string into a structured object usable in CHECK/MUST expressions.
//
// Args: text string (required)
// Returns: {data: <parsed>, ok: true}
func jsonParse(_ context.Context, args map[string]any) (any, error) {
	text, _ := args["text"].(string)
	if text == "" {
		// also accept "data" key for piping llm output directly
		if d, ok := args["data"].(string); ok {
			text = d
		}
	}
	if text == "" {
		return nil, fmt.Errorf("json.parse: text is required")
	}
	var out any
	if err := json.Unmarshal([]byte(strings.TrimSpace(text)), &out); err != nil {
		return nil, fmt.Errorf("json.parse: %w", err)
	}
	return map[string]any{"data": out, "ok": true}, nil
}

// json.stringify — convert any value to a JSON string.
//
// Args: data any (required), indent bool (optional, default false)
// Returns: {text: string}
func jsonStringify(_ context.Context, args map[string]any) (any, error) {
	data := args["data"]
	indent, _ := args["indent"].(bool)

	var b []byte
	var err error
	if indent {
		b, err = json.MarshalIndent(data, "", "  ")
	} else {
		b, err = json.Marshal(data)
	}
	if err != nil {
		return nil, fmt.Errorf("json.stringify: %w", err)
	}
	return map[string]any{"text": string(b)}, nil
}

// csv.parse — parse CSV text into structured rows.
//
// Args:
//
//	text       string  (required)
//	has_header bool    (default true — first row = column names)
//	delimiter  string  single character (default ",")
//
// Returns: {rows: [{col: val}], columns: [string], count: int}
func csvParse(_ context.Context, args map[string]any) (any, error) {
	text, _ := args["text"].(string)
	if text == "" {
		return nil, fmt.Errorf("csv.parse: text is required")
	}

	hasHeader := true
	if h, ok := args["has_header"].(bool); ok {
		hasHeader = h
	}

	delim := ','
	if d, ok := args["delimiter"].(string); ok && len(d) > 0 {
		delim = rune(d[0])
	}

	r := csv.NewReader(strings.NewReader(text))
	r.Comma = delim
	r.TrimLeadingSpace = true
	r.LazyQuotes = true

	records, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("csv.parse: %w", err)
	}

	if len(records) == 0 {
		return map[string]any{"rows": []any{}, "columns": []string{}, "count": 0}, nil
	}

	var columns []string
	startRow := 0
	if hasHeader {
		columns = records[0]
		startRow = 1
	} else {
		for i := range records[0] {
			columns = append(columns, fmt.Sprintf("col%d", i))
		}
	}

	var rows []any
	for _, record := range records[startRow:] {
		row := map[string]any{}
		for i, col := range columns {
			if i < len(record) {
				row[col] = record[i]
			}
		}
		rows = append(rows, row)
	}
	if rows == nil {
		rows = []any{}
	}
	return map[string]any{"rows": rows, "columns": columns, "count": len(rows)}, nil
}

// csv.write — convert a list of row objects back to CSV text.
//
// Args:
//
//	rows    []map  (required)
//	columns []string  (optional — if omitted, keys from first row are used)
//
// Returns: {text: string, count: int}
func csvWrite(_ context.Context, args map[string]any) (any, error) {
	rows, ok := args["rows"].([]any)
	if !ok {
		return nil, fmt.Errorf("csv.write: rows (array) is required")
	}

	var columns []string
	if cols, ok := args["columns"].([]any); ok {
		for _, c := range cols {
			columns = append(columns, fmt.Sprintf("%v", c))
		}
	}

	var sb strings.Builder
	w := csv.NewWriter(&sb)

	// Auto-detect columns from first row if not provided.
	if len(columns) == 0 && len(rows) > 0 {
		if row0, ok := rows[0].(map[string]any); ok {
			for k := range row0 {
				columns = append(columns, k)
			}
		}
	}
	if len(columns) > 0 {
		w.Write(columns) //nolint
	}

	for _, row := range rows {
		var record []string
		switch r := row.(type) {
		case map[string]any:
			for _, col := range columns {
				record = append(record, fmt.Sprintf("%v", r[col]))
			}
		case []any:
			for _, v := range r {
				record = append(record, fmt.Sprintf("%v", v))
			}
		}
		w.Write(record) //nolint
	}

	w.Flush()
	if err := w.Error(); err != nil {
		return nil, fmt.Errorf("csv.write: %w", err)
	}
	return map[string]any{"text": sb.String(), "count": len(rows)}, nil
}

// text.chunk — split text into overlapping word-chunks for RAG pipelines.
//
// Args:
//
//	text     string  (required)
//	size     int     words per chunk   (default 500)
//	overlap  int     words of overlap  (default 50)
//
// Returns: {chunks: [{text, index, start, end}], count: int}
func textChunk(_ context.Context, args map[string]any) (any, error) {
	text, _ := args["text"].(string)
	if text == "" {
		return nil, fmt.Errorf("text.chunk: text is required")
	}

	size := 500
	if s, ok := args["size"].(float64); ok && s > 0 {
		size = int(s)
	}
	overlap := 50
	if o, ok := args["overlap"].(float64); ok && o >= 0 {
		overlap = int(o)
	}

	words := strings.Fields(text)
	var chunks []any

	for i := 0; i < len(words); {
		end := i + size
		if end > len(words) {
			end = len(words)
		}
		chunks = append(chunks, map[string]any{
			"text":  strings.Join(words[i:end], " "),
			"index": len(chunks),
			"start": i,
			"end":   end,
		})
		if end == len(words) {
			break
		}
		i += size - overlap
	}

	if chunks == nil {
		chunks = []any{}
	}
	return map[string]any{"chunks": chunks, "count": len(chunks), "size": size, "overlap": overlap}, nil
}
