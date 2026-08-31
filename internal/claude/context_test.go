package claude

import "testing"

func TestContextWindowResolvesDatedAndSuffixedIds(t *testing.T) {
	cases := map[string]int64{
		"claude-opus-5":             1_000_000,
		"claude-opus-5[1m]":         1_000_000,
		"claude-sonnet-5":           1_000_000,
		"claude-haiku-4-5":          200_000,
		"claude-haiku-4-5-20251001": 200_000,
	}
	for id, want := range cases {
		got, ok := ContextWindow(id)
		if !ok || got != want {
			t.Errorf("ContextWindow(%q) = %d %v, want %d", id, got, ok, want)
		}
	}
	// An unknown model gets no reading. A wrong context bar is worse than an
	// absent one, because a wrong one gets acted on.
	for _, id := range []string{"", "gpt-4", "claude-future-9"} {
		if _, ok := ContextWindow(id); ok {
			t.Errorf("ContextWindow(%q) should not resolve", id)
		}
	}
}

func TestContextFraction(t *testing.T) {
	tail := Tail{Context: 250_000, ContextModel: "claude-opus-5"}
	f, ok := tail.ContextFraction()
	if !ok || f < 0.24 || f > 0.26 {
		t.Errorf("fraction = %.3f %v, want about 0.25", f, ok)
	}
	// Haiku's window is five times smaller, so the same tokens fill it.
	small := Tail{Context: 250_000, ContextModel: "claude-haiku-4-5"}
	if f, _ := small.ContextFraction(); f != 1 {
		t.Errorf("an overfull window should clamp to 1, got %.3f", f)
	}
	for _, tl := range []Tail{{}, {Context: 100}, {ContextModel: "claude-opus-5"}} {
		if _, ok := tl.ContextFraction(); ok {
			t.Errorf("%+v should report no reading", tl)
		}
	}
}
