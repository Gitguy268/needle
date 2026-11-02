package fx

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testCache(t *testing.T, maxMB int) (*cache, string) {
	t.Helper()
	dir := t.TempDir()
	c, err := newCache(filepath.Join(dir, "effects"), maxMB, "test")
	if err != nil {
		t.Fatal(err)
	}
	return c, dir
}

func TestCacheKeyTracksFileSizeTimeAndChain(t *testing.T) {
	c, dir := testCache(t, 1)
	src := filepath.Join(dir, "song.wav")
	if err := os.WriteFile(src, []byte("0123456789"), 0o600); err != nil {
		t.Fatal(err)
	}
	chain := []Plugin{{"type": "Gain", "gain_db": 1.0}}
	k1, err := c.key(src, chain)
	if err != nil {
		t.Fatal(err)
	}
	k2, err := c.key(src, []Plugin{{"type": "Gain", "gain_db": 2.0}})
	if err != nil {
		t.Fatal(err)
	}
	if k1 == k2 {
		t.Fatal("different chains share a cache key")
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(src, old, old); err != nil {
		t.Fatal(err)
	}
	k3, err := c.key(src, chain)
	if err != nil {
		t.Fatal(err)
	}
	if k3 == k1 {
		t.Fatal("a new mtime did not change the cache key")
	}
}

func TestCacheRendersOnce(t *testing.T) {
	c, dir := testCache(t, 1)
	src := filepath.Join(dir, "song.wav")
	if err := os.WriteFile(src, []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	render := func(_, dst string) error {
		calls++
		return os.WriteFile(dst, bytes.Repeat([]byte{0}, 100), 0o600)
	}
	chain := []Plugin{{"type": "Gain", "gain_db": -1.0}}
	first, err := c.render(src, chain, render)
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.render(src, chain, render)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("rendered %d times, want 1", calls)
	}
	if first != second {
		t.Fatalf("paths differ: %q %q", first, second)
	}
	if info, err := os.Stat(first); err != nil || info.Size() != 100 {
		t.Fatalf("cached file: %v %v", info, err)
	}
}

func TestCacheSweepDropsOldest(t *testing.T) {
	c, _ := testCache(t, 1)
	now := time.Now()
	mk := func(name string, age time.Duration) string {
		path := filepath.Join(c.dir, name+".wav")
		if err := os.WriteFile(path, bytes.Repeat([]byte{0}, 600*1024), 0o600); err != nil {
			t.Fatal(err)
		}
		tm := now.Add(-age)
		if err := os.Chtimes(path, tm, tm); err != nil {
			t.Fatal(err)
		}
		return path
	}
	oldest := mk("oldest", 3*time.Hour)
	middle := mk("middle", 2*time.Hour)
	newest := mk("newest", time.Hour)
	c.sweep("")
	if _, err := os.Stat(oldest); !os.IsNotExist(err) {
		t.Fatalf("oldest survived: %v", err)
	}
	if _, err := os.Stat(middle); !os.IsNotExist(err) {
		t.Fatalf("middle survived: %v", err)
	}
	if _, err := os.Stat(newest); err != nil {
		t.Fatalf("newest was removed: %v", err)
	}
}

func TestSweepKeepsTheFileAboutToPlay(t *testing.T) {
	c, _ := testCache(t, 1)
	path := filepath.Join(c.dir, "playing.wav")
	if err := os.WriteFile(path, bytes.Repeat([]byte{0}, 2<<20), 0o600); err != nil {
		t.Fatal(err)
	}
	c.sweep(path)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("keep file was removed: %v", err)
	}
}

func TestSweepDropsStalePartials(t *testing.T) {
	c, _ := testCache(t, 1)
	part := filepath.Join(c.dir, "x.123.part.wav")
	if err := os.WriteFile(part, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(part, old, old); err != nil {
		t.Fatal(err)
	}
	c.sweep("")
	if _, err := os.Stat(part); !os.IsNotExist(err) {
		t.Fatalf("stale partial survived: %v", err)
	}
}
