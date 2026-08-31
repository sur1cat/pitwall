package claude

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
	"time"
)

// Dropped is one message a compaction threw away.
type Dropped struct {
	UUID  string
	Role  string
	At    time.Time
	Text  string
	Tools []string
	// Typed marks prose the human wrote, as opposed to the agent's output or a
	// tool result arriving under the user role. A compaction keeps almost none
	// of it, which is why it feels like the agent forgot what was asked.
	Typed bool
}

// DroppedFrom returns the messages a compaction discarded, in the order they
// were written.
//
// This is subtraction rather than inference. The boundary record lists exactly
// which message UUIDs survived, so everything written before it that is not on
// that list is what went away. It is still in the file — only the model lost
// it — which is why recovering it needs no guesswork and no cooperation from
// anything.
func DroppedFrom(path string, boundaryUUID string) []Dropped {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	type record struct {
		UUID      string    `json:"uuid"`
		Type      string    `json:"type"`
		Timestamp time.Time `json:"timestamp"`
		Message   struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"message"`
		CompactMetadata *struct {
			PreservedMessages struct {
				UUIDs []string `json:"uuids"`
			} `json:"preservedMessages"`
		} `json:"compactMetadata"`
	}

	var before []record
	kept := map[string]bool{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 32*1024*1024)
	for sc.Scan() {
		var r record
		if json.Unmarshal(sc.Bytes(), &r) != nil {
			continue
		}
		if r.UUID == boundaryUUID {
			if r.CompactMetadata != nil {
				for _, u := range r.CompactMetadata.PreservedMessages.UUIDs {
					kept[u] = true
				}
			}
			break // everything after the boundary was never dropped
		}
		before = append(before, r)
	}

	var out []Dropped
	for _, r := range before {
		if kept[r.UUID] || r.UUID == "" {
			continue
		}
		text, tools := readContent(r.Message.Content)
		if text == "" && len(tools) == 0 {
			continue
		}
		role := r.Message.Role
		if role == "" {
			role = r.Type
		}
		typed := r.Message.Role == "user" && isTyped(text)
		out = append(out, Dropped{UUID: r.UUID, Role: role, At: r.Timestamp,
			Text: text, Tools: tools, Typed: typed})
	}
	return out
}

// machinePrefixes open a message that arrived under the user role without a
// person writing it: a slash command and its output, an injected reminder, the
// caveat Claude Code prepends to command output. Counting these as prose would
// bury the handful of real prompts a compaction discarded among dozens of
// them, which is the opposite of the point.
var machinePrefixes = []string{
	"<local-command", "<command-name>", "<command-message>", "<command-args>",
	"<system-reminder>", "<user-prompt-submit-hook>", "Caveat:",
	"[Request interrupted", "<bash-input>", "<bash-stdout>",
	"Base directory for this skill", "<ide_", "<attachment", "# ",
}

// isTyped reports whether a user-role message is something a person wrote.
func isTyped(text string) bool {
	t := strings.TrimSpace(text)
	if t == "" {
		return false
	}
	for _, p := range machinePrefixes {
		if strings.HasPrefix(t, p) {
			return false
		}
	}
	return true
}

// readContent pulls the readable text and the tool names out of a message,
// whose content is either a plain string or a list of blocks.
func readContent(raw json.RawMessage) (string, []string) {
	if len(raw) == 0 {
		return "", nil
	}
	var plain string
	if json.Unmarshal(raw, &plain) == nil {
		return strings.TrimSpace(plain), nil
	}
	var blocks []struct {
		Type  string          `json:"type"`
		Text  string          `json:"text"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return "", nil
	}
	var text []string
	var tools []string
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if t := strings.TrimSpace(b.Text); t != "" {
				text = append(text, t)
			}
		case "tool_use":
			if b.Name != "" {
				tools = append(tools, b.Name)
			}
		}
	}
	return strings.Join(text, "\n\n"), tools
}
