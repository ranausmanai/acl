package cache_test

import (
	"os"
	"testing"

	"acl/internal/cache"
)

func tempCache(t *testing.T) (*cache.Cache, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "acl-cache-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	c, err := cache.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	return c, dir
}

func TestGetMiss(t *testing.T) {
	c, _ := tempCache(t)
	_, ok := c.Get("nonexistent")
	if ok {
		t.Error("expected miss")
	}
}

func TestSetAndGet(t *testing.T) {
	c, _ := tempCache(t)
	key := c.Key("http.get", map[string]any{"url": "https://example.com"}, "1")
	value := map[string]any{"status": int64(200), "text": "hello"}

	if err := c.Set(key, value); err != nil {
		t.Fatal(err)
	}

	got, ok := c.Get(key)
	if !ok {
		t.Fatal("expected cache hit")
	}
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("type: %T", got)
	}
	if m["text"] != "hello" {
		t.Errorf("text: %v", m["text"])
	}
}

func TestKeyDeterminism(t *testing.T) {
	c, _ := tempCache(t)
	args := map[string]any{"b": "2", "a": "1"}
	k1 := c.Key("tool", args, "1")
	k2 := c.Key("tool", args, "1")
	if k1 != k2 {
		t.Error("key not deterministic")
	}
}

func TestKeyArgOrder(t *testing.T) {
	c, _ := tempCache(t)
	a1 := map[string]any{"x": "1", "y": "2"}
	a2 := map[string]any{"y": "2", "x": "1"}
	k1 := c.Key("tool", a1, "1")
	k2 := c.Key("tool", a2, "1")
	if k1 != k2 {
		t.Error("key order not normalised")
	}
}

func TestVersionInKey(t *testing.T) {
	c, _ := tempCache(t)
	args := map[string]any{"url": "x"}
	k1 := c.Key("http.get", args, "1")
	k2 := c.Key("http.get", args, "2")
	if k1 == k2 {
		t.Error("version should affect key")
	}
}

func TestStats(t *testing.T) {
	c, _ := tempCache(t)
	k1 := c.Key("t", map[string]any{"a": 1}, "1")
	k2 := c.Key("t", map[string]any{"b": 2}, "1")
	_ = c.Set(k1, "val1")
	_ = c.Set(k2, "val2")

	count, bytes, err := c.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Errorf("count: %d", count)
	}
	if bytes == 0 {
		t.Error("bytes should be > 0")
	}
}

func TestClear(t *testing.T) {
	c, _ := tempCache(t)
	k := c.Key("t", map[string]any{}, "1")
	_ = c.Set(k, "x")

	removed, err := c.Clear()
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Errorf("removed: %d", removed)
	}
	_, ok := c.Get(k)
	if ok {
		t.Error("expected miss after clear")
	}
}
