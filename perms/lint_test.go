package perms

import "testing"

func TestHasSecretIsNarrow(t *testing.T) {
	yes := []string{
		`Bash(TOKEN=eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9 curl https://api.example.com)`,
		`Bash(PGPASSWORD=hunter2xyz psql -h db)`,
		`Bash(GITLAB_TOKEN=glpat-AAAABBBBCCCC glab mr list)`,
		`Bash(curl -H "Authorization: Bearer sk-ant-abcdefghijkl" https://x)`,
	}
	no := []string{
		// A reference leaks nothing — the value lives elsewhere.
		`Bash(TOKEN=$GITLAB_TOKEN glab mr list)`,
		`Bash(PGPASSWORD=$(pass db) psql -h db)`,
		`Bash(TOKEN=* curl https://api.example.com)`,
		// Searching for the word is not the same as storing the value.
		`Bash(rg "api_key" src/*)`,
		`Bash(grep -r password .)`,
		`Bash(npm run build)`,
		`Read(./.env)`,
	}
	for _, s := range yes {
		if !hasSecret(s) {
			t.Errorf("hasSecret(%q) = false, want true", s)
		}
	}
	for _, s := range no {
		if hasSecret(s) {
			t.Errorf("hasSecret(%q) = true, want false", s)
		}
	}
}

func TestLintRedactsRatherThanEchoes(t *testing.T) {
	r := Rule{Raw: `Bash(TOKEN=eyJhbGciOiJIUzI1NiJ9 curl x)`, Kind: "allow", Tool: "Bash", Arg: "TOKEN=eyJhbGciOiJIUzI1NiJ9 curl x"}
	fs := Lint([]Rule{r})
	if len(fs) != 1 {
		t.Fatalf("got %d findings, want 1", len(fs))
	}
	if fs[0].Category != "secret" || !fs[0].Redacted {
		t.Errorf("a credential rule must be reported as a redacted secret, got %+v", fs[0])
	}
}
