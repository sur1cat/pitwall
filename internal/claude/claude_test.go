package claude

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeTranscript builds a fake session transcript inside a temporary
// CLAUDE_CONFIG_DIR and returns its path.
func writeTranscript(t *testing.T, sessionID string, lines ...string) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", root)
	dir := filepath.Join(root, "projects", "-tmp-project")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, sessionID+".jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadTailDetectsUnansweredQuestion(t *testing.T) {
	path := writeTranscript(t, "sess-1",
		`{"type":"assistant","timestamp":"2026-08-31T10:00:00.000Z","message":{"role":"assistant","content":[{"type":"text","text":"Checking the schema."}]}}`,
		`{"type":"assistant","timestamp":"2026-08-31T10:01:00.000Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu_1","name":"AskUserQuestion","input":{"questions":[{"question":"Drop the legacy index?","header":"Index"}]}}]}}`,
	)
	tail := ReadTail(path)
	if tail.Pending != "AskUserQuestion" {
		t.Errorf("Pending = %q, want AskUserQuestion", tail.Pending)
	}
	if tail.Question != "Drop the legacy index?" {
		t.Errorf("Question = %q", tail.Question)
	}
	if tail.LastText != "Checking the schema." {
		t.Errorf("LastText = %q", tail.LastText)
	}
	if tail.LastActivity.IsZero() {
		t.Error("LastActivity not parsed")
	}
}

func TestReadTailClearsAnsweredQuestion(t *testing.T) {
	path := writeTranscript(t, "sess-2",
		`{"type":"assistant","timestamp":"2026-08-31T10:01:00.000Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu_1","name":"AskUserQuestion","input":{"questions":[{"question":"Drop it?"}]}}]}}`,
		`{"type":"user","timestamp":"2026-08-31T10:02:00.000Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu_1"}]}}`,
	)
	tail := ReadTail(path)
	if tail.Pending != "" || tail.Question != "" {
		t.Errorf("answered question still pending: %q / %q", tail.Pending, tail.Question)
	}
	if tail.LastRole != "user" {
		t.Errorf("LastRole = %q, want user", tail.LastRole)
	}
}

func TestReadTailPicksUpRecapAndTurnDuration(t *testing.T) {
	path := writeTranscript(t, "sess-3",
		`{"type":"system","subtype":"away_summary","timestamp":"2026-08-31T10:05:00.000Z","content":"Deployed phases 3-6; callback error 2001 still open."}`,
		`{"type":"system","subtype":"turn_duration","timestamp":"2026-08-31T10:06:00.000Z","durationMs":517815}`,
		`{"type":"assistant","timestamp":"2026-08-31T10:06:01.000Z","message":{"role":"assistant","content":[{"type":"text","text":"Done."}]}}`,
	)
	tail := ReadTail(path)
	if !strings.HasPrefix(tail.Recap, "Deployed phases") {
		t.Errorf("Recap = %q", tail.Recap)
	}
	if tail.LastTurn != 517815*time.Millisecond {
		t.Errorf("LastTurn = %s", tail.LastTurn)
	}
	if tail.LastRole != "assistant" {
		t.Errorf("LastRole = %q, want assistant", tail.LastRole)
	}
}

func TestTranscriptPathAndMissingFile(t *testing.T) {
	path := writeTranscript(t, "sess-4", `{"type":"assistant","message":{"role":"assistant","content":[]}}`)
	// Paths come back with symlinks resolved, because a symlinked projects
	// directory is otherwise never descended into. On macOS a temp directory
	// is itself behind /var -> /private/var, so the expectation has to be
	// resolved too rather than compared literally.
	want, err := filepath.EvalSymlinks(path)
	if err != nil {
		want = path
	}
	if got := TranscriptPath("sess-4"); got != want {
		t.Errorf("TranscriptPath = %q, want %q", got, want)
	}
	if got := TranscriptPath("nope"); got != "" {
		t.Errorf("TranscriptPath for unknown session = %q, want empty", got)
	}
	if tail := ReadTail(""); tail.LastActivity.IsZero() == false {
		t.Error("ReadTail on an empty path should be zero-valued")
	}
}
