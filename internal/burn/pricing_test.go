package burn

import (
	"math"
	"testing"
	"time"
)

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"claude-opus-5":             "claude-opus-5",
		"claude-opus-5[1m]":         "claude-opus-5",
		"claude-haiku-4-5-20251001": "claude-haiku-4-5",
		"opus":                      "claude-opus-5",
		"sonnet":                    "claude-sonnet-5",
		"haiku":                     "claude-haiku-4-5",
		"<synthetic>":               "",
		"":                          "",
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestComputeOpus(t *testing.T) {
	// 1M input, 1M output on Opus 5 is $5 + $25.
	c, ok := Compute("claude-opus-5", time.Now(), Usage{Input: 1_000_000, Output: 1_000_000})
	if !ok {
		t.Fatal("claude-opus-5 should be priced")
	}
	if !closeTo(c.Input, 5) || !closeTo(c.Output, 25) {
		t.Errorf("input=%.4f output=%.4f, want 5 and 25", c.Input, c.Output)
	}
	if !closeTo(c.Total(), 30) {
		t.Errorf("total = %.4f, want 30", c.Total())
	}
}

func TestComputeCacheMultipliers(t *testing.T) {
	// Cache writes cost more than fresh input; cache reads cost a tenth.
	c, _ := Compute("claude-opus-5", time.Now(), Usage{CacheWrite5m: 1_000_000})
	if !closeTo(c.CacheWrite, 5*CacheWrite5m) {
		t.Errorf("5m cache write = %.4f, want %.4f", c.CacheWrite, 5*CacheWrite5m)
	}
	c, _ = Compute("claude-opus-5", time.Now(), Usage{CacheWrite1h: 1_000_000})
	if !closeTo(c.CacheWrite, 5*CacheWrite1h) {
		t.Errorf("1h cache write = %.4f, want %.4f", c.CacheWrite, 5*CacheWrite1h)
	}
	c, _ = Compute("claude-opus-5", time.Now(), Usage{CacheRead: 1_000_000})
	if !closeTo(c.CacheRead, 5*CacheRead) {
		t.Errorf("cache read = %.4f, want %.4f", c.CacheRead, 5*CacheRead)
	}
}

func TestIntroPricingAppliesByDate(t *testing.T) {
	before := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	after := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

	c, _ := Compute("claude-sonnet-5", before, Usage{Input: 1_000_000})
	if !closeTo(c.Input, 2) {
		t.Errorf("intro input = %.4f, want 2", c.Input)
	}
	c, _ = Compute("claude-sonnet-5", after, Usage{Input: 1_000_000})
	if !closeTo(c.Input, 3) {
		t.Errorf("standard input = %.4f, want 3", c.Input)
	}
}

func TestUnknownModelPricesToZero(t *testing.T) {
	c, ok := Compute("claude-from-the-future-9", time.Now(), Usage{Input: 1_000_000})
	if ok {
		t.Error("unknown model should report ok=false")
	}
	if c.Total() != 0 {
		t.Errorf("unknown model cost = %.4f, want 0", c.Total())
	}
}

func closeTo(a, b float64) bool { return math.Abs(a-b) < 1e-9 }
