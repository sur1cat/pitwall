// Package claude reads local Claude Code state: the live session registry and
// the tail of each session's transcript. Everything is read-only.
package claude

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// Dir returns the Claude configuration directory.
func Dir() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude")
}

// Session is one Claude Code process from ~/.claude/sessions/*.json.
type Session struct {
	PID       int    `json:"pid"`
	SessionID string `json:"sessionId"`
	CWD       string `json:"cwd"`
	Name      string `json:"name"`
	Status    string `json:"status"` // busy | idle
	Kind      string `json:"kind"`
	StartedAt int64  `json:"startedAt"`
	UpdatedAt int64  `json:"updatedAt"`
	Version   string `json:"version"`

	Alive bool `json:"alive"`
}

// Updated returns when Claude last recorded a status change.
func (s Session) Updated() time.Time {
	if s.UpdatedAt == 0 {
		return time.Time{}
	}
	return time.UnixMilli(s.UpdatedAt)
}

// Started returns when the session began.
func (s Session) Started() time.Time {
	if s.StartedAt == 0 {
		return time.Time{}
	}
	return time.UnixMilli(s.StartedAt)
}

// Sessions lists every registered session, marking each alive or stale.
func Sessions() []Session {
	entries, err := os.ReadDir(filepath.Join(Dir(), "sessions"))
	if err != nil {
		return nil
	}
	var out []Session
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(Dir(), "sessions", e.Name()))
		if err != nil {
			continue
		}
		var s Session
		if json.Unmarshal(raw, &s) != nil || s.SessionID == "" {
			continue
		}
		s.Alive = pidAlive(s.PID)
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	return out
}

func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	switch {
	case err == nil, errors.Is(err, syscall.EPERM):
		return true
	case errors.Is(err, os.ErrProcessDone):
		return false
	case strings.Contains(err.Error(), "not supported"):
		return true
	}
	return false
}

// TranscriptPath locates a session's .jsonl transcript.
func TranscriptPath(sessionID string) string {
	matches, err := filepath.Glob(filepath.Join(Dir(), "projects", "*", sessionID+".jsonl"))
	if err != nil || len(matches) == 0 {
		return ""
	}
	return matches[0]
}

// Tail is what the end of a transcript says about a session's turn.
type Tail struct {
	// LastActivity is the newest timestamp in the transcript.
	LastActivity time.Time
	// LastText is the most recent thing the agent said.
	LastText string
	// LastRole is whether the transcript ends on the agent or the user side.
	LastRole string
	// Pending names the tool call that is still awaiting a result, if any.
	Pending string
	// Question is the header of an unanswered AskUserQuestion.
	Question string
	// Recap is the newest away-summary Claude wrote for you.
	Recap string
	// RecapAt is when that summary was written.
	RecapAt time.Time
	// LastTurn is how long the most recent completed turn took.
	LastTurn time.Duration
	// TurnEndedAt is when that turn finished.
	TurnEndedAt time.Time

	// Turn holds the tokens spent since the last thing you typed — the price
	// of the exchange you are looking at.
	Turn TurnUsage

	// Context is how full the model's context window was at the newest
	// assistant message: input plus both kinds of cache, which is what
	// occupies the window. Output tokens are excluded, matching how Claude
	// Code computes the figure it shows in a status line.
	Context int64
	// ContextModel is the model that message ran on, since the size of the
	// window depends on it.
	ContextModel string
}

// TurnUsage is one turn's token cost, left unpriced here so this package
// stays free of pricing concerns.
type TurnUsage struct {
	Model        string
	Input        int64
	Output       int64
	CacheWrite5m int64
	CacheWrite1h int64
	CacheRead    int64
	Messages     int
}

type entry struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	Timestamp string `json:"timestamp"`
	Content   string `json:"content"`
	Duration  int64  `json:"durationMs"`
	Message   struct {
		Role  string `json:"role"`
		Model string `json:"model"`
		Usage struct {
			Input       int64 `json:"input_tokens"`
			Output      int64 `json:"output_tokens"`
			CacheCreate int64 `json:"cache_creation_input_tokens"`
			CacheRead   int64 `json:"cache_read_input_tokens"`
			Creation    struct {
				E5m int64 `json:"ephemeral_5m_input_tokens"`
				E1h int64 `json:"ephemeral_1h_input_tokens"`
			} `json:"cache_creation"`
		} `json:"usage"`
		Content []struct {
			Type      string          `json:"type"`
			Text      string          `json:"text"`
			Name      string          `json:"name"`
			ID        string          `json:"id"`
			ToolUseID string          `json:"tool_use_id"`
			Input     json.RawMessage `json:"input"`
		} `json:"content"`
	} `json:"message"`
}

