package claude

import "strings"

// contextWindows is how many tokens each model can hold. A model that is not
// listed gets no reading rather than a guessed one: a context bar that is
// wrong is worse than one that is absent, because it is acted on.
var contextWindows = map[string]int64{
	"claude-opus-5":     1_000_000,
	"claude-opus-4-8":   1_000_000,
	"claude-opus-4-7":   1_000_000,
	"claude-opus-4-6":   1_000_000,
	"claude-sonnet-5":   1_000_000,
	"claude-sonnet-4-6": 1_000_000,
	"claude-fable-5":    1_000_000,
	"claude-haiku-4-5":  200_000,
}

// ContextWindow returns the window size for a model id, matching on the
// longest listed prefix so that dated ids like claude-haiku-4-5-20251001 and
// suffixed ones like claude-opus-5[1m] both resolve.
func ContextWindow(model string) (int64, bool) {
	if model == "" {
		return 0, false
	}
	best, bestLen := int64(0), 0
	for id, size := range contextWindows {
		if strings.HasPrefix(model, id) && len(id) > bestLen {
			best, bestLen = size, len(id)
		}
	}
	return best, bestLen > 0
}

// ContextFraction is how full the window was, from 0 to 1. It reports false
// when the model is unknown or nothing has been measured.
func (t Tail) ContextFraction() (float64, bool) {
	size, ok := ContextWindow(t.ContextModel)
	if !ok || size == 0 || t.Context <= 0 {
		return 0, false
	}
	f := float64(t.Context) / float64(size)
	if f > 1 {
		f = 1
	}
	return f, true
}
