// Package primer drafts a project starting point from what past Claude Code
// sessions already discovered about a repository — the commands that get run,
// the files that get opened first, where the code actually lives.
package primer

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/sur1cat/pitwall/internal/claude"
)

// Draft is the material gathered for one repository.
type Draft struct {
	Repo      string    `json:"repo"`
	Name      string    `json:"name"`
	Sessions  int       `json:"sessions"`
	ToolCalls int       `json:"tool_calls"`
	First     time.Time `json:"first"`
	Last      time.Time `json:"last"`
	Commands  []Count   `json:"commands"`
	Files     []Count   `json:"files"`
	Dirs      []Count   `json:"dirs"`
	Branches  []Count   `json:"branches"`
}

// Count is one observed thing and how often it appeared.
type Count struct {
	Name string `json:"name"`
	N    int    `json:"n"`
}

type line struct {
	Type      string `json:"type"`
	CWD       string `json:"cwd"`
	SessionID string `json:"sessionId"`
	GitBranch string `json:"gitBranch"`
	Timestamp string `json:"timestamp"`
	Message   struct {
		Content []struct {
			Type  string `json:"type"`
			Name  string `json:"name"`
			Input struct {
				Command  string `json:"command"`
				FilePath string `json:"file_path"`
				Pattern  string `json:"pattern"`
			} `json:"input"`
		} `json:"content"`
	} `json:"message"`
}

