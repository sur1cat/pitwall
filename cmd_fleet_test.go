package main

import "testing"

func TestOneLineDropsMarkdownAndCodeBlocks(t *testing.T) {
	in := "Committed and pushed.\n\n```\n25b7f73 connector number\n← main = origin/main\n```\n\n## What changed\n\n- **one** thing\n- another"
	got := oneLine(in)
	for _, unwanted := range []string{"```", "25b7f73", "origin/main", "##", "**"} {
		if contains(got, unwanted) {
			t.Errorf("oneLine kept %q: %s", unwanted, got)
		}
	}
	want := "Committed and pushed. · What changed · one thing · another"
	if got != want {
		t.Errorf("oneLine = %q, want %q", got, want)
	}
}

func TestOneLineFallsBackWhenEverythingIsStripped(t *testing.T) {
	// A message that is nothing but a code block still has to say something.
	if got := oneLine("```\nnpm test\n```"); got == "" {
		t.Error("a message of only fenced code must not flatten to nothing")
	}
	if got := oneLine("plain text"); got != "plain text" {
		t.Errorf("oneLine(%q) = %q", "plain text", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
