// Package scan discovers every git worktree reachable from the repositories
// you work in, joins each one to the Claude Code session that created it, and
// classifies what it is safe to do with it.
package worktree

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sur1cat/pitwall/internal/claude"
)

// State is what pitwall concluded about a worktree.
type State string

const (
	// StatePrimary is a repository's main work tree. Never removable.
	StatePrimary State = "PRIMARY"
	// StateActive has a live Claude Code session working inside it.
	StateActive State = "ACTIVE"
	// StateStranded holds uncommitted work with no live session to finish it.
	StateStranded State = "STRANDED"
	// StateAhead has commits that are not in the base branch yet.
	StateAhead State = "AHEAD"
	// StateDead is fully merged and clean: nothing would be lost by removing it.
	StateDead State = "DEAD"
	// StateOrphan is a worktree git considers prunable (its directory is gone).
	StateOrphan State = "ORPHAN"
)

// Removable reports whether `pitwall tree gc` may remove a worktree in this state.
func (s State) Removable() bool { return s == StateDead || s == StateStranded || s == StateOrphan }

// Worktree is one checkout plus everything pitwall knows about it.
type Worktree struct {
	RepoRoot   string      `json:"repo_root"`
	Path       string      `json:"path"`
	Name       string      `json:"name"`
	Branch     string      `json:"branch"`
	State      State       `json:"state"`
	Primary    bool        `json:"primary"`
	Detached   bool        `json:"detached"`
	Locked     bool        `json:"locked"`
	Prunable   bool        `json:"prunable"`
	Reason     string      `json:"reason,omitempty"`
	Ahead      int         `json:"ahead"`
	Behind     int         `json:"behind"`
	HasCounts  bool        `json:"has_counts"`
	Modified   []string    `json:"modified,omitempty"`
	Untracked  []string    `json:"untracked,omitempty"`
	LastCommit time.Time   `json:"last_commit"`
	SizeBytes  int64       `json:"size_bytes"`
	Base       string      `json:"base"`
	Session    *SessionRef `json:"session,omitempty"`
	Owner      *OwnerRef   `json:"owner,omitempty"`
}

// Dirty counts uncommitted paths.
func (w *Worktree) Dirty() int { return len(w.Modified) + len(w.Untracked) }

// SessionRef is a live Claude Code session working in this worktree.
type SessionRef struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	PID     int       `json:"pid"`
	Status  string    `json:"status"`
	Updated time.Time `json:"updated"`
}

// OwnerRef is the session that originally created the worktree, alive or not.
type OwnerRef struct {
	ID       string    `json:"id"`
	LastSeen time.Time `json:"last_seen"`
	Alive    bool      `json:"alive"`
}

// Repo is one repository and its worktrees.
type Repo struct {
	Root      string      `json:"root"`
	Base      string      `json:"base"`
	Worktrees []*Worktree `json:"worktrees"`
}

// Result is a full inventory.
type Result struct {
	Repos    []*Repo      `json:"repos"`
	Index    claude.Index `json:"-"`
	Sessions []claude.Session
}

// Options controls discovery.
type Options struct {
	// Roots restricts the scan to these paths. Empty means auto-discover.
	Roots []string
	// WithSize computes on-disk size per worktree (a directory walk).
	WithSize bool
	// NoCache forces a full re-read of the transcript corpus.
	NoCache bool
}

// Run builds the inventory.
func Run(opt Options) (Result, error) {
	var res Result
	if !gitAvailable() {
		return res, errNoGit
	}

	// Sizes are cached between runs and written back once at the end, because
	// measuring them is what made this command slow enough to avoid.
	sizes := loadSizeCache(filepath.Join(claude.Dir(), "pitwall"))
	if opt.NoCache {
		sizes = &sizeCache{path: sizes.path, entries: map[string]sizeEntry{}}
	}
	defer sizes.save()

	res.Sessions = claude.Sessions()

	var candidates []string
	if len(opt.Roots) > 0 {
		// An explicit --path is a hard limit: do not widen it with the
		// repositories Claude happens to be running in.
		candidates = opt.Roots
	} else {
		res.Index = claude.BuildIndex(!opt.NoCache)
		candidates = append(candidates, res.Index.CWDs...)
		for path, link := range res.Index.Links {
			candidates = append(candidates, path, link.OriginalCWD)
		}
		for _, s := range res.Sessions {
			if s.Alive {
				candidates = append(candidates, s.CWD)
			}
		}
		if wd, err := os.Getwd(); err == nil {
			candidates = append(candidates, wd)
		}
	}

	roots := map[string]bool{}
	tried := map[string]bool{}
	for _, dir := range candidates {
		if dir == "" || tried[dir] {
			continue
		}
		tried[dir] = true
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			continue
		}
		root, err := repoRoot(dir)
		if err != nil || root == "" {
			continue
		}
		roots[root] = true
	}

	var rootList []string
	for r := range roots {
		rootList = append(rootList, r)
	}
	sort.Strings(rootList)

	for _, root := range rootList {
		repo := inspect(root, opt, sizes)
		if repo != nil && len(repo.Worktrees) > 0 {
			res.Repos = append(res.Repos, repo)
		}
	}

	attachSessions(res.Repos, res.Sessions)
	attachOwners(res.Repos, res.Index, res.Sessions)
	for _, repo := range res.Repos {
		for _, wt := range repo.Worktrees {
			classify(wt)
		}
	}
	return res, nil
}

type gitError string

func (e gitError) Error() string { return string(e) }

const errNoGit = gitError("git was not found on PATH")

