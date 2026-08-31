// Package lint judges a prompt by the only standard that matters: how often
// prompts shaped like it forced another round trip before any work happened.
//
// Every rule here comes from measuring one developer's own history, and each
// carries the numbers it was derived from.
package lint

import (
	"regexp"
	"strings"
)

// Rule is one measured prompt habit.
type Rule struct {
	ID string `json:"id"`
	// Title is what the rule found, in one line.
	Title string `json:"title"`
	// Evidence is the measurement behind it.
	Evidence string `json:"evidence"`
	// Fix is what to do instead.
	Fix string `json:"fix"`
	// Delta is the change in round-trip rate, in percentage points. Negative
	// means prompts with this trait needed fewer clarifications.
	Delta float64 `json:"delta"`
}

// Finding is a rule that fired on a specific prompt.
type Finding struct {
	Rule
	// Detail names what triggered it in this prompt.
	Detail string `json:"detail"`
}

var (
	fileRef    = regexp.MustCompile(`@[\w./-]+`)
	pathLike   = regexp.MustCompile(`[\w/-]+\.(go|py|ts|tsx|js|jsx|sql|md|json|ya?ml|rs|java|kt|rb|php|sh)\b`)
	vagueVerb  = regexp.MustCompile(`(?i)(посмотри|разберись|изучи|глянь|подумай|проанализируй|look at|investigate|check out|take a look)`)
	review     = regexp.MustCompile(`(?i)(ревью|review|аудит|audit|code-review)`)
	acceptance = regexp.MustCompile(`(?i)(чтобы|должн|проверь что|убедись|so that|make sure|expect|acceptance)`)
)

// stepSplit counts how many separate requests a prompt stacks.
func stepSplit(prompt string) int {
	n := strings.Count(prompt, " / ")
	for _, marker := range []string{"\n1.", "\n2.", "\n- ", "\n* "} {
		n += strings.Count(prompt, marker) / 2
	}
	return n + 1
}

// Check runs every rule against a prompt.
func Check(prompt string) []Finding {
	p := strings.TrimSpace(prompt)
	if p == "" || strings.HasPrefix(p, "/") {
		return nil
	}
	var out []Finding

	steps := stepSplit(p)
	if steps >= 4 {
		out = append(out, Finding{
			Rule: Rule{
				ID:       "stacked-steps",
				Title:    "Several separate requests in one prompt",
				Evidence: "prompts with 3+ stacked steps needed another round 9.5% of the time, against 4.8% without",
				Fix:      "send the first one, then the next — a stacked prompt gets answered with a question",
				Delta:    4.7,
			},
			Detail: "found about " + itoa(steps) + " separate asks",
		})
	}

	hasRef := fileRef.MatchString(p) || pathLike.MatchString(p)
	if !hasRef {
		out = append(out, Finding{
			Rule: Rule{
				ID:       "no-file-reference",
				Title:    "No file or path named",
				Evidence: "prompts naming a file with @ needed another round 1.3% of the time, against 5.6% without — and cost less",
				Fix:      "point at the file: @path/to/thing.go",
				Delta:    -4.3,
			},
			Detail: "nothing in this prompt says where to look",
		})
	}

	if vagueVerb.MatchString(p) && !hasRef {
		out = append(out, Finding{
			Rule: Rule{
				ID:       "vague-verb",
				Title:    "An open-ended verb with nothing to anchor it",
				Evidence: "prompts like this needed another round 9.0% of the time, against 5.1%",
				Fix:      "say what you want changed or decided, not only what to look at",
				Delta:    4.0,
			},
			Detail: strings.ToLower(vagueVerb.FindString(p)) + " — without a file or a decision to make",
		})
	}

	if review.MatchString(p) && !acceptance.MatchString(p) && steps < 4 {
		out = append(out, Finding{
			Rule: Rule{
				ID:       "unscoped-review",
				Title:    "A review with no scope or bar",
				Evidence: "review prompts needed another round 9.8% of the time, against 4.6% — and cost $7.56 against $4.87",
				Fix:      "name the diff or files, and what counts as worth reporting",
				Delta:    5.2,
			},
			Detail: "this is your most expensive prompt shape",
		})
	}

	return out
}

// Context turns findings into a note for the agent. It never rewrites the
// prompt: it tells the agent what is unstated and asks it to assume rather
// than interrupt, which is where the round-trip cost actually goes.
func Context(findings []Finding) string {
	if len(findings) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("pitwall notes about this prompt, from how earlier prompts like it played out:\n")
	for _, f := range findings {
		b.WriteString("- " + f.Title + ". " + f.Fix + "\n")
	}
	b.WriteString("\nPrompts shaped like this one usually ended with you asking a question and " +
		"the user answering in a few words, which costs a full turn each way. Prefer making the " +
		"reasonable assumption and saying which one you made, in a sentence, then doing the work. " +
		"Ask only when proceeding either way would be unsafe or would waste the effort.\n")
	return b.String()
}

func itoa(n int) string {
	if n <= 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
