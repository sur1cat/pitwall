package burn

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// assistantLine builds one transcript record carrying token usage.
func assistantLine(msgID, model, effort, ts string, in, out, cacheWrite, cacheRead int64) string {
	return fmt.Sprintf(`{"type":"assistant","timestamp":%q,"effort":%q,"cwd":"/src/demo","sessionId":"s1","message":{"id":%q,"model":%q,"usage":{"input_tokens":%d,"output_tokens":%d,"cache_creation_input_tokens":%d,"cache_read_input_tokens":%d,"cache_creation":{"ephemeral_5m_input_tokens":%d,"ephemeral_1h_input_tokens":0}}}}`,
		ts, effort, msgID, model, in, out, cacheWrite, cacheRead, cacheWrite)
}

func setup(t *testing.T, files map[string][]string) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", root)
	for name, lines := range files {
		dir := filepath.Join(root, "projects", "-src-demo")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		body := ""
		for _, l := range lines {
			body += l + "\n"
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestScanAggregatesUsage(t *testing.T) {
	setup(t, map[string][]string{
		"a.jsonl": {
			assistantLine("msg_1", "claude-opus-5", "high", "2026-08-31T10:00:00.000Z", 100, 200, 300, 400),
			assistantLine("msg_2", "claude-opus-5", "high", "2026-08-31T10:30:00.000Z", 1, 2, 3, 4),
		},
	})
	rep := Scan(false)
	if len(rep.Records) != 1 {
		t.Fatalf("expected 1 hourly bucket, got %d", len(rep.Records))
	}
	r := rep.Records[0]
	if r.Messages != 2 {
		t.Errorf("messages = %d, want 2", r.Messages)
	}
	if r.Usage.Input != 101 || r.Usage.Output != 202 || r.Usage.CacheWrite5m != 303 || r.Usage.CacheRead != 404 {
		t.Errorf("usage = %+v", r.Usage)
	}
	if r.Model != "claude-opus-5" || r.Effort != "high" || r.Project != "demo" {
		t.Errorf("dimensions = %s/%s/%s", r.Model, r.Effort, r.Project)
	}
}

// TestScanDeduplicatesReplayedMessages covers session forks and resumes, which
// copy earlier assistant messages into a second transcript.
func TestScanDeduplicatesReplayedMessages(t *testing.T) {
	shared := assistantLine("msg_shared", "claude-opus-5", "high", "2026-08-31T10:00:00.000Z", 1000, 2000, 0, 0)
	setup(t, map[string][]string{
		"a.jsonl": {shared},
		"b.jsonl": {
			shared, // replayed into the forked session
			assistantLine("msg_new", "claude-opus-5", "high", "2026-08-31T11:00:00.000Z", 10, 20, 0, 0),
		},
	})
	rep := Scan(false)
	if rep.Duplicates != 1 {
		t.Errorf("duplicates = %d, want 1", rep.Duplicates)
	}
	var in, out int64
	for _, r := range rep.Records {
		in += r.Usage.Input
		out += r.Usage.Output
	}
	if in != 1010 || out != 2020 {
		t.Errorf("totals = %d in / %d out, want 1010 / 2020 (shared message counted once)", in, out)
	}
}

func TestScanSplitsEffortAndSubagents(t *testing.T) {
	sub := `{"type":"assistant","timestamp":"2026-08-31T10:00:00.000Z","effort":"low","isSidechain":true,"cwd":"/src/demo","message":{"id":"msg_sub","model":"claude-opus-5","usage":{"input_tokens":5,"output_tokens":5}}}`
	setup(t, map[string][]string{
		"a.jsonl": {
			assistantLine("msg_main", "claude-opus-5", "max", "2026-08-31T10:00:00.000Z", 10, 10, 0, 0),
			sub,
		},
	})
	rep := Scan(false)
	if len(rep.Records) != 2 {
		t.Fatalf("expected separate buckets for main and subagent work, got %d", len(rep.Records))
	}
	var sawMax, sawSub bool
	for _, r := range rep.Records {
		if r.Effort == "max" && !r.Sub {
			sawMax = true
		}
		if r.Effort == "low" && r.Sub {
			sawSub = true
		}
	}
	if !sawMax || !sawSub {
		t.Errorf("effort/subagent split missing: %+v", rep.Records)
	}
}

func TestScanIgnoresSyntheticAndEmptyRecords(t *testing.T) {
	setup(t, map[string][]string{
		"a.jsonl": {
			assistantLine("msg_zero", "claude-opus-5", "high", "2026-08-31T10:00:00.000Z", 0, 0, 0, 0),
			assistantLine("msg_synth", "<synthetic>", "high", "2026-08-31T10:00:00.000Z", 100, 100, 0, 0),
			`{"type":"user","message":{"role":"user","content":[]}}`,
			`not json at all`,
		},
	})
	rep := Scan(false)
	if len(rep.Records) != 0 {
		t.Errorf("expected no billable records, got %+v", rep.Records)
	}
}
