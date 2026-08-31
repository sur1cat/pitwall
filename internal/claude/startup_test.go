package claude

import "testing"

func TestFirstRequestReadsTheOpeningNotAnAggregate(t *testing.T) {
	dir := t.TempDir()
	// The opening request is the first assistant reply with usage. Summing the
	// session, or reading an hour of it, overstates this by orders of
	// magnitude — that mistake produced 3.8M where the answer was 34K.
	p := writeSubTranscript(t, dir, "s.jsonl",
		`{"type":"user","message":{"role":"user","content":"hello"}}`,
		`{"type":"assistant","message":{"model":"claude-opus-5","usage":{"input_tokens":12,"cache_read_input_tokens":34000,"cache_creation_input_tokens":400}}}`,
		`{"type":"assistant","message":{"model":"claude-opus-5","usage":{"input_tokens":9,"cache_read_input_tokens":900000}}}`)

	got, ok := firstRequest(p)
	if !ok {
		t.Fatal("a transcript with a priced reply has an opening size")
	}
	if got.Tokens != 34412 {
		t.Errorf("tokens = %d, want 34412 (only the first reply)", got.Tokens)
	}
	if got.Model != "claude-opus-5" {
		t.Errorf("model = %q", got.Model)
	}
}

func TestFirstRequestSkipsUnpricedLines(t *testing.T) {
	dir := t.TempDir()
	p := writeSubTranscript(t, dir, "s.jsonl",
		`{"type":"system","subtype":"init"}`,
		`{"type":"assistant","message":{"model":"m","usage":{}}}`,
		`{"type":"assistant","message":{"model":"m","usage":{"input_tokens":500}}}`)
	got, ok := firstRequest(p)
	if !ok || got.Tokens != 500 {
		t.Errorf("got %d %v, want the first reply that was actually priced", got.Tokens, ok)
	}

	empty := writeSubTranscript(t, dir, "empty.jsonl", `{"type":"user"}`)
	if _, ok := firstRequest(empty); ok {
		t.Error("a transcript with no priced reply has no opening size")
	}
}

func TestMedianStartupTakesTheMiddleOfASortedList(t *testing.T) {
	s := []Startup{{Tokens: 100}, {Tokens: 50}, {Tokens: 10}}
	if got := MedianStartup(s); got != 50 {
		t.Errorf("median = %d, want 50", got)
	}
	if got := MedianStartup(nil); got != 0 {
		t.Errorf("no sessions have no median, got %d", got)
	}
}
