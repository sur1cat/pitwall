package worktree

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// sizeEntry remembers what a directory measured, and what the directory looked
// like when it was measured.
type sizeEntry struct {
	Bytes int64     `json:"bytes"`
	Mod   time.Time `json:"mod"`
	At    time.Time `json:"at"`
}

// sizeCache keeps directory sizes between runs. Walking a worktree tree is by
// far the slowest thing this package does — measuring 2.7 GB took eight of the
// eight and a half seconds `pitwall tree` spent — and a command that takes
// eight seconds is one nobody runs twice.
type sizeCache struct {
	mu      sync.Mutex
	path    string
	entries map[string]sizeEntry
	dirty   bool
}

// sizeCacheTTL is how long a measurement is trusted when the directory's own
// timestamp has not moved. Files change inside a worktree without the
// directory's timestamp changing, so the reading is refreshed periodically
// rather than kept forever.
const sizeCacheTTL = 6 * time.Hour

func loadSizeCache(dir string) *sizeCache {
	c := &sizeCache{path: filepath.Join(dir, "sizes.json"), entries: map[string]sizeEntry{}}
	raw, err := os.ReadFile(c.path)
	if err != nil {
		return c
	}
	_ = json.Unmarshal(raw, &c.entries)
	return c
}

// size returns a directory's size, measuring it only when the cached reading
// is missing, stale, or was taken before the directory last changed.
func (c *sizeCache) size(path string) int64 {
	st, err := os.Stat(path)
	if err != nil {
		return 0
	}
	mod := st.ModTime()

	c.mu.Lock()
	e, ok := c.entries[path]
	c.mu.Unlock()
	if ok && e.Mod.Equal(mod) && time.Since(e.At) < sizeCacheTTL {
		return e.Bytes
	}

	n := dirSize(path)
	c.mu.Lock()
	c.entries[path] = sizeEntry{Bytes: n, Mod: mod, At: time.Now()}
	c.dirty = true
	c.mu.Unlock()
	return n
}

// save writes the cache back, dropping entries whose directory is gone so the
// file does not grow forever as worktrees come and go.
func (c *sizeCache) save() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.dirty {
		return
	}
	for p := range c.entries {
		if _, err := os.Stat(p); err != nil {
			delete(c.entries, p)
		}
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return
	}
	raw, err := json.Marshal(c.entries)
	if err != nil {
		return
	}
	_ = os.WriteFile(c.path, raw, 0o600)
}
