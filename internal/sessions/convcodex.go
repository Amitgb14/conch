package sessions

import (
	"encoding/json"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// codexConversation reads a Codex rollout whole. Codex has written its
// tools three ways, and a rollout holds one of them:
//
//   - response_items function_call / function_call_output (shell,
//     exec_command, shell_command, update_plan), local_shell_call, and
//     custom_tool_call apply_patch — before 0.158;
//   - from 0.158, item_completed events: CommandExecution (with what each
//     part of the command did, so a sed that only reads is a read),
//     McpToolCall and Extension; the edits are apply_patch calls inside
//     the JavaScript of a custom_tool_call "exec", which is otherwise only
//     plumbing for the commands the events already say.
//
// Messages come from the events, as Transcript reads them: the older
// user_message/agent_message when there are any, else item_completed.
func codexConversation(path string) (Conversation, error) {
	f, err := os.Open(path)
	if err != nil {
		return Conversation{}, err
	}
	defer f.Close()
	c := newConv()
	// Messages and image notes from each kind of event, to drop one kind
	// at the end.
	var fromEvents, fromItems []int
	said := func(role, text string, t time.Time, events bool) {
		n := len(c.items)
		c.message(role, text, t)
		for i := n; i < len(c.items); i++ {
			if events {
				fromEvents = append(fromEvents, i)
			} else {
				fromItems = append(fromItems, i)
			}
		}
	}
	image := func(t time.Time, events bool) {
		c.note("user", "Image attached", false, t)
		if events {
			fromEvents = append(fromEvents, len(c.items)-1)
		} else {
			fromItems = append(fromItems, len(c.items)-1)
		}
	}
	lines(f, 1<<30, func(b []byte) bool {
		var l struct {
			Type      string    `json:"type"`
			Timestamp time.Time `json:"timestamp"`
			Payload   struct {
				Type      string          `json:"type"`
				Message   string          `json:"message"`
				Images    []any           `json:"images"`
				Item      json.RawMessage `json:"item"`
				Name      string          `json:"name"`
				Arguments string          `json:"arguments"`
				Input     string          `json:"input"`
				CallID    string          `json:"call_id"`
				Output    json.RawMessage `json:"output"`
				Action    struct {
					Type    string   `json:"type"`
					Command []string `json:"command"`
				} `json:"action"`
				Error *struct {
					Message string `json:"message"`
				} `json:"error"`
				Reason string `json:"reason"`
			} `json:"payload"`
		}
		if json.Unmarshal(b, &l) != nil {
			return true
		}
		p, t := l.Payload, l.Timestamp
		switch l.Type {
		case "event_msg":
			switch p.Type {
			case "user_message":
				if len(p.Images) > 0 {
					image(t, true)
				}
				said("user", p.Message, t, true)
			case "agent_message":
				said("assistant", p.Message, t, true)
			case "item_completed":
				codexItemDone(c, p.Item, t, said, image)
			case "turn_aborted":
				c.note("user", "Interrupted", false, t)
			case "task_complete", "error":
				msg := p.Message
				if p.Error != nil {
					msg = p.Error.Message
				}
				if msg = strings.TrimSpace(msg); msg != "" {
					c.note("assistant", oneLine(msg), true, t)
				}
			}
		case "response_item":
			switch p.Type {
			case "function_call":
				codexFunctionCall(c, p.CallID, p.Name, p.Arguments, t)
			case "local_shell_call":
				if p.Action.Type == "exec" {
					codexShell(c, p.CallID, p.Action.Command, t)
				}
			case "custom_tool_call":
				switch p.Name {
				case "apply_patch":
					codexPatch(c, p.CallID, p.Input, t)
				case "exec":
					// Only the patches: the commands it runs are events of
					// their own.
					for _, patch := range patchesIn(p.Input) {
						codexPatch(c, p.CallID, patch, t)
					}
				default:
					c.call(p.CallID, Item{Kind: KindTool, Text: p.Name, Input: p.Input, Time: t})
				}
			case "function_call_output", "custom_tool_call_output":
				out, failed, exit := codexResult(p.Output)
				c.result(p.CallID, out, failed, exit)
			}
		}
		return true
	})
	drop := fromItems
	if len(fromEvents) == 0 {
		drop = nil
	}
	conv := c.done()
	conv.Items = without(conv.Items, drop)
	return conv, nil
}

// without is items less those at the indexes given, in order.
func without(items []Item, drop []int) []Item {
	if len(drop) == 0 {
		return items
	}
	gone := make(map[int]bool, len(drop))
	for _, i := range drop {
		gone[i] = true
	}
	out := items[:0:0]
	for i, it := range items {
		if !gone[i] {
			out = append(out, it)
		}
	}
	return out
}

// codexItemDone adds an item_completed event's item.
func codexItemDone(c *conv, raw json.RawMessage, t time.Time, said func(string, string, time.Time, bool), image func(time.Time, bool)) {
	var it struct {
		Type    string          `json:"type"`
		Content json.RawMessage `json:"content"`
		// CommandExecution
		Command   []string `json:"command"`
		Output    string   `json:"aggregated_output"`
		ExitCode  *int     `json:"exit_code"`
		Status    string   `json:"status"`
		ParsedCmd []struct {
			Type string  `json:"type"`
			Path *string `json:"path"`
		} `json:"parsed_cmd"`
		// McpToolCall
		Server    string          `json:"server"`
		Tool      string          `json:"tool"`
		Arguments json.RawMessage `json:"arguments"`
		// Extension
		Kind  string `json:"kind"`
		Query string `json:"query"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &it) != nil {
		return
	}
	switch it.Type {
	case "UserMessage":
		var parts []struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(it.Content, &parts)
		for _, p := range parts {
			if p.Type == "local_image" || p.Type == "image" {
				image(t, false)
				break
			}
		}
		said("user", codexText(it.Content), t, false)
	case "AgentMessage":
		said("assistant", codexText(it.Content), t, false)
	case "CommandExecution":
		failed := it.Status == "failed" || it.Status == "declined" || it.ExitCode != nil && *it.ExitCode != 0
		// A command that only read files is those reads.
		reads := len(it.ParsedCmd) > 0
		for _, pc := range it.ParsedCmd {
			if pc.Type != "read" || pc.Path == nil || *pc.Path == "" {
				reads = false
			}
		}
		if reads && !failed {
			for _, pc := range it.ParsedCmd {
				c.call("", Item{Kind: KindRead, Text: *pc.Path, Done: true, Size: len(it.Output), Time: t})
			}
			return
		}
		item := Item{Kind: KindCommand, Text: shellLine(it.Command), Done: true, Failed: failed, Size: len(it.Output), Time: t,
			Output: clipMiddle(strings.TrimRight(it.Output, "\n "), maxItemOutput)}
		if failed && it.ExitCode != nil {
			item.Exit = *it.ExitCode
		}
		c.call("", item)
	case "McpToolCall":
		name := it.Tool
		if it.Server != "" {
			name = it.Server + "." + it.Tool
		}
		c.call("", Item{Kind: KindTool, Text: name, Input: string(it.Arguments), Done: true, Failed: it.Status == "failed", Time: t})
	case "Extension":
		c.call("", Item{Kind: KindTool, Text: it.Kind, Input: it.Query, Done: true, Time: t})
	}
	// Reasoning is left out: it is encrypted, and only Codex can use it.
}

// codexFunctionCall adds a function_call response item.
func codexFunctionCall(c *conv, id, name, args string, t time.Time) {
	var in map[string]any
	_ = json.Unmarshal([]byte(args), &in)
	str := func(k string) string { s, _ := in[k].(string); return s }
	switch name {
	case "shell", "container.exec":
		var cmd []string
		if list, ok := in["command"].([]any); ok {
			for _, a := range list {
				s, _ := a.(string)
				cmd = append(cmd, s)
			}
		}
		codexShell(c, id, cmd, t)
	case "exec_command":
		c.call(id, Item{Kind: KindCommand, Text: str("cmd"), Time: t})
	case "shell_command":
		c.call(id, Item{Kind: KindCommand, Text: str("command"), Time: t})
	case "apply_patch":
		codexPatch(c, id, str("input"), t)
	case "update_plan":
		p := Plan{Text: strings.TrimSpace(str("explanation")), Time: t}
		steps, _ := in["plan"].([]any)
		for _, s := range steps {
			m, _ := s.(map[string]any)
			text, _ := m["step"].(string)
			status, _ := m["status"].(string)
			if strings.TrimSpace(text) != "" {
				p.Steps = append(p.Steps, PlanStep{Text: oneLine(text), Status: status})
			}
		}
		c.setPlan(p)
	case "wait", "write_stdin":
		// Plumbing for a command already shown.
	default:
		c.call(id, Item{Kind: KindTool, Text: name, Input: args, Time: t})
	}
}

// codexShell adds a command given as its arguments: apply_patch run this
// way is the edits it makes.
func codexShell(c *conv, id string, cmd []string, t time.Time) {
	if len(cmd) >= 2 && cmd[0] == "apply_patch" {
		codexPatch(c, id, cmd[1], t)
		return
	}
	c.call(id, Item{Kind: KindCommand, Text: shellLine(cmd), Time: t})
}

// shellLine is a command's arguments as a person would type them: the
// script itself when it is run by a shell.
func shellLine(cmd []string) string {
	if len(cmd) == 3 && (cmd[1] == "-lc" || cmd[1] == "-c") {
		return cmd[2]
	}
	parts := make([]string, len(cmd))
	for i, a := range cmd {
		if a == "" || strings.ContainsAny(a, " \t\n'\"\\$`|&;<>()*?[]{}") {
			a = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		}
		parts[i] = a
	}
	return strings.Join(parts, " ")
}

