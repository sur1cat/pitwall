package perms

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Options controls how aggressive a fix plan is.
type Options struct {
	// DropOneOffs also removes literal allow rules. They work; they are just
	// clutter that only ever matches one command again. Removing one means
	// Claude Code asks about that command the next time, which is a change in
	// behaviour, so it is off by default.
	DropOneOffs bool
}

// Rewrite replaces a rule's text with a corrected one.
type Rewrite struct {
	Rule Rule
	To   string
}

// Plan is the set of edits for a single settings file.
type Plan struct {
	Source  Source
	Remove  []Finding
	Rewrite []Rewrite
	Report  []Finding // findings this plan deliberately leaves alone
}

// Empty reports whether the plan would change nothing.
func (p Plan) Empty() bool { return len(p.Remove) == 0 && len(p.Rewrite) == 0 }

// widens reports whether applying a rewrite to this rule would grant access
// that the broken rule does not currently grant. A rule Claude Code ignores
// grants nothing, so repairing an allow rule turns it on for the first time —
// that is the user's decision, never the tool's.
func widens(r Rule) bool { return r.Kind == "allow" }

// PlanFixes turns findings into per-file edits. Only rules that cannot do
// anything at all are removed, and a rewrite is applied only when it narrows
// or preserves what is permitted.
func PlanFixes(findings []Finding, opts Options) []Plan {
	byFile := map[string]*Plan{}
	get := func(src Source) *Plan {
		p, ok := byFile[src.Path]
		if !ok {
			p = &Plan{Source: src}
			byFile[src.Path] = p
		}
		return p
	}

	for _, f := range findings {
		// An administrator's file is not ours to edit.
		if f.Rule.Source.Scope == ScopeManaged {
			continue
		}
		p := get(f.Rule.Source)
		switch f.Category {
		case "fragment", "unmatchable", "duplicate", "shadowed", "secret":
			p.Remove = append(p.Remove, f)
		case "ignored", "never-consulted":
			if f.Fix == "" || widens(f.Rule) {
				p.Report = append(p.Report, f)
				continue
			}
			p.Rewrite = append(p.Rewrite, Rewrite{Rule: f.Rule, To: f.Fix})
		case "one-off":
			if opts.DropOneOffs {
				p.Remove = append(p.Remove, f)
			} else {
				p.Report = append(p.Report, f)
			}
		default:
			p.Report = append(p.Report, f)
		}
	}

	out := make([]Plan, 0, len(byFile))
	for _, p := range byFile {
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool {
		if a, b := len(out[i].Remove)+len(out[i].Rewrite), len(out[j].Remove)+len(out[j].Rewrite); a != b {
			return a > b
		}
		return out[i].Source.Path < out[j].Source.Path
	})
	return out
}

// Apply writes the plan to disk after copying the original into backupDir. It
// rebuilds only the permission lists and leaves every other key byte-identical
// and in its original order, so the diff a user reviews is the change itself
// and nothing else.
func Apply(p Plan, backupDir string) (backup string, err error) {
	if p.Empty() {
		return "", nil
	}
	original, err := os.ReadFile(p.Source.Path)
	if err != nil {
		return "", err
	}
	edited, err := rewriteFile(original, p)
	if err != nil {
		return "", err
	}
	// Refuse to write something that is not valid JSON, whatever went wrong.
	var probe map[string]any
	if err := json.Unmarshal(edited, &probe); err != nil {
		return "", fmt.Errorf("refusing to write invalid JSON to %s: %w", p.Source.Path, err)
	}
	if backup, err = writeBackup(backupDir, p.Source.Path, original); err != nil {
		return "", err
	}
	if err := os.WriteFile(p.Source.Path, edited, 0o600); err != nil {
		return backup, err
	}
	return backup, nil
}

// writeBackup copies the original file somewhere recoverable before anything
// is written over it.
func writeBackup(dir, path string, content []byte) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	stamp := time.Now().Format("20060102-150405")
	name := fmt.Sprintf("%s.%s.json", sanitize(path), stamp)
	dst := filepath.Join(dir, name)
	if err := os.WriteFile(dst, content, 0o600); err != nil {
		return "", err
	}
	return dst, nil
}

func sanitize(path string) string {
	b := []byte(path)
	for i, c := range b {
		if c == '/' || c == '\\' || c == ':' || c == ' ' {
			b[i] = '-'
		}
	}
	return string(bytes.TrimLeft(b, "-"))
}

// rewriteFile rebuilds the JSON with the permission lists edited, preserving
// the order of every top-level key.
func rewriteFile(original []byte, p Plan) ([]byte, error) {
	keys, values, err := topLevel(original)
	if err != nil {
		return nil, err
	}
	idx := -1
	for i, k := range keys {
		if k == "permissions" {
			idx = i
		}
	}
	if idx < 0 {
		return original, nil // nothing to edit
	}
	permKeys, permValues, err := topLevel(values[idx])
	if err != nil {
		return nil, err
	}

	drop := map[string]bool{}
	for _, f := range p.Remove {
		drop[ruleKey(f.Rule)] = true
	}
	repl := map[string]string{}
	for _, w := range p.Rewrite {
		repl[ruleKey(w.Rule)] = w.To
	}

	for i, k := range permKeys {
		if k != "allow" && k != "deny" && k != "ask" {
			continue
		}
		var list []string
		if json.Unmarshal(permValues[i], &list) != nil {
			continue
		}
		kept := make([]string, 0, len(list))
		for j, raw := range list {
			key := fmt.Sprintf("%s\x00%d\x00%s", k, j, raw)
			if drop[key] {
				continue
			}
			if to, ok := repl[key]; ok {
				raw = to
			}
			kept = append(kept, raw)
		}
		enc, err := json.Marshal(kept)
		if err != nil {
			return nil, err
		}
		permValues[i] = enc
	}

	values[idx] = assemble(permKeys, permValues)
	out := assemble(keys, values)
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, out, "", "  "); err != nil {
		return nil, err
	}
	pretty.WriteByte('\n')
	return pretty.Bytes(), nil
}

// ruleKey identifies a rule by its position, so that two identical rule
// strings in the same list are not confused with one another.
func ruleKey(r Rule) string {
	return fmt.Sprintf("%s\x00%d\x00%s", r.Kind, r.Index, r.Raw)
}

// topLevel splits a JSON object into its keys and raw values, in order.
func topLevel(data []byte) (keys []string, values []json.RawMessage, err error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return nil, nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, nil, fmt.Errorf("expected a JSON object")
	}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, nil, err
		}
		k, ok := kt.(string)
		if !ok {
			return nil, nil, fmt.Errorf("expected an object key")
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, nil, err
		}
		keys = append(keys, k)
		values = append(values, v)
	}
	return keys, values, nil
}

// assemble rebuilds a JSON object from ordered keys and raw values.
func assemble(keys []string, values []json.RawMessage) []byte {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		b.Write(kb)
		b.WriteByte(':')
		b.Write(values[i])
	}
	b.WriteByte('}')
	return b.Bytes()
}
