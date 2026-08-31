package worktree

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLooksLikeConfig(t *testing.T) {
	yes := []string{".env", ".env.local", "api/.env.production", ".npmrc",
		"config.local.json", ".claude/settings.local.json", ".tool-versions"}
	for _, p := range yes {
		if !looksLikeConfig(p) {
			t.Errorf("%q should count as local config", p)
		}
	}
	no := []string{"node_modules/x/index.js", "dist/app.js", "coverage.out",
		"README.md", "main.go", ".DS_Store"}
	for _, p := range no {
		if looksLikeConfig(p) {
			t.Errorf("%q must not be copied", p)
		}
	}
}

// TestPrepCopiesIgnoredConfigOnly is the whole promise: a fresh worktree gets
// the .env it needs, and none of the things it must not have.
func TestPrepCopiesIgnoredConfigOnly(t *testing.T) {
	root := newRepo(t)
	write(t, filepath.Join(root, ".gitignore"), ".env\n.env.*\nnode_modules/\ndist/\n")
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-m", "ignore local files")

	write(t, filepath.Join(root, ".env"), "DATABASE_URL=postgres://local\n")
	write(t, filepath.Join(root, ".env.local"), "TOKEN=abc\n")
	write(t, filepath.Join(root, "node_modules", "left-pad", "index.js"), "module.exports=1\n")
	write(t, filepath.Join(root, "dist", "app.js"), "console.log(1)\n")

	wt := filepath.Join(t.TempDir(), "feature")
	runGit(t, root, "worktree", "add", "-b", "feature", wt)

	items, err := Prep(wt, false)
	if err != nil {
		t.Fatal(err)
	}
	copied := map[string]bool{}
	for _, i := range items {
		if i.Copied {
			copied[i.Path] = true
		}
	}
	if !copied[".env"] || !copied[".env.local"] {
		t.Errorf("expected both env files, got %+v", items)
	}
	for path := range copied {
		if strings.HasPrefix(path, "node_modules") || strings.HasPrefix(path, "dist") {
			t.Errorf("copied something it must not: %s", path)
		}
	}
	body, err := os.ReadFile(filepath.Join(wt, ".env"))
	if err != nil || !strings.Contains(string(body), "postgres://local") {
		t.Errorf("the copied .env is wrong: %v %q", err, body)
	}
}

func TestPrepNeverOverwrites(t *testing.T) {
	root := newRepo(t)
	write(t, filepath.Join(root, ".gitignore"), ".env\n")
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-m", "ignore env")
	write(t, filepath.Join(root, ".env"), "FROM=main\n")

	wt := filepath.Join(t.TempDir(), "feature")
	runGit(t, root, "worktree", "add", "-b", "feature", wt)
	write(t, filepath.Join(wt, ".env"), "FROM=worktree\n")

	items, err := Prep(wt, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range items {
		if i.Path == ".env" && i.Copied {
			t.Fatal("an existing .env was overwritten")
		}
	}
	body, _ := os.ReadFile(filepath.Join(wt, ".env"))
	if !strings.Contains(string(body), "FROM=worktree") {
		t.Errorf("the worktree's own file was replaced: %q", body)
	}
}

func TestPrepRefusesTheMainCheckout(t *testing.T) {
	root := newRepo(t)
	if _, err := Prep(root, true); err == nil {
		t.Error("preparing the main checkout should be refused")
	}
}
