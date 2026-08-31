package worktree

// This file is a thin, dependency-free wrapper over the git CLI.

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// errNotRepo is returned when a directory is not inside a git work tree.
var errNotRepo = errors.New("not a git repository")

const defaultTimeout = 30 * time.Second

// git executes the git CLI in dir and returns trimmed stdout.
func git(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()
	full := append([]string{"-C", dir}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Env = append(cmd.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return "", errors.New(strings.TrimSpace(string(ee.Stderr)))
		}
		return "", err
	}
	return strings.TrimRight(string(out), "\n"), nil
}

// gitOut runs git and returns stdout, swallowing errors as an empty string.
func gitOut(dir string, args ...string) string {
	s, err := git(dir, args...)
	if err != nil {
		return ""
	}
	return s
}

// gitAvailable reports whether the git binary can be found.
func gitAvailable() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

// repoRoot resolves any directory to the root of its main work tree. Linked
// worktrees resolve to the same root as their parent repository, so callers
// can deduplicate repositories by this value.
func repoRoot(dir string) (string, error) {
	common, err := git(dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", errNotRepo
	}
	common = strings.TrimSpace(common)
	if common == "" {
		return "", errNotRepo
	}
	// A non-bare repository's common dir is <root>/.git.
	if filepath.Base(common) == ".git" {
		return filepath.Dir(common), nil
	}
	return common, nil
}

// gitEntry is one entry from `git worktree list --porcelain`.
type gitEntry struct {
	Path     string
	Head     string
	Branch   string // short name, empty when detached
	Bare     bool
	Detached bool
	Locked   bool
	Prunable bool
	Reason   string // prune or lock reason, when git reports one
}

// gitWorktrees lists every worktree attached to the repository at root.
func gitWorktrees(root string) ([]gitEntry, error) {
	out, err := git(root, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var list []gitEntry
	var cur *gitEntry
	flush := func() {
		if cur != nil {
			list = append(list, *cur)
			cur = nil
		}
	}
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			flush()
			cur = &gitEntry{Path: strings.TrimPrefix(line, "worktree ")}
		case cur == nil:
			continue
		case strings.HasPrefix(line, "HEAD "):
			cur.Head = strings.TrimPrefix(line, "HEAD ")
		case strings.HasPrefix(line, "branch "):
			cur.Branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		case line == "bare":
			cur.Bare = true
		case line == "detached":
			cur.Detached = true
		case strings.HasPrefix(line, "locked"):
			cur.Locked = true
			cur.Reason = strings.TrimSpace(strings.TrimPrefix(line, "locked"))
		case strings.HasPrefix(line, "prunable"):
			cur.Prunable = true
			cur.Reason = strings.TrimSpace(strings.TrimPrefix(line, "prunable"))
		}
	}
	flush()
	return list, nil
}

// baseBranch picks the integration branch a repository merges into. It prefers
// the remote HEAD, then falls back to conventional names.
func baseBranch(root string) string {
	if ref := gitOut(root, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); ref != "" {
		if name := strings.TrimPrefix(ref, "origin/"); hasBranch(root, name) {
			return name
		}
	}
	for _, name := range []string{"main", "master", "develop", "trunk"} {
		if hasBranch(root, name) {
			return name
		}
	}
	return ""
}

// hasBranch reports whether a local branch exists.
func hasBranch(root, name string) bool {
	if name == "" {
		return false
	}
	_, err := git(root, "show-ref", "--verify", "--quiet", "refs/heads/"+name)
	return err == nil
}

// aheadBehind counts commits unique to ref and unique to base.
func aheadBehind(root, base, ref string) (ahead, behind int, ok bool) {
	if base == "" || ref == "" {
		return 0, 0, false
	}
	out, err := git(root, "rev-list", "--left-right", "--count", base+"..."+ref)
	if err != nil {
		return 0, 0, false
	}
	fields := strings.Fields(out)
	if len(fields) != 2 {
		return 0, 0, false
	}
	behind, _ = strconv.Atoi(fields[0])
	ahead, _ = strconv.Atoi(fields[1])
	return ahead, behind, true
}

// gitStatus is the porcelain working-tree state of one worktree.
type gitStatus struct {
	Modified  []string
	Untracked []string
}

// Total counts every path with pending changes.
func (s gitStatus) Total() int { return len(s.Modified) + len(s.Untracked) }

// gitWorktreeStatus reports uncommitted work in a worktree, including untracked
// files (which `git worktree remove` would otherwise refuse to discard).
func gitWorktreeStatus(dir string) gitStatus {
	out := gitOut(dir, "status", "--porcelain=v1", "--untracked-files=all")
	var st gitStatus
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 4 {
			continue
		}
		path := strings.TrimSpace(line[3:])
		if idx := strings.Index(path, " -> "); idx >= 0 {
			path = path[idx+4:]
		}
		if strings.HasPrefix(line, "??") {
			st.Untracked = append(st.Untracked, path)
		} else {
			st.Modified = append(st.Modified, path)
		}
	}
	return st
}

// lastCommit returns the committer date of HEAD.
func lastCommit(dir string) time.Time {
	out := gitOut(dir, "log", "-1", "--format=%cI")
	if out == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, out)
	if err != nil {
		return time.Time{}
	}
	return t
}

// changedFiles lists files that differ between base and ref since they diverged.
func changedFiles(root, base, ref string) []string {
	out := gitOut(root, "diff", "--name-only", base+"..."+ref)
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}
