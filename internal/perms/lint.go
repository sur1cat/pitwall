package perms

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Finding is one rule that does not do what its author intended, with the
// documented reason it fails and what to do instead.
type Finding struct {
	Rule     Rule
	Category string
	Why      string
	Fix      string // what to write instead; empty means the rule should go
	Redacted bool   // the rule text carries a secret and must not be printed
}

// Categories in the order they are worth a user's attention: rules that do
// nothing, then rules that grant more than they appear to, then clutter.
var categoryOrder = []string{
	"secret", "ignored", "never-consulted", "fragment", "unmatchable",
	"wildcard-inside", "duplicate", "shadowed", "one-off",
}

// CategoryRank orders findings for display.
func CategoryRank(c string) int {
	for i, k := range categoryOrder {
		if k == c {
			return i
		}
	}
	return len(categoryOrder)
}

// contentFields are each tool's primary content field. A rule that names one,
// such as Bash(command:rm *), is ignored by Claude Code and warned about at
// startup, because a compound command would slip past it.
var contentFields = map[string]string{
	"Bash": "command", "PowerShell": "command",
	"Read": "file_path", "Edit": "file_path", "Write": "file_path",
	"Grep": "path", "Glob": "path",
	"NotebookEdit": "notebook_path", "WebFetch": "url",
}

// neverConsulted are tools whose path rules Claude Code accepts and then never
// looks at. File access is checked against Read and Edit rules only.
var neverConsulted = map[string]string{
	"Write": "Edit", "NotebookEdit": "Edit", "MultiEdit": "Edit", "Glob": "Read",
}

// secretAssign matches an environment assignment whose name reads like a
// credential. The value must be a literal: TOKEN=$GITLAB_TOKEN names a
// variable and leaks nothing, while TOKEN=eyJhbGciOi… is the secret itself.
var secretAssign = regexp.MustCompile(`(?i)(?:^|[\s;&|(])[A-Z_]*(?:TOKEN|API_?KEY|SECRET|PASSWORD|PASSWD|CREDENTIAL)=(\S{8,})`)

// secretHeader matches a bearer credential written into a request header.
var secretHeader = regexp.MustCompile(`(?i)authorization:\s*(?:bearer|basic)\s+(\S{8,})`)

// hasSecret reports whether a rule embeds a credential rather than referring
// to one. It is deliberately narrow: a false alarm here teaches the user to
// ignore the whole category.
func hasSecret(s string) bool {
	for _, re := range []*regexp.Regexp{secretAssign, secretHeader} {
		for _, m := range re.FindAllStringSubmatch(s, -1) {
			v := strings.Trim(m[1], `"'`)
			if v == "" || strings.HasPrefix(v, "$") || strings.HasPrefix(v, "*") {
				continue // a variable reference or a wildcard, not a secret
			}
			return true
		}
	}
	return false
}

// Lint reports every finding across the rules given. Rules are examined
// together so that shadowing and duplication can be seen.
func Lint(rules []Rule) []Finding {
	var out []Finding
	seen := map[string]Rule{}

	for _, r := range rules {
		if hasSecret(r.Raw) {
			out = append(out, Finding{Rule: r, Category: "secret", Redacted: true,
				Why: "the rule text contains what looks like a credential, stored in plaintext",
				Fix: ""})
			continue // never analyse a secret further, and never echo it
		}
		if f, ok := staticFinding(r); ok {
			out = append(out, f)
			continue
		}
		key := r.Kind + "\x00" + r.Source.Repo + "\x00" + normalizePattern(r.Raw)
		if first, dup := seen[key]; dup {
			out = append(out, Finding{Rule: r, Category: "duplicate",
				Why: fmt.Sprintf("already present in %s", first.Source.Short())})
			continue
		}
		seen[key] = r
	}

	out = append(out, shadowed(rules)...)
	out = append(out, oneOffs(rules)...)

	sort.SliceStable(out, func(i, j int) bool {
		if a, b := CategoryRank(out[i].Category), CategoryRank(out[j].Category); a != b {
			return a < b
		}
		return out[i].Rule.Raw < out[j].Rule.Raw
	})
	return out
}

