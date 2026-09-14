package sessions

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Turn is one message of a saved conversation: what the user typed or what
// the agent answered. Tool calls, their output and reasoning are left out.
type Turn struct {
	Role string // "user" or "assistant"
	Text string
	Time time.Time
}

// maxTranscriptBytes bounds the text kept from one session.
const maxTranscriptBytes = 4 << 20

// Transcript reads a session's conversation, oldest first.
func Transcript(ctx context.Context, e Env, s Session) ([]Turn, error) {
	var turns []Turn
	var err error
	switch s.Agent {
	case "claude":
		turns, err = claudeTranscript(s.Path)
	case "codex":
		turns, err = codexTranscript(s.Path)
	case "gemini":
		turns, err = geminiTranscript(s.Path)
	case "opencode":
		if strings.HasSuffix(s.Path, ".db") {
			turns, err = opencodeDBTranscript(ctx, s.Path, s.ID)
		} else {
			turns, err = opencodeFileTranscript(e, s.ID)
		}
	default:
		return nil, fmt.Errorf("conch can't read %s sessions", s.Agent)
	}
	if err != nil {
		return nil, err
	}
	return capTurns(turns), nil
}

// capTurns keeps the most recent turns within maxTranscriptBytes.
func capTurns(turns []Turn) []Turn {
	total := 0
	for i := len(turns) - 1; i >= 0; i-- {
		total += len(turns[i].Text)
		if total > maxTranscriptBytes {
			return turns[i+1:]
		}
	}
	return turns
}

func addTurn(turns []Turn, role, text string, t time.Time) []Turn {
	text = strings.TrimSpace(text)
	if text == "" || role == "user" && !realPrompt(text) {
		return turns
	}
	// An answer written in several blocks (between tool calls) is one turn.
	if n := len(turns); n > 0 && role == "assistant" && turns[n-1].Role == "assistant" {
		turns[n-1].Text += "\n\n" + text
		return turns
	}
	return append(turns, Turn{Role: role, Text: text, Time: t})
}

// textParts joins the text of a message content that is a string or a list
// of typed parts, skipping tool calls, tool results and thinking.
func textParts(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
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
		if (p.Type == "" || p.Type == "text" || p.Type == "input_text" || p.Type == "output_text") && p.Text != "" {
			if b.Len() > 0 {
				b.WriteString("\n\n")
			}
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

func claudeTranscript(path string) ([]Turn, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var turns []Turn
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
		turns = addTurn(turns, l.Type, textParts(l.Message.Content), l.Timestamp)
		return true
	})
	return turns, nil
}

