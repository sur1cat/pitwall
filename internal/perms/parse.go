package perms

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// Scope is where a settings file sits in Claude Code's precedence order.
// Managed settings win over everything, including command line arguments.
type Scope int

const (
	ScopeUser Scope = iota
	ScopeShared
	ScopeLocal
	ScopeManaged
)

func (s Scope) String() string {
	switch s {
	case ScopeManaged:
		return "managed"
	case ScopeLocal:
		return "local"
	case ScopeShared:
		return "shared"
	default:
		return "user"
	}
}

// Source is one settings file that contributed rules.
type Source struct {
	Path  string
	Scope Scope
	Repo  string // the project the file belongs to, empty for user and managed
}

// Rule is a single permission entry as written.
type Rule struct {
	Raw    string // exactly as it appears in the file
	Kind   string // allow, deny or ask
	Tool   string // the tool name, or the whole entry when it is a bare name
	Arg    string // the text inside the parentheses
	Bare   bool   // written without parentheses
	Source Source
	Index  int // position within its list, for stable ordering
}

var ruleRE = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_*]*)\((.*)\)$`)

// ParseRule splits "Tool(arg)" into its parts. An entry with no parentheses is
// a bare tool name, which in a deny rule removes the tool from Claude's context
// entirely.
func ParseRule(raw string) (tool, arg string, bare bool) {
	if m := ruleRE.FindStringSubmatch(raw); m != nil {
		return m[1], m[2], false
	}
	return raw, "", true
}

// settingsDoc is the slice of a settings file this package reads.
type settingsDoc struct {
	Permissions struct {
		Allow []string `json:"allow"`
		Deny  []string `json:"deny"`
		Ask   []string `json:"ask"`
	} `json:"permissions"`
}

// Read parses one settings file into rules. A file that does not exist or does
// not parse yields nothing rather than an error: a broken project file should
// not stop the audit of every other one.
func Read(src Source) []Rule {
	raw, err := os.ReadFile(src.Path)
	if err != nil {
		return nil
	}
	var doc settingsDoc
	if json.Unmarshal(raw, &doc) != nil {
		return nil
	}
	var out []Rule
	add := func(kind string, list []string) {
		for i, r := range list {
			tool, arg, bare := ParseRule(r)
			out = append(out, Rule{Raw: r, Kind: kind, Tool: tool, Arg: arg, Bare: bare, Source: src, Index: i})
		}
	}
	add("allow", doc.Permissions.Allow)
	add("deny", doc.Permissions.Deny)
	add("ask", doc.Permissions.Ask)
	return out
}

// ManagedPath is where an administrator's settings live on this platform.
func ManagedPath() string {
	switch runtime.GOOS {
	case "darwin":
		return "/Library/Application Support/ClaudeCode/managed-settings.json"
	case "windows":
		return filepath.Join(os.Getenv("PROGRAMDATA"), "ClaudeCode", "managed-settings.json")
	default:
		return "/etc/claude-code/managed-settings.json"
	}
}

// Discover finds every settings file that carries permission rules: the
// managed file, the user file, and the shared and local files of each repo
// passed in.
func Discover(claudeDir string, repos []string) []Source {
	var out []Source
	if p := ManagedPath(); exists(p) {
		out = append(out, Source{Path: p, Scope: ScopeManaged})
	}
	if p := filepath.Join(claudeDir, "settings.json"); exists(p) {
		out = append(out, Source{Path: p, Scope: ScopeUser})
	}
	seen := map[string]bool{}
	for _, repo := range repos {
		if repo == "" || seen[repo] {
			continue
		}
		seen[repo] = true
		for _, c := range []struct {
			name  string
			scope Scope
		}{
			{"settings.json", ScopeShared},
			{"settings.local.json", ScopeLocal},
		} {
			p := filepath.Join(repo, ".claude", c.name)
			if exists(p) {
				out = append(out, Source{Path: p, Scope: c.scope, Repo: repo})
			}
		}
	}
	return out
}

func exists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// Short renders a source path for display, collapsing the home directory.
func (s Source) Short() string {
	p := s.Path
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home) {
		p = "~" + strings.TrimPrefix(p, home)
	}
	return p
}
