package main

import (
	"testing"
	"time"
)

func TestParseSince(t *testing.T) {
	day := 24 * time.Hour
	ok := []struct {
		in   string
		want time.Duration
	}{
		{"30", 30 * day},  // the form that used to be a parse error
		{"30d", 30 * day}, // and so was this one
		{"2w", 14 * day},
		{"720h", 720 * time.Hour},
		{"12h", 12 * time.Hour},
		{"90m", 90 * time.Minute},
		{"1h30m", 90 * time.Minute},
		{"1.5d", 36 * time.Hour},
		{" 7D ", 7 * day},
		{"", 0},
	}
	for _, c := range ok {
		got, err := parseSince(c.in)
		if err != nil {
			t.Errorf("parseSince(%q) errored: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("parseSince(%q) = %v, want %v", c.in, got, c.want)
		}
	}
	for _, bad := range []string{"abc", "-5d", "-3h", "d", "30x", "w"} {
		if got, err := parseSince(bad); err == nil {
			t.Errorf("parseSince(%q) = %v, want an error", bad, got)
		}
	}
}

func TestSinceFlagRoundTrip(t *testing.T) {
	var d time.Duration
	f := sinceFlag{&d}
	if err := f.Set("30d"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if d != 30*24*time.Hour {
		t.Fatalf("d = %v, want 720h", d)
	}
	if got := f.String(); got != "30d" {
		t.Errorf("String() = %q, want %q", got, "30d")
	}
	_ = f.Set("90m")
	if got := f.String(); got != "1h30m0s" {
		t.Errorf("String() = %q, want a plain duration", got)
	}
}
