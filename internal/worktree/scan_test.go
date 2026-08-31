package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"-C", dir,
		"-c", "user.name=test", "-c", "user.email=test@localhost",
		"-c", "commit.gpgsign=false", "-c", "init.defaultBranch=main"}, args...)
	out, err := exec.Command("git", full...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// newRepo creates a repository with one commit on main.
func newRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	runGit(t, root, "init", "--initial-branch=main")
	write(t, filepath.Join(root, "README.md"), "hello\n")
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-m", "init")
	return root
}

func TestMigrationNumber(t *testing.T) {
	cases := []struct {
		path string
		want string
		ok   bool
	}{
		{"migrations/versions/0059_tenant_settings.py", "59", true},
		{"db/migrate/20260101120000_add_users.rb", "20260101120000", true},
		{"internal/migrations/000297_add_fines.up.sql", "297", true},
		{"src/app/main.go", "", false},
		{"migrations/README.md", "", false},
		{"migrations/12_short.sql", "", false}, // too short to be a sequence
	}
	for _, c := range cases {
		got, ok := migrationNumber(c.path)
		if ok != c.ok || got != c.want {
			t.Errorf("migrationNumber(%q) = (%q,%v), want (%q,%v)", c.path, got, ok, c.want, c.ok)
		}
	}
}

