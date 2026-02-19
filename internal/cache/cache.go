// Package cache implements a SHA-256 keyed file cache for tool results.
//
// Cache key = SHA-256(json({tool, normalised_args, version}))
// Cache files live in <dir>/<hex>.json
package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Cache is a persistent, file-backed result cache.
type Cache struct {
	dir string
}

// New returns a Cache rooted at dir, creating it if necessary.
func New(dir string) (*Cache, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Cache{dir: dir}, nil
}

// Key computes the cache key for a tool invocation.
func (c *Cache) Key(toolName string, args map[string]any, version string) string {
	payload := map[string]any{
		"tool":    toolName,
		"args":    normalise(args),
		"version": version,
	}
	b, _ := json.Marshal(payload)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Get retrieves a cached result.  Returns (value, true) on hit, (nil, false) on miss.
func (c *Cache) Get(key string) (any, bool) {
	path := c.path(key)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, false
	}
	return v, true
}

// Set persists a result under key.
func (c *Cache) Set(key string, value any) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(c.path(key), b, 0o644)
}

// Clear removes all entries.  Returns the number of files removed.
func (c *Cache) Clear() (int, error) {
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".json" {
			if err := os.Remove(filepath.Join(c.dir, e.Name())); err == nil {
				removed++
			}
		}
	}
	return removed, nil
}

// Stats returns the number of entries and total bytes.
func (c *Cache) Stats() (int, int64, error) {
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return 0, 0, err
	}
	var count int
	var total int64
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".json" {
			info, err := e.Info()
			if err == nil {
				count++
				total += info.Size()
			}
		}
	}
	return count, total, nil
}

func (c *Cache) path(key string) string {
	return filepath.Join(c.dir, fmt.Sprintf("%s.json", key))
}

// normalise produces a deterministic, sort-keyed representation for hashing.
func normalise(v any) any {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := make(map[string]any, len(x))
		for _, k := range keys {
			out[k] = normalise(x[k])
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = normalise(item)
		}
		return out
	}
	return v
}
