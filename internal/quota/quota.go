// Package quota reads Anthropic's own view of how much of your plan you have
// used. It is the only part of pitwall that touches the network, and it talks
// to exactly one host with the credential Claude Code already stored.
package quota

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Endpoint is the usage API Claude Code itself calls.
const Endpoint = "https://api.anthropic.com/api/oauth/usage"

// betaHeader is required; without it the request is rejected.
const betaHeader = "oauth-2025-04-20"

// userAgent matters more than it looks: requests without a claude-code agent
// string get rate limited hard and stay limited.
const userAgent = "claude-code/2.1.222 (pitwall)"

// cacheTTL is the polling floor. The endpoint rate limits aggressively, and
// there is nothing to gain from asking more often.
const cacheTTL = 180 * time.Second

// Window is one usage bucket.
type Window struct {
	Utilization float64   `json:"utilization"`
	ResetsAt    time.Time `json:"resets_at"`
}

// Remaining is how much of this window is left, as a percentage.
func (w Window) Remaining() float64 {
	r := 100 - w.Utilization
	if r < 0 {
		return 0
	}
	return r
}

// Until is how long before this window resets.
func (w Window) Until() time.Duration {
	if w.ResetsAt.IsZero() {
		return 0
	}
	d := time.Until(w.ResetsAt)
	if d < 0 {
		return 0
	}
	return d
}

// Usage is Anthropic's answer.
type Usage struct {
	FiveHour       Window  `json:"five_hour"`
	SevenDay       Window  `json:"seven_day"`
	SevenDayOpus   *Window `json:"seven_day_opus"`
	SevenDaySonnet *Window `json:"seven_day_sonnet"`
	ExtraUsage     *struct {
		IsEnabled    bool     `json:"is_enabled"`
		MonthlyLimit *float64 `json:"monthly_limit"`
		UsedCredits  *float64 `json:"used_credits"`
		Utilization  *float64 `json:"utilization"`
	} `json:"extra_usage"`

	// FetchedAt is when this answer was obtained, so a cached one can say so.
	FetchedAt time.Time `json:"fetched_at"`
	// Cached reports whether this came from disk rather than the network.
	Cached bool `json:"cached"`

	// FiveHourPace and SevenDayPace are measured by pitwall, not reported by
	// the API.
	FiveHourPace Pace `json:"five_hour_pace"`
	SevenDayPace Pace `json:"seven_day_pace"`
}

// WeekLength is the span of the seven-day window. Its name and Anthropic's own
// documentation — "a rolling five_hour window and a weekly seven_day window" —
// give the length, so the elapsed share of it is arithmetic rather than a
// guess. That matters: a rate averaged over the days the window has been open
// rests on far more evidence than one measured across a few readings.
const WeekLength = 7 * 24 * time.Hour

// Pace is how fast a window is filling, measured from pitwall's own readings.
// Utilization arrives as whole percentage points, so a short span carries very
// little signal: two ticks of movement is two ticks whether they took ten
// minutes or an hour, and rounding alone puts ±1 point on the difference.
// TrustworthyDelta is the gate that keeps such a reading from being projected
// across a day.
type Pace struct {
	// PerHour is percentage points of the window consumed per hour.
	PerHour float64 `json:"per_hour"`
	// Span is how long the readings behind this rate cover.
	Span time.Duration `json:"span"`
	// OK is false until there are enough readings far enough apart.
	OK bool `json:"ok"`
}

// minPaceSpan is how much observation a rate needs before it means anything.
const minPaceSpan = 10 * time.Minute

// trustworthyDelta is how many whole percentage points a window must have
// moved before the measured rate is worth extrapolating. Below this the
// quantisation dominates: a rise of 2 points is 2 ± 1 after rounding, which
// swings an eighteen-hour projection between twelve hours and thirty-six.
const trustworthyDelta = 5.0

// Opened is when the window began, for a window of known length.
func (w Window) Opened(length time.Duration) (time.Time, bool) {
	if w.ResetsAt.IsZero() {
		return time.Time{}, false
	}
	return w.ResetsAt.Add(-length), true
}

