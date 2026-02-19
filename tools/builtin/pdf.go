package builtin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// pdfRender generates a real PDF file from a template name and data.
//
// Args:
//
//	template  string  — document title / template name
//	data      any     — content: string, map, or list of maps (rendered as table)
//	file      string  — output path (optional; default: ACL_PDF_DIR or OS temp dir)
//
// Returns {"file_id":string, "path":string, "page_count":int, "size_bytes":int}
func pdfRender(_ context.Context, args map[string]any) (any, error) {
	template, _ := args["template"].(string)
	if template == "" {
		return nil, fmt.Errorf("pdf.render: template argument required")
	}
	data := args["data"]

	// Resolve output path.
	outPath, _ := args["file"].(string)
	if outPath == "" {
		dir := os.Getenv("ACL_PDF_DIR")
		if dir == "" {
			dir = os.TempDir()
		}
		ts := time.Now().UTC().Format("20060102-150405")
		safe := strings.Map(func(r rune) rune {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
				return r
			}
			return '_'
		}, template)
		outPath = filepath.Join(dir, fmt.Sprintf("acl_%s_%s.pdf", safe, ts))
	}

	// Build text lines to render.
	lines := buildPDFLines(template, data)

	// Render minimal PDF 1.4.
	pdfBytes := renderPDF(template, lines)

	if err := os.WriteFile(outPath, pdfBytes, 0644); err != nil {
		return nil, fmt.Errorf("pdf.render: write %s: %w", outPath, err)
	}

	pageCount := countPages(lines)
	return map[string]any{
		"file_id":    filepath.Base(outPath),
		"path":       outPath,
		"page_count": int64(pageCount),
		"size_bytes": int64(len(pdfBytes)),
		"template":   template,
	}, nil
}

// ── Content builder ───────────────────────────────────────────────────────────

func buildPDFLines(title string, data any) []string {
	var lines []string
	lines = append(lines, title)
	lines = append(lines, strings.Repeat("─", min(len(title)+4, 72)))
	lines = append(lines, "Generated: "+time.Now().UTC().Format(time.RFC1123))
	lines = append(lines, "")

	if data == nil {
		return lines
	}

	switch v := data.(type) {
	case string:
		// Word-wrap at 80 chars.
		lines = append(lines, wrapText(v, 80)...)
	case map[string]any:
		// Check if it has a "rows" key (SQL result).
		if rows, ok := v["rows"].([]any); ok {
			lines = append(lines, formatTableLines(rows)...)
		} else {
			// Render as key: value pairs.
			for k, val := range v {
				lines = append(lines, fmt.Sprintf("%-20s %v", k+":", val))
			}
		}
	case []any:
		lines = append(lines, formatTableLines(v)...)
	default:
		b, _ := json.MarshalIndent(data, "", "  ")
		lines = append(lines, strings.Split(string(b), "\n")...)
	}
	return lines
}

func formatTableLines(rows []any) []string {
	if len(rows) == 0 {
		return []string{"(no rows)"}
	}
	// Collect column names from first row.
	firstRow, ok := rows[0].(map[string]any)
	if !ok {
		b, _ := json.MarshalIndent(rows, "", "  ")
		return strings.Split(string(b), "\n")
	}
	cols := make([]string, 0, len(firstRow))
	for k := range firstRow {
		cols = append(cols, k)
	}

	// Column widths.
	widths := make(map[string]int, len(cols))
	for _, c := range cols {
		widths[c] = len(c)
	}
	for _, r := range rows {
		if row, ok := r.(map[string]any); ok {
			for _, c := range cols {
				s := fmt.Sprintf("%v", row[c])
				if len(s) > widths[c] {
					widths[c] = len(s)
				}
			}
		}
	}

	var lines []string
	// Header.
	var hdr strings.Builder
	var sep strings.Builder
	for _, c := range cols {
		w := widths[c]
		hdr.WriteString(fmt.Sprintf("%-*s  ", w, c))
		sep.WriteString(strings.Repeat("-", w) + "  ")
	}
	lines = append(lines, hdr.String())
	lines = append(lines, sep.String())

	// Rows.
	for _, r := range rows {
		if row, ok := r.(map[string]any); ok {
			var sb strings.Builder
			for _, c := range cols {
				w := widths[c]
				sb.WriteString(fmt.Sprintf("%-*s  ", w, fmt.Sprintf("%v", row[c])))
			}
			lines = append(lines, sb.String())
		}
	}
	return lines
}

func wrapText(text string, width int) []string {
	words := strings.Fields(text)
	var lines []string
	var cur strings.Builder
	for _, w := range words {
		if cur.Len()+len(w)+1 > width && cur.Len() > 0 {
			lines = append(lines, cur.String())
			cur.Reset()
		}
		if cur.Len() > 0 {
			cur.WriteByte(' ')
		}
		cur.WriteString(w)
	}
	if cur.Len() > 0 {
		lines = append(lines, cur.String())
	}
	return lines
}

