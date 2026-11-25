package sessions

import (
	"encoding/json"
	"os"
	"strings"
	"time"
)

// claudeConversation reads a Claude Code transcript
// (~/.claude/projects/<dir>/<id>.jsonl) whole: messages, and the tools
// Claude used with what came back — matched by tool_use id, since a call
// is in an assistant line and its result in the user line after it.
func claudeConversation(path string) (Conversation, error) {
	f, err := os.Open(path)
	if err != nil {
		return Conversation{}, err
	}
	defer f.Close()
	c := newConv()
	lines(f, 1<<30, func(b []byte) bool {
		var l struct {
			Type      string    `json:"type"`
			Timestamp time.Time `json:"timestamp"`
			IsMeta    bool      `json:"isMeta"`
			Sidechain bool      `json:"isSidechain"`
			Message   struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(b, &l) != nil || l.IsMeta || l.Sidechain || (l.Type != "user" && l.Type != "assistant") {
			return true
		}
		var parts []struct {
			Type      string          `json:"type"`
			Text      string          `json:"text"`
			ID        string          `json:"id"`
			Name      string          `json:"name"`
			Input     json.RawMessage `json:"input"`
			ToolUseID string          `json:"tool_use_id"`
			Content   json.RawMessage `json:"content"`
			IsError   bool            `json:"is_error"`
		}
		if json.Unmarshal(l.Message.Content, &parts) != nil {
			var s string
			if json.Unmarshal(l.Message.Content, &s) == nil {
				claudeText(c, l.Type, s, l.Timestamp)
			}
			return true
		}
		for _, p := range parts {
			switch p.Type {
			case "text":
				claudeText(c, l.Type, p.Text, l.Timestamp)
			case "image":
				if l.Type == "user" {
					c.note("user", "Image attached", false, l.Timestamp)
				}
			case "tool_use":
				claudeCall(c, p.ID, p.Name, p.Input, l.Timestamp)
			case "tool_result":
				at := c.calls[p.ToolUseID]
				out := resultText(p.Content)
				c.result(p.ToolUseID, out, p.IsError, exitCode(out))
				for _, i := range at {
					it := &c.items[i]
					switch {
					case it.Kind == KindEdit && strings.HasPrefix(out, "File created"):
						it.Created = true
					case it.Kind == KindCommand && it.Exit != 0:
						// Said by the exit code already.
						_, rest, _ := strings.Cut(it.Output, "\n")
						it.Output = rest
					}
				}
			}
			// Thinking is left out: it is signed, and only Claude can use it.
		}
		return true
	})
	return c.done(), nil
}

func claudeText(c *conv, role, text string, t time.Time) {
	if role == "user" && strings.HasPrefix(strings.TrimSpace(text), "[Request interrupted") {
		c.note("user", "Interrupted", false, t)
		return
	}
	c.message(role, text, t)
}

// claudeCall adds one of Claude's tool calls as what it did: a command, a
// file read or edited, a plan, or any other tool.
func claudeCall(c *conv, id, name string, raw json.RawMessage, t time.Time) {
	var in map[string]any
	_ = json.Unmarshal(raw, &in)
	str := func(k string) string { s, _ := in[k].(string); return s }
	switch name {
	case "Bash":
		c.call(id, Item{Kind: KindCommand, Text: str("command"), Time: t})
	case "Read", "NotebookRead":
		path := str("file_path")
		if path == "" {
			path = str("notebook_path")
		}
		c.call(id, Item{Kind: KindRead, Text: path, Time: t})
	case "Edit":
		a, r := lineStat(str("old_string"), str("new_string"))
		c.call(id, Item{Kind: KindEdit, Text: str("file_path"), Added: a, Removed: r, Time: t})
	case "MultiEdit":
		it := Item{Kind: KindEdit, Text: str("file_path"), Time: t}
		edits, _ := in["edits"].([]any)
		for _, e := range edits {
			m, _ := e.(map[string]any)
			o, _ := m["old_string"].(string)
			n, _ := m["new_string"].(string)
			a, r := lineStat(o, n)
			it.Added += a
			it.Removed += r
		}
		c.call(id, it)
	case "Write":
		c.call(id, Item{Kind: KindEdit, Text: str("file_path"), Added: len(splitLines(str("content"))), Time: t})
	case "NotebookEdit":
		c.call(id, Item{Kind: KindEdit, Text: str("notebook_path"), Added: len(splitLines(str("new_source"))), Time: t})
	case "TodoWrite":
		var p Plan
		todos, _ := in["todos"].([]any)
		for _, td := range todos {
			m, _ := td.(map[string]any)
			text, _ := m["content"].(string)
			status, _ := m["status"].(string)
			if strings.TrimSpace(text) != "" {
				p.Steps = append(p.Steps, PlanStep{Text: oneLine(text), Status: status})
			}
		}
		p.Time = t
		c.setPlan(p)
	case "ExitPlanMode":
		c.setPlan(Plan{Text: strings.TrimSpace(str("plan")), Time: t})
	default:
		input := toolDetail(raw)
		if input == "" && len(in) > 0 {
			b, _ := json.Marshal(in)
			input = string(b)
		}
		c.call(id, Item{Kind: KindTool, Text: name, Input: input, Time: t})
	}
}
