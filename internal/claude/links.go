package claude

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// Link ties a git worktree to the Claude session that entered it.
type Link struct {
	SessionID          string    `json:"session_id"`
	Path               string    `json:"path"`
	Name               string    `json:"name"`
	Branch             string    `json:"branch"`
	OriginalBranch     string    `json:"original_branch"`
	OriginalCWD        string    `json:"original_cwd"`
	OriginalHeadCommit string    `json:"original_head_commit"`
	LastSeen           time.Time `json:"last_seen"`
}

type worktreeStateLine struct {
	Type            string `json:"type"`
	WorktreeSession struct {
		OriginalCwd        string `json:"originalCwd"`
		WorktreePath       string `json:"worktreePath"`
		WorktreeName       string `json:"worktreeName"`
		WorktreeBranch     string `json:"worktreeBranch"`
		OriginalBranch     string `json:"originalBranch"`
		OriginalHeadCommit string `json:"originalHeadCommit"`
		SessionID          string `json:"sessionId"`
	} `json:"worktreeSession"`
}

var marker = []byte(`"worktree-state"`)

// Index is the result of scanning the transcript corpus once.
type Index struct {
	// Links maps an absolute worktree path to the most recent session that
	// was recorded working in it.
	Links map[string]Link
	// CWDs is every working directory Claude Code has been run in.
	CWDs []string
	// Scanned counts transcript files read from disk (cache misses).
	Scanned int
	// Cached counts transcript files served from the on-disk cache.
	Cached int
}

type cacheEntry struct {
	Size    int64  `json:"size"`
	ModTime int64  `json:"mtime"`
	CWD     string `json:"cwd"`
	Links   []Link `json:"links"`
}

// BuildIndex walks ~/.claude/projects, extracting session-to-worktree links
// and the set of directories Claude has run in. Results are cached per
// transcript file, so repeat runs only read files that changed.
func BuildIndex(useCache bool) Index {
	idx := Index{Links: map[string]Link{}}
	root := filepath.Join(Dir(), "projects")
	files := transcripts(root)
	if len(files) == 0 {
		return idx
	}

	cachePath := filepath.Join(Dir(), "pitwall", "worktree-cache.json")
	cache := map[string]cacheEntry{}
	if useCache {
		if raw, err := os.ReadFile(cachePath); err == nil {
			_ = json.Unmarshal(raw, &cache)
		}
	}

	type result struct {
		path  string
		entry cacheEntry
		hit   bool
	}

	jobs := make(chan string)
	results := make(chan result)
	workers := runtime.NumCPU()
	if workers > 8 {
		workers = 8
	}
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range jobs {
				info, err := os.Stat(path)
				if err != nil {
					continue
				}
				if prev, ok := cache[path]; ok && prev.Size == info.Size() && prev.ModTime == info.ModTime().UnixNano() {
					results <- result{path: path, entry: prev, hit: true}
					continue
				}
				results <- result{path: path, entry: scanTranscript(path, info)}
			}
		}()
	}
	go func() {
		for _, f := range files {
			jobs <- f
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()

	fresh := make(map[string]cacheEntry, len(files))
	cwds := map[string]bool{}
	for r := range results {
		fresh[r.path] = r.entry
		if r.hit {
			idx.Cached++
		} else {
			idx.Scanned++
		}
		if r.entry.CWD != "" {
			cwds[r.entry.CWD] = true
		}
		for _, l := range r.entry.Links {
			if l.Path == "" {
				continue
			}
			if prev, ok := idx.Links[l.Path]; !ok || l.LastSeen.After(prev.LastSeen) {
				idx.Links[l.Path] = l
			}
		}
	}

	for c := range cwds {
		idx.CWDs = append(idx.CWDs, c)
	}
	sort.Strings(idx.CWDs)

	if useCache {
		if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err == nil {
			if raw, err := json.Marshal(fresh); err == nil {
				_ = os.WriteFile(cachePath, raw, 0o644)
			}
		}
	}
	return idx
}

func transcripts(root string) []string {
	var out []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() && strings.HasSuffix(path, ".jsonl") {
			out = append(out, path)
		}
		return nil
	})
	return out
}

