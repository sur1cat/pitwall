package claude

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeSubTranscript(t *testing.T, dir, name string, lines ...string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPendingCallsSeesOnlyUnansweredOnes(t *testing.T) {
	dir := t.TempDir()

	done := writeSubTranscript(t, dir, "done.jsonl",
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"a","name":"Bash"}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"a"}]}}`)
	if got := pendingCalls(done); len(got) != 0 {
		t.Errorf("an answered call is not pending, got %v", got)
	}

	stuck := writeSubTranscript(t, dir, "stuck.jsonl",
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"a","name":"Bash"}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"a"}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"b","name":"mcp__db__query"}]}}`)
	got := pendingCalls(stuck)
	if len(got) != 1 || got[0] != "mcp__db__query" {
		t.Errorf("the unanswered call should be reported by name, got %v", got)
	}

	// Order is the order the calls were made, so the oldest block is first.
	many := writeSubTranscript(t, dir, "many.jsonl",
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"x","name":"First"}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"y","name":"Second"}]}}`)
	if got := pendingCalls(many); len(got) != 2 || got[0] != "First" {
		t.Errorf("pending calls should keep their order, got %v", got)
	}
}

func TestStalledNeedsBothSilenceAndAnUnansweredCall(t *testing.T) {
	// A subagent that finished is quiet forever and is not stalled; that is
	// the normal case and mistaking it for a hang would make the whole signal
	// useless, because most subagents are finished.
	finished := Subagent{Quiet: 9 * time.Hour}
	if finished.Stalled(StallThreshold) {
		t.Error("a finished subagent is quiet, not stalled")
	}
	// A call made a moment ago is simply running.
	running := Subagent{Quiet: time.Minute, Pending: []string{"Bash"}}
	if running.Stalled(StallThreshold) {
		t.Error("a call in flight is not a stall")
	}
	stuck := Subagent{Quiet: 30 * time.Minute, Pending: []string{"mcp__db__query"}}
	if !stuck.Stalled(StallThreshold) {
		t.Error("an unanswered call plus silence is a stall")
	}
}

func TestSubagentsIsQuietWhenThereAreNone(t *testing.T) {
	if got := Subagents("no-such-session"); got != nil {
		t.Errorf("an unknown session has no subagents, got %v", got)
	}
}
