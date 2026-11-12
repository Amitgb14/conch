package sessions

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"time"
)

// ChatTurn is one entry of a conversation as a chat draws it: something
// the user typed, something the agent said, or a tool the agent used —
// which Transcript leaves out, and which is most of what a chat beside a
// terminal is for: what it read, what it changed, what it ran.
type ChatTurn struct {
	Role   string // "user", "assistant" or "tool"
	Text   string // for a tool, its name
	Detail string // for a tool, what it was used on: a file, a command
	Time   time.Time
}

// maxChatTurns bounds a chat: the newest turns, which is what is read.
const maxChatTurns = 400

// maxToolDetail bounds what is kept of a tool's input.
const maxToolDetail = 240

// Chat reads a session's conversation with the tools used, oldest first.
// Only Claude's transcripts say which tools were used in a form read here;
// for the others it is their conversation, as Transcript reads it.
func Chat(ctx context.Context, e Env, s Session) ([]ChatTurn, error) {
	if s.Agent == "claude" {
		turns, err := claudeChat(s.Path)
		if err != nil {
			return nil, err
		}
		return capChat(turns), nil
	}
	plain, err := Transcript(ctx, e, s)
	if err != nil {
		return nil, err
	}
	turns := make([]ChatTurn, 0, len(plain))
	for _, t := range plain {
		turns = append(turns, ChatTurn{Role: t.Role, Text: t.Text, Time: t.Time})
	}
	return capChat(turns), nil
}

// capChat keeps the newest turns: at most maxChatTurns, and within
// maxTranscriptBytes of text.
func capChat(turns []ChatTurn) []ChatTurn {
	if len(turns) > maxChatTurns {
		turns = turns[len(turns)-maxChatTurns:]
	}
	total := 0
	for i := len(turns) - 1; i >= 0; i-- {
		total += len(turns[i].Text) + len(turns[i].Detail)
		if total > maxTranscriptBytes {
			return turns[i+1:]
		}
	}
	return turns
}

func claudeChat(path string) ([]ChatTurn, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var turns []ChatTurn
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
		if l.Type == "user" {
			turns = addChat(turns, ChatTurn{Role: "user", Text: textParts(l.Message.Content), Time: l.Timestamp})
			return true
		}
		// An answer is a list of parts — text and tool calls in the order
		// made — or, rarely, a string.
		var parts []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		}
		if json.Unmarshal(l.Message.Content, &parts) != nil {
			turns = addChat(turns, ChatTurn{Role: "assistant", Text: textParts(l.Message.Content), Time: l.Timestamp})
			return true
		}
		for _, p := range parts {
			switch p.Type {
			case "text":
				turns = addChat(turns, ChatTurn{Role: "assistant", Text: p.Text, Time: l.Timestamp})
			case "tool_use":
				turns = addChat(turns, ChatTurn{Role: "tool", Text: p.Name, Detail: toolDetail(p.Input), Time: l.Timestamp})
			}
		}
		return true
	})
	return turns, nil
}

// addChat appends a turn: a user's only when it is something they typed
// (not a command's output or a tool's result), text the agent wrote in
// several blocks joined into one, and nothing empty.
func addChat(turns []ChatTurn, t ChatTurn) []ChatTurn {
	t.Text = strings.TrimSpace(t.Text)
	switch t.Role {
	case "user":
		if !realPrompt(t.Text) {
			return turns
		}
	case "assistant":
		if t.Text == "" {
			return turns
		}
		if n := len(turns); n > 0 && turns[n-1].Role == "assistant" {
			turns[n-1].Text += "\n\n" + t.Text
			return turns
		}
	case "tool":
		if t.Text == "" {
			return turns
		}
	}
	return append(turns, t)
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
