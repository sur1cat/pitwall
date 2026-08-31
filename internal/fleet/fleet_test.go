package fleet

import (
	"testing"
	"time"

	"github.com/sur1cat/pitwall/internal/claude"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		name  string
		alive bool
		stat  string
		tail  claude.Tail
		idle  time.Duration
		want  State
	}{
		{
			name: "dead process is stale", alive: false, stat: "idle", want: StateStale,
		},
		{
			name:  "unanswered question blocks on you",
			alive: true, stat: "idle",
			tail: claude.Tail{Pending: "AskUserQuestion", Question: "Delete the index?"},
			want: StateWaiting,
		},
		{
			name:  "pending tool while running is not waiting",
			alive: true, stat: "busy",
			tail: claude.Tail{Pending: "Bash"},
			want: StateWorking,
		},
		{
			name:  "agent finished and you have not replied",
			alive: true, stat: "idle",
			tail: claude.Tail{LastRole: "assistant"}, idle: 20 * time.Minute,
			want: StateDone,
		},
		{
			name:  "a finished turn goes quiet after the done window",
			alive: true, stat: "idle",
			tail: claude.Tail{LastRole: "assistant"}, idle: 30 * time.Hour,
			want: StateIdle,
		},
		{
			name:  "nothing outstanding is idle",
			alive: true, stat: "idle",
			tail: claude.Tail{LastRole: "user"}, idle: time.Minute,
			want: StateIdle,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := claude.Session{Alive: c.alive, Status: c.stat}
			if got := classify(s, c.tail, c.idle); got != c.want {
				t.Errorf("classify() = %s, want %s", got, c.want)
			}
		})
	}
}

func TestNeedsYou(t *testing.T) {
	if !StateWaiting.NeedsYou() || !StateDone.NeedsYou() {
		t.Error("waiting and done must pull you back")
	}
	if StateWorking.NeedsYou() || StateIdle.NeedsYou() || StateStale.NeedsYou() {
		t.Error("working, idle and stale must not interrupt you")
	}
}

// TestSortOrder pins the ranking the table relies on: whatever is blocking
// you sorts above whatever is merely running.
func TestSortOrder(t *testing.T) {
	ranked := []State{StateWaiting, StateDone, StateWorking, StateIdle, StateStale}
	for i := 1; i < len(ranked); i++ {
		if priority(ranked[i-1]) >= priority(ranked[i]) {
			t.Fatalf("%s must sort above %s", ranked[i-1], ranked[i])
		}
	}
}
