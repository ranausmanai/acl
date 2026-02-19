package builtin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// resolveFilePath applies ACL_FILE_DIR sandboxing if set.
// When ACL_FILE_DIR is set, all paths are relative to that directory and
// directory traversal is blocked. Without it, any path is accepted.
func resolveFilePath(path string) (string, error) {
	if baseDir := os.Getenv("ACL_FILE_DIR"); baseDir != "" {
		clean := filepath.Clean(path)
		if filepath.IsAbs(clean) {
			return "", fmt.Errorf("file: absolute paths not allowed when ACL_FILE_DIR is set")
		}
		full := filepath.Join(baseDir, clean)
		rel, err := filepath.Rel(baseDir, full)
		if err != nil || strings.HasPrefix(rel, "..") {
			return "", fmt.Errorf("file: path escapes ACL_FILE_DIR")
		}
		return full, nil
	}
	return filepath.Clean(path), nil
}

// file.read — read a file and return its text content.
//
// Args: path string (required)
// Returns: {text, path, size_bytes}
func fileRead(_ context.Context, args map[string]any) (any, error) {
	path, _ := args["path"].(string)
	if path == "" {
		return nil, fmt.Errorf("file.read: path is required")
	}
	resolved, err := resolveFilePath(path)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return nil, fmt.Errorf("file.read: %w", err)
	}
	return map[string]any{
		"text":       string(data),
		"path":       resolved,
		"size_bytes": len(data),
	}, nil
}

// file.write — write (overwrite) a file with the given content.
//
// Args: path string (required), content string (required)
// Returns: {path, size_bytes, written: true}
func fileWrite(_ context.Context, args map[string]any) (any, error) {
	path, _ := args["path"].(string)
	if path == "" {
		return nil, fmt.Errorf("file.write: path is required")
	}
	content, _ := args["content"].(string)
	resolved, err := resolveFilePath(path)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
		return nil, fmt.Errorf("file.write: mkdir: %w", err)
	}
	if err := os.WriteFile(resolved, []byte(content), 0o644); err != nil {
		return nil, fmt.Errorf("file.write: %w", err)
	}
	return map[string]any{
		"path":       resolved,
		"size_bytes": len(content),
		"written":    true,
	}, nil
}

// file.append — append content to a file (creates it if it doesn't exist).
//
// Args: path string (required), content string (required)
// Returns: {path, written, total_bytes}
func fileAppend(_ context.Context, args map[string]any) (any, error) {
	path, _ := args["path"].(string)
	if path == "" {
		return nil, fmt.Errorf("file.append: path is required")
	}
	content, _ := args["content"].(string)
	resolved, err := resolveFilePath(path)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
		return nil, fmt.Errorf("file.append: mkdir: %w", err)
	}
	f, err := os.OpenFile(resolved, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("file.append: %w", err)
	}
	defer f.Close()
	n, err := f.WriteString(content)
	if err != nil {
		return nil, fmt.Errorf("file.append: %w", err)
	}
	info, _ := f.Stat()
	totalSize := 0
	if info != nil {
		totalSize = int(info.Size())
	}
	return map[string]any{
		"path":        resolved,
		"written":     n,
		"total_bytes": totalSize,
	}, nil
}

// file.delete — delete a file.
//
// Args: path string (required)
// Returns: {path, deleted: true}
func fileDelete(_ context.Context, args map[string]any) (any, error) {
	path, _ := args["path"].(string)
	if path == "" {
		return nil, fmt.Errorf("file.delete: path is required")
	}
	resolved, err := resolveFilePath(path)
	if err != nil {
		return nil, err
	}
	if err := os.Remove(resolved); err != nil {
		return nil, fmt.Errorf("file.delete: %w", err)
	}
	return map[string]any{"path": resolved, "deleted": true}, nil
}

// file.list — list the contents of a directory.
//
// Args: path string (default ".")
// Returns: {files: [{name, is_dir, size, modified}], path, count}
func fileList(_ context.Context, args map[string]any) (any, error) {
	path, _ := args["path"].(string)
	if path == "" {
		path = "."
	}
	resolved, err := resolveFilePath(path)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(resolved)
	if err != nil {
		return nil, fmt.Errorf("file.list: %w", err)
	}
	var files []any
	for _, e := range entries {
		info, _ := e.Info()
		size := int64(0)
		modTime := ""
		if info != nil {
			size = info.Size()
			modTime = info.ModTime().UTC().Format(time.RFC3339)
		}
		files = append(files, map[string]any{
			"name":     e.Name(),
			"is_dir":   e.IsDir(),
			"size":     size,
			"modified": modTime,
		})
	}
	if files == nil {
		files = []any{}
	}
	return map[string]any{"files": files, "path": resolved, "count": len(files)}, nil
}
