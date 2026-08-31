package perms

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestRehearsalOnRealSettings runs the fix against copies of the settings files
// actually on this machine and checks the invariant that matters: the rules
// left behind are exactly the original rules, minus the ones the plan removed,
// with the planned rewrites applied — and every other key survives byte for
// byte. It is skipped when there are no settings files to read.
func TestRehearsalOnRealSettings(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	var dirs []string
	for _, base := range []string{"Desktop"} {
		for _, pat := range []string{"*", "*/*", "*/*/*"} {
			m, _ := filepath.Glob(filepath.Join(home, base, pat))
			dirs = append(dirs, m...)
		}
	}
	sources := Discover(filepath.Join(home, ".claude"), dirs)
	if len(sources) == 0 {
		t.Skip("no settings files on this machine")
	}

	checked := 0
	for _, src := range sources {
		original, err := os.ReadFile(src.Path)
		if err != nil {
			continue
		}
		rules := Read(src)
		if len(rules) == 0 {
			continue
		}
		// Work on a copy: the rehearsal must never touch the real file.
		copyPath := filepath.Join(t.TempDir(), "settings.json")
		if err := os.WriteFile(copyPath, original, 0o600); err != nil {
			t.Fatal(err)
		}
		copySrc := src
		copySrc.Path = copyPath
		copyRules := Read(copySrc)

		plans := PlanFixes(Lint(copyRules), Options{})
		var plan Plan
		for _, p := range plans {
			if p.Source.Path == copyPath {
				plan = p
			}
		}
		if plan.Empty() {
			continue
		}
		if _, err := Apply(plan, t.TempDir()); err != nil {
			t.Fatalf("%s: Apply: %v", src.Short(), err)
		}
		checked++

		// Build what should be left, from the original lists.
		removed := map[string]bool{}
		for _, f := range plan.Remove {
			removed[ruleKey(f.Rule)] = true
		}
		rewritten := map[string]string{}
		for _, w := range plan.Rewrite {
			rewritten[ruleKey(w.Rule)] = w.To
		}
		want := map[string][]string{}
		for _, kind := range []string{"allow", "deny", "ask"} {
			for _, r := range copyRules {
				if r.Kind != kind {
					continue
				}
				k := ruleKey(r)
				if removed[k] {
					continue
				}
				if to, ok := rewritten[k]; ok {
					want[kind] = append(want[kind], to)
					continue
				}
				want[kind] = append(want[kind], r.Raw)
			}
		}

		var got struct {
			Permissions map[string][]string `json:"permissions"`
		}
		out, _ := os.ReadFile(copyPath)
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatalf("%s: result is not valid JSON: %v", src.Short(), err)
		}
		for _, kind := range []string{"allow", "deny", "ask"} {
			g, w := got.Permissions[kind], want[kind]
			if len(g) != len(w) {
				t.Errorf("%s: %s has %d rules, want %d", src.Short(), kind, len(g), len(w))
				continue
			}
			for i := range w {
				if g[i] != w[i] {
					t.Errorf("%s: %s[%d] changed unexpectedly", src.Short(), kind, i)
					break
				}
			}
		}

		// Every top-level key other than permissions must survive untouched.
		ok, ov, err := topLevel(original)
		if err != nil {
			continue
		}
		nk, nv, err := topLevel(out)
		if err != nil {
			t.Fatalf("%s: rewritten file does not parse: %v", src.Short(), err)
		}
		if len(ok) != len(nk) {
			t.Errorf("%s: top-level keys went from %d to %d", src.Short(), len(ok), len(nk))
			continue
		}
		for i := range ok {
			if ok[i] != nk[i] {
				t.Errorf("%s: key order changed at %d: %q vs %q", src.Short(), i, ok[i], nk[i])
				break
			}
			if ok[i] == "permissions" {
				continue
			}
			var a, b any
			_ = json.Unmarshal(ov[i], &a)
			_ = json.Unmarshal(nv[i], &b)
			if !jsonEqual(a, b) {
				t.Errorf("%s: key %q was modified", src.Short(), ok[i])
			}
		}
	}
	t.Logf("rehearsed %d settings files with pending changes", checked)
}

func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
