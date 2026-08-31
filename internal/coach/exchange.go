// Package coach measures how a person actually spends their agent budget:
// what each prompt bought, where the round trips are, and which habits cost
// the most. It reads only local transcripts.
package coach

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sur1cat/pitwall/internal/burn"
	"github.com/sur1cat/pitwall/internal/claude"
)

// Exchange is one human prompt and every assistant turn it produced, up to
// the next human prompt.
type Exchange struct {
	Prompt   string    `json:"-"`
	Time     time.Time `json:"time"`
	Session  string    `json:"session"`
	CWD      string    `json:"cwd"`
	Repo     string    `json:"repo"`
	Cost     float64   `json:"cost"`
	Messages int       `json:"messages"`
	Tools    int       `json:"tools"`
	Edits    int       `json:"edits"`
	Reads    int       `json:"reads"`
	Bash     int       `json:"bash"`
	Asks     int       `json:"asks"`
	Effort   string    `json:"effort"`
	// EndedOnQuestion means the last thing the agent said was a question.
	EndedOnQuestion bool `json:"ended_on_question"`
	// Index is this exchange's position within its session, starting at 1.
	Index int `json:"index"`
	// Len is the prompt's true length, kept separately because the stored
	// prompt is truncated.
	Len int `json:"len"`

	ids   []string
	costs []float64
}

// Length is the prompt's character count, before truncation.
func (e *Exchange) Length() int { return e.Len }

// Class describes what an exchange produced.
type Class string

const (
	// ClassExecute changed code.
	ClassExecute Class = "execute"
	// ClassInvestigate used tools but changed nothing.
	ClassInvestigate Class = "investigate"
	// ClassTalk used no tools at all.
	ClassTalk Class = "talk"
)

// Class buckets an exchange by its outcome.
func (e *Exchange) Class() Class {
	switch {
	case e.Edits > 0:
		return ClassExecute
	case e.Tools > 0:
		return ClassInvestigate
	default:
		return ClassTalk
	}
}

var editTools = map[string]bool{"Edit": true, "Write": true, "NotebookEdit": true, "MultiEdit": true}
var readTools = map[string]bool{"Read": true, "Grep": true, "Glob": true, "WebFetch": true, "WebSearch": true, "ToolSearch": true}

type line struct {
	Type        string `json:"type"`
	Timestamp   string `json:"timestamp"`
	Effort      string `json:"effort"`
	CWD         string `json:"cwd"`
	SessionID   string `json:"sessionId"`
	IsMeta      bool   `json:"isMeta"`
	IsSidechain bool   `json:"isSidechain"`
	Message     struct {
		ID      string          `json:"id"`
		Role    string          `json:"role"`
		Model   string          `json:"model"`
		Content json.RawMessage `json:"content"`
		Usage   struct {
			Input       int64 `json:"input_tokens"`
			Output      int64 `json:"output_tokens"`
			CacheCreate int64 `json:"cache_creation_input_tokens"`
			CacheRead   int64 `json:"cache_read_input_tokens"`
			Creation    struct {
				E5m int64 `json:"ephemeral_5m_input_tokens"`
				E1h int64 `json:"ephemeral_1h_input_tokens"`
			} `json:"cache_creation"`
		} `json:"usage"`
	} `json:"message"`
}

type block struct {
	Type string `json:"type"`
	Text string `json:"text"`
	Name string `json:"name"`
}

// skipPrefixes are wrappers Claude Code injects that are not things a human typed.
var skipPrefixes = []string{"<command-name>", "<local-command", "<system-reminder", "Caveat:", "[Request interrupted"}

// Progress reports how far a scan has got, so a caller can show something
// during the seconds it takes to read a large transcript corpus.
type Progress func(done, total int)

// Collect reads every transcript and returns the exchanges, newest last.
func Collect() ([]*Exchange, error) { return CollectWithProgress(nil) }

// cacheEntry is one transcript's exchanges, kept so a rescan only touches
// files that changed. Reading the whole corpus takes fifteen seconds; reading
// the handful that moved takes none.
type cacheEntry struct {
	Size      int64       `json:"size"`
	ModTime   int64       `json:"mtime"`
	Exchanges []*Exchange `json:"exchanges"`
	IDs       [][]string  `json:"ids"`
	Costs     [][]float64 `json:"costs"`
}

