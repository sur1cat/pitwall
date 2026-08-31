package perms

import "testing"

// The cases below are Claude Code's own documented matching table, kept
// verbatim so a change in behaviour shows up as a failing test rather than as
// wrong advice printed to a user.
func TestMatchBashPatternAgainstTheDocumentedTable(t *testing.T) {
	cases := []struct {
		pattern string
		yes     []string
		no      []string
	}{
		{"npm run build", []string{"npm run build"}, []string{"npm run build --watch"}},
		{"npm run *", []string{"npm run build", "npm run test --watch", "npm run"}, []string{"npm install"}},
		{"git log * main", []string{"git log --oneline main", "git log -5 main", "git log --output=<file> main"},
			[]string{"git log main", "git push origin main"}},
		{"git * main", []string{"git merge main", "git push origin main", "git -c core.fsmonitor=<script> diff main"},
			[]string{"git log"}},
		{"* --version", []string{"node --version", "bash -c 'echo hi' --version"}, []string{"node -v"}},
		{"ls *", []string{"ls -la", "ls"}, []string{"lsof"}},
		{"ls*", []string{"ls -la", "lsof"}, nil},
		{"* --help *", []string{"npm --help x"}, []string{"npm --help"}},
		{"*", []string{"anything at all", "rm -rf /"}, nil},
		// ":*" is the same as a trailing " *", and only at the end.
		{"ls:*", []string{"ls -la", "ls"}, []string{"lsof"}},
		{"git:* push", nil, []string{"git push", "git origin push"}},
	}
	for _, c := range cases {
		for _, cmd := range c.yes {
			if !MatchBashPattern(c.pattern, cmd) {
				t.Errorf("Bash(%s) should match %q", c.pattern, cmd)
			}
		}
		for _, cmd := range c.no {
			if MatchBashPattern(c.pattern, cmd) {
				t.Errorf("Bash(%s) should NOT match %q", c.pattern, cmd)
			}
		}
	}
}

func TestMatchBashRequiresEverySubcommand(t *testing.T) {
	// "Claude Code is aware of shell operators, so a rule like Bash(safe-cmd *)
	// won't give it permission to run the command safe-cmd && other-cmd."
	if MatchBash("safe-cmd *", "safe-cmd && other-cmd") {
		t.Error("a rule must match each subcommand independently")
	}
	if !MatchBash("git *", "git add . && git status") {
		t.Error("a rule that covers both subcommands should match")
	}
	// A dangling separator is unparseable, so nothing is approved.
	if MatchBash("npm *", "npm test &&") {
		t.Error("an unparseable command must not be approved")
	}
}

func TestSplitCommandRespectsQuotes(t *testing.T) {
	parts, ok := SplitCommand(`echo "a && b"`)
	if !ok || len(parts) != 1 || parts[0] != `echo "a && b"` {
		t.Errorf("quoted separators must not split: got %q ok=%v", parts, ok)
	}
	parts, _ = SplitCommand("a && b | c ; d")
	if len(parts) != 4 {
		t.Errorf("got %d subcommands, want 4: %q", len(parts), parts)
	}
	if _, ok := SplitCommand("   "); ok {
		t.Error("an empty command is not parseable")
	}
}

func TestHasSeparator(t *testing.T) {
	for _, s := range []string{"a && b", "a || b", "a; b", "a | b", "a & b", "a\nb"} {
		if !HasSeparator(s) {
			t.Errorf("HasSeparator(%q) = false", s)
		}
	}
	for _, s := range []string{"git status", "npm run build", "ls -la"} {
		if HasSeparator(s) {
			t.Errorf("HasSeparator(%q) = true", s)
		}
	}
}

func TestCoversOnlyClaimsWhatItCanDecide(t *testing.T) {
	if !Covers("npm run *", "npm run build") {
		t.Error("a wildcard rule covers a literal it matches")
	}
	if !Covers("*", "anything") {
		t.Error("Bash(*) covers everything")
	}
	if !Covers("git status", "git status") {
		t.Error("a rule covers itself")
	}
	if Covers("npm run build", "npm run *") {
		t.Error("a literal does not cover a wildcard")
	}
	// Two wildcards are not compared: no claim beats a wrong claim.
	if Covers("git *", "git log *") {
		t.Error("wildcard-vs-wildcard should not be decided")
	}
}
