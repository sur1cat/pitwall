package quota

import (
	"testing"
	"time"
)

func TestRemainingAndUntil(t *testing.T) {
	w := Window{Utilization: 62, ResetsAt: time.Now().Add(2 * time.Hour)}
	if w.Remaining() != 38 {
		t.Errorf("Remaining = %.0f, want 38", w.Remaining())
	}
	if d := w.Until(); d < 110*time.Minute || d > 2*time.Hour {
		t.Errorf("Until = %s, want about 2h", d)
	}
	past := Window{Utilization: 100, ResetsAt: time.Now().Add(-time.Hour)}
	if past.Remaining() != 0 || past.Until() != 0 {
		t.Error("a finished window has nothing left and no time to run")
	}
}

// TestPaceFromMeasuresObservedRate is the whole point of measuring instead of
// assuming: two readings an hour apart, ten points apart, is ten points an hour.
func TestPaceFromMeasuresObservedRate(t *testing.T) {
	samples := []Sample{{At: time.Now().Add(-2 * time.Hour), FiveHour: 20}}
	p := paceFrom(samples, func(s Sample) float64 { return s.FiveHour }, 40)
	if !p.OK {
		t.Fatal("expected a measurable pace")
	}
	if p.PerHour < 9 || p.PerHour > 11 {
		t.Errorf("PerHour = %.2f, want about 10", p.PerHour)
	}
	if p.Span < 110*time.Minute {
		t.Errorf("Span = %s, want about 2h", p.Span)
	}
}

func TestPaceFromRefusesTooLittleObservation(t *testing.T) {
	if p := paceFrom(nil, func(s Sample) float64 { return s.FiveHour }, 40); p.OK {
		t.Error("no readings can produce no pace")
	}
	recent := []Sample{{At: time.Now().Add(-2 * time.Minute), FiveHour: 10}}
	if p := paceFrom(recent, func(s Sample) float64 { return s.FiveHour }, 40); p.OK {
		t.Error("two minutes of observation is not a pace")
	}
}

// TestPaceFromIgnoresReadingsBeforeAReset keeps a rollover from looking like a
// sudden drop in consumption.
func TestPaceFromIgnoresReadingsBeforeAReset(t *testing.T) {
	now := time.Now()
	samples := []Sample{
		{At: now.Add(-6 * time.Hour), FiveHour: 90},
		{At: now.Add(-5 * time.Hour), FiveHour: 95},
		{At: now.Add(-2 * time.Hour), FiveHour: 5}, // the window reset here
	}
	p := paceFrom(samples, func(s Sample) float64 { return s.FiveHour }, 25)
	if !p.OK {
		t.Fatal("expected a pace measured after the reset")
	}
	if p.PerHour < 8 || p.PerHour > 12 {
		t.Errorf("PerHour = %.2f, want about 10 from the post-reset readings", p.PerHour)
	}
	if p.Span > 3*time.Hour {
		t.Errorf("Span = %s, should only cover readings since the reset", p.Span)
	}
}

func TestPaceFromReportsASteadyWindow(t *testing.T) {
	samples := []Sample{{At: time.Now().Add(-time.Hour), FiveHour: 40}}
	p := paceFrom(samples, func(s Sample) float64 { return s.FiveHour }, 40)
	if !p.OK || p.PerHour != 0 {
		t.Errorf("a window that did not move is a measured zero, got %+v", p)
	}
}

func TestExhaustedInUsesTheMeasuredPace(t *testing.T) {
	w := Window{Utilization: 60, ResetsAt: time.Now().Add(3 * time.Hour)}
	// Twenty points an hour leaves forty points lasting two hours.
	d, ok := w.ExhaustedIn(Pace{PerHour: 20, Span: time.Hour, OK: true})
	if !ok {
		t.Fatal("expected a projection")
	}
	if d < 110*time.Minute || d > 130*time.Minute {
		t.Errorf("projected %s, want about 2h", d)
	}
}