func loadCache() map[string]cacheEntry {
	raw, err := os.ReadFile(cachePath())
	if err != nil {
		return map[string]cacheEntry{}
	}
	out := map[string]cacheEntry{}
	if json.Unmarshal(raw, &out) != nil {
		return map[string]cacheEntry{}
	}
	return out
}

func saveCache(entries map[string]cacheEntry) {
	path := cachePath()
	if os.MkdirAll(filepath.Dir(path), 0o755) != nil {
		return
	}
	if raw, err := json.Marshal(entries); err == nil {
		_ = os.WriteFile(path, raw, 0o644)
	}
}

func cachePath() string {
	return filepath.Join(claude.Dir(), "pitwall", "coach-cache.json")
}

// CollectWithProgress is Collect with a callback fired as files are read.
func CollectWithProgress(progress Progress) ([]*Exchange, error) {
	root := filepath.Join(claude.Dir(), "projects")
	var files []string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".jsonl") {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Reading a couple of gigabytes one file at a time is what made this
	// command sit silent for fifteen seconds.
	cache := loadCache()
	fresh := make([]cacheEntry, len(files))

	type result struct {
		index int
		entry cacheEntry
	}
	jobs := make(chan int)
	out := make(chan result)
	workers := runtime.NumCPU()
	if workers > 8 {
		workers = 8
	}
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				path := files[idx]
				info, err := os.Stat(path)
				if err != nil {
					out <- result{index: idx}
					continue
				}
				if prev, ok := cache[path]; ok && prev.Size == info.Size() && prev.ModTime == info.ModTime().UnixNano() {
					out <- result{index: idx, entry: prev}
					continue
				}
				ex := scanFile(path)
				entry := cacheEntry{Size: info.Size(), ModTime: info.ModTime().UnixNano(), Exchanges: ex}
				for _, e := range ex {
					entry.IDs = append(entry.IDs, e.ids)
					entry.Costs = append(entry.Costs, e.costs)
				}
				out <- result{index: idx, entry: entry}
			}
		}()
	}
	go func() {
		for i := range files {
			jobs <- i
		}
		close(jobs)
		wg.Wait()
		close(out)
	}()

	done := 0
	for r := range out {
		fresh[r.index] = r.entry
		done++
		if progress != nil {
			progress(done, len(files))
		}
	}

	var all []*Exchange
	for i, entry := range fresh {
		for j, e := range entry.Exchanges {
			if j < len(entry.IDs) {
				e.ids, e.costs = entry.IDs[j], entry.Costs[j]
			}
			all = append(all, e)
		}
		if i < len(files) {
			cache[files[i]] = entry
		}
	}
	saveCache(cache)
	sort.Slice(all, func(i, j int) bool { return all[i].Time.Before(all[j].Time) })

	// Session forks replay earlier assistant messages; count each API
	// response once, against the exchange that saw it first.
	seen := map[string]bool{}
	kept := all[:0]
	perSession := map[string]int{}
	for _, e := range all {
		cost, msgs := 0.0, 0
		for i, id := range e.ids {
			if id != "" && seen[id] {
				continue
			}
			if id != "" {
				seen[id] = true
			}
			cost += e.costs[i]
			msgs++
		}
		e.Cost, e.Messages = cost, msgs
		e.ids, e.costs = nil, nil
		perSession[e.Session]++
		e.Index = perSession[e.Session]
		kept = append(kept, e)
	}
	return kept, nil
}

func scanFile(path string) []*Exchange {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var out []*Exchange
	var cur *Exchange
	r := bufio.NewReaderSize(f, 1<<20)
	for {
		raw, err := r.ReadBytes('\n')
		if len(raw) > 0 && raw[0] == '{' {
			var l line
			if json.Unmarshal(raw, &l) == nil && !l.IsSidechain {
				switch l.Type {
				case "user":
					if txt, ok := humanText(l); ok {
						full := []rune(txt)
						if len(full) > 400 {
							txt = string(full[:400])
						}
						cur = &Exchange{
							Prompt:  txt,
							Len:     len(full),
							Time:    parseTime(l.Timestamp),
							Session: l.SessionID,
							CWD:     l.CWD,
						}
						out = append(out, cur)
					}
				case "assistant":
					if cur != nil {
						absorb(cur, l)
					}
				}
			}
		}
		if err != nil {
			break
		}
	}
	return out
}

