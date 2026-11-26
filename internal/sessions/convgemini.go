package sessions

import (
	"bytes"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"time"
)

// Gemini CLI and Qwen Code (a fork of it) keep the same parts — Gemini's
// API's {text}, {functionCall}, {functionResponse}, {inlineData} — and
// most of the same tools: run_shell_command, read_file, write_file, and
// an edit (Gemini's replace, Qwen's edit) with old_string and new_string.
// What follows reads them alike; the files around them differ.

// geminiPart is one part of a Gemini or Qwen message.
type geminiPart struct {
	Text         string `json:"text"`
	Thought      bool   `json:"thought"`
	FunctionCall *struct {
		ID   string          `json:"id"`
		Name string          `json:"name"`
		Args json.RawMessage `json:"args"`
	} `json:"functionCall"`
	FunctionResponse *struct {
		ID       string          `json:"id"`
		Name     string          `json:"name"`
		Response json.RawMessage `json:"response"`
	} `json:"functionResponse"`
	InlineData *struct {
		MimeType string `json:"mimeType"`
	} `json:"inlineData"`
	FileData *struct {
		MimeType string `json:"mimeType"`
	} `json:"fileData"`
}

// geminiParts reads a message's content: a string, one part, or a list.
func geminiParts(raw json.RawMessage) []geminiPart {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return []geminiPart{{Text: s}}
	}
	var parts []geminiPart
	if json.Unmarshal(raw, &parts) == nil {
		return parts
	}
	var one geminiPart
	if json.Unmarshal(raw, &one) == nil {
		return []geminiPart{one}
	}
	return nil
}

