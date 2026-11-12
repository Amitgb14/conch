package sessions

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// ChatTurn is one entry of a conversation as a chat draws it: something
// the user typed, something the agent said, a tool it used — with what it
// was given and what came back — or a note (an interruption).
type ChatTurn struct {
	Seq       int    // its place in the whole conversation, from 0; stable as turns are added
	Role      string // "user", "assistant", "tool" or "note"
	Text      string // for a tool, its name
	Detail    string // for a tool, what it was used on, on one line: a file, a command
	Input     string // for a tool, what it was given, as a chat shows it
	InputKind string // "diff" (lines marked - and +), "code", or "" for text
	Output    string // for a tool, what came back
	Error     bool   // the tool failed
	Done      bool   // the tool's result has come
	Time      time.Time
	ver       int64 // the log's version when this turn last changed
}

const (
	// maxChatTurns is how many turns a chat is sent when it starts: the
	// newest, which is what is read.
	maxChatTurns = 400
	// keepChatTurns bounds what a log holds in memory.
	keepChatTurns = 1000
	// maxToolInput and maxToolOutput bound what is kept of a tool's input
	// and of what it returned.
	maxToolInput  = 6 << 10
	maxToolOutput = 8 << 10
	// maxToolDetail bounds a tool's one-line summary.
	maxToolDetail = 240
	// maxChatText bounds one message.
	maxChatText = 64 << 10
)

// ChatUpdate is what a chat is sent: the turns that are new or changed
// since the version it has, or — Reset — the newest turns to start again
// from, when it has none, or another log's (a new conversation, a file
// rewritten, a server restarted).
type ChatUpdate struct {
	Epoch string
	Ver   int64
	Reset bool
	Turns []ChatTurn
}

// chatLog is a conversation read so far. Claude's transcripts are read from
// where the last read stopped, so a chat polled every second of a long
// session costs only what was appended — reading one whole took four
// seconds, longer than the chat waited between asking; the others are read
// again whole when their file changes.
type chatLog struct {
	mu      sync.Mutex
	s       Session
	epoch   string
	ver     int64
	offset  int64 // Claude: bytes read
	size    int64
	mod     time.Time
	turns   []ChatTurn
	next    int            // the next turn's Seq
	tools   map[string]int // a tool call's id → its turn's Seq, until its result comes
	used    time.Time
	partial []byte // Claude: a line not finished when last read
}

var (
	chatMu   sync.Mutex
	chatLogs = map[string]*chatLog{}
)

// maxChatLogs bounds the conversations kept read.
const maxChatLogs = 16

func newEpoch() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func logFor(s Session) *chatLog {
	chatMu.Lock()
	defer chatMu.Unlock()
	k := s.Agent + "\x00" + s.Path + "\x00" + s.ID
	if l, ok := chatLogs[k]; ok {
		l.used = time.Now()
		return l
	}
	if len(chatLogs) >= maxChatLogs {
		var oldest string
		var at time.Time
		for key, l := range chatLogs {
			if oldest == "" || l.used.Before(at) {
				oldest, at = key, l.used
			}
		}
		delete(chatLogs, oldest)
	}
	l := &chatLog{s: s, epoch: newEpoch(), tools: map[string]int{}, used: time.Now()}
	chatLogs[k] = l
	return l
}

// ChatSince brings a session's chat up to date and says what a chat that
// has version ver of log epoch is missing.
func ChatSince(ctx context.Context, e Env, s Session, epoch string, ver int64) (ChatUpdate, error) {
	l := logFor(s)
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.refresh(ctx, e); err != nil {
		return ChatUpdate{}, err
	}
	up := ChatUpdate{Epoch: l.epoch, Ver: l.ver}
	if epoch != l.epoch || ver > l.ver {
		up.Reset = true
		from := max(len(l.turns)-maxChatTurns, 0)
		up.Turns = append([]ChatTurn(nil), l.turns[from:]...)
		return up, nil
	}
	for _, t := range l.turns {
		if t.ver > ver {
			up.Turns = append(up.Turns, t)
		}
	}
	return up, nil
}

// Chat reads a session's conversation, oldest first: the newest turns.
func Chat(ctx context.Context, e Env, s Session) ([]ChatTurn, error) {
	up, err := ChatSince(ctx, e, s, "", 0)
	return up.Turns, err
}

func (l *chatLog) reset() {
	l.epoch, l.ver, l.offset, l.turns, l.next, l.partial = newEpoch(), 0, 0, nil, 0, nil
	l.tools = map[string]int{}
}