// staticFinding reports the problems visible in a single rule.
func staticFinding(r Rule) (Finding, bool) {
	if r.Bare {
		if strings.HasPrefix(r.Tool, "mcp__") || !strings.Contains(r.Tool, "*") {
			return Finding{}, false
		}
		// An unanchored allow glob is skipped with a warning.
		if r.Kind == "allow" {
			return Finding{Rule: r, Category: "ignored",
				Why: "an allow glob must start with a literal mcp__<server>__ prefix, so Claude Code skips this rule",
				Fix: "name the server, e.g. mcp__github__get_*"}, true
		}
		return Finding{}, false
	}

	if strings.HasPrefix(r.Tool, "mcp__") {
		return Finding{Rule: r, Category: "ignored",
			Why: "Claude Code skips any mcp__ rule written with parentheses",
			Fix: r.Tool}, true
	}

	if field, ok := contentFields[r.Tool]; ok && strings.HasPrefix(r.Arg, field+":") {
		alt := r.Tool + "(" + strings.TrimPrefix(r.Arg, field+":") + ")"
		return Finding{Rule: r, Category: "ignored",
			Why: fmt.Sprintf("a rule on the %s field is ignored — a compound command would slip past it", field),
			Fix: alt}, true
	}

	if want, ok := neverConsulted[r.Tool]; ok && r.Arg != "" {
		return Finding{Rule: r, Category: "never-consulted",
			Why: fmt.Sprintf("file access is only checked against Read and Edit rules, so a %s path rule is never consulted", r.Tool),
			Fix: want + "(" + r.Arg + ")"}, true
	}

	if r.Tool == "Bash" && r.Arg != "" {
		if isFragment(r.Arg) {
			return Finding{Rule: r, Category: "fragment",
				Why: "not a command — a comment line or a piece of a multi-line block the approval dialog saved verbatim"}, true
		}
		if HasSeparator(r.Arg) {
			return Finding{Rule: r, Category: "unmatchable",
				Why: "Claude Code matches each subcommand separately, so a rule containing a shell separator can never match"}, true
		}
		if p := normalizePattern(r.Arg); strings.Contains(p, "*") && !isTrailingOnly(p) {
			return Finding{Rule: r, Category: "wildcard-inside",
				Why: "a * before the end stands in for arbitrary text, including flags like 'git -c' that run a program you name"}, true
		}
	}
	return Finding{}, false
}

// isFragment reports whether a rule is a leftover of the approval dialog
// rather than a command: a comment line, or a piece of a multi-line block
// stored with its newlines encoded. Neither can ever match a command Claude
// runs, so they sit in the file forever doing nothing.
func isFragment(arg string) bool {
	t := strings.TrimSpace(arg)
	if strings.Contains(t, "__NEW_LINE__") || strings.HasPrefix(t, "#") || t == "" {
		return true
	}
	// A rule that opens with a shell keyword is the middle of a block, not a
	// command: "elif echo ..." only exists inside an if that was split apart.
	first, _, _ := strings.Cut(t, " ")
	switch first {
	case "elif", "else", "fi", "do", "done", "then", "esac", "}", ")", ";;", "{":
		return true
	}
	return false
}

// isTrailingOnly reports whether the pattern's only wildcard is the trailing
// one, which is the safe shape: everything before it is matched as written.
func isTrailingOnly(p string) bool {
	return strings.Count(p, "*") == 1 && strings.HasSuffix(p, "*")
}

// shadowed finds rules that a broader rule of the same kind and scope already
// covers. Only Bash rules are compared, and only where coverage is decidable.
func shadowed(rules []Rule) []Finding {
	var out []Finding
	for i, r := range rules {
		if r.Tool != "Bash" || r.Arg == "" || r.Bare || hasSecret(r.Raw) {
			continue
		}
		for j, other := range rules {
			if i == j || other.Tool != "Bash" || other.Arg == "" || other.Kind != r.Kind {
				continue
			}
			if other.Source.Repo != r.Source.Repo && other.Source.Scope != ScopeUser {
				continue
			}
			if normalizePattern(other.Arg) == normalizePattern(r.Arg) {
				continue // duplicates are reported separately
			}
			if Covers(other.Arg, r.Arg) {
				out = append(out, Finding{Rule: r, Category: "shadowed",
					Why: fmt.Sprintf("Bash(%s) already covers it", other.Arg)})
				break
			}
		}
	}
	return out
}

// oneOffs finds literal Bash allow rules — no wildcard at all, so they can only
// ever match that exact command string again. These are what the permission
// dialog saves when a command is approved with its arguments baked in.
func oneOffs(rules []Rule) []Finding {
	var out []Finding
	for _, r := range rules {
		if r.Kind != "allow" || r.Tool != "Bash" || r.Arg == "" || r.Bare {
			continue
		}
		if strings.Contains(normalizePattern(r.Arg), "*") || HasSeparator(r.Arg) || isFragment(r.Arg) {
			continue
		}
		if hasSecret(r.Raw) {
			continue
		}
		out = append(out, Finding{Rule: r, Category: "one-off",
			Why: "no wildcard, so it only ever matches this exact command again"})
	}
	return out
}

// Prefixes groups one-off rules by the command prefix that would cover them,
// so the user can see which single rule replaces how many. Widening a
// permission is the user's call, so this only reports; it never rewrites.
func Prefixes(findings []Finding, minCount int) []Prefix {
	byPrefix := map[string][]string{}
	for _, f := range findings {
		if f.Category != "one-off" {
			continue
		}
		fields := strings.Fields(f.Rule.Arg)
		if len(fields) < 2 {
			continue
		}
		key := fields[0] + " " + fields[1]
		byPrefix[key] = append(byPrefix[key], f.Rule.Arg)
	}
	var out []Prefix
	for k, v := range byPrefix {
		if len(v) >= minCount {
			out = append(out, Prefix{Pattern: k + " *", Covers: len(v)})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Covers != out[j].Covers {
			return out[i].Covers > out[j].Covers
		}
		return out[i].Pattern < out[j].Pattern
	})
	return out
}

// Prefix is a candidate rule that would replace several one-off rules.
type Prefix struct {
	Pattern string `json:"pattern"`
	Covers  int    `json:"covers"`
}
