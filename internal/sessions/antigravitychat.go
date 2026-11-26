package sessions

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
	"time"
)

// Antigravity CLI's chat, tools and all, from a conversation's
// transcript.jsonl (agy 1.5.16): a model step carries its words and the
// tools it calls (tool_calls, each argument a JSON string of its own), and
// each call's result is a GENERIC step after it, in the order the calls
// were made, behind agy's own "Created At"/"Completed At" lines. The file
// is read again whole when it changes, as the other agents' are; a
// conversation is short enough, and agy may rewrite it.

// refreshAntigravity reads the transcript again when it has changed.
func (l *chatLog) refreshAntigravity(e Env) error {
	path := agyTranscriptPath(agyRootOf(e, l.s), l.s.ID)
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if l.ver > 0 && st.Size() == l.size && st.ModTime().Equal(l.mod) {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	l.reset()
	l.size, l.mod = st.Size(), st.ModTime()
	var pending []int // the Seqs of calls whose results have not come
	r := bufio.NewReaderSize(f, 1<<16)
	for {
		b, err := r.ReadBytes('\n')
		if len(b) > 0 && len(b) < 16<<20 {
			pending = l.agyStep(b, pending)
		}
		if err != nil {
			break
		}
	}
	l.ver++ // read: a version a chat can hold, even of nothing
	return nil
}

// agyStep takes one step, and returns the calls still waiting for results.
func (l *chatLog) agyStep(b []byte, pending []int) []int {
	var st struct {
		Type    string    `json:"type"`
		Status  string    `json:"status"`
		Created time.Time `json:"created_at"`
		Content string    `json:"content"`
		Calls   []struct {
			Name string                     `json:"name"`
			Args map[string]json.RawMessage `json:"args"`
		} `json:"tool_calls"`
	}
	if json.Unmarshal(b, &st) != nil {
		return pending
	}
	switch st.Type {
	case "USER_INPUT":
		l.add(ChatTurn{Role: "user", Text: userRequest(st.Content), Time: st.Created})
	case "PLANNER_RESPONSE":
		l.add(ChatTurn{Role: "assistant", Text: st.Content, Time: st.Created})
		for _, c := range st.Calls {
			args := agyArgs(c.Args)
			detail, input, kind := agyToolInput(c.Name, args)
			n := len(l.turns)
			l.add(ChatTurn{Role: "tool", Text: c.Name, Detail: detail, Input: input, InputKind: kind, Time: st.Created})
			if len(l.turns) > n {
				pending = append(pending, l.turns[len(l.turns)-1].Seq)
			}
		}
	case "GENERIC":
		if len(pending) == 0 {
			return pending
		}
		if i := l.index(pending[0]); i >= 0 {
			l.ver++
			t := &l.turns[i]
			t.Output, t.Done, t.Error, t.ver = clipMiddle(agyResult(st.Content), maxToolOutput), true, st.Status != "DONE", l.ver
		}
		pending = pending[1:]
	}
	return pending
}

// agyArgs are a call's arguments as values: agy writes each one as JSON
// inside a string ("\"ls -la\"", "false").
func agyArgs(raw map[string]json.RawMessage) map[string]any {
	out := map[string]any{}
	for k, v := range raw {
		var s string
		if json.Unmarshal(v, &s) != nil {
			var x any
			if json.Unmarshal(v, &x) == nil {
				out[k] = x
			}
			continue
		}
		var x any
		if json.Unmarshal([]byte(s), &x) == nil {
			out[k] = x
		} else {
			out[k] = s
		}
	}
	return out
}

// agyToolInput is how a chat shows a call: what it was used on, and the
// input itself — a command or a file written as code, an edit as a diff.
func agyToolInput(name string, in map[string]any) (detail, input, kind string) {
	str := func(keys ...string) string {
		for _, k := range keys {
			if s, ok := in[k].(string); ok && strings.TrimSpace(s) != "" {
				return s
			}
		}
		return ""
	}
	detail = str("Path", "AbsolutePath", "TargetFile", "DirectoryPath", "SearchPath", "Url", "Query", "toolSummary")
	switch name {
	case "run_command":
		input, kind = str("CommandLine"), "code"
		if d := str("toolSummary", "CommandLine"); d != "" {
			detail = strings.Join(strings.Fields(d), " ") // a command can run to several lines
		}
	case "write_file", "write_to_file":
		input, kind = str("Content", "CodeContent"), "code"
	case "edit_file", "replace_file_content":
		input, kind = diffLines(str("Target", "TargetContent"), str("Replacement", "ReplacementContent")), "diff"
	case "read_file", "view_file", "list_dir", "list_directory":
		// What it was used on says it all.
	case "grep_search":
		if q, p := str("Query"), str("SearchPath", "Path"); q != "" && p != "" {
			detail = q + "  in " + p
		}
	default:
		if b, err := json.MarshalIndent(in, "", "  "); err == nil && len(in) > 0 {
			input, kind = string(b), "code"
		}
	}
	return clip(strings.ReplaceAll(detail, "\n", " "), maxToolDetail), clipMiddle(input, maxToolInput), kind
}

// agyResult is what a call returned, without agy's timing lines.
func agyResult(s string) string {
	lines := strings.Split(s, "\n")
	for len(lines) > 0 && (strings.HasPrefix(lines[0], "Created At:") || strings.HasPrefix(lines[0], "Completed At:")) {
		lines = lines[1:]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