// geminiSaid is what parts say, thoughts left out, joined as Gemini joins
// them: with nothing between.
func geminiSaid(parts []geminiPart) string {
	var b strings.Builder
	for _, p := range parts {
		if !p.Thought {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

// geminiImage says whether parts carry an image or another file.
func geminiImage(parts []geminiPart) bool {
	for _, p := range parts {
		if p.InlineData != nil || p.FileData != nil {
			return true
		}
	}
	return false
}

// geminiResponse is what a function response says: its output, else its
// error, else the whole response.
func geminiResponse(raw json.RawMessage) (out string, failed bool) {
	var r map[string]any
	if json.Unmarshal(raw, &r) != nil {
		return strings.TrimSpace(string(raw)), false
	}
	if s, ok := r["output"].(string); ok {
		return s, false
	}
	if s, ok := r["error"].(string); ok {
		return s, true
	}
	if s, ok := r["content"].(string); ok {
		return s, false
	}
	b, _ := json.Marshal(r)
	return string(b), false
}

// shellResult unwraps what Gemini CLI's or Qwen Code's shell tool
// returned to the model into what the command printed and its exit code.
// Gemini CLI 0.66 returns "Output: …" with "Error: …", "Exit Code: N" and
// "Signal: …" lines after it when there is something to say; Qwen Code
// 0.27 (and older Gemini CLIs) put "Command: …" and "Directory: …" before
// it and always say the exit code, signal and process group. Anything
// else — a command cancelled, moved to the background — is kept whole.
func shellResult(out string) (string, int) {
	ls := strings.Split(out, "\n")
	start := -1
	for i, l := range ls {
		if strings.HasPrefix(l, "Output: ") {
			start = i
			break
		}
		if !strings.HasPrefix(l, "Command: ") && !strings.HasPrefix(l, "Directory: ") {
			break
		}
	}
	if start < 0 {
		return out, 0
	}
	exit := 0
	end := len(ls)
trailer:
	for end > start+1 {
		l := ls[end-1]
		switch {
		case strings.HasPrefix(l, "Exit Code: "):
			exit, _ = strconv.Atoi(strings.Fields(strings.TrimPrefix(l, "Exit Code: ") + " x")[0])
		case strings.HasPrefix(l, "Signal: "), strings.HasPrefix(l, "Process Group PGID: "), strings.HasPrefix(l, "Error: "):
		default:
			break trailer
		}
		end--
	}
	body := strings.Join(append([]string{strings.TrimPrefix(ls[start], "Output: ")}, ls[start+1:end]...), "\n")
	if body == "(empty)" {
		body = ""
	}
	return body, exit
}

// geminiCall adds one of Gemini's or Qwen's tool calls as what it did.
func geminiCall(c *conv, id, name string, raw json.RawMessage, t time.Time) {
	var in map[string]any
	_ = json.Unmarshal(raw, &in)
	str := func(keys ...string) string {
		for _, k := range keys {
			if s, ok := in[k].(string); ok && s != "" {
				return s
			}
		}
		return ""
	}
	switch name {
	case "run_shell_command":
		c.call(id, Item{Kind: KindCommand, Text: str("command"), Time: t})
	case "read_file":
		c.call(id, Item{Kind: KindRead, Text: str("file_path", "absolute_path", "path"), Time: t})
	case "read_many_files":
		paths, _ := in["paths"].([]any)
		if len(paths) == 0 {
			paths, _ = in["include"].([]any)
		}
		var names []string
		for _, p := range paths {
			if s, ok := p.(string); ok {
				names = append(names, s)
			}
		}
		c.call(id, Item{Kind: KindRead, Text: strings.Join(names, ", "), Time: t})
	case "replace", "edit":
		a, r := lineStat(str("old_string"), str("new_string"))
		c.call(id, Item{Kind: KindEdit, Text: str("file_path", "absolute_path", "path"), Added: a, Removed: r,
			Created: str("old_string") == "" && str("new_string") != "", Time: t})
	case "write_file":
		c.call(id, Item{Kind: KindEdit, Text: str("file_path", "absolute_path", "path"), Added: len(splitLines(str("content"))), Time: t})
	case "write_todos", "todo_write":
		var p Plan
		todos, _ := in["todos"].([]any)
		for _, td := range todos {
			m, _ := td.(map[string]any)
			text, _ := m["description"].(string) // Gemini's
			if text == "" {
				text, _ = m["content"].(string) // Qwen's
			}
			status, _ := m["status"].(string)
			if status == "cancelled" {
				continue
			}
			if strings.TrimSpace(text) != "" {
				p.Steps = append(p.Steps, PlanStep{Text: oneLine(text), Status: status})
			}
		}
		p.Time = t
		c.setPlan(p)
	case "exit_plan_mode":
		c.setPlan(Plan{Text: strings.TrimSpace(str("plan")), Time: t})
	default:
		input := ""
		if len(in) > 0 {
			b, _ := json.Marshal(in)
			input = string(b)
		}
		c.call(id, Item{Kind: KindTool, Text: name, Input: input, Time: t})
	}
}

// geminiResult fills in a call's result from what it returned: a
// command's output unwrapped, failed when its exit code was not 0.
func geminiResult(c *conv, id, out string, failed bool) {
	exit := 0
	if at, ok := c.calls[id]; ok && len(at) > 0 && c.items[at[0]].Kind == KindCommand {
		out, exit = shellResult(out)
		if exit != 0 {
			failed = true
		}
	}
	c.result(id, out, failed, exit)
}

// ---- Gemini CLI ----

// geminiRecord is a message of a Gemini CLI chat, with its tool calls.
type geminiRecord struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	Timestamp time.Time       `json:"timestamp"`
	Content   json.RawMessage `json:"content"`
	ToolCalls []geminiToolRec `json:"toolCalls"`
}

// geminiToolRec is a tool call as Gemini CLI records it on the message
// that made it, its result beside it.
type geminiToolRec struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Args      json.RawMessage `json:"args"`
	Result    json.RawMessage `json:"result"`
	Status    string          `json:"status"`
	Timestamp time.Time       `json:"timestamp"`
}

// geminiRecords reads a Gemini CLI chat file's messages as Gemini CLI
// 0.66 does (chatRecordingService.ts): one JSON object, or JSONL — a
// header, message lines (a message written again under its id as it
// grows), {"$set"} lines that may replace the whole list, {"$rewindTo"}
// dropping a message and everything after it, and {"$patch"} lines that
// update a message's content or its tool calls' results, remove messages
// or reorder them.
func geminiRecords(b []byte) []geminiRecord {
	var whole struct {
		Messages []geminiRecord `json:"messages"`
	}
	if json.Unmarshal(b, &whole) == nil {
		return whole.Messages
	}
	var order []string
	byID := map[string]*geminiRecord{}
	var noID []geminiRecord // an older line with no id
	put := func(m geminiRecord) {
		if m.ID == "" {
			noID = append(noID, m)
			return
		}
		if _, ok := byID[m.ID]; !ok {
			order = append(order, m.ID)
		}
		mm := m
		byID[m.ID] = &mm
	}
	reset := func() { order, byID, noID = nil, map[string]*geminiRecord{}, nil }
	for _, line := range bytes.Split(b, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var l struct {
			geminiRecord
			SessionID string          `json:"sessionId"`
			Messages  []geminiRecord  `json:"messages"`
			Set       json.RawMessage `json:"$set"`
			Rewind    *string         `json:"$rewindTo"`
			Patch     *struct {
				ID        string          `json:"id"`
				Content   json.RawMessage `json:"content"`
				ToolCalls []geminiToolRec `json:"toolCalls"`
				Updates   []struct {
					ID        string          `json:"id"`
					Content   json.RawMessage `json:"content"`
					ToolCalls []geminiToolRec `json:"toolCalls"`
				} `json:"updates"`
				RemoveIDs []string `json:"removeIds"`
				OrderIDs  []string `json:"orderIds"`
			} `json:"$patch"`
		}
		if json.Unmarshal(line, &l) != nil {
			continue // a line cut off mid-write
		}
		patch := func(id string, content json.RawMessage, calls []geminiToolRec) {
			m := byID[id]
			if m == nil {
				return
			}
			if len(content) > 0 {
				m.Content = content
			}
			for _, pc := range calls {
				for i := range m.ToolCalls {
					if m.ToolCalls[i].ID == pc.ID && len(pc.Result) > 0 {
						m.ToolCalls[i].Result = pc.Result
					}
				}
			}
		}
		switch {
		case l.Rewind != nil:
			cut := -1
			for i, id := range order {
				if id == *l.Rewind {
					cut = i
					break
				}
			}
			if cut < 0 {
				reset()
				continue
			}
			for _, id := range order[cut:] {
				delete(byID, id)
			}
			order = order[:cut]
		case l.Patch != nil:
			p := l.Patch
			if p.ID != "" {
				patch(p.ID, p.Content, p.ToolCalls)
			}
			for _, u := range p.Updates {
				patch(u.ID, u.Content, u.ToolCalls)
			}
			if len(p.RemoveIDs) > 0 {
				gone := map[string]bool{}
				for _, id := range p.RemoveIDs {
					gone[id] = true
					delete(byID, id)
				}
				kept := order[:0]
				for _, id := range order {
					if !gone[id] {
						kept = append(kept, id)
					}
				}
				order = kept
			}
			if len(p.OrderIDs) > 0 {
				named := map[string]bool{}
				var tail []string
				for _, id := range p.OrderIDs {
					if byID[id] != nil && !named[id] {
						named[id] = true
						tail = append(tail, id)
					}
				}
				var head []string
				for _, id := range order {
					if !named[id] {
						head = append(head, id)
					}
				}
				order = append(head, tail...)
			}
		case len(l.Set) > 0:
			var set struct {
				Messages *[]geminiRecord `json:"messages"`
			}
			if json.Unmarshal(l.Set, &set) == nil && set.Messages != nil {
				reset()
				for _, m := range *set.Messages {
					put(m)
				}
			}
		case l.SessionID != "":
			for _, m := range l.Messages {
				put(m)
			}
		case l.Type != "":
			put(l.geminiRecord)
		}
	}
	out := noID
	for _, id := range order {
		out = append(out, *byID[id])
	}
	return out
}

// geminiConversation reads a Gemini CLI chat whole: what was said, each
// tool call with the result recorded beside it, errors as notes.
// Thoughts are left out; info and warning lines are Gemini CLI's own.
func geminiConversation(path string) (Conversation, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Conversation{}, err
	}
	c := newConv()
	for _, m := range geminiRecords(b) {
		parts := geminiParts(m.Content)
		switch m.Type {
		case "user":
			if geminiImage(parts) {
				c.note("user", "Image attached", false, m.Timestamp)
			}
			// A user message that only answers tool calls carries no
			// words; its responses fill in calls recorded elsewhere.
			for _, p := range parts {
				if p.FunctionResponse != nil {
					out, failed := geminiResponse(p.FunctionResponse.Response)
					geminiResult(c, p.FunctionResponse.ID, out, failed)
				}
			}
			c.message("user", geminiSaid(parts), m.Timestamp)
		case "gemini", "model", "assistant":
			c.message("assistant", geminiSaid(parts), m.Timestamp)
			for _, p := range parts {
				if p.FunctionCall != nil {
					geminiCall(c, p.FunctionCall.ID, p.FunctionCall.Name, p.FunctionCall.Args, m.Timestamp)
				}
			}
			for _, tc := range m.ToolCalls {
				at := tc.Timestamp
				if at.IsZero() {
					at = m.Timestamp
				}
				if _, seen := c.calls[tc.ID]; !seen || tc.ID == "" {
					geminiCall(c, tc.ID, tc.Name, tc.Args, at)
				}
				if len(tc.Result) == 0 || string(tc.Result) == "null" {
					continue
				}
				out, failed := geminiToolResult(tc.Result)
				geminiResult(c, tc.ID, out, failed || tc.Status == "error")
			}
		case "error":
			c.note("assistant", "Error: "+oneLine(geminiSaid(parts)), true, m.Timestamp)
		}
	}
	return c.done(), nil
}

// geminiToolResult is what a recorded tool call returned: a string, or
// the function response parts Gemini CLI keeps.
func geminiToolResult(raw json.RawMessage) (string, bool) {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s, false
	}
	var out []string
	failed := false
	for _, p := range geminiParts(raw) {
		switch {
		case p.FunctionResponse != nil:
			o, f := geminiResponse(p.FunctionResponse.Response)
			out, failed = append(out, o), failed || f
		case p.Text != "":
			out = append(out, p.Text)
		}
	}
	return strings.Join(out, "\n"), failed
}
