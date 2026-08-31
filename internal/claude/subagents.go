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

// Subagent is one delegated run, and whether it is still owed an answer.
type Subagent struct {
	// ID is the agent identifier from the transcript filename.
	ID string
	// Path is the transcript on disk.
	Path string
	// Quiet is how long since anything was written to it.
	Quiet time.Duration
	// Pending names the tool calls that were made and never answered. A
	// subagent that finished has none; one that is stuck on a permission gate
	// or an unresponsive server has one and stops writing.
	Pending []string
}

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
		out = append(out, Subagent{
			ID:      strings.TrimSuffix(strings.TrimPrefix(e.Name(), "agent-"), ".jsonl"),
			Path:    p,
			Quiet:   time.Since(info.ModTime()),
			Pending: pendingCalls(p),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Quiet > out[j].Quiet })
	return out
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
