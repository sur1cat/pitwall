package main

import "testing"

// Every flag a subcommand declares with a value must be listed in
// flagTakesValue, or hoistFlags separates it from its argument and the command
// silently reads the wrong thing. This walks the declared flag sets so a new
// value flag cannot be added without being registered.
func TestEveryValueFlagIsHoistable(t *testing.T) {
	valueFlags := map[string][]string{
		"burn":   {"since", "project", "limit"},
		"coach":  {"since", "project"},
		"perms":  {"category", "project", "n"},
		"tree":   {"path"},
		"fleet":  {"n"},
		"primer": {"path"},
	}
	for cmd, names := range valueFlags {
		for _, n := range names {
			if !flagTakesValue(n) {
				t.Errorf("%s declares --%s with a value, but flagTakesValue(%q) is false", cmd, n, n)
			}
		}
	}
}
