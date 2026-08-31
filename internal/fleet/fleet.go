// Package fleet turns Claude Code's session registry into one question:
// which of your agents is waiting for you right now?
package fleet

import (
	"context"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sur1cat/pitwall/internal/burn"
	"github.com/sur1cat/pitwall/internal/claude"
)

// State is what an agent is doing from your point of view.
type State string

const (
	// StateWaiting is blocked on you: a question or a tool awaiting approval.
	StateWaiting State = "WAITING"
	// StateDone finished its turn and you have not replied since.
	StateDone State = "DONE"
	// StateWorking is running right now.
	StateWorking State = "WORKING"
	// StateIdle is parked with nothing outstanding.
	StateIdle State = "IDLE"
	// StateStale is a registry entry whose process is gone.
	StateStale State = "STALE"
)

// NeedsYou reports whether this state should pull you back to the terminal.
func (s State) NeedsYou() bool { return s == StateWaiting || s == StateDone }

// doneWindow is how long a finished turn stays worth surfacing before it
// becomes background noise.
const doneWindow = 12 * time.Hour

// Agent is one Claude Code session and everything worth knowing about it.
type Agent struct {
	Name      string        `json:"name"`
	SessionID string        `json:"session_id"`
	PID       int           `json:"pid"`
	CWD       string        `json:"cwd"`
	Project   string        `json:"project"`
	Branch    string        `json:"branch,omitempty"`
	Alive     bool          `json:"alive"`
	Status    string        `json:"status"`
	State     State         `json:"state"`
	Idle      time.Duration `json:"idle"`
	Started   time.Time     `json:"started"`
	LastTurn  time.Duration `json:"last_turn"`
	// TurnCost is what the exchange you are looking at cost, at API rates.
	TurnCost float64   `json:"turn_cost"`
	Question string    `json:"question,omitempty"`
	Pending  string    `json:"pending_tool,omitempty"`
	LastText string    `json:"last_text,omitempty"`
	Recap    string    `json:"recap,omitempty"`
	RecapAt  time.Time `json:"recap_at,omitempty"`
}

// Options controls how much work a snapshot does.
type Options struct {
	// NoGit skips resolving the git branch of each session's directory.
	NoGit bool
	// IncludeStale keeps registry entries whose process has exited.
	IncludeStale bool
}

// Snapshot reads the current state of every Claude Code session.
func Snapshot(opt Options) []Agent {
	sessions := claude.Sessions()
	agents := make([]Agent, 0, len(sessions))
	for _, s := range sessions {
		if !s.Alive && !opt.IncludeStale {
			continue
		}
		a := Agent{
			Name:      s.Name,
			SessionID: s.SessionID,
			PID:       s.PID,
			CWD:       s.CWD,
			Project:   filepath.Base(s.CWD),
			Alive:     s.Alive,
			Status:    s.Status,
			Started:   s.Started(),
		}
		if a.Name == "" {
			a.Name = shortID(s.SessionID)
		}
		tail := claude.ReadTail(claude.TranscriptPath(s.SessionID))
		a.Question, a.Pending = tail.Question, tail.Pending
		a.LastText, a.Recap, a.RecapAt = tail.LastText, tail.Recap, tail.RecapAt
		a.LastTurn = tail.LastTurn
		if c, ok := burn.Compute(tail.Turn.Model, time.Now(), burn.Usage{
			Input: tail.Turn.Input, Output: tail.Turn.Output,
			CacheWrite5m: tail.Turn.CacheWrite5m, CacheWrite1h: tail.Turn.CacheWrite1h,
			CacheRead: tail.Turn.CacheRead,
		}); ok {
			a.TurnCost = c.Total()
		}

		last := tail.LastActivity
		if last.IsZero() {
			last = s.Updated()
		}
		if !last.IsZero() {
			a.Idle = time.Since(last)
		}
		a.State = classify(s, tail, a.Idle)
		if !opt.NoGit {
			a.Branch = branch(s.CWD)
		}
		agents = append(agents, a)
	}
	sort.Slice(agents, func(i, j int) bool {
		pi, pj := priority(agents[i].State), priority(agents[j].State)
		if pi != pj {
			return pi < pj
		}
		return agents[i].Idle < agents[j].Idle
	})
	return agents
}

func classify(s claude.Session, t claude.Tail, idle time.Duration) State {
	switch {
	case !s.Alive:
		return StateStale
	case t.Pending != "" && s.Status != "busy":
		// A tool call with no result while the agent is not running means it
		// is parked on you — a question, or a permission prompt.
		return StateWaiting
	case s.Status == "busy":
		return StateWorking
	case t.LastRole == "assistant" && idle < doneWindow:
		return StateDone
	default:
		return StateIdle
	}
}

func priority(s State) int {
	switch s {
	case StateWaiting:
		return 0
	case StateDone:
		return 1
	case StateWorking:
		return 2
	case StateIdle:
		return 3
	default:
		return 4
	}
}

// NeedYou returns the agents that are blocked on you or newly finished.
func NeedYou(agents []Agent) []Agent {
	var out []Agent
	for _, a := range agents {
		if a.State.NeedsYou() {
			out = append(out, a)
		}
	}
	return out
}

// branch resolves the current git branch of a directory, or "".
func branch(dir string) string {
	if dir == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
