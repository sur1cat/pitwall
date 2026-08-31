package perms

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSettings drops a settings file into a temp dir and returns its source.
func writeSettings(t *testing.T, body string) Source {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "settings.local.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return Source{Path: p, Scope: ScopeLocal, Repo: dir}
}

func TestApplyPreservesEverythingItDoesNotTouch(t *testing.T) {
	src := writeSettings(t, `{
  "model": "opus",
  "permissions": {
    "allow": ["Bash(git status)", "Bash(a && b)", "Bash(git status)"],
    "deny": ["Bash(rm -rf *)"]
  },
  "env": {"FOO": "bar"},
  "statusLine": {"type": "command", "command": "pitwall statusline"}
}`)
	rules := Read(src)
	plan := PlanFixes(Lint(rules), Options{})
	if len(plan) != 1 {
		t.Fatalf("got %d plans, want 1", len(plan))
	}
	backupDir := t.TempDir()
	backup, err := Apply(plan[0], backupDir)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := os.Stat(backup); err != nil {
		t.Fatalf("no backup written: %v", err)
	}

	out, _ := os.ReadFile(src.Path)
	// Top-level key order is preserved, so the diff shows only the change.
	keys, _, err := topLevel(out)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"model", "permissions", "env", "statusLine"}
	for i := range want {
		if i >= len(keys) || keys[i] != want[i] {
			t.Fatalf("key order changed: got %v, want %v", keys, want)
		}
	}
	var doc struct {
		Model       string `json:"model"`
		Env         map[string]string
		Permissions struct {
			Allow []string
			Deny  []string
		}
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}
	if doc.Model != "opus" || doc.Env["FOO"] != "bar" {
		t.Error("untouched keys must survive verbatim")
	}
	// "Bash(a && b)" can never match and the third entry duplicates the first.
	if len(doc.Permissions.Allow) != 1 || doc.Permissions.Allow[0] != "Bash(git status)" {
		t.Errorf("allow = %v, want one Bash(git status)", doc.Permissions.Allow)
	}
	if len(doc.Permissions.Deny) != 1 {
		t.Errorf("deny list must be left alone, got %v", doc.Permissions.Deny)
	}
}

func TestFixNeverWidensAnAllowRule(t *testing.T) {
	// Write(path) is never consulted, so repairing it to Edit(path) would grant
	// access that is not granted today. The deny form only takes access away.
	src := writeSettings(t, `{
  "permissions": {
    "allow": ["Write(src/**)"],
    "deny": ["Write(secrets/**)"]
  }
}`)
	plans := PlanFixes(Lint(Read(src)), Options{})
	if len(plans) != 1 {
		t.Fatalf("got %d plans", len(plans))
	}
	p := plans[0]
	if len(p.Rewrite) != 1 || p.Rewrite[0].Rule.Kind != "deny" {
		t.Fatalf("only the deny rule may be rewritten, got %+v", p.Rewrite)
	}
	var sawAllow bool
	for _, f := range p.Report {
		if f.Rule.Kind == "allow" && f.Category == "never-consulted" {
			sawAllow = true
		}
	}
	if !sawAllow {
		t.Error("the broken allow rule must be reported rather than silently repaired")
	}

	if _, err := Apply(p, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(src.Path)
	if !strings.Contains(string(out), `"Write(src/**)"`) {
		t.Error("the allow rule must be left exactly as written")
	}
	if !strings.Contains(string(out), `"Edit(secrets/**)"`) {
		t.Error("the deny rule should have been narrowed to a form that is consulted")
	}
}

func TestFixLeavesOneOffsAloneUnlessAsked(t *testing.T) {
	body := `{"permissions": {"allow": ["Bash(npm run build)"]}}`
	src := writeSettings(t, body)
	if plans := PlanFixes(Lint(Read(src)), Options{}); len(plans) == 1 && len(plans[0].Remove) > 0 {
		t.Error("a working one-off rule must not be removed by default")
	}
	plans := PlanFixes(Lint(Read(src)), Options{DropOneOffs: true})
	if len(plans) != 1 || len(plans[0].Remove) != 1 {
		t.Fatalf("DropOneOffs should remove it, got %+v", plans)
	}
}

func TestApplyNeverTouchesManagedSettings(t *testing.T) {
	src := writeSettings(t, `{"permissions": {"allow": ["Bash(a && b)"]}}`)
	src.Scope = ScopeManaged
	rules := Read(src)
	for i := range rules {
		rules[i].Source.Scope = ScopeManaged
	}
	for _, p := range PlanFixes(Lint(rules), Options{}) {
		if !p.Empty() {
			t.Fatal("managed settings must never be edited")
		}
	}
}

func TestApplyRemovesTheRightDuplicate(t *testing.T) {
	// Two identical strings: only the later one is a duplicate, and removal is
	// by position so the surviving entry is the first.
	src := writeSettings(t, `{"permissions": {"allow": ["Bash(ls *)", "Bash(git *)", "Bash(ls *)"]}}`)
	plans := PlanFixes(Lint(Read(src)), Options{})
	if _, err := Apply(plans[0], t.TempDir()); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Permissions struct{ Allow []string }
	}
	out, _ := os.ReadFile(src.Path)
	_ = json.Unmarshal(out, &doc)
	if len(doc.Permissions.Allow) != 2 {
		t.Fatalf("allow = %v, want two entries", doc.Permissions.Allow)
	}
}

func TestApplyIsANoOpForAnEmptyPlan(t *testing.T) {
	src := writeSettings(t, `{"permissions": {"allow": ["Bash(git status)"]}}`)
	before, _ := os.ReadFile(src.Path)
	if b, err := Apply(Plan{Source: src}, t.TempDir()); err != nil || b != "" {
		t.Fatalf("empty plan should do nothing, got backup=%q err=%v", b, err)
	}
	after, _ := os.ReadFile(src.Path)
	if string(before) != string(after) {
		t.Error("an empty plan must not rewrite the file")
	}
}