// Average is percentage points per hour since the window opened. For the
// weekly window this rests on days of usage rather than on a handful of
// readings, which makes it the projection worth leading with.
func (w Window) Average(length time.Duration) (Pace, bool) {
	opened, ok := w.Opened(length)
	if !ok {
		return Pace{}, false
	}
	elapsed := time.Since(opened)
	if elapsed < time.Hour || elapsed > length {
		return Pace{}, false
	}
	return Pace{PerHour: w.Utilization / elapsed.Hours(), Span: elapsed, OK: true}, true
}

// Trustworthy reports whether a measured pace rests on enough movement to be
// worth extrapolating, as opposed to enough elapsed time.
func (p Pace) Trustworthy() bool {
	return p.OK && p.Span > 0 && p.PerHour*p.Span.Hours() >= trustworthyDelta
}

// ExhaustedIn projects when a window reaches 100% at a measured pace. It
// returns false when the pace is unknown, the window is idle or full, or the
// window would reset before filling.
func (w Window) ExhaustedIn(p Pace) (time.Duration, bool) {
	if !p.OK || p.PerHour <= 0 || w.Utilization >= 100 {
		return 0, false
	}
	hours := w.Remaining() / p.PerHour
	at := time.Duration(hours * float64(time.Hour))
	if w.Until() > 0 && at > w.Until() {
		return 0, false
	}
	return at, true
}

// Sample is one reading, kept so the next one can be compared against it.
type Sample struct {
	At       time.Time `json:"at"`
	FiveHour float64   `json:"five_hour"`
	SevenDay float64   `json:"seven_day"`
}

type cacheFile struct {
	Usage      Usage     `json:"usage"`
	BackoffTil time.Time `json:"backoff_until"`
	Failures   int       `json:"failures"`
	Samples    []Sample  `json:"samples"`
}

// paceFrom measures how fast a window filled across the retained readings. A
// drop means the window reset, so everything before it is discarded.
func paceFrom(samples []Sample, pick func(Sample) float64, now float64) Pace {
	if len(samples) == 0 {
		return Pace{}
	}
	start := 0
	for i := 1; i < len(samples); i++ {
		if pick(samples[i]) < pick(samples[i-1]) {
			start = i
		}
	}
	first := samples[start]
	if pick(first) > now {
		return Pace{} // the window reset since the last reading
	}
	span := time.Since(first.At)
	if span < minPaceSpan {
		return Pace{}
	}
	delta := now - pick(first)
	if delta <= 0 {
		return Pace{Span: span, OK: true} // steady: a real, measured zero
	}
	return Pace{PerHour: delta / span.Hours(), Span: span, OK: true}
}

// retainSamples keeps a day of readings, which is plenty to measure a pace and
// small enough to stay a rounding error on disk.
func retainSamples(samples []Sample) []Sample {
	cutoff := time.Now().Add(-24 * time.Hour)
	out := samples[:0]
	for _, s := range samples {
		if s.At.After(cutoff) {
			out = append(out, s)
		}
	}
	if len(out) > 500 {
		out = out[len(out)-500:]
	}
	return out
}

// Options controls a fetch.
type Options struct {
	// Dir is where the cache lives, normally ~/.claude/pitwall.
	Dir string
	// Force ignores the cache TTL, but never the backoff after a 429.
	Force bool
	// Token overrides credential discovery.
	Token string
	// CacheOnly never touches the network: for status lines and other paths
	// that must not wait.
	CacheOnly bool
}

// ErrNoCredential means Claude Code has not signed in on this machine, or the
// credential is somewhere pitwall does not look.
var ErrNoCredential = fmt.Errorf("no Claude Code credential found")