func countPages(lines []string) int {
	const linesPerPage = 50
	pages := (len(lines) + linesPerPage - 1) / linesPerPage
	if pages < 1 {
		return 1
	}
	return pages
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ── Minimal PDF 1.4 writer ────────────────────────────────────────────────────
//
// Generates a valid, Acrobat-readable PDF with Helvetica text, no external deps.

const (
	pdfPageWidth  = 595.0 // A4 points
	pdfPageHeight = 842.0
	pdfMargin     = 50.0
	pdfFontSize   = 11.0
	pdfLineHeight = 15.0
	pdfLinesPerPg = 48
)

func renderPDF(title string, contentLines []string) []byte {
	var buf bytes.Buffer
	offsets := []int{}

	w := func(s string) { buf.WriteString(s) }
	wf := func(f string, a ...any) { buf.WriteString(fmt.Sprintf(f, a...)) }

	// Header.
	w("%PDF-1.4\n")

	// Object 1 — Catalog.
	offsets = append(offsets, buf.Len())
	w("1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")

	// Split lines into pages.
	pages := splitPages(contentLines, pdfLinesPerPg)
	numPages := len(pages)
	if numPages == 0 {
		numPages = 1
		pages = [][]string{{""}}
	}

	// We'll allocate object IDs as follows:
	//   1 = catalog
	//   2 = pages
	//   3 = font
	//   4..4+numPages-1 = page content streams
	//   4+numPages..4+2*numPages-1 = page dicts
	contentBase := 4
	pageBase := contentBase + numPages

	// Object 2 — Pages dict.
	offsets = append(offsets, buf.Len())
	wf("2 0 obj\n<< /Type /Pages /Count %d /Kids [", numPages)
	for i := 0; i < numPages; i++ {
		wf("%d 0 R ", pageBase+i)
	}
	w("] >>\nendobj\n")

	// Object 3 — Font (Helvetica, no embedding needed).
	offsets = append(offsets, buf.Len())
	w("3 0 obj\n<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>\nendobj\n")

	// Content stream objects (4..4+numPages-1).
	for i, pg := range pages {
		stream := buildContentStream(pg)
		offsets = append(offsets, buf.Len())
		wf("%d 0 obj\n<< /Length %d >>\nstream\n%s\nendstream\nendobj\n",
			contentBase+i, len(stream), stream)
	}

	// Page dict objects (pageBase..pageBase+numPages-1).
	for i := 0; i < numPages; i++ {
		offsets = append(offsets, buf.Len())
		wf("%d 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %.1f %.1f] /Contents %d 0 R /Resources << /Font << /F1 3 0 R >> >> >>\nendobj\n",
			pageBase+i, pdfPageWidth, pdfPageHeight, contentBase+i)
	}

	// Cross-reference table.
	xrefOffset := buf.Len()
	totalObjs := 1 + len(offsets) // object 0 is always free
	wf("xref\n0 %d\n", totalObjs)
	w("0000000000 65535 f \n")
	for _, off := range offsets {
		wf("%010d 00000 n \n", off)
	}

	// Trailer.
	wf("trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", totalObjs, xrefOffset)

	return buf.Bytes()
}

func splitPages(lines []string, perPage int) [][]string {
	var pages [][]string
	for i := 0; i < len(lines); i += perPage {
		end := i + perPage
		if end > len(lines) {
			end = len(lines)
		}
		pages = append(pages, lines[i:end])
	}
	return pages
}

// buildContentStream renders lines as PDF content stream operators.
func buildContentStream(lines []string) string {
	var sb strings.Builder
	sb.WriteString("BT\n")
	sb.WriteString(fmt.Sprintf("/F1 %.1f Tf\n", pdfFontSize))

	y := pdfPageHeight - pdfMargin - pdfFontSize
	sb.WriteString(fmt.Sprintf("%.1f %.1f Td\n", pdfMargin, y))
	sb.WriteString(fmt.Sprintf("%.1f TL\n", pdfLineHeight))

	for i, line := range lines {
		if i == 0 {
			// First line is placed by Td above.
			sb.WriteString(fmt.Sprintf("(%s) Tj\n", pdfEscapeString(line)))
		} else {
			sb.WriteString(fmt.Sprintf("T* (%s) Tj\n", pdfEscapeString(line)))
		}
	}
	sb.WriteString("ET\n")
	return sb.String()
}

// pdfEscapeString escapes special PDF string characters.
func pdfEscapeString(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "(", "\\(")
	s = strings.ReplaceAll(s, ")", "\\)")
	// Keep only printable ASCII (PDF strings in LiteralString require it).
	var out strings.Builder
	for _, r := range s {
		if r >= 32 && r <= 126 {
			out.WriteRune(r)
		} else {
			out.WriteRune(' ')
		}
	}
	return out.String()
}