// TestCollisions builds two unmerged branches that both add migration 0059
// and both edit the same source file, then asserts pitwall reports it.
func TestCollisions(t *testing.T) {
	root := newRepo(t)
	write(t, filepath.Join(root, "app.py"), "base\n")
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-m", "app")

	for _, b := range []struct{ branch, migration, body string }{
		{"feat/a", "migrations/versions/0059_add_teams.py", "a\n"},
		{"feat/b", "migrations/versions/0059_add_settings.py", "b\n"},
	} {
		runGit(t, root, "checkout", "-b", b.branch, "main")
		write(t, filepath.Join(root, b.migration), b.body)
		write(t, filepath.Join(root, "app.py"), "base\n"+b.body)
		runGit(t, root, "add", ".")
		runGit(t, root, "commit", "-m", "work on "+b.branch)
	}
	runGit(t, root, "checkout", "main")

	res, err := Run(Options{Roots: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Repos) != 1 {
		t.Fatalf("expected 1 repo, got %d", len(res.Repos))
	}
	// Collisions inspects branches; attach them via worktrees so the scan sees them.
	repo := res.Repos[0]
	for _, b := range []string{"feat/a", "feat/b"} {
		repo.Worktrees = append(repo.Worktrees, &Worktree{
			RepoRoot: root, Path: filepath.Join(root, "wt-"+strings.ReplaceAll(b, "/", "-")),
			Branch: b, Base: repo.Base, Ahead: 1, HasCounts: true, State: StateAhead,
		})
	}

	c := Collisions(repo)
	if len(c.Migrations) != 1 {
		t.Fatalf("expected 1 migration clash, got %d (%+v)", len(c.Migrations), c.Migrations)
	}
	if c.Migrations[0].Number != "59" {
		t.Errorf("clash number = %q, want 59", c.Migrations[0].Number)
	}
	if len(c.Overlaps) != 1 || len(c.Overlaps[0].Files) == 0 {
		t.Fatalf("expected an overlap on app.py, got %+v", c.Overlaps)
	}
	found := false
	for _, f := range c.Overlaps[0].Files {
		if f == "app.py" {
			found = true
		}
	}
	if !found {
		t.Errorf("app.py missing from overlap files: %v", c.Overlaps[0].Files)
	}
}

// TestClassifyStranded asserts that a merged-but-dirty worktree is STRANDED —
// exactly the case a naive "remove merged worktrees" tool would delete.
func TestClassifyStranded(t *testing.T) {
	root := newRepo(t)
	wt := filepath.Join(t.TempDir(), "feature")
	runGit(t, root, "worktree", "add", "-b", "feature", wt)
	write(t, filepath.Join(wt, "unsaved.py"), "work in progress\n")

	res, err := Run(Options{Roots: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	got := findWorktree(t, res, wt)
	if got.State != StateStranded {
		t.Fatalf("state = %s, want STRANDED (dirty=%d)", got.State, got.Dirty())
	}
	if got.Dirty() != 1 {
		t.Errorf("dirty = %d, want 1", got.Dirty())
	}
}

// TestSalvageThenRemove is the core promise: uncommitted work survives gc.
func TestSalvageThenRemove(t *testing.T) {
	claudeDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claudeDir)

	root := newRepo(t)
	wt := filepath.Join(t.TempDir(), "feature")
	runGit(t, root, "worktree", "add", "-b", "feature", wt)
	const secret = "the work that must not be lost\n"
	write(t, filepath.Join(wt, "important.py"), secret)

	res, err := Run(Options{Roots: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	if got := findWorktree(t, res, wt); got.State != StateStranded {
		t.Fatalf("precondition: state = %s, want STRANDED", got.State)
	}

	scanned := findWorktree(t, res, wt)
	actions := GC(res.Repos, GCOptions{Salvage: true})
	var act *Action
	for i := range actions {
		if Resolve(actions[i].Path) == Resolve(scanned.Path) {
			act = &actions[i]
		}
	}
	if act == nil {
		t.Fatalf("no action recorded for %s (got %+v)", wt, actions)
	}
	if act.Error != "" {
		t.Fatalf("gc error: %s", act.Error)
	}
	if !act.Salvaged {
		t.Fatal("work was removed without being salvaged")
	}
	if !act.Removed {
		t.Fatal("worktree was not removed")
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("worktree directory still exists: %v", err)
	}

	// The commit must still be reachable through the salvage ref.
	if act.SalvageRef == "" {
		t.Fatal("no salvage ref recorded")
	}
	blob := runGit(t, root, "show", act.SalvageRef+":important.py")
	if strings.TrimSpace(blob) != strings.TrimSpace(secret) {
		t.Errorf("salvage ref content = %q, want %q", blob, secret)
	}

	// And the patch archive must be replayable on its own.
	if act.PatchPath == "" {
		t.Fatal("no patch archived")
	}
	patch, err := os.ReadFile(act.PatchPath)
	if err != nil {
		t.Fatalf("reading patch: %v", err)
	}
	if !strings.Contains(string(patch), "the work that must not be lost") {
		t.Error("patch does not contain the salvaged content")
	}

	fresh := newRepo(t)
	cmd := exec.Command("git", "-C", fresh,
		"-c", "user.name=test", "-c", "user.email=test@localhost", "am", act.PatchPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git am failed to replay the salvage patch: %v\n%s", err, out)
	}
	restored, err := os.ReadFile(filepath.Join(fresh, "important.py"))
	if err != nil {
		t.Fatalf("restored file missing: %v", err)
	}
	if string(restored) != secret {
		t.Errorf("restored content = %q, want %q", restored, secret)
	}
}

// TestGCNeverTouchesUnmerged guards the other direction: a clean branch with
// unique commits must not be removed by default.
func TestGCNeverTouchesUnmerged(t *testing.T) {
	root := newRepo(t)
	wt := filepath.Join(t.TempDir(), "feature")
	runGit(t, root, "worktree", "add", "-b", "feature", wt)
	write(t, filepath.Join(wt, "new.py"), "committed work\n")
	runGit(t, wt, "add", ".")
	runGit(t, wt, "commit", "-m", "real work")

	res, err := Run(Options{Roots: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	got := findWorktree(t, res, wt)
	if got.State != StateAhead {
		t.Fatalf("state = %s, want AHEAD", got.State)
	}
	if plan := Plan(res.Repos, GCOptions{Salvage: true}); len(plan) != 0 {
		t.Fatalf("gc planned to remove unmerged work: %+v", plan[0])
	}
	if plan := Plan(res.Repos, GCOptions{Salvage: true, IncludeAhead: true}); len(plan) != 1 {
		t.Fatalf("--include-ahead should select it, got %d", len(plan))
	}
}

func findWorktree(t *testing.T, res Result, path string) *Worktree {
	t.Helper()
	for _, repo := range res.Repos {
		for _, wt := range repo.Worktrees {
			if Resolve(wt.Path) == Resolve(path) {
				return wt
			}
		}
	}
	t.Fatalf("worktree %s not found in scan", path)
	return nil
}