var patchBlock = regexp.MustCompile(`(?s)\*\*\* Begin Patch.*?\*\*\* End Patch`)

// patchesIn finds the patches in a script that calls apply_patch. The
// script is JavaScript, so a patch in it may be a string literal with its
// newlines escaped.
func patchesIn(script string) []string {
	var out []string
	for _, m := range patchBlock.FindAllString(script, -1) {
		if !strings.Contains(m, "\n") {
			m = strings.NewReplacer(`\n`, "\n", `\t`, "\t", `\"`, `"`, `\\`, `\`).Replace(m)
		}
		out = append(out, m)
	}
	return out
}

// codexPatch adds the edits of an apply_patch patch, one per file.
func codexPatch(c *conv, id, patch string, t time.Time) {
	var cur *Item
	var edits []Item
	for _, line := range strings.Split(patch, "\n") {
		line = strings.TrimRight(line, "\r")
		switch {
		case strings.HasPrefix(line, "*** Add File: "):
			edits = append(edits, Item{Kind: KindEdit, Text: strings.TrimPrefix(line, "*** Add File: "), Created: true, Time: t})
			cur = &edits[len(edits)-1]
		case strings.HasPrefix(line, "*** Update File: "):
			edits = append(edits, Item{Kind: KindEdit, Text: strings.TrimPrefix(line, "*** Update File: "), Time: t})
			cur = &edits[len(edits)-1]
		case strings.HasPrefix(line, "*** Delete File: "):
			edits = append(edits, Item{Kind: KindEdit, Text: strings.TrimPrefix(line, "*** Delete File: "), Deleted: true, Time: t})
			cur = nil
		case strings.HasPrefix(line, "*** Move to: ") && cur != nil:
			cur.Text = strings.TrimPrefix(line, "*** Move to: ")
		case strings.HasPrefix(line, "***"):
		case cur == nil:
		case strings.HasPrefix(line, "+"):
			cur.Added++
		case strings.HasPrefix(line, "-"):
			cur.Removed++
		}
	}
	for _, e := range edits {
		c.call(id, e)
	}
}

var exitedWith = regexp.MustCompile(`(?m)^(?:Process exited with code|Exit code:?) (-?\d+)`)

// codexResult is what a call returned: a string, a list of parts, or —
// before 0.4 — a string holding {"output", "metadata": {"exit_code"}}.
// The header newer Codex puts before a command's output is left out.
func codexResult(raw json.RawMessage) (out string, failed bool, exit int) {
	var s string
	if json.Unmarshal(raw, &s) != nil {
		var parts []struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(raw, &parts) == nil {
			var b strings.Builder
			for _, p := range parts {
				b.WriteString(p.Text)
			}
			s = b.String()
		} else {
			var obj struct {
				Content string `json:"content"`
				Success *bool  `json:"success"`
			}
			_ = json.Unmarshal(raw, &obj)
			s = obj.Content
			if obj.Success != nil && !*obj.Success {
				failed = true
			}
		}
	}
	var old struct {
		Output   *string `json:"output"`
		Metadata struct {
			ExitCode int `json:"exit_code"`
		} `json:"metadata"`
	}
	if strings.HasPrefix(strings.TrimSpace(s), "{") && json.Unmarshal([]byte(s), &old) == nil && old.Output != nil {
		return *old.Output, failed || old.Metadata.ExitCode != 0, old.Metadata.ExitCode
	}
	if m := exitedWith.FindStringSubmatch(s); m != nil {
		exit, _ = strconv.Atoi(m[1])
		failed = failed || exit != 0
	}
	if strings.HasPrefix(s, "Script failed") {
		failed = true
	}
	if head, body, ok := strings.Cut(s, "Output:\n"); ok && strings.Count(head, "\n") <= 6 {
		s = body
	}
	return s, failed, exit
}