func (l *chatLog) refresh(ctx context.Context, e Env) error {
	if l.s.Agent != "claude" {
		st, statErr := os.Stat(l.s.Path)
		if statErr == nil && l.ver > 0 && st.Size() == l.size && st.ModTime().Equal(l.mod) {
			return nil
		}
		plain, err := Transcript(ctx, e, l.s)
		if err != nil {
			return err
		}
		l.reset()
		if statErr == nil {
			l.size, l.mod = st.Size(), st.ModTime()
		}
		for _, t := range plain {
			l.add(ChatTurn{Role: t.Role, Text: t.Text, Time: t.Time})
		}
		l.ver++ // read: a version a chat can hold, even of nothing
		return nil
	}
	f, err := os.Open(l.s.Path)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if st.Size() < l.offset {
		l.reset() // rewritten shorter: start again
	}
	if l.ver == 0 {
		l.ver = 1
	}
	if st.Size() == l.offset {
		return nil
	}
	if _, err := f.Seek(l.offset, io.SeekStart); err != nil {
		return err
	}
	data, err := io.ReadAll(io.LimitReader(f, st.Size()-l.offset))
	if err != nil {
		return err
	}
	l.offset += int64(len(data))
	if len(l.partial) > 0 {
		data = append(l.partial, data...)
		l.partial = nil
	}
	for len(data) > 0 {
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			// The agent is still writing this line: kept for the next read.
			if len(data) < 16<<20 {
				l.partial = append([]byte(nil), data...)
			}
			break
		}
		if line := bytes.TrimSpace(data[:i]); len(line) > 0 && len(line) < 16<<20 {
			l.claudeLine(line)
		}
		data = data[i+1:]
	}
	return nil
}

// add appends a turn, or joins text the agent wrote in several blocks to
// the turn before; either way the turn takes a new version.
func (l *chatLog) add(t ChatTurn) {
	t.Text = strings.TrimSpace(t.Text)
	switch t.Role {
	case "user":
		if strings.HasPrefix(t.Text, "[Request interrupted") {
			t.Role, t.Text = "note", "Interrupted"
		} else if !realPrompt(t.Text) {
			return
		}
	case "assistant":
		if t.Text == "" {
			return
		}
		if n := len(l.turns); n > 0 && l.turns[n-1].Role == "assistant" {
			l.ver++
			last := &l.turns[n-1]
			last.Text = clip(last.Text+"\n\n"+t.Text, maxChatText)
			last.ver = l.ver
			return
		}
	case "tool":
		if t.Text == "" {
			return
		}
	}
	t.Text = clip(t.Text, maxChatText)
	l.ver++
	t.Seq, t.ver = l.next, l.ver
	l.next++
	l.turns = append(l.turns, t)
	if len(l.turns) > keepChatTurns {
		l.turns = append([]ChatTurn(nil), l.turns[len(l.turns)-keepChatTurns:]...)
		// Calls whose turns have gone will never be shown with a result.
		for id, seq := range l.tools {
			if seq < l.turns[0].Seq {
				delete(l.tools, id)
			}
		}
	}
}

