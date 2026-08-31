package coach

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func ex(prompt string, cost float64, tools, edits int, session string, at time.Time) *Exchange {
	return &Exchange{Prompt: prompt, Cost: cost, Tools: tools, Edits: edits, Session: session, Time: at}
}

func TestClass(t *testing.T) {
	cases := []struct {
		tools, edits int
		want         Class
	}{
		{0, 0, ClassTalk},
		{5, 0, ClassInvestigate},
		{5, 2, ClassExecute},
	}
	for _, c := range cases {
		e := &Exchange{Tools: c.tools, Edits: c.edits}
		if got := e.Class(); got != c.want {
			t.Errorf("tools=%d edits=%d -> %s, want %s", c.tools, c.edits, got, c.want)
		}
	}
}

// TestCorrectionMatchesCyrillic guards a real bug: Go's \b is ASCII-only, so a
// word-boundary anchor never fires next to Cyrillic and every Russian
// correction went undetected.
func TestCorrectionMatchesCyrillic(t *testing.T) {
	hits := []string{
		"стоп ты не то сделал",
		"это неправильно, откати",
		"я же говорил не трогать миграции",
		"зачем ты переписал весь файл",
		"revert that please",
		"undo the last change",
	}
	for _, s := range hits {
		if !correction.MatchString(s) {
			t.Errorf("expected a correction: %q", s)
		}
	}
	misses := []string{
		"добавь тест на этот случай",
		"посмотри почему падает сборка",
		"undoubtedly the best approach", // must not fire on a substring
	}
	for _, s := range misses {
		if correction.MatchString(s) {
			t.Errorf("false positive: %q", s)
		}
	}
}

func TestContinuationMatchesShortNudges(t *testing.T) {
	for _, s := range []string{"давай", "продолжай", "да", "ok", "continue", "Давай дальше"} {
		if !continuation.MatchString(s) {
			t.Errorf("expected a continuation: %q", s)
		}
	}
	if continuation.MatchString("давай перепишем этот сервис на gRPC") {
		t.Error("a real instruction must not count as a nudge")
	}
}

func TestRoundTripsAndRework(t *testing.T) {
	now := time.Now()
	list := []*Exchange{
		ex("сделай ревью схемы", 10, 8, 0, "s1", now),
		ex("да", 4, 3, 1, "s1", now.Add(time.Minute)),
		ex("перепиши обработчик платежей полностью, вот требования: ...", 12, 20, 4, "s1", now.Add(2*time.Minute)),
		ex("стоп, ты не то сделал", 2, 1, 0, "s1", now.Add(3*time.Minute)),
	}
	list[0].EndedOnQuestion = true

	r := Analyse(list)
	var rt, rw *Finding
	for i := range r.Findings {
		switch r.Findings[i].ID {
		case "round-trips":
			rt = &r.Findings[i]
		case "rework":
			rw = &r.Findings[i]
		}
	}
	if rt == nil {
		t.Fatal("no round-trip finding")
	}
	if rt.Amount != 14 { // the question turn plus the one-word answer
		t.Errorf("round-trip amount = %.0f, want 14", rt.Amount)
	}
	if rw == nil {
		t.Fatal("no rework finding")
	}
	if rw.Amount != 12 { // the turn the correction undid
		t.Errorf("rework amount = %.0f, want 12", rw.Amount)
	}
}

func TestPrimedDetectsEveryStartingPoint(t *testing.T) {
	bare := t.TempDir()
	if Primed(bare) {
		t.Error("an empty repository is not primed")
	}
	withMD := t.TempDir()
	if err := os.WriteFile(filepath.Join(withMD, "CLAUDE.md"), []byte("# x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !Primed(withMD) {
		t.Error("a CLAUDE.md should count")
	}
	withAgents := t.TempDir()
	dir := filepath.Join(withAgents, ".claude", "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if Primed(withAgents) {
		t.Error("an empty agents directory should not count")
	}
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !Primed(withAgents) {
		t.Error("project agents should count")
	}
}

func TestColdStartComparesPrimedAgainstCold(t *testing.T) {
	cold, warm := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(warm, "CLAUDE.md"), []byte("# warm\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	var list []*Exchange
	for i := 0; i < 3; i++ {
		e := ex("work", 10, 40, 1, "cold-session", now.Add(time.Duration(i)*time.Minute))
		e.CWD = cold
		list = append(list, e)
	}
	for i := 0; i < 3; i++ {
		e := ex("work", 2, 10, 1, "warm-session", now.Add(time.Duration(i)*time.Minute))
		e.CWD = warm
		list = append(list, e)
	}
	r := Analyse(list)
	var f *Finding
	for i := range r.Findings {
		if r.Findings[i].ID == "cold-start" {
			f = &r.Findings[i]
		}
	}
	if f == nil {
		t.Fatal("no cold-start finding")
	}
	if f.Amount != 24 { // ($10 - $2) over three cold opening prompts
		t.Errorf("recoverable = %.0f, want 24", f.Amount)
	}
	if !f.Correlated {
		t.Error("cold-start is an association and must be labelled as one")
	}
}

func TestAnalyseIsEmptyWithoutInput(t *testing.T) {
	if r := Analyse(nil); r.Prompts != 0 || len(r.Findings) != 0 {
		t.Errorf("expected an empty report, got %+v", r)
	}
}
