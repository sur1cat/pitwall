package lint

import "testing"

func ids(fs []Finding) map[string]bool {
	out := map[string]bool{}
	for _, f := range fs {
		out[f.ID] = true
	}
	return out
}

func TestFileReferenceSilencesTheAnchorRules(t *testing.T) {
	got := ids(Check("исправь обработку ошибок в @internal/api/server.go"))
	if got["no-file-reference"] {
		t.Error("a prompt naming a file should not be flagged for having none")
	}
	if got["vague-verb"] {
		t.Error("an anchored prompt should not be flagged as vague")
	}
}

func TestNoFileReference(t *testing.T) {
	if !ids(Check("почини баг с оплатой"))["no-file-reference"] {
		t.Error("expected the missing-reference rule to fire")
	}
	if ids(Check("fix the retry in cmd/worker/main.go"))["no-file-reference"] {
		t.Error("a bare path should count as a reference")
	}
}

func TestStackedSteps(t *testing.T) {
	p := "почини логин / добавь тест / обнови доки / задеплой"
	if !ids(Check(p))["stacked-steps"] {
		t.Error("expected stacked steps to fire on four requests")
	}
	if ids(Check("почини логин / добавь тест"))["stacked-steps"] {
		t.Error("two steps is not a stack")
	}
}

func TestVagueVerbNeedsNoAnchor(t *testing.T) {
	if !ids(Check("посмотри что там с очередью"))["vague-verb"] {
		t.Error("expected the vague-verb rule to fire")
	}
	if ids(Check("посмотри @internal/queue/worker.go"))["vague-verb"] {
		t.Error("a vague verb with a file is anchored")
	}
}

func TestUnscopedReview(t *testing.T) {
	if !ids(Check("сделай код ревью"))["unscoped-review"] {
		t.Error("expected an unscoped review to fire")
	}
	if ids(Check("сделай ревью @api/handlers.go, важно чтобы не было гонок"))["unscoped-review"] {
		t.Error("a scoped review with a bar should not fire")
	}
}

func TestSlashCommandsAndEmptyAreIgnored(t *testing.T) {
	if len(Check("/code-review")) != 0 {
		t.Error("slash commands are not prompts to lint")
	}
	if len(Check("   ")) != 0 {
		t.Error("an empty prompt has nothing to say about it")
	}
}

func TestContextIsEmptyWithoutFindings(t *testing.T) {
	if Context(nil) != "" {
		t.Error("no findings must produce no context")
	}
	if Context(Check("сделай код ревью")) == "" {
		t.Error("findings must produce a note")
	}
}