// Get returns the current usage, from cache when it is fresh enough.
func Get(opt Options) (Usage, error) {
	path := filepath.Join(opt.Dir, "quota-cache.json")
	var cache cacheFile
	if raw, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(raw, &cache)
	}

	fresh := time.Since(cache.Usage.FetchedAt) < cacheTTL
	blocked := time.Now().Before(cache.BackoffTil)
	if !cache.Usage.FetchedAt.IsZero() && (blocked || (fresh && !opt.Force)) {
		u := cache.Usage
		u.Cached = true
		if blocked {
			return u, fmt.Errorf("rate limited by the usage API; showing the reading from %s",
				u.FetchedAt.Format("15:04"))
		}
		return u, nil
	}

	if opt.CacheOnly {
		if cache.Usage.FetchedAt.IsZero() {
			return Usage{}, fmt.Errorf("no cached reading yet — run: pitwall quota")
		}
		u := cache.Usage
		u.Cached = true
		return u, nil
	}

	token := opt.Token
	if token == "" {
		var err error
		if token, err = Credential(); err != nil {
			return cache.Usage, err
		}
	}

	usage, err := fetch(token)
	if err != nil {
		// Back off hard: this endpoint stays limited for a long time.
		cache.Failures++
		steps := []time.Duration{3 * time.Minute, 6 * time.Minute, 12 * time.Minute, 15 * time.Minute}
		step := steps[min(cache.Failures, len(steps))-1]
		cache.BackoffTil = time.Now().Add(step)
		save(path, cache)
		if !cache.Usage.FetchedAt.IsZero() {
			u := cache.Usage
			u.Cached = true
			return u, fmt.Errorf("%w; showing the reading from %s", err, u.FetchedAt.Format("15:04"))
		}
		return Usage{}, err
	}

	usage.FetchedAt = time.Now()
	usage.FiveHourPace = paceFrom(cache.Samples, func(s Sample) float64 { return s.FiveHour }, usage.FiveHour.Utilization)
	usage.SevenDayPace = paceFrom(cache.Samples, func(s Sample) float64 { return s.SevenDay }, usage.SevenDay.Utilization)
	samples := append(retainSamples(cache.Samples), Sample{
		At: usage.FetchedAt, FiveHour: usage.FiveHour.Utilization, SevenDay: usage.SevenDay.Utilization,
	})
	save(path, cacheFile{Usage: usage, Samples: samples})
	return usage, nil
}

func save(path string, c cacheFile) {
	if os.MkdirAll(filepath.Dir(path), 0o755) != nil {
		return
	}
	if raw, err := json.Marshal(c); err == nil {
		_ = os.WriteFile(path, raw, 0o600)
	}
}

func fetch(token string) (Usage, error) {
	req, err := http.NewRequest(http.MethodGet, Endpoint, nil)
	if err != nil {
		return Usage{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("anthropic-beta", betaHeader)
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return Usage{}, fmt.Errorf("could not reach the usage API: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusTooManyRequests:
		return Usage{}, fmt.Errorf("the usage API is rate limiting this account")
	case http.StatusUnauthorized, http.StatusForbidden:
		return Usage{}, fmt.Errorf("the stored credential was rejected — run claude and sign in again")
	default:
		return Usage{}, fmt.Errorf("usage API returned %d", resp.StatusCode)
	}

	var u Usage
	if err := json.Unmarshal(body, &u); err != nil {
		return Usage{}, fmt.Errorf("could not read the usage response: %w", err)
	}
	return u, nil
}

// Credential finds the OAuth token Claude Code stored. It never returns it to
// anywhere but the request that needs it.
func Credential() (string, error) {
	if t := strings.TrimSpace(os.Getenv("CLAUDE_CODE_OAUTH_TOKEN")); t != "" {
		return t, nil
	}
	if runtime.GOOS == "darwin" {
		out, err := exec.Command("security", "find-generic-password",
			"-s", "Claude Code-credentials", "-w").Output()
		if err == nil {
			if tok := tokenFromBlob(strings.TrimSpace(string(out))); tok != "" {
				return tok, nil
			}
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", ErrNoCredential
	}
	raw, err := os.ReadFile(filepath.Join(home, ".claude", ".credentials.json"))
	if err != nil {
		return "", ErrNoCredential
	}
	if tok := tokenFromBlob(string(raw)); tok != "" {
		return tok, nil
	}
	return "", ErrNoCredential
}

// tokenFromBlob accepts either a bare token or the JSON envelope Claude Code
// stores, without caring which shape this version happens to use.
func tokenFromBlob(blob string) string {
	blob = strings.TrimSpace(blob)
	if blob == "" {
		return ""
	}
	if !strings.HasPrefix(blob, "{") {
		return blob
	}
	var envelope map[string]any
	if json.Unmarshal([]byte(blob), &envelope) != nil {
		return ""
	}
	return findToken(envelope, 0)
}

func findToken(node any, depth int) string {
	if depth > 4 {
		return ""
	}
	m, ok := node.(map[string]any)
	if !ok {
		return ""
	}
	for _, key := range []string{"accessToken", "access_token", "token"} {
		if v, ok := m[key].(string); ok && v != "" {
			return v
		}
	}
	for _, v := range m {
		if found := findToken(v, depth+1); found != "" {
			return found
		}
	}
	return ""
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