// scanTranscript reads one .jsonl transcript, pulling out its working
// directory and any worktree-state records.
func scanTranscript(path string, info os.FileInfo) cacheEntry {
	entry := cacheEntry{Size: info.Size(), ModTime: info.ModTime().UnixNano()}
	f, err := os.Open(path)
	if err != nil {
		return entry
	}
	defer f.Close()

	seen := map[string]bool{}
	r := bufio.NewReaderSize(f, 1<<20)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			if entry.CWD == "" {
				entry.CWD = extractCWD(line)
			}
			if bytes.Contains(line, marker) {
				var ws worktreeStateLine
				if json.Unmarshal(line, &ws) == nil && ws.Type == "worktree-state" {
					s := ws.WorktreeSession
					if s.WorktreePath != "" && !seen[s.WorktreePath+s.SessionID] {
						seen[s.WorktreePath+s.SessionID] = true
						entry.Links = append(entry.Links, Link{
							SessionID:          s.SessionID,
							Path:               s.WorktreePath,
							Name:               s.WorktreeName,
							Branch:             s.WorktreeBranch,
							OriginalBranch:     s.OriginalBranch,
							OriginalCWD:        s.OriginalCwd,
							OriginalHeadCommit: s.OriginalHeadCommit,
							LastSeen:           info.ModTime(),
						})
					}
				}
			}
		}
		if err != nil {
			if err != io.EOF {
				return entry
			}
			break
		}
	}
	return entry
}

// extractCWD pulls the "cwd" field out of a raw transcript line without
// unmarshalling the whole (potentially megabyte-sized) record.
func extractCWD(line []byte) string {
	i := bytes.Index(line, []byte(`"cwd":"`))
	if i < 0 {
		return ""
	}
	rest := line[i+7:]
	j := bytes.IndexByte(rest, '"')
	if j < 0 {
		return ""
	}
	var s string
	if json.Unmarshal(append(append([]byte(`"`), rest[:j]...), '"'), &s) != nil {
		return ""
	}
	return s
}

// Workdirs lists the distinct working directories Claude Code has been run in,
// read from the transcripts themselves. The encoded directory names under
// projects/ cannot be decoded reliably — a dash in the name is indistinguishable
// from a path separator — so the cwd recorded inside each file is the only
// dependable source.
func Workdirs() []string {
	root := filepath.Join(Dir(), "projects")
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(root, e.Name()))
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".jsonl") {
				continue
			}
			if cwd := firstCWD(filepath.Join(root, e.Name(), f.Name())); cwd != "" && !seen[cwd] {
				seen[cwd] = true
				out = append(out, cwd)
				break // one transcript is enough to identify the directory
			}
		}
	}
	sort.Strings(out)
	return out
}

// firstCWD reads just enough of a transcript to find its working directory.
func firstCWD(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for n := 0; n < 20 && sc.Scan(); n++ {
		if cwd := extractCWD(sc.Bytes()); cwd != "" {
			return cwd
		}
	}
	return ""
}

// HistoryDirs lists every directory Claude Code has been run in according to
// history.jsonl. That file is not pruned on the transcript schedule, so it
// reaches back much further — which is what makes it the right source for
// anything that must find all of a user's projects rather than the recent ones.
func HistoryDirs() []string {
	f, err := os.Open(filepath.Join(Dir(), "history.jsonl"))
	if err != nil {
		return nil
	}
	defer f.Close()
	seen := map[string]bool{}
	var out []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		var rec struct {
			Project string `json:"project"`
		}
		if json.Unmarshal(sc.Bytes(), &rec) != nil || rec.Project == "" || seen[rec.Project] {
			continue
		}
		seen[rec.Project] = true
		out = append(out, rec.Project)
	}
	sort.Strings(out)
	return out
}
