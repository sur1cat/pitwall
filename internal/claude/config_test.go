package claude

import "testing"

func TestCompareGatesNamesEveryKindOfMove(t *testing.T) {
	before := map[string]bool{"stays_on": true, "goes_off": true, "comes_on": false, "disappears": true}
	after := map[string]bool{"stays_on": true, "goes_off": false, "comes_on": true, "appears": true}

	d := CompareGates(before, after)
	if !d.Any() {
		t.Fatal("four changes should register as a change")
	}
	eq := func(got []string, want ...string) bool {
		if len(got) != len(want) {
			return false
		}
		for i := range want {
			if got[i] != want[i] {
				return false
			}
		}
		return true
	}
	if !eq(d.TurnedOn, "comes_on") {
		t.Errorf("TurnedOn = %v", d.TurnedOn)
	}
	if !eq(d.TurnedOff, "goes_off") {
		t.Errorf("TurnedOff = %v", d.TurnedOff)
	}
	if !eq(d.Appeared, "appears") {
		t.Errorf("Appeared = %v", d.Appeared)
	}
	if !eq(d.Vanished, "disappears") {
		t.Errorf("Vanished = %v", d.Vanished)
	}
	// A switch that did not move must not be reported: a diff full of noise is
	// one nobody reads, and the point is to notice the one thing that changed.
	if got := CompareGates(before, before); got.Any() {
		t.Errorf("identical readings should show nothing, got %+v", got)
	}
}

func TestCompareGatesHandlesEmptyReadings(t *testing.T) {
	if got := CompareGates(nil, nil); got.Any() {
		t.Error("two empty readings differ in nothing")
	}
	if got := CompareGates(nil, map[string]bool{"a": true}); len(got.Appeared) != 1 {
		t.Errorf("everything is new against an empty baseline, got %+v", got)
	}
}