func (l *chatLog) claudeLine(b []byte) {
	var ln struct {
		Type      string    `json:"type"`
		Timestamp time.Time `json:"timestamp"`
		IsMeta    bool      `json:"isMeta"`
		Sidechain bool      `json:"isSidechain"`
		Message   struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(b, &ln) != nil || ln.IsMeta || ln.Sidechain || (ln.Type != "user" && ln.Type != "assistant") {
		return
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
	if json.Unmarshal(ln.Message.Content, &parts) != nil {
		// A message that is a string.
		var s string
		if json.Unmarshal(ln.Message.Content, &s) == nil {
			l.add(ChatTurn{Role: ln.Type, Text: s, Time: ln.Timestamp})
		}
		return
	}
	for _, p := range parts {
		switch p.Type {
		case "text":
			l.add(ChatTurn{Role: ln.Type, Text: p.Text, Time: ln.Timestamp})
		case "image":
			if ln.Type == "user" {
				l.add(ChatTurn{Role: "note", Text: "Image attached", Time: ln.Timestamp})
			}
		case "tool_use":
			detail, input, kind := toolInput(p.Name, p.Input)
			// Counted by Seq, not by length: once the window is full a turn
			// added leaves the length as it was.
			before := l.next
			l.add(ChatTurn{Role: "tool", Text: p.Name, Detail: detail, Input: input, InputKind: kind, Time: ln.Timestamp})
			if p.ID != "" && l.next > before {
				l.tools[p.ID] = l.turns[len(l.turns)-1].Seq
			}
		case "tool_result":
			seq, ok := l.tools[p.ToolUseID]
			if !ok {
				continue
			}
			delete(l.tools, p.ToolUseID)
			if i := l.index(seq); i >= 0 {
				l.ver++
				t := &l.turns[i]
				t.Output, t.Error, t.Done, t.ver = clipMiddle(resultText(p.Content), maxToolOutput), p.IsError, true, l.ver
			}
		}
	}
}

// index finds a turn by its Seq in the window held.
func (l *chatLog) index(seq int) int {
	if len(l.turns) == 0 {
		return -1
	}
	i := seq - l.turns[0].Seq
	if i < 0 || i >= len(l.turns) || l.turns[i].Seq != seq {
		return -1
	}
	return i
}

// resultText is a tool result's text: a string, or the text of its parts.
func resultText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimRight(s, "\n ")
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	var b strings.Builder
	for _, p := range parts {
		var piece string
		switch p.Type {
		case "text":
			piece = p.Text
		case "image":
			piece = "[image]"
		default:
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(piece)
	}
	return strings.TrimRight(b.String(), "\n ")
}

// toolInput is how a chat shows what a tool was given: a line saying what
// it was used on, and the input itself — an edit as the lines it took out
// and put in, a command or a file written as code.
func toolInput(name string, raw json.RawMessage) (detail, input, kind string) {
	var in map[string]any
	if json.Unmarshal(raw, &in) != nil {
		return "", "", ""
	}
	str := func(k string) string { s, _ := in[k].(string); return s }
	detail = toolDetail(raw)
	switch name {
	case "Bash":
		input, kind = str("command"), "code"
		if d := str("description"); d != "" {
			detail = clip(strings.Join(strings.Fields(d), " "), maxToolDetail)
		}
	case "Edit":
		input, kind = diffLines(str("old_string"), str("new_string")), "diff"
	case "MultiEdit":
		edits, _ := in["edits"].([]any)
		var parts []string
		for _, e := range edits {
			m, _ := e.(map[string]any)
			o, _ := m["old_string"].(string)
			n, _ := m["new_string"].(string)
			parts = append(parts, diffLines(o, n))
		}
		input, kind = strings.Join(parts, "\n⋯\n"), "diff"
	case "Write":
		input, kind = str("content"), "code"
	case "NotebookEdit":
		input, kind = str("new_source"), "code"
	case "Read", "LS", "Glob", "WebFetch", "WebSearch":
		// What it was used on says it all.
	case "Grep":
		if p := str("path"); p != "" {
			detail = clip(str("pattern")+"  in "+p, maxToolDetail)
		}
	case "TodoWrite":
		todos, _ := in["todos"].([]any)
		var lines []string
		for _, t := range todos {
			m, _ := t.(map[string]any)
			c, _ := m["content"].(string)
			mark := "☐"
			switch m["status"] {
			case "in_progress":
				mark = "◐"
			case "completed":
				mark = "☑"
			}
			lines = append(lines, mark+" "+c)
		}
		input, detail = strings.Join(lines, "\n"), fmt.Sprintf("%d to-dos", len(todos))
	case "Task", "Agent":
		input = str("prompt")
		if d := str("description"); d != "" {
			detail = clip(d, maxToolDetail)
		}
	default:
		if b, err := json.MarshalIndent(in, "", "  "); err == nil && len(in) > 0 {
			input, kind = string(b), "code"
		}
	}
	return detail, clipMiddle(input, maxToolInput), kind
}

// diffLines is an edit as a diff shows it: the old lines marked -, the new +.
func diffLines(old, new string) string {
	var b strings.Builder
	if old != "" {
		for _, l := range strings.Split(old, "\n") {
			b.WriteString("- " + l + "\n")
		}
	}
	if new != "" {
		for _, l := range strings.Split(new, "\n") {
			b.WriteString("+ " + l + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// clip keeps the first n bytes of s, on a character boundary.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "") + "…"
}

// clipMiddle keeps the start and the end of s within about n bytes — a
// command's output matters most at its end, a file's at its start.
func clipMiddle(s string, n int) string {
	if len(s) <= n {
		return s
	}
	head, tail := n*2/5, n*3/5
	cut := strings.Count(s[head:len(s)-tail], "\n")
	return strings.ToValidUTF8(s[:head], "") + fmt.Sprintf("\n… %d lines not shown …\n", cut) + strings.ToValidUTF8(s[len(s)-tail:], "")
}

// toolDetail is what a tool was used on, from its input: the file, the
// command, the pattern — whichever the tool takes — on one line.
func toolDetail(raw json.RawMessage) string {
	var in map[string]any
	if json.Unmarshal(raw, &in) != nil {
		return ""
	}
	for _, k := range []string{"file_path", "notebook_path", "command", "pattern", "path", "url", "query", "description", "prompt"} {
		s, ok := in[k].(string)
		if !ok || strings.TrimSpace(s) == "" {
			continue
		}
		s = strings.Join(strings.Fields(s), " ")
		if len(s) > maxToolDetail {
			s = strings.ToValidUTF8(s[:maxToolDetail], "") + "…"
		}
		return s
	}
	return ""
}
