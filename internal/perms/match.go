// Package perms reads Claude Code permission rules and reports the ones that
// cannot do what their author intended: rules that can never match, rules the
// loader silently ignores, rules a broader rule already covers, and rules that
// carry a secret in their text.
//
// The matching rules implemented here follow Claude Code's documented
// behaviour. They are deliberately conservative: where the documentation is
// silent, this package declines to make a claim rather than guessing, because
// a linter that invents findings is worse than no linter.
package perms

import (
	"strings"
)

// separators split a Bash command into subcommands. Claude Code matches each
// subcommand independently, which is why a rule whose own text contains one of
// these can never match anything.
var separators = []string{"&&", "||", "|&", ";", "|", "&", "\n"}

// HasSeparator reports whether s contains a shell command separator.
func HasSeparator(s string) bool {
	for _, sep := range separators {
		if strings.Contains(s, sep) {
			return true
		}
	}
	return false
}

// SplitCommand breaks a command line into the subcommands Claude Code matches
// separately. Quoted regions are left alone, so "echo 'a && b'" stays one
// subcommand — splitting inside quotes would invent separators that the shell
// never sees and make a working rule look unused. A trailing separator with
// nothing after it makes the whole command unparseable, which Claude Code
// treats as "no allow rule applies"; ok reports that case.
func SplitCommand(cmd string) (parts []string, ok bool) {
	var buf strings.Builder
	var quote byte
	flush := func() {
		if p := strings.TrimSpace(buf.String()); p != "" {
			parts = append(parts, p)
		}
		buf.Reset()
	}
	for i := 0; i < len(cmd); i++ {
		c := cmd[i]
		if quote != 0 {
			buf.WriteByte(c)
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
			buf.WriteByte(c)
		case '\n', ';':
			flush()
		case '&', '|':
			// Consume the doubled forms and "|&" as a single separator.
			if i+1 < len(cmd) && (cmd[i+1] == c || (c == '|' && cmd[i+1] == '&')) {
				i++
			}
			flush()
		default:
			buf.WriteByte(c)
		}
	}
	flush()
	if len(parts) == 0 {
		return nil, false
	}
	// "npm test &&" ends on a separator: unparseable, so nothing is approved.
	if t := strings.TrimSpace(cmd); strings.HasSuffix(t, "&&") || strings.HasSuffix(t, "||") {
		return parts, false
	}
	return parts, true
}

// normalizePattern rewrites the ":*" suffix into the equivalent " *" form.
// The colon form is only recognised at the end of a pattern; anywhere else it
// is a literal character.
func normalizePattern(p string) string {
	if strings.HasSuffix(p, ":*") {
		return strings.TrimSuffix(p, ":*") + " *"
	}
	return p
}

// MatchBashPattern reports whether a single Bash pattern matches one
// subcommand. It does not split on separators; use MatchBash for a full
// command line.
func MatchBashPattern(pattern, sub string) bool {
	p := normalizePattern(pattern)
	if p == "*" || p == "" {
		return p == "*"
	}
	// A trailing " *" that is the pattern's only wildcard also matches the
	// bare command: Bash(ls *) matches "ls".
	if strings.Count(p, "*") == 1 && strings.HasSuffix(p, " *") {
		if sub == strings.TrimSuffix(p, " *") {
			return true
		}
	}
	return globMatch(p, sub)
}

// globMatch matches a pattern whose only metacharacter is "*", standing in for
// any run of text. Literal segments must appear in order, the first anchored at
// the start and the last at the end, without overlapping — which is what makes
// Bash(git log * main) miss "git log main".
func globMatch(pattern, s string) bool {
	segs := strings.Split(pattern, "*")
	if len(segs) == 1 {
		return pattern == s
	}
	if !strings.HasPrefix(s, segs[0]) {
		return false
	}
	s = s[len(segs[0]):]
	last := segs[len(segs)-1]
	mid := segs[1 : len(segs)-1]
	for _, seg := range mid {
		if seg == "" {
			continue
		}
		i := strings.Index(s, seg)
		if i < 0 {
			return false
		}
		s = s[i+len(seg):]
	}
	if last == "" {
		return true
	}
	return len(s) >= len(last) && strings.HasSuffix(s, last)
}

// MatchBash reports whether a pattern approves a whole command line. Every
// subcommand must match independently, so Bash(safe-cmd *) does not approve
// "safe-cmd && other-cmd".
func MatchBash(pattern, cmd string) bool {
	parts, ok := SplitCommand(cmd)
	if !ok {
		return false
	}
	for _, p := range parts {
		if !MatchBashPattern(pattern, p) {
			return false
		}
	}
	return true
}

// Covers reports whether pattern a approves everything pattern b approves, so
// b adds nothing. It is answered only for cases that can be decided by
// inspection: an exact duplicate, or a literal b that a already matches.
// Anything else returns false rather than a guess.
func Covers(a, b string) bool {
	an, bn := normalizePattern(a), normalizePattern(b)
	if an == bn {
		return true
	}
	if an == "*" {
		return true
	}
	if strings.Contains(bn, "*") {
		return false // comparing two wildcards is not decided here
	}
	return MatchBashPattern(an, bn)
}