func codexTranscript(path string) ([]Turn, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var turns []Turn
	lines(f, 1<<30, func(b []byte) bool {
		var l struct {
			Type      string    `json:"type"`
			Timestamp time.Time `json:"timestamp"`
			Payload   struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"payload"`
		}
		if json.Unmarshal(b, &l) != nil || l.Type != "event_msg" {
			return true
		}
		switch l.Payload.Type {
		case "user_message":
			turns = addTurn(turns, "user", l.Payload.Message, l.Timestamp)
		case "agent_message":
			turns = addTurn(turns, "assistant", l.Payload.Message, l.Timestamp)
		}
		return true
	})
	return turns, nil
}

type geminiMessage struct {
	Type      string          `json:"type"`
	Timestamp time.Time       `json:"timestamp"`
	Content   json.RawMessage `json:"content"`
}

func geminiTranscript(path string) ([]Turn, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var rec struct {
		Messages []geminiMessage `json:"messages"`
	}
	var msgs []geminiMessage
	if json.Unmarshal(b, &rec) == nil {
		msgs = rec.Messages
	} else {
		// JSONL: a header line, then one message per line.
		for _, line := range bytes.Split(b, []byte("\n")) {
			var m geminiMessage
			if json.Unmarshal(line, &m) == nil && m.Type != "" {
				msgs = append(msgs, m)
			}
		}
	}
	var turns []Turn
	for _, m := range msgs {
		switch m.Type {
		case "user":
			turns = addTurn(turns, "user", textParts(m.Content), m.Timestamp)
		case "gemini", "model", "assistant":
			turns = addTurn(turns, "assistant", textParts(m.Content), m.Timestamp)
		}
	}
	return turns, nil
}

func opencodeDBTranscript(ctx context.Context, db, id string) ([]Turn, error) {
	bin, err := exec.LookPath("sqlite3")
	if err != nil {
		return nil, fmt.Errorf("reading OpenCode sessions needs the sqlite3 command")
	}
	query := "SELECT json_extract(m.data, '$.role') AS role, p.time_created AS time, json_extract(p.data, '$.text') AS text " +
		"FROM part p JOIN message m ON m.id = p.message_id WHERE p.session_id = " + sqlQuote(id) +
		" AND json_extract(p.data, '$.type') = 'text' AND coalesce(json_extract(p.data, '$.synthetic'), 0) = 0" +
		" ORDER BY p.time_created, p.id"
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "-readonly", "-json", db, query).Output()
	if err != nil {
		return nil, fmt.Errorf("read OpenCode session: %w", err)
	}
	var rows []struct {
		Role string `json:"role"`
		Time int64  `json:"time"`
		Text string `json:"text"`
	}
	if len(bytes.TrimSpace(out)) > 0 {
		if err := json.Unmarshal(out, &rows); err != nil {
			return nil, fmt.Errorf("read OpenCode session: %w", err)
		}
	}
	var turns []Turn
	for _, r := range rows {
		role := "assistant"
		if r.Role == "user" {
			role = "user"
		}
		turns = addTurn(turns, role, r.Text, time.UnixMilli(r.Time))
	}
	return turns, nil
}

// opencodeFileTranscript reads the older one-file-per-record store:
// storage/message/<session>/<message>.json and
// storage/part/<message>/<part>.json.
func opencodeFileTranscript(e Env, id string) ([]Turn, error) {
	storage := filepath.Join(opencodeData(e), "storage")
	msgFiles, _ := filepath.Glob(filepath.Join(storage, "message", id, "*.json"))
	type msg struct {
		ID   string `json:"id"`
		Role string `json:"role"`
		Time struct {
			Created int64 `json:"created"`
		} `json:"time"`
	}
	var msgs []msg
	for _, f := range msgFiles {
		var m msg
		if b, err := os.ReadFile(f); err == nil && json.Unmarshal(b, &m) == nil && m.ID != "" {
			msgs = append(msgs, m)
		}
	}
	sort.SliceStable(msgs, func(i, j int) bool { return msgs[i].Time.Created < msgs[j].Time.Created })
	var turns []Turn
	for _, m := range msgs {
		partFiles, _ := filepath.Glob(filepath.Join(storage, "part", m.ID, "*.json"))
		sort.Strings(partFiles)
		var text []string
		for _, f := range partFiles {
			var p struct {
				Type      string `json:"type"`
				Text      string `json:"text"`
				Synthetic bool   `json:"synthetic"`
			}
			if b, err := os.ReadFile(f); err == nil && json.Unmarshal(b, &p) == nil && p.Type == "text" && !p.Synthetic {
				text = append(text, p.Text)
			}
		}
		role := "assistant"
		if m.Role == "user" {
			role = "user"
		}
		turns = addTurn(turns, role, strings.Join(text, "\n\n"), time.UnixMilli(m.Time.Created))
	}
	return turns, nil
}

// ---- search ----

// searchCache keeps each session's conversation text, lowercased, while its
// file is unchanged.
var searchCache sync.Map // agent|id → searchEntry

type searchEntry struct {
	mod   time.Time
	size  int64
	turns []Turn
	lower []string
}

// Match finds the sessions whose title, branch, agent, ID or conversation
// contains every word of query (case-insensitive). For conversation matches
// the result has a snippet around the first word; metadata matches have an
// empty snippet. The map is keyed by Key(session).
func Match(ctx context.Context, e Env, list []Session, query string) map[string]string {
	words := strings.Fields(strings.ToLower(query))
	out := map[string]string{}
	if len(words) == 0 {
		return out
	}
	for _, s := range list {
		if ctx.Err() != nil {
			break
		}
		// Not the directory: every session of a project shares most of it.
		meta := strings.ToLower(strings.Join([]string{s.Title, s.Branch, s.Agent, s.ID}, " "))
		if containsAll(meta, words) {
			out[Key(s)] = ""
			continue
		}
		turns, lower := searchText(ctx, e, s)
		if !containsAll(strings.Join(lower, "\n"), words) {
			continue
		}
		for i, l := range lower {
			if at := strings.Index(l, words[0]); at >= 0 {
				out[Key(s)] = snippet(turns[i].Text, at, len(words[0]))
				break
			}
		}
	}
	return out
}

// Key identifies a session among all agents' sessions.
func Key(s Session) string { return s.Agent + "|" + s.ID }

func containsAll(text string, words []string) bool {
	for _, w := range words {
		if !strings.Contains(text, w) {
			return false
		}
	}
	return true
}

func searchText(ctx context.Context, e Env, s Session) ([]Turn, []string) {
	var mod time.Time
	var size int64
	if st, err := os.Stat(s.Path); err == nil {
		mod, size = st.ModTime(), st.Size()
	}
	if v, ok := searchCache.Load(Key(s)); ok {
		c := v.(searchEntry)
		if c.mod.Equal(mod) && c.size == size {
			return c.turns, c.lower
		}
	}
	turns, err := Transcript(ctx, e, s)
	if err != nil {
		return nil, nil
	}
	lower := make([]string, len(turns))
	for i, t := range turns {
		lower[i] = strings.ToLower(t.Text)
	}
	searchCache.Store(Key(s), searchEntry{mod: mod, size: size, turns: turns, lower: lower})
	return turns, lower
}

// snippet is one line of text around the match at byte offset at. The
// offset comes from the lowercased text, which can differ in length for
// some scripts, so it is clamped.
func snippet(text string, at, n int) string {
	const around = 40
	at = min(max(at, 0), len(text))
	start, end := max(at-around, 0), min(at+n+around, len(text))
	for start > 0 && !utf8Start(text[start]) {
		start--
	}
	for end < len(text) && !utf8Start(text[end]) {
		end++
	}
	s := strings.Join(strings.Fields(text[start:end]), " ")
	if start > 0 {
		s = "…" + s
	}
	if end < len(text) {
		s += "…"
	}
	return s
}

func utf8Start(b byte) bool { return b&0xC0 != 0x80 }
