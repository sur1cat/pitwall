package primer

import (
	"strings"
	"testing"
)

func TestNormalizeCommand(t *testing.T) {
	cases := map[string]string{
		"go test ./...":                        "go test",
		"cd /src/api && go build ./cmd/server": "go build",
		"PGPASSWORD=secret psql -h db -U app":  "psql",
		"npx vitest run --reporter dot":        "npx vitest",
		"golangci-lint run":                    "golangci-lint run",
		"/usr/local/bin/make test":             "make test",
		"ls -la":                               "",
		"echo hello":                           "",
		"for f in *.go; do echo $f; done":      "",
		"source .venv/bin/activate":            "",
		"git diff --stat HEAD~1":               "git diff",
		"":                                     "",
		"   ":                                  "",
		"docker compose up -d":                 "docker compose",
	}
	for in, want := range cases {
		if got := normalizeCommand(in); got != want {
			t.Errorf("normalizeCommand(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestNormalizeCommandIgnoresHeredocBodies covers the artefacts that made the
// first version report "func" and "'" as frequently used commands.
func TestNormalizeCommandIgnoresHeredocBodies(t *testing.T) {
	// "cat" is itself noise, so the point here is that the Go source inside
	// the heredoc never surfaces as a command.
	cmd := "cat > main.go <<'EOF'\nfunc main() {\n\tprintln(\"x\")\n}\nEOF"
	if got := normalizeCommand(cmd); got != "" {
		t.Errorf("heredoc body leaked: got %q", got)
	}
	for _, leak := range []string{"func", "func main()", "'", "EOF"} {
		if normalizeCommand(cmd) == leak {
			t.Errorf("heredoc body leaked as %q", leak)
		}
	}
	sql := "psql -d app <<'SQL'\nSELECT * FROM users;\nSQL"
	if got := normalizeCommand(sql); got != "psql" {
		t.Errorf("got %q, want psql", got)
	}
}

func TestTopFiltersRareEntries(t *testing.T) {
	got := top(map[string]int{"a": 5, "b": 1, "c": 3}, 10, 2)
	if len(got) != 2 || got[0].Name != "a" || got[1].Name != "c" {
		t.Errorf("top() = %+v, want a then c", got)
	}
}

func TestMarkdownAndContext(t *testing.T) {
	d := Draft{
		Name: "demo", Sessions: 9, ToolCalls: 400,
		Commands: []Count{{"go test", 40}, {"make deploy", 5}},
		Dirs:     []Count{{"internal/api", 90}},
		Files:    []Count{{"internal/api/server.go", 30}},
	}
	md := d.Markdown()
	for _, want := range []string{"# demo", "go test", "internal/api", "Fill these in"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q", want)
		}
	}
	ctx := d.Context()
	if !strings.Contains(ctx, "go test") || !strings.Contains(ctx, "no CLAUDE.md") {
		t.Errorf("context is missing its substance:\n%s", ctx)
	}
	if (Draft{}).Context() != "" {
		t.Error("an empty draft must produce no context")
	}
}

func TestTopTwoTrimsDeepPaths(t *testing.T) {
	if got := topTwo("a/b/c/d"); got != "a/b" {
		t.Errorf("topTwo = %q, want a/b", got)
	}
	if got := topTwo("a"); got != "a" {
		t.Errorf("topTwo = %q, want a", got)
	}
}
