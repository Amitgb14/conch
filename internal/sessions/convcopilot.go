package sessions

import (
	"encoding/json"
	"os"
	"strings"
	"time"
)

// copilotLine is a line of Copilot CLI's events.jsonl as
// copilotConversation reads it (copilot-sdk/generated/session-events.d.ts,
// 1.0.77–1.0.112): what was typed, what came back, and each tool's start
// and completion, matched by toolCallId. A subagent's events name the
// call that started it (parentToolCallId) and are left out, as Copilot
// keeps them out of the conversation it resumes.
type copilotLine struct {
	Timestamp time.Time `json:"timestamp"`
	Type      string    `json:"type"`
	Data      struct {
		Content          string          `json:"content"`
		Source           string          `json:"source"`
		ParentToolCallID string          `json:"parentToolCallId"`
		ToolCallID       string          `json:"toolCallId"`
		ToolName         string          `json:"toolName"`
		Arguments        json.RawMessage `json:"arguments"`
		Success          *bool           `json:"success"`
		Result           *struct {
			Content string `json:"content"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
		Message     string `json:"message"`
		Attachments []struct {
			Type string `json:"type"`
		} `json:"attachments"`
	} `json:"data"`
}

// copilotConversation reads a Copilot CLI session whole. Reasoning is
// left out: Copilot keeps it encrypted (encryptedContent,
// reasoningOpaque).
func copilotConversation(path string) (Conversation, error) {
	f, err := os.Open(path)
	if err != nil {
		return Conversation{}, err
	}
	defer f.Close()
	c := newConv()
	lines(f, 1<<30, func(b []byte) bool {
		var ev copilotLine
		if json.Unmarshal(b, &ev) != nil || ev.Data.ParentToolCallID != "" {
			return true
		}
		d, t := ev.Data, ev.Timestamp
		switch ev.Type {
		case "user.message":
			if d.Source != "" {
				return true // a skill's hidden prompt, another agent
			}
			for _, a := range d.Attachments {
				if a.Type == "image" || a.Type == "blob" {
					c.note("user", "Image attached", false, t)
					break
				}
			}
			c.message("user", d.Content, t)
		case "assistant.message":
			c.message("assistant", d.Content, t)
		case "tool.execution_start":
			copilotCall(c, d.ToolCallID, d.ToolName, d.Arguments, t)
		case "tool.execution_complete":
			out := ""
			if d.Result != nil {
				out = d.Result.Content
			}
			failed := d.Success != nil && !*d.Success
			if d.Error != nil && d.Error.Message != "" {
				failed = true
				if out == "" {
					out = d.Error.Message
				}
			}
			// Copilot says whether a command failed, not its exit code.
			c.result(d.ToolCallID, out, failed, 0)
		case "session.error":
			c.note("assistant", "Error: "+oneLine(d.Message), true, t)
		case "abort":
			c.note("user", "Interrupted", false, t)
		}
		return true
	})
	return c.done(), nil
}

// copilotCall adds one of Copilot's tool calls as what it did.
func copilotCall(c *conv, id, name string, raw json.RawMessage, t time.Time) {
	var in map[string]any
	_ = json.Unmarshal(raw, &in)
	str := func(k string) string { s, _ := in[k].(string); return s }
	switch name {
	case "bash", "powershell", "shell":
		c.call(id, Item{Kind: KindCommand, Text: str("command"), Time: t})
	case "view", "show_file":
		c.call(id, Item{Kind: KindRead, Text: str("path"), Time: t})
	case "edit", "str_replace":
		a, r := lineStat(str("old_str"), str("new_str"))
		c.call(id, Item{Kind: KindEdit, Text: str("path"), Added: a, Removed: r, Time: t})
	case "create":
		c.call(id, Item{Kind: KindEdit, Text: str("path"), Added: len(splitLines(str("file_text"))), Created: true, Time: t})
	case "report_intent":
		// What Copilot shows as its status line: not a step.
	case "update_todo":
		if s := strings.TrimSpace(str("todos")); s != "" {
			c.setPlan(Plan{Text: s, Time: t})
		}
	default:
		input := ""
		if len(in) > 0 {
			b, _ := json.Marshal(in)
			input = string(b)
		}
		c.call(id, Item{Kind: KindTool, Text: name, Input: input, Time: t})
	}
}