// commandName is what a plausible executable looks like, which filters out
// heredoc bodies and quoting artefacts.
var commandName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9._+-]*$`)

// subcommandName is what a plausible subcommand looks like.
var subcommandName = regexp.MustCompile(`^[a-z][a-z0-9:_-]*$`)

// noise are commands, and shell keywords, that say nothing about how a
// project is built or run.
var noise = map[string]bool{
	"done": true, "do": true, "fi": true, "then": true, "else": true, "elif": true,
	"for": true, "while": true, "if": true, "case": true, "esac": true, "source": true,
	"set": true, "unset": true, "eval": true, "exit": true, "return": true, "read": true,
	// language and SQL keywords that leak in from heredoc bodies
	"func": true, "type": true, "var": true, "const": true, "import": true,
	"package": true, "class": true, "def": true, "print": true, "select": true,
	"insert": true, "update": true, "delete": true, "from": true, "where": true,
	"begin": true, "end": true, "commit": true, "rollback": true, "with": true,
	"ls": true, "cat": true, "echo": true, "pwd": true, "cd": true, "head": true,
	"tail": true, "wc": true, "which": true, "true": true, "sleep": true, "mkdir": true,
	"rm": true, "cp": true, "mv": true, "chmod": true, "touch": true, "export": true,
	"find": true, "grep": true, "rg": true, "sed": true, "awk": true, "sort": true,
	"uniq": true, "tr": true, "cut": true, "xargs": true, "test": true, "printf": true,
}

// Gather scans local transcripts for everything learned about one repository.
func Gather(repo string) (Draft, error) {
	repo = filepath.Clean(repo)
	if abs, err := filepath.Abs(repo); err == nil {
		repo = abs
	}
	d := Draft{Repo: repo, Name: filepath.Base(repo)}
	root := filepath.Join(claude.Dir(), "projects")

	cmds, files, dirs, branches := map[string]int{}, map[string]int{}, map[string]int{}, map[string]int{}
	sessions := map[string]bool{}

	err := filepath.WalkDir(root, func(p string, e os.DirEntry, err error) error {
		if err != nil || e.IsDir() || !strings.HasSuffix(p, ".jsonl") {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return nil
		}
		defer f.Close()
		r := bufio.NewReaderSize(f, 1<<20)
		for {
			raw, err := r.ReadBytes('\n')
			if len(raw) > 0 && raw[0] == '{' {
				var l line
				if json.Unmarshal(raw, &l) == nil && l.Type == "assistant" && within(l.CWD, repo) {
					sessions[l.SessionID] = true
					if l.GitBranch != "" {
						branches[l.GitBranch]++
					}
					if t, e := time.Parse(time.RFC3339, l.Timestamp); e == nil {
						if d.First.IsZero() || t.Before(d.First) {
							d.First = t
						}
						if t.After(d.Last) {
							d.Last = t
						}
					}
					for _, b := range l.Message.Content {
						if b.Type != "tool_use" {
							continue
						}
						d.ToolCalls++
						switch b.Name {
						case "Bash":
							if c := normalizeCommand(b.Input.Command); c != "" {
								cmds[c]++
							}
						case "Read", "Edit", "Write":
							if rel := relative(b.Input.FilePath, repo); rel != "" && !strings.HasPrefix(rel, ".claude/") {
								files[rel]++
								if dir := filepath.Dir(rel); dir != "." {
									dirs[topTwo(dir)]++
								}
							}
						}
					}
				}
			}
			if err != nil {
				return nil
			}
		}
	})
	if err != nil {
		return d, err
	}

	d.Sessions = len(sessions)
	d.Commands = top(cmds, 14, 2)
	d.Files = top(files, 12, 3)
	d.Dirs = top(dirs, 10, 3)
	d.Branches = top(branches, 6, 2)
	return d, nil
}

func within(path, dir string) bool {
	if path == "" {
		return false
	}
	path = filepath.Clean(path)
	return path == dir || strings.HasPrefix(path, dir+string(filepath.Separator))
}

func relative(path, repo string) string {
	if path == "" || !within(path, repo) {
		return ""
	}
	rel, err := filepath.Rel(repo, filepath.Clean(path))
	if err != nil {
		return ""
	}
	return filepath.ToSlash(rel)
}

func topTwo(dir string) string {
	parts := strings.Split(filepath.ToSlash(dir), "/")
	if len(parts) > 2 {
		parts = parts[:2]
	}
	return strings.Join(parts, "/")
}

// normalizeCommand reduces a shell command to the tool and subcommand that
// identify it, so "cd x && go test ./... -run Foo" becomes "go test".
func normalizeCommand(cmd string) string {
	// Only the first line matters: anything after it is a heredoc body or a
	// continuation, and treating those as commands produces nonsense.
	if i := strings.IndexByte(cmd, '\n'); i >= 0 {
		cmd = cmd[:i]
	}
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return ""
	}
	// Walk the chain and keep the last segment that names a real command, so
	// "cd x && go test ./..." becomes "go test" but "psql <<'SQL'" stays psql.
	best := ""
	for _, seg := range splitChain(cmd) {
		if c := commandOf(seg); c != "" {
			best = c
		}
	}
	return best
}

// splitChain breaks a shell line on the operators that separate commands.
func splitChain(cmd string) []string {
	out := []string{}
	start, depth := 0, 0
	for i := 0; i < len(cmd); i++ {
		switch cmd[i] {
		case '(', '{':
			depth++
		case ')', '}':
			if depth > 0 {
				depth--
			}
		case '&', ';', '|':
			if depth > 0 {
				continue
			}
			out = append(out, cmd[start:i])
			for i+1 < len(cmd) && (cmd[i+1] == '&' || cmd[i+1] == '|') {
				i++
			}
			start = i + 1
		}
	}
	return append(out, cmd[start:])
}

// commandOf reduces one segment to "tool subcommand", or "" if it is not a
// command worth recording.
func commandOf(seg string) string {
	fields := strings.Fields(seg)
	for len(fields) > 0 && strings.Contains(fields[0], "=") && !strings.HasPrefix(fields[0], "-") {
		fields = fields[1:] // strip leading VAR=value assignments
	}
	if len(fields) == 0 {
		return ""
	}
	head := filepath.Base(fields[0])
	if !commandName.MatchString(head) || noise[head] {
		return ""
	}
	out := head
	if len(fields) > 1 {
		if sub := fields[1]; subcommandName.MatchString(sub) && len(sub) < 20 {
			out += " " + sub
		}
	}
	if len(out) > 40 {
		return ""
	}
	return out
}

func top(m map[string]int, n, min int) []Count {
	var out []Count
	for k, v := range m {
		if v >= min {
			out = append(out, Count{k, v})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].N != out[j].N {
			return out[i].N > out[j].N
		}
		return out[i].Name < out[j].Name
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// Markdown renders the draft as a CLAUDE.md.
func (d Draft) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", d.Name)
	fmt.Fprintf(&b, "<!-- Drafted by pitwall from %d Claude Code session(s) and %d tool calls\n",
		d.Sessions, d.ToolCalls)
	if !d.First.IsZero() {
		fmt.Fprintf(&b, "     recorded between %s and %s.\n",
			d.First.Format("2006-01-02"), d.Last.Format("2006-01-02"))
	}
	b.WriteString("     This is observed behaviour, not documentation. Edit it, delete what is\n")
	b.WriteString("     wrong, and add the intent no transcript can tell you. -->\n\n")

	if len(d.Commands) > 0 {
		b.WriteString("## Commands that actually get used here\n\n")
		for _, c := range d.Commands {
			fmt.Fprintf(&b, "- `%s` — run %d times\n", c.Name, c.N)
		}
		b.WriteString("\n")
	}
	if len(d.Dirs) > 0 {
		b.WriteString("## Where the code lives\n\n")
		for _, c := range d.Dirs {
			fmt.Fprintf(&b, "- `%s/` — touched in %d tool calls\n", c.Name, c.N)
		}
		b.WriteString("\n")
	}
	if len(d.Files) > 0 {
		b.WriteString("## Files sessions keep opening\n\n")
		b.WriteString("Anything an agent reads at the start of every session belongs here as a\n")
		b.WriteString("summary instead, so it does not have to read it again.\n\n")
		for _, c := range d.Files {
			fmt.Fprintf(&b, "- `%s` (%d)\n", c.Name, c.N)
		}
		b.WriteString("\n")
	}
	if len(d.Branches) > 0 {
		names := make([]string, 0, len(d.Branches))
		for _, c := range d.Branches {
			names = append(names, "`"+c.Name+"`")
		}
		fmt.Fprintf(&b, "## Branches\n\nWork has happened on %s.\n\n", strings.Join(names, ", "))
	}

	b.WriteString("## Fill these in — they are what saves the most time\n\n")
	b.WriteString("- **What this is:** one paragraph. What the service does and who uses it.\n")
	b.WriteString("- **How to run it:** the exact commands for local dev, tests and deploy.\n")
	b.WriteString("- **Conventions:** anything an agent would otherwise infer wrongly.\n")
	b.WriteString("- **Do not touch:** generated files, vendored code, production config.\n")
	b.WriteString("- **Where things are:** the two or three entry points worth knowing.\n")
	return b.String()
}

// Context renders a compact primer for injection into a session that has no
// CLAUDE.md, small enough that it is cheaper than rediscovering the same
// facts with tool calls.
func (d Draft) Context() string {
	if d.ToolCalls == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Project notes for %s, observed by pitwall across %d earlier Claude Code "+
		"session(s). This is what previous sessions actually did here — treat it as a head "+
		"start, verify anything you rely on.\n", d.Name, d.Sessions)
	if len(d.Commands) > 0 {
		b.WriteString("\nCommands used here: ")
		for i, c := range d.Commands {
			if i >= 8 {
				break
			}
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "%s (%d)", c.Name, c.N)
		}
		b.WriteString("\n")
	}
	if len(d.Dirs) > 0 {
		b.WriteString("Active directories: ")
		for i, c := range d.Dirs {
			if i >= 6 {
				break
			}
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(c.Name + "/")
		}
		b.WriteString("\n")
	}
	if len(d.Files) > 0 {
		b.WriteString("Files earlier sessions opened most: ")
		for i, c := range d.Files {
			if i >= 6 {
				break
			}
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(c.Name)
		}
		b.WriteString("\n")
	}
	b.WriteString("\nThere is no CLAUDE.md in this repository. If you learn something durable, " +
		"suggest adding it to one.\n")
	return b.String()
}
