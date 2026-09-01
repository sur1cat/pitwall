package main

import (
	"flag"
	"testing"
)

// The list this test used to guard is gone: hoistFlags now asks the FlagSet
// which flags take a value, so a new one cannot be forgotten. What is worth
// checking is that the asking works.
func TestHoistFlagsAsksTheFlagSet(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	var since, out string
	var write bool
	fs.StringVar(&since, "since", "", "")
	fs.StringVar(&out, "out", "", "")
	fs.BoolVar(&write, "write", false, "")

	cases := []struct{ in, want []string }{
		{[]string{"PATH", "--write"}, []string{"--write", "PATH"}},
		{[]string{"--since", "30d", "PATH"}, []string{"--since", "30d", "PATH"}},
		{[]string{"word", "--out", "f.md"}, []string{"--out", "f.md", "word"}},
		// A boolean must not swallow what follows it.
		{[]string{"--write", "PATH"}, []string{"--write", "PATH"}},
		// An unknown flag is left alone rather than guessed at.
		{[]string{"--unknown", "value"}, []string{"--unknown", "value"}},
		{[]string{"--", "--literal"}, []string{"--", "--literal"}},
	}
	for _, c := range cases {
		got := hoistFlags(fs, c.in)
		if len(got) != len(c.want) {
			t.Errorf("hoistFlags(%q) = %q, want %q", c.in, got, c.want)
			continue
		}
		for i := range c.want {
			if got[i] != c.want[i] {
				t.Errorf("hoistFlags(%q) = %q, want %q", c.in, got, c.want)
				break
			}
		}
	}
}
