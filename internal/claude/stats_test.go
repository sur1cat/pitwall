package claude

import (
	"os"
	"path/filepath"
	"testing"
)

func withStats(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	if body != "" {
		if err := os.WriteFile(filepath.Join(dir, "stats-cache.json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReadStatsDeclinesWhatItDoesNotUnderstand(t *testing.T) {
	// The file is undocumented, so a shape this code has not seen must produce
	// no reading rather than a wrong one — a lifetime cost figure that is
	// quietly wrong is worse than an absent one.
	cases := map[string]string{
		"missing file":    "",
		"not json":        "{{{",
		"unknown version": `{"version":99,"modelUsage":{"m":{"inputTokens":1}}}`,
		"no model usage":  `{"version":5,"modelUsage":{}}`,
	}
	for name, body := range cases {
		withStats(t, body)
		if _, ok := ReadStats(); ok {
			t.Errorf("%s: should not have produced a reading", name)
		}
	}
}

func TestReadStatsSumsAndSorts(t *testing.T) {
	withStats(t, `{
      "version": 5,
      "firstSessionDate": "2026-06-24T08:43:17.038Z",
      "lastComputedDate": "2026-08-24",
      "totalSessions": 237,
      "totalMessages": 227244,
      "modelUsage": {
        "claude-opus-5": {"inputTokens":10,"outputTokens":20,"cacheReadInputTokens":30,"cacheCreationInputTokens":40}
      },
      "dailyActivity": [
        {"date":"2026-08-24","messageCount":5,"sessionCount":1,"toolCallCount":9},
        {"date":"2026-06-24","messageCount":3,"sessionCount":2,"toolCallCount":4}
      ]
    }`)
	st, ok := ReadStats()
	if !ok {
		t.Fatal("a known version with model usage should read")
	}
	if st.TotalSessions != 237 || st.TotalMessages != 227244 {
		t.Errorf("totals not carried through: %+v", st)
	}
	if got := st.ByModel["claude-opus-5"].Total(); got != 100 {
		t.Errorf("model total = %d, want 100", got)
	}
	if len(st.Daily) != 2 || st.Daily[0].Date != "2026-06-24" {
		t.Errorf("daily series should be oldest first, got %+v", st.Daily)
	}
	if st.Span() <= 0 {
		t.Error("a first-session date gives a span")
	}
}