func humanText(l line) (string, bool) {
	if l.IsMeta || l.Message.Role != "user" {
		return "", false
	}
	var s string
	if json.Unmarshal(l.Message.Content, &s) == nil {
		return clean(s)
	}
	var blocks []block
	if json.Unmarshal(l.Message.Content, &blocks) != nil {
		return "", false
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "tool_result" {
			return "", false // a tool answering, not a person typing
		}
		if b.Type == "text" {
			parts = append(parts, b.Text)
		}
	}
	if len(parts) == 0 {
		return "", false
	}
	return clean(strings.Join(parts, "\n"))
}

func clean(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", false
	}
	for _, p := range skipPrefixes {
		if strings.HasPrefix(s, p) {
			return "", false
		}
	}
	return s, true
}

func absorb(e *Exchange, l line) {
	u := l.Message.Usage
	w5, w1 := u.Creation.E5m, u.Creation.E1h
	if w5+w1 == 0 {
		w5 = u.CacheCreate
	}
	c, _ := burn.Compute(l.Message.Model, parseTime(l.Timestamp), burn.Usage{
		Input: u.Input, Output: u.Output,
		CacheWrite5m: w5, CacheWrite1h: w1, CacheRead: u.CacheRead,
	})
	e.ids = append(e.ids, l.Message.ID)
	e.costs = append(e.costs, c.Total())
	if l.Effort != "" && e.Effort == "" {
		e.Effort = l.Effort
	}

	var blocks []block
	if json.Unmarshal(l.Message.Content, &blocks) != nil {
		return
	}
	last := ""
	for _, b := range blocks {
		switch b.Type {
		case "tool_use":
			e.Tools++
			switch {
			case editTools[b.Name]:
				e.Edits++
			case readTools[b.Name]:
				e.Reads++
			case b.Name == "Bash":
				e.Bash++
			case b.Name == "AskUserQuestion":
				e.Asks++
			}
		case "text":
			if strings.TrimSpace(b.Text) != "" {
				last = b.Text
			}
		}
	}
	if last != "" {
		e.EndedOnQuestion = strings.HasSuffix(strings.TrimSpace(last), "?")
	}
}

func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

var (
	repoCacheMu sync.Mutex
	repoCache   = map[string]string{}
)

// RepoOf resolves a working directory to the repository it belongs to. Linked
// worktrees resolve to their main checkout, so a repository does not appear
// once per branch an agent happened to work on.
func RepoOf(cwd string) string {
	if cwd == "" {
		return ""
	}
	repoCacheMu.Lock()
	if hit, ok := repoCache[cwd]; ok {
		repoCacheMu.Unlock()
		return hit
	}
	repoCacheMu.Unlock()

	resolved := resolveRepoRoot(cwd)
	repoCacheMu.Lock()
	repoCache[cwd] = resolved
	repoCacheMu.Unlock()
	return resolved
}

func resolveRepoRoot(cwd string) string {
	// The common git dir is shared by a repository and all of its worktrees.
	out, err := exec.Command("git", "-C", cwd, "rev-parse",
		"--path-format=absolute", "--git-common-dir").Output()
	if err == nil {
		common := strings.TrimSpace(string(out))
		if filepath.Base(common) == ".git" {
			return filepath.Dir(common)
		}
		if common != "" {
			return common
		}
	}
	p := cwd
	for p != "" && p != string(filepath.Separator) {
		if st, err := os.Stat(filepath.Join(p, ".git")); err == nil && (st.IsDir() || st.Mode().IsRegular()) {
			return p
		}
		parent := filepath.Dir(p)
		if parent == p {
			break
		}
		p = parent
	}
	return cwd
}

// Primed reports whether a repository gives a fresh session anything to start
// from: a CLAUDE.md, project agents, or project commands.
func Primed(repo string) bool {
	if repo == "" {
		return false
	}
	if _, err := os.Stat(filepath.Join(repo, "CLAUDE.md")); err == nil {
		return true
	}
	for _, sub := range []string{"agents", "commands", "skills"} {
		if entries, err := os.ReadDir(filepath.Join(repo, ".claude", sub)); err == nil && len(entries) > 0 {
			return true
		}
	}
	return false
}
