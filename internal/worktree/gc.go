package worktree

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sur1cat/pitwall/internal/claude"
)

// GCOptions controls what `pitwall tree gc` is allowed to do.
type GCOptions struct {
	// Salvage commits uncommitted work and archives a patch before removal.
	// On by default; disabling it means STRANDED worktrees are skipped unless
	// Force is also set.
	Salvage bool
	// DryRun plans the work without touching anything.
	DryRun bool
	// Force permits discarding uncommitted work when Salvage is off.
	Force bool
	// PruneBranches deletes branches that are fully merged into the base.
	PruneBranches bool
	// IncludeAhead also removes worktrees holding unmerged commits.
	IncludeAhead bool
	// SalvageOnly rescues stranded work but leaves every worktree in place.
	SalvageOnly bool
}

// Action is what happened (or would happen) to one worktree.
type Action struct {
	Path          string `json:"path"`
	Branch        string `json:"branch,omitempty"`
	State         State  `json:"state"`
	Salvaged      bool   `json:"salvaged"`
	SalvagedFiles int    `json:"salvaged_files,omitempty"`
	SalvageRef    string `json:"salvage_ref,omitempty"`
	SalvageCommit string `json:"salvage_commit,omitempty"`
	PatchPath     string `json:"patch_path,omitempty"`
	Removed       bool   `json:"removed"`
	BranchDeleted bool   `json:"branch_deleted"`
	FreedBytes    int64  `json:"freed_bytes"`
	Skipped       string `json:"skipped,omitempty"`
	Error         string `json:"error,omitempty"`
}

// Plan selects the worktrees gc would act on, in removal order.
func Plan(repos []*Repo, opt GCOptions) []*Worktree {
	var out []*Worktree
	for _, repo := range repos {
		for _, wt := range repo.Worktrees {
			switch wt.State {
			case StateDead, StateOrphan:
				out = append(out, wt)
			case StateStranded:
				if opt.Salvage || opt.Force {
					out = append(out, wt)
				}
			case StateAhead:
				if opt.IncludeAhead {
					out = append(out, wt)
				}
			}
		}
	}
	return out
}

// GC salvages and removes the planned worktrees. ACTIVE and PRIMARY worktrees
// are never touched.
func GC(repos []*Repo, opt GCOptions) []Action {
	var actions []Action
	prune := map[string]bool{}

	for _, wt := range Plan(repos, opt) {
		act := Action{Path: wt.Path, Branch: wt.Branch, State: wt.State, FreedBytes: wt.SizeBytes}

		if wt.State == StateOrphan {
			if opt.SalvageOnly {
				continue
			}
			prune[wt.RepoRoot] = true
			act.Removed = !opt.DryRun
			actions = append(actions, act)
			continue
		}
		if wt.Locked {
			act.Skipped = "worktree is locked"
			actions = append(actions, act)
			continue
		}

		if wt.Dirty() > 0 {
			if !opt.Salvage {
				if !opt.Force {
					act.Skipped = "uncommitted work and --no-salvage without --force"
					actions = append(actions, act)
					continue
				}
			} else {
				if opt.DryRun {
					act.Salvaged, act.SalvagedFiles = true, wt.Dirty()
				} else {
					ref, sha, patch, err := salvage(wt)
					if err != nil {
						act.Error = fmt.Sprintf("salvage failed: %v", err)
						actions = append(actions, act)
						continue // never remove work we could not save
					}
					act.Salvaged, act.SalvagedFiles = true, wt.Dirty()
					act.SalvageRef, act.SalvageCommit, act.PatchPath = ref, sha, patch
				}
			}
		}

		if opt.SalvageOnly {
			actions = append(actions, act)
			continue
		}
		if opt.DryRun {
			act.Removed = true
			actions = append(actions, act)
			continue
		}

		args := []string{"worktree", "remove", wt.Path}
		if opt.Force && !act.Salvaged {
			args = append(args, "--force")
		}
		if _, err := git(wt.RepoRoot, args...); err != nil {
			act.Error = err.Error()
			actions = append(actions, act)
			continue
		}
		act.Removed = true

		if opt.PruneBranches && wt.Branch != "" && wt.Branch != wt.Base {
			// -d refuses to delete unmerged branches, which is the safety we
			// want: a salvage commit makes the branch unmerged on purpose.
			if _, err := git(wt.RepoRoot, "branch", "-d", wt.Branch); err == nil {
				act.BranchDeleted = true
			}
		}
		actions = append(actions, act)
	}

	if !opt.DryRun {
		for root := range prune {
			_, _ = git(root, "worktree", "prune")
		}
	}
	return actions
}

// salvage commits everything pending in a worktree, pins the commit behind a
// ref so git never garbage-collects it, and writes a standalone patch file.
func salvage(wt *Worktree) (ref, sha, patch string, err error) {
	if _, err = git(wt.Path, "add", "-A"); err != nil {
		return "", "", "", fmt.Errorf("git add: %w", err)
	}
	msg := fmt.Sprintf("pitwall: salvage %d uncommitted file(s) from %s", wt.Dirty(), wt.Name)
	if _, err = git(wt.Path,
		"-c", "user.name=pitwall", "-c", "user.email=pitwall@localhost",
		"commit", "--no-verify", "--no-gpg-sign", "-m", msg,
	); err != nil {
		return "", "", "", fmt.Errorf("git commit: %w", err)
	}
	sha, err = git(wt.Path, "rev-parse", "HEAD")
	if err != nil {
		return "", "", "", fmt.Errorf("git rev-parse: %w", err)
	}

	ref = "refs/pitwall/salvage/" + slug(filepath.Base(wt.RepoRoot)) + "/" + slug(wt.Name)
	if _, err := git(wt.RepoRoot, "update-ref", ref, sha); err != nil {
		// A missing ref is survivable — the patch file below is the real backup.
		ref = ""
	}

	if body, err := git(wt.Path, "format-patch", "-1", "--stdout", sha); err == nil && body != "" {
		dir := filepath.Join(claude.Dir(), "pitwall", "salvage")
		if err := os.MkdirAll(dir, 0o755); err == nil {
			name := fmt.Sprintf("%s_%s_%s.patch",
				slug(filepath.Base(wt.RepoRoot)), slug(wt.Name), time.Now().Format("20060102-150405"))
			p := filepath.Join(dir, name)
			if os.WriteFile(p, []byte(body+"\n"), 0o644) == nil {
				patch = p
			}
		}
	}
	return ref, sha, patch, nil
}

func slug(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "unnamed"
	}
	return out
}
