package claude

import (
	"testing"
	"time"
)

func TestRetentionTrimming(t *testing.T) {
	cases := []struct {
		name string
		r    Retention
		want bool
	}{
		{"empty archive is not trimming", Retention{}, false},
		{"young archive under the default", Retention{Files: 5, Span: 9 * 24 * time.Hour}, false},
		{"archive at the default boundary", Retention{Files: 5, Span: 31 * 24 * time.Hour}, true},
		{"just inside the two-day margin", Retention{Files: 5, Span: 28 * 24 * time.Hour}, true},
		{"raised limit buys room", Retention{Files: 5, Span: 31 * 24 * time.Hour, Limit: 365, Set: true}, false},
		{"raised limit eventually fills", Retention{Files: 5, Span: 400 * 24 * time.Hour, Limit: 365, Set: true}, true},
	}
	for _, c := range cases {
		if got := c.r.Trimming(); got != c.want {
			t.Errorf("%s: Trimming() = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestEffectiveLimitFallsBackToTheDefault(t *testing.T) {
	if got := (Retention{}).EffectiveLimit(); got != DefaultCleanupDays {
		t.Errorf("unset limit = %d, want %d", got, DefaultCleanupDays)
	}
	if got := (Retention{Limit: 90, Set: true}).EffectiveLimit(); got != 90 {
		t.Errorf("configured limit = %d, want 90", got)
	}
	// A limit recorded without Set is not trusted: an absent key reads as 0.
	if got := (Retention{Limit: 90}).EffectiveLimit(); got != DefaultCleanupDays {
		t.Errorf("unconfirmed limit = %d, want %d", got, DefaultCleanupDays)
	}
}
