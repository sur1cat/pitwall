package claude

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Startup is what a session had loaded before the person typed anything.
//
// Every session opens with a system prompt, the tool definitions, whatever
// skills, plugins and MCP servers are enabled, and the project's CLAUDE.md.
// All of it arrives as the first request's input and is paid for, and the
// counter a person sees starts afterwards.
type Startup struct {
	Session string
	Project string
	Model   string
	// Tokens is the input of the session's very first priced request — not an
	// hour of them. Reading an aggregate here overstates it by orders of
	// magnitude, which is exactly the mistake this type exists to avoid.
	Tokens int64
}

// Startups reads the opening size of every retained session, heaviest first.
// It stops at the first assistant message of each transcript, so the whole
// sweep touches only the head of each file.
func Startups() []Startup {
	root := ProjectsDir()
	var out []Startup
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		if strings.Contains(path, string(filepath.Separator)+"subagents"+string(filepath.Separator)) {
			return nil // a delegated run does not pay a session's opening cost
		}
		if s, ok := firstRequest(path); ok {
			out = append(out, s)
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Tokens > out[j].Tokens })
	return out
}

// firstRequest reads the head of a transcript up to its first priced reply.
func firstRequest(path string) (Startup, bool) {
	f, err := os.Open(path)
	if err != nil {
		return Startup{}, false
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for n := 0; n < 400 && sc.Scan(); n++ {
		var rec struct {
			Type    string `json:"type"`
			Message struct {
				Model string `json:"model"`
				Usage struct {
					Input       int64 `json:"input_tokens"`
					CacheCreate int64 `json:"cache_creation_input_tokens"`
					CacheRead   int64 `json:"cache_read_input_tokens"`
				} `json:"usage"`
			} `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &rec) != nil || rec.Type != "assistant" {
			continue
		}
		u := rec.Message.Usage
		tokens := u.Input + u.CacheCreate + u.CacheRead
		if tokens == 0 {
			continue
		}
		return Startup{
			Session: strings.TrimSuffix(filepath.Base(path), ".jsonl"),
			Project: projectName(filepath.Base(filepath.Dir(path))),
			Model:   rec.Message.Model,
			Tokens:  tokens,
		}, true
	}
	return Startup{}, false
}

// MedianStartup is the middle opening size, which describes the habit better
// than an average one enormous session can drag around.
func MedianStartup(s []Startup) int64 {
	if len(s) == 0 {
		return 0
	}
	return s[len(s)/2].Tokens // Startups is sorted, so the middle is the median
}
