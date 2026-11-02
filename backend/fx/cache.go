package fx

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// helperVersion is bumped when the helper's processing changes, so old cache
// files are not reused.
const helperVersion = 1

// cache stores processed WAV files, keyed by the source file, its metadata and
// the effect chain. It keeps the directory under maxBytes, oldest first.
type cache struct {
	dir        string
	maxBytes   int64
	pedalboard string
}

func newCache(dir string, maxMB int, pedalboard string) (*cache, error) {
	if maxMB <= 0 {
		maxMB = 1024
	}
	c := &cache{dir: dir, maxBytes: int64(maxMB) << 20, pedalboard: pedalboard}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	c.sweep("")
	return c, nil
}

// render returns the processed path for src, rendering it with render when the
// cache does not have it.
func (c *cache) render(src string, chain []Plugin, render func(src, dst string) error) (string, error) {
	key, err := c.key(src, chain)
	if err != nil {
		return "", err
	}
	if path, ok := c.get(key); ok {
		return path, nil
	}
	dst := filepath.Join(c.dir, key+".wav")
	// Pedalboard picks the encoder from the extension, so the partial file
	// still has to end in .wav.
	part := fmt.Sprintf("%s.%d.part.wav", strings.TrimSuffix(dst, ".wav"), os.Getpid())
	defer os.Remove(part)
	if err := render(src, part); err != nil {
		return "", err
	}
	if err := os.Rename(part, dst); err != nil {
		return "", err
	}
	c.sweep(dst)
	return dst, nil
}

func (c *cache) key(src string, chain []Plugin) (string, error) {
	info, err := os.Stat(src)
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(src)
	if err != nil {
		return "", err
	}
	chainJSON, err := json.Marshal(chain)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	fmt.Fprintf(h, "helper=%d\npedalboard=%s\npath=%s\nsize=%d\nmtime=%d\nchain=%s",
		helperVersion, c.pedalboard, abs, info.Size(), info.ModTime().UnixNano(), chainJSON)
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (c *cache) get(key string) (string, bool) {
	path := filepath.Join(c.dir, key+".wav")
	if info, err := os.Stat(path); err == nil && info.Size() > 44 {
		now := time.Now()
		_ = os.Chtimes(path, now, now)
		return path, true
	}
	return "", false
}

// sweep drops stale partial files and the oldest WAVs until the cache fits.
// keep is never removed, so the file that is about to play stays.
func (c *cache) sweep(keep string) {
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return
	}
	type entry struct {
		path string
		size int64
		mod  time.Time
	}
	var files []entry
	var total int64
	stale := time.Now().Add(-24 * time.Hour)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		path := filepath.Join(c.dir, name)
		info, err := e.Info()
		if err != nil {
			continue
		}
		switch {
		case strings.Contains(name, ".part"):
			if info.ModTime().Before(stale) {
				_ = os.Remove(path)
			}
		case strings.HasSuffix(name, ".wav"):
			files = append(files, entry{path, info.Size(), info.ModTime()})
			total += info.Size()
		}
	}
	if total <= c.maxBytes {
		return
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.Before(files[j].mod) })
	for _, f := range files {
		if total <= c.maxBytes {
			break
		}
		if f.path == keep {
			continue
		}
		if err := os.Remove(f.path); err == nil {
			total -= f.size
		}
	}
}