// inspect gathers git facts for one repository, in parallel across worktrees.
func inspect(root string, opt Options, sizes *sizeCache) *Repo {
	list, err := gitWorktrees(root)
	if err != nil {
		return nil
	}
	repo := &Repo{Root: root, Base: baseBranch(root)}

	sem := make(chan struct{}, max(2, runtime.NumCPU()))
	var wg sync.WaitGroup
	var mu sync.Mutex

	for _, entry := range list {
		if entry.Bare {
			continue
		}
		wt := &Worktree{
			RepoRoot: root,
			Path:     entry.Path,
			Name:     filepath.Base(entry.Path),
			Branch:   entry.Branch,
			Primary:  filepath.Clean(entry.Path) == filepath.Clean(root),
			Detached: entry.Detached,
			Locked:   entry.Locked,
			Prunable: entry.Prunable,
			Reason:   entry.Reason,
			Base:     repo.Base,
		}
		mu.Lock()
		repo.Worktrees = append(repo.Worktrees, wt)
		mu.Unlock()

		if wt.Prunable {
			continue // directory is gone; nothing left to measure
		}
		wg.Add(1)
		go func(wt *Worktree) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			st := gitWorktreeStatus(wt.Path)
			wt.Modified, wt.Untracked = st.Modified, st.Untracked
			wt.LastCommit = lastCommit(wt.Path)
			if wt.Branch != "" && repo.Base != "" && wt.Branch != repo.Base {
				wt.Ahead, wt.Behind, wt.HasCounts = aheadBehind(root, repo.Base, wt.Branch)
			} else if wt.Branch == repo.Base {
				wt.HasCounts = true
			}
			if opt.WithSize {
				wt.SizeBytes = sizes.size(wt.Path)
			}
		}(wt)
	}
	wg.Wait()

	sort.Slice(repo.Worktrees, func(i, j int) bool {
		a, b := repo.Worktrees[i], repo.Worktrees[j]
		if a.Primary != b.Primary {
			return a.Primary
		}
		return a.Path < b.Path
	})
	return repo
}

// attachSessions binds each live session to the deepest worktree containing it.
func attachSessions(repos []*Repo, sessions []claude.Session) {
	var all []*Worktree
	for _, r := range repos {
		all = append(all, r.Worktrees...)
	}
	for _, s := range sessions {
		if !s.Alive || s.CWD == "" {
			continue
		}
		var best *Worktree
		for _, wt := range all {
			if !within(s.CWD, wt.Path) {
				continue
			}
			if best == nil || len(wt.Path) > len(best.Path) {
				best = wt
			}
		}
		if best != nil && best.Session == nil {
			best.Session = &SessionRef{ID: s.SessionID, Name: s.Name, PID: s.PID, Status: s.Status, Updated: s.Updated()}
		}
	}
}

// attachOwners records which session originally created each worktree.
func attachOwners(repos []*Repo, idx claude.Index, sessions []claude.Session) {
	alive := map[string]bool{}
	for _, s := range sessions {
		if s.Alive {
			alive[s.SessionID] = true
		}
	}
	byPath := make(map[string]claude.Link, len(idx.Links))
	for p, l := range idx.Links {
		byPath[Resolve(p)] = l
	}
	for _, r := range repos {
		for _, wt := range r.Worktrees {
			link, ok := byPath[Resolve(wt.Path)]
			if !ok {
				continue
			}
			wt.Owner = &OwnerRef{ID: link.SessionID, LastSeen: link.LastSeen, Alive: alive[link.SessionID]}
			if wt.Name == "" {
				wt.Name = link.Name
			}
		}
	}
}

// classify assigns the state that drives every other command.
func classify(wt *Worktree) {
	switch {
	case wt.Prunable:
		wt.State = StateOrphan
	case wt.Primary:
		wt.State = StatePrimary
	case wt.Session != nil || (wt.Owner != nil && wt.Owner.Alive):
		wt.State = StateActive
	case wt.Dirty() > 0:
		wt.State = StateStranded
	case !wt.HasCounts || wt.Ahead > 0 || wt.Detached:
		// Unknown lineage is treated as "has unique work" — never auto-remove
		// something we could not prove is merged.
		wt.State = StateAhead
	default:
		wt.State = StateDead
	}
}

func within(path, dir string) bool {
	path, dir = Resolve(path), Resolve(dir)
	if path == dir {
		return true
	}
	return strings.HasPrefix(path, dir+string(filepath.Separator))
}

// Resolve canonicalises a path so comparisons survive symlinks — on macOS
// /var and /tmp are links, so git and the OS report different strings for the
// same directory.
func Resolve(p string) string {
	if p == "" {
		return ""
	}
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if real, err := filepath.EvalSymlinks(p); err == nil {
		return filepath.Clean(real)
	}
	return filepath.Clean(p)
}

func dirSize(root string) int64 {
	var total int64
	_ = filepath.WalkDir(root, func(_ string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Totals summarises a scan.
type Totals struct {
	Worktrees int           `json:"worktrees"`
	ByState   map[State]int `json:"by_state"`
	Bytes     int64         `json:"bytes"`
	Dirty     int           `json:"dirty"`
}

// Summarize aggregates counts across every repository.
func Summarize(repos []*Repo) Totals {
	t := Totals{ByState: map[State]int{}}
	for _, r := range repos {
		for _, wt := range r.Worktrees {
			t.Worktrees++
			t.ByState[wt.State]++
			t.Bytes += wt.SizeBytes
			t.Dirty += wt.Dirty()
		}
	}
	return t
}