func TestExhaustedInStaysSilentWhenItCannotKnow(t *testing.T) {
	w := Window{Utilization: 60, ResetsAt: time.Now().Add(3 * time.Hour)}
	if _, ok := w.ExhaustedIn(Pace{}); ok {
		t.Error("no measured pace means no projection")
	}
	if _, ok := w.ExhaustedIn(Pace{PerHour: 0, OK: true}); ok {
		t.Error("a steady window is not running out")
	}
	// One point an hour would take forty hours; the window resets in three.
	if _, ok := w.ExhaustedIn(Pace{PerHour: 1, Span: time.Hour, OK: true}); ok {
		t.Error("a pace that lands after the reset is not running out")
	}
	full := Window{Utilization: 100, ResetsAt: time.Now().Add(time.Hour)}
	if _, ok := full.ExhaustedIn(Pace{PerHour: 5, OK: true}); ok {
		t.Error("an exhausted window has nothing left to project")
	}
}

func TestTokenFromBlob(t *testing.T) {
	if got := tokenFromBlob("sk-ant-oat01-plain"); got != "sk-ant-oat01-plain" {
		t.Errorf("a bare token should pass through, got %q", got)
	}
	nested := `{"claudeAiOauth":{"accessToken":"sk-ant-nested","refreshToken":"r"}}`
	if got := tokenFromBlob(nested); got != "sk-ant-nested" {
		t.Errorf("nested token = %q", got)
	}
	if got := tokenFromBlob(`{"unrelated":1}`); got != "" {
		t.Errorf("expected nothing, got %q", got)
	}
	if got := tokenFromBlob("  "); got != "" {
		t.Errorf("expected nothing for blank input, got %q", got)
	}
}

func TestRetainSamplesDropsOldReadings(t *testing.T) {
	samples := []Sample{
		{At: time.Now().Add(-48 * time.Hour)},
		{At: time.Now().Add(-2 * time.Hour)},
	}
	if got := retainSamples(samples); len(got) != 1 {
		t.Errorf("kept %d readings, want only the recent one", len(got))
	}
}

func TestAverageUsesTheWholeOpenWindow(t *testing.T) {
	// A weekly window that resets in 69 hours has been open for 99, so 67%
	// consumed is 0.68 points an hour — an estimate resting on days of usage.
	w := Window{Utilization: 67, ResetsAt: time.Now().Add(69 * time.Hour)}
	avg, ok := w.Average(WeekLength)
	if !ok {
		t.Fatal("a window with a reset time has a computable average")
	}
	if avg.PerHour < 0.6 || avg.PerHour > 0.8 {
		t.Errorf("average = %.2f%%/h, want about 0.68", avg.PerHour)
	}
	d, ok := w.ExhaustedIn(avg)
	if !ok {
		t.Fatal("at that rate the window fills before it resets")
	}
	if h := d.Hours(); h < 45 || h > 53 {
		t.Errorf("full in %.0fh, want about 49", h)
	}
}

func TestAverageDeclinesWithoutAResetTime(t *testing.T) {
	if _, ok := (Window{Utilization: 50}).Average(WeekLength); ok {
		t.Error("without a reset time the window start is unknown")
	}
	// A window barely open has nothing to average over.
	w := Window{Utilization: 1, ResetsAt: time.Now().Add(WeekLength - time.Minute)}
	if _, ok := w.Average(WeekLength); ok {
		t.Error("a window open for a minute cannot supply a rate")
	}
}

func TestPaceIsUntrustworthyUntilTheWindowHasVisiblyMoved(t *testing.T) {
	// Utilization arrives in whole points. Two points over an hour is the
	// reading that produced an eighteen-hour projection from nothing.
	weak := Pace{PerHour: 1.83, Span: 65 * time.Minute, OK: true}
	if weak.Trustworthy() {
		t.Error("two points of movement must not be extrapolated across a day")
	}
	strong := Pace{PerHour: 1.83, Span: 5 * time.Hour, OK: true}
	if !strong.Trustworthy() {
		t.Error("nine points of movement is enough to project from")
	}
	if (Pace{}).Trustworthy() {
		t.Error("an absent pace is never trustworthy")
	}
}

func TestOpenedIsTheResetMinusTheLength(t *testing.T) {
	reset := time.Now().Add(24 * time.Hour)
	w := Window{ResetsAt: reset}
	opened, ok := w.Opened(WeekLength)
	if !ok || !opened.Equal(reset.Add(-WeekLength)) {
		t.Errorf("opened = %v, want %v", opened, reset.Add(-WeekLength))
	}
}
