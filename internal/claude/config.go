package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Config is ~/.claude.json, the file Claude Code keeps its own state in.
//
// Two things in it are not available anywhere else. The feature gates the
// server has turned on for this account are cached here, which makes "did
// something change under me" a question with an answer — the complaint behind
// the April postmortem, and one the community concluded could not be checked
// locally. And skill usage is counted here, which is the per-skill attribution
// that otherwise needs an OpenTelemetry collector nobody runs.
type Config struct {
	// Gates are the server-side switches, by name. Values are true, false, or
	// a structure; only the boolean ones are kept, since those are the ones
	// whose flipping is legible.
	Gates map[string]bool
	// Skills is how many times each skill has been used.
	Skills map[string]SkillUse
	// Projects are the directories Claude Code has state for — a longer list
	// than either the transcripts or the prompt history give.
	Projects []string
	// FirstStart is when Claude Code first ran here.
	FirstStart time.Time
	// Startups is how many times it has been launched.
	Startups int
}

// SkillUse is one skill's usage record.
type SkillUse struct {
	Count int       `json:"usageCount"`
	Last  time.Time `json:"-"`
}

// ReadConfig loads ~/.claude.json. It is undocumented and large, so unknown
// keys are ignored and a shape that does not parse yields nothing.
func ReadConfig() (Config, bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Config{}, false
	}
	raw, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	if err != nil {
		return Config{}, false
	}
	var doc struct {
		StatsigGates map[string]any             `json:"cachedStatsigGates"`
		GrowthBook   map[string]any             `json:"cachedGrowthBookFeatures"`
		SkillUsage   map[string]json.RawMessage `json:"skillUsage"`
		Projects     map[string]json.RawMessage `json:"projects"`
		FirstStart   string                     `json:"firstStartTime"`
		NumStartups  int                        `json:"numStartups"`
	}
	if json.Unmarshal(raw, &doc) != nil {
		return Config{}, false
	}

	c := Config{Gates: map[string]bool{}, Skills: map[string]SkillUse{}, Startups: doc.NumStartups}
	for _, src := range []map[string]any{doc.StatsigGates, doc.GrowthBook} {
		for name, v := range src {
			switch b := v.(type) {
			case bool:
				c.Gates[name] = b
			case map[string]any:
				if inner, ok := b["value"].(bool); ok {
					c.Gates[name] = inner
				}
			}
		}
	}
	for name, raw := range doc.SkillUsage {
		var u struct {
			Count int   `json:"usageCount"`
			Last  int64 `json:"lastUsedAt"`
		}
		if json.Unmarshal(raw, &u) != nil {
			continue
		}
		s := SkillUse{Count: u.Count}
		if u.Last > 0 {
			s.Last = time.UnixMilli(u.Last)
		}
		c.Skills[name] = s
	}
	for p := range doc.Projects {
		c.Projects = append(c.Projects, p)
	}
	sort.Strings(c.Projects)
	c.FirstStart, _ = time.Parse(time.RFC3339, doc.FirstStart)
	return c, len(c.Gates) > 0 || len(c.Skills) > 0
}

// GateDiff is what changed between two readings of the gates.
type GateDiff struct {
	TurnedOn  []string
	TurnedOff []string
	Appeared  []string
	Vanished  []string
}

// Any reports whether anything changed at all.
func (d GateDiff) Any() bool {
	return len(d.TurnedOn)+len(d.TurnedOff)+len(d.Appeared)+len(d.Vanished) > 0
}

// CompareGates reports how the switches moved between an earlier reading and
// a later one.
func CompareGates(before, after map[string]bool) GateDiff {
	var d GateDiff
	for name, now := range after {
		was, seen := before[name]
		switch {
		case !seen:
			d.Appeared = append(d.Appeared, name)
		case was && !now:
			d.TurnedOff = append(d.TurnedOff, name)
		case !was && now:
			d.TurnedOn = append(d.TurnedOn, name)
		}
	}
	for name := range before {
		if _, ok := after[name]; !ok {
			d.Vanished = append(d.Vanished, name)
		}
	}
	for _, s := range [][]string{d.TurnedOn, d.TurnedOff, d.Appeared, d.Vanished} {
		sort.Strings(s)
	}
	return d
}