// maxTail is how much of the end of a transcript to read. Transcripts grow
// without bound; the last megabyte always covers the current turn.
const maxTail = 1 << 20

// ReadTail parses the end of a session transcript.
func ReadTail(path string) Tail {
	var t Tail
	if path == "" {
		return t
	}
	f, err := os.Open(path)
	if err != nil {
		return t
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return t
	}
	offset := int64(0)
	if info.Size() > maxTail {
		offset = info.Size() - maxTail
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return t
	}
	r := bufio.NewReaderSize(f, 1<<16)
	if offset > 0 {
		_, _ = r.ReadBytes('\n') // discard the partial first line
	}

	pending := map[string]string{}   // tool_use id -> tool name
	questions := map[string]string{} // tool_use id -> question header

	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 && bytes.HasPrefix(bytes.TrimSpace(line), []byte("{")) {
			var e entry
			if json.Unmarshal(line, &e) == nil {
				t.absorb(e, pending, questions)
			}
		}
		if err != nil {
			break
		}
	}

	for id, name := range pending {
		t.Pending = name
		if q, ok := questions[id]; ok {
			t.Question = q
		}
		break
	}
	return t
}

func (t *Tail) absorb(e entry, pending, questions map[string]string) {
	if ts := parseTime(e.Timestamp); !ts.IsZero() && ts.After(t.LastActivity) {
		t.LastActivity = ts
	}
	switch e.Type {
	case "system":
		switch e.Subtype {
		case "away_summary":
			if e.Content != "" {
				t.Recap = e.Content
				t.RecapAt = parseTime(e.Timestamp)
			}
		case "turn_duration":
			t.LastTurn = time.Duration(e.Duration) * time.Millisecond
			t.TurnEndedAt = parseTime(e.Timestamp)
		}
	case "assistant", "user":
		if e.Message.Role != "" {
			t.LastRole = e.Message.Role
		}
		if e.Type == "user" && isHumanTurn(e) {
			t.Turn = TurnUsage{} // a new prompt starts a new turn
		}
		if e.Type == "assistant" {
			u := e.Message.Usage
			w5, w1 := u.Creation.E5m, u.Creation.E1h
			if w5+w1 == 0 {
				w5 = u.CacheCreate
			}
			t.Turn.Model = e.Message.Model
			t.Turn.Input += u.Input
			t.Turn.Output += u.Output
			t.Turn.CacheWrite5m += w5
			t.Turn.CacheWrite1h += w1
			t.Turn.CacheRead += u.CacheRead
			t.Turn.Messages++
			// The newest message's own totals are the current occupancy, not
			// the running sum: each message reports the whole window it saw.
			if occ := u.Input + u.CacheRead + w5 + w1; occ > 0 {
				t.Context, t.ContextModel = occ, e.Message.Model
			}
		}
		for _, b := range e.Message.Content {
			switch b.Type {
			case "text":
				if e.Type == "assistant" && strings.TrimSpace(b.Text) != "" {
					t.LastText = strings.TrimSpace(b.Text)
				}
			case "tool_use":
				pending[b.ID] = b.Name
				if b.Name == "AskUserQuestion" {
					questions[b.ID] = firstQuestion(b.Input)
				}
			case "tool_result":
				delete(pending, b.ToolUseID)
				delete(questions, b.ToolUseID)
			}
		}
	}
}

// firstQuestion pulls the leading question text out of an AskUserQuestion call.
func firstQuestion(raw json.RawMessage) string {
	var in struct {
		Questions []struct {
			Question string `json:"question"`
			Header   string `json:"header"`
		} `json:"questions"`
	}
	if json.Unmarshal(raw, &in) != nil || len(in.Questions) == 0 {
		return ""
	}
	if q := strings.TrimSpace(in.Questions[0].Question); q != "" {
		return q
	}
	return strings.TrimSpace(in.Questions[0].Header)
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// isHumanTurn reports whether a user entry is something a person typed rather
// than a tool answering.
func isHumanTurn(e entry) bool {
	for _, b := range e.Message.Content {
		if b.Type == "tool_result" {
			return false
		}
	}
	return true
}
