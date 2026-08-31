package claude

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Subagent is one delegated run: what it was asked to do, what it has spent,
// and whether it is still going.
//
// None of this reaches the person who started it. The parent narrates what it
// delegated and the panel shows one busy session, while several agents work
// underneath it — on this machine they are 23% of the spend and nearly half of
// all messages.
type Subagent struct {
	// ID is the agent identifier from the transcript filename.
	ID string
	// Slug is the readable name Claude Code gave the run.
	Slug string
	// Task is the opening line of what it was asked to do.
	Task string
	// Path is the transcript on disk.
	Path string
	// Started is when its first record was written.
	Started time.Time
	// Quiet is how long since anything was written to it.
	Quiet time.Duration
	// Tools is how many tool calls it has made.
	Tools int
	// Usage is what it has spent, left unpriced here.
	Usage TurnUsage
	// Pending names the tool calls that were made and never answered. A
	// subagent that finished has none; one that is stuck on a permission gate
	// or an unresponsive server has one and stops writing.
	Pending []string
}

// ActiveWithin reports whether the run has written recently enough to be
// considered still going.
func (s Subagent) ActiveWithin(d time.Duration) bool { return s.Quiet <= d }

// Stalled reports whether this run looks stuck rather than finished. Claude
// Code gives the parent no signal when a subagent blocks — issues #61315 and
// #61405 record a delegation that hung for over twelve hours — so the only
// evidence is the transcript itself: a tool call that was never answered, and
// silence since.
func (s Subagent) Stalled(threshold time.Duration) bool {
	return len(s.Pending) > 0 && s.Quiet >= threshold
}

// StallThreshold is how long a subagent must be quiet on an unanswered call
// before it is worth mentioning. Long enough that a slow build or a large test
// run is not reported, short enough to catch a hang before the day is gone.
const StallThreshold = 10 * time.Minute

// Subagents reads the delegated runs belonging to a session.
func Subagents(sessionID string) []Subagent {
	main := TranscriptPath(sessionID)
	if main == "" {
		return nil
	}
	dir := filepath.Join(strings.TrimSuffix(main, ".jsonl"), "subagents")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []Subagent
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		p := filepath.Join(dir, e.Name())
		info, err := e.Info()
		if err != nil {
			continue
		}
		sub := Subagent{
			ID:    strings.TrimSuffix(strings.TrimPrefix(e.Name(), "agent-"), ".jsonl"),
			Path:  p,
			Quiet: time.Since(info.ModTime()),
		}
		readSubagent(p, &sub)
		out = append(out, sub)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Quiet > out[j].Quiet })
	return out
}

// readSubagent fills in what one delegated run was asked to do and what it has
// spent, in a single pass over its transcript.
func readSubagent(path string, sub *Subagent) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	open := map[string]string{}
	var order []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	first := true
	for sc.Scan() {
		var rec struct {
			Slug      string    `json:"slug"`
			Timestamp time.Time `json:"timestamp"`
			Type      string    `json:"type"`
			Message   struct {
				Role    string          `json:"role"`
				Model   string          `json:"model"`
				Content json.RawMessage `json:"content"`
				Usage   struct {
					Input       int64 `json:"input_tokens"`
					Output      int64 `json:"output_tokens"`
					CacheCreate int64 `json:"cache_creation_input_tokens"`
					CacheRead   int64 `json:"cache_read_input_tokens"`
				} `json:"usage"`
			} `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &rec) != nil {
			continue
		}
		if sub.Slug == "" && rec.Slug != "" {
			sub.Slug = rec.Slug
		}
		if first && !rec.Timestamp.IsZero() {
			sub.Started, first = rec.Timestamp, false
		}
		// The opening user message is the instruction the parent handed over.
		if sub.Task == "" && rec.Message.Role == "user" {
			if text, _ := readSubContent(rec.Message.Content); text != "" {
				sub.Task = firstLine(text)
			}
		}
		if rec.Type == "assistant" {
			u := rec.Message.Usage
			sub.Usage.Model = rec.Message.Model
			sub.Usage.Input += u.Input
			sub.Usage.Output += u.Output
			sub.Usage.CacheWrite5m += u.CacheCreate
			sub.Usage.CacheRead += u.CacheRead
			sub.Usage.Messages++
		}
		_, calls := readSubContent(rec.Message.Content)
		for _, c := range calls {
			sub.Tools++
			if _, seen := open[c.id]; !seen {
				order = append(order, c.id)
			}
			open[c.id] = c.name
		}
		for _, id := range resultIDs(rec.Message.Content) {
			delete(open, id)
		}
	}
	for _, id := range order {
		if name, ok := open[id]; ok {
			sub.Pending = append(sub.Pending, name)
		}
	}
}

type subCall struct{ id, name string }

// readSubContent pulls the text and the tool calls out of a message body.
func readSubContent(raw json.RawMessage) (string, []subCall) {
	if len(raw) == 0 {
		return "", nil
	}
	var plain string
	if json.Unmarshal(raw, &plain) == nil {
		return plain, nil
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return "", nil
	}
	var text string
	var calls []subCall
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if text == "" {
				text = b.Text
			}
		case "tool_use":
			calls = append(calls, subCall{b.ID, b.Name})
		}
	}
	return text, calls
}

// resultIDs lists the tool calls a message answers.
func resultIDs(raw json.RawMessage) []string {
	var blocks []struct {
		Type      string `json:"type"`
		ToolUseID string `json:"tool_use_id"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return nil
	}
	var out []string
	for _, b := range blocks {
		if b.Type == "tool_result" && b.ToolUseID != "" {
			out = append(out, b.ToolUseID)
		}
	}
	return out
}

// firstLine trims an instruction to the part worth showing in a list.
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i > 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// pendingCalls returns the names of tool calls with no matching result. It
// reads the whole file because a call and its answer can be far apart, but a
// subagent transcript is small next to a session one.
func pendingCalls(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	open := map[string]string{}
	order := []string{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if !bytesHasAny(line, "tool_use", "tool_result") {
			continue
		}
		var rec struct {
			Message struct {
				Content []struct {
					Type      string `json:"type"`
					ID        string `json:"id"`
					Name      string `json:"name"`
					ToolUseID string `json:"tool_use_id"`
				} `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &rec) != nil {
			continue
		}
		for _, b := range rec.Message.Content {
			switch b.Type {
			case "tool_use":
				if b.ID != "" {
					if _, seen := open[b.ID]; !seen {
						order = append(order, b.ID)
					}
					open[b.ID] = b.Name
				}
			case "tool_result":
				delete(open, b.ToolUseID)
			}
		}
	}
	var names []string
	for _, id := range order {
		if name, ok := open[id]; ok {
			names = append(names, name)
		}
	}
	return names
}

func bytesHasAny(b []byte, needles ...string) bool {
	s := string(b)
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}
