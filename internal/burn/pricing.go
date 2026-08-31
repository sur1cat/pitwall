// Package pricing converts token counts into dollars using Anthropic's
// published first-party rates.
package burn

import (
	"encoding/json"
	"os"
	"strings"
	"time"
)

// Rate is the price of one model, in US dollars per million tokens.
type Rate struct {
	Input  float64 `json:"input"`
	Output float64 `json:"output"`

	// IntroInput and IntroOutput apply until IntroUntil, for models launched
	// with promotional pricing.
	IntroInput  float64 `json:"intro_input,omitempty"`
	IntroOutput float64 `json:"intro_output,omitempty"`
	IntroUntil  string  `json:"intro_until,omitempty"`
}

// Cache multipliers relative to a model's input rate.
const (
	// CacheWrite5m is the 5-minute-TTL cache write premium.
	CacheWrite5m = 1.25
	// CacheWrite1h is the 1-hour-TTL cache write premium.
	CacheWrite1h = 2.0
	// CacheRead is what a cache hit costs.
	CacheRead = 0.1
)

// table holds Anthropic's first-party API rates. Third-party platforms
// (Bedrock, Vertex) bill separately; override them with a pricing file.
var table = map[string]Rate{
	"claude-fable-5":  {Input: 10, Output: 50},
	"claude-mythos-5": {Input: 10, Output: 50},

	"claude-opus-5":   {Input: 5, Output: 25},
	"claude-opus-4-8": {Input: 5, Output: 25},
	"claude-opus-4-7": {Input: 5, Output: 25},
	"claude-opus-4-6": {Input: 5, Output: 25},

	// Claude Sonnet 5 launched with promotional pricing.
	"claude-sonnet-5":   {Input: 3, Output: 15, IntroInput: 2, IntroOutput: 10, IntroUntil: "2026-08-31"},
	"claude-sonnet-4-6": {Input: 3, Output: 15},

	"claude-haiku-4-5": {Input: 1, Output: 5},

	// Fast mode runs the same model at premium rates.
	"claude-opus-5-fast":   {Input: 10, Output: 50},
	"claude-opus-4-8-fast": {Input: 10, Output: 50},
}

// aliases maps the short names Claude Code sometimes records.
var aliases = map[string]string{
	"opus":   "claude-opus-5",
	"sonnet": "claude-sonnet-5",
	"haiku":  "claude-haiku-4-5",
}

// Load merges a user pricing file over the built-in table. The file is a JSON
// object of model id to {"input": N, "output": N} in dollars per million
// tokens — use it for Bedrock/Vertex rates or models released after this
// build.
func Load(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var custom map[string]Rate
	if err := json.Unmarshal(raw, &custom); err != nil {
		return err
	}
	for id, rate := range custom {
		table[id] = rate
	}
	return nil
}

// Normalize reduces a recorded model string to a table key. It strips context
// suffixes such as "[1m]" and dated snapshots, and resolves short aliases.
func Normalize(model string) string {
	m := strings.TrimSpace(model)
	if m == "" || m == "<synthetic>" {
		return ""
	}
	if i := strings.Index(m, "["); i > 0 {
		m = m[:i]
	}
	m = strings.TrimSuffix(m, "-latest")
	if full, ok := aliases[m]; ok {
		return full
	}
	if _, ok := table[m]; ok {
		return m
	}
	// Strip a trailing -YYYYMMDD snapshot, e.g. claude-haiku-4-5-20251001.
	if i := strings.LastIndex(m, "-"); i > 0 && len(m)-i == 9 && isDigits(m[i+1:]) {
		if base := m[:i]; base != "" {
			return base
		}
	}
	return m
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return len(s) > 0
}

// Usage is a token count to be priced.
type Usage struct {
	Input        int64
	Output       int64
	CacheWrite5m int64
	CacheWrite1h int64
	CacheRead    int64
}

// Add accumulates another usage record.
func (u *Usage) Add(o Usage) {
	u.Input += o.Input
	u.Output += o.Output
	u.CacheWrite5m += o.CacheWrite5m
	u.CacheWrite1h += o.CacheWrite1h
	u.CacheRead += o.CacheRead
}

// Total counts every token, billed or cached.
func (u Usage) Total() int64 {
	return u.Input + u.Output + u.CacheWrite5m + u.CacheWrite1h + u.CacheRead
}

// Cost breaks a bill down by what drove it.
type Cost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheWrite float64 `json:"cache_write"`
	CacheRead  float64 `json:"cache_read"`
}

// Total is the sum of every component.
func (c Cost) Total() float64 { return c.Input + c.Output + c.CacheWrite + c.CacheRead }

// Add accumulates another cost.
func (c *Cost) Add(o Cost) {
	c.Input += o.Input
	c.Output += o.Output
	c.CacheWrite += o.CacheWrite
	c.CacheRead += o.CacheRead
}

// Compute prices a usage record for a model at a point in time. The boolean
// reports whether the model was known; unknown models price at zero so token
// counts stay accurate even when a rate is missing.
func Compute(model string, at time.Time, u Usage) (Cost, bool) {
	rate, ok := table[Normalize(model)]
	if !ok {
		return Cost{}, false
	}
	in, out := rate.Input, rate.Output
	if rate.IntroUntil != "" && rate.IntroInput > 0 {
		if until, err := time.Parse("2006-01-02", rate.IntroUntil); err == nil {
			if !at.IsZero() && !at.After(until.AddDate(0, 0, 1)) {
				in, out = rate.IntroInput, rate.IntroOutput
			}
		}
	}
	const million = 1_000_000.0
	return Cost{
		Input:      float64(u.Input) / million * in,
		Output:     float64(u.Output) / million * out,
		CacheWrite: (float64(u.CacheWrite5m)*CacheWrite5m + float64(u.CacheWrite1h)*CacheWrite1h) / million * in,
		CacheRead:  float64(u.CacheRead) / million * in * CacheRead,
	}, true
}

// Known reports whether a model has a rate.
func Known(model string) bool {
	_, ok := table[Normalize(model)]
	return ok
}

// Models lists every priced model id.
func Models() []string {
	out := make([]string, 0, len(table))
	for id := range table {
		out = append(out, id)
	}
	return out
}
