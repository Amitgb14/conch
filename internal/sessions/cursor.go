package sessions

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Cursor's CLI (cursor-agent, or agent) keeps a chat as a folder,
// ~/.cursor/chats/<hash>/<id>/: meta.json says where it ran, its title,
// whether anything was said and when, and the conversation itself is in
// a database of Cursor's own beside it. What conch can read of the
// conversation is the transcript Cursor writes too,
// ~/.cursor/projects/<project>/agent-transcripts/<id>/<id>.jsonl, the
// person's words wrapped in <user_query>. Read off cursor-agent
// 2026.10.22's own files; `cursor-agent --resume <id>` reopens one.

func cursorHome(e Env) string { return filepath.Join(e.Home, ".cursor") }

func cursor(e Env, dirs []string) []Session {
	files, _ := filepath.Glob(filepath.Join(cursorHome(e), "chats", "*", "*", "meta.json"))
	var out []Session
	for _, f := range files {
		if s := parseCached(f, parseCursor); s != nil && withinAny(s.Dir, dirs) && time.Since(s.Updated) <= maxAge {
			out = append(out, *s)
		}
	}
	return out
}

func parseCursor(path string, st os.FileInfo) *Session {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var meta struct {
		Cwd     string `json:"cwd"`
		Title   string `json:"title"`
		Said    bool   `json:"hasConversation"`
		Created int64  `json:"createdAtMs"`
		Updated int64  `json:"updatedAtMs"`
	}
	if json.Unmarshal(b, &meta) != nil || !meta.Said || meta.Cwd == "" {
		return nil // a chat opened and left: nothing to go back to
	}
	s := &Session{Agent: "cursor", ID: filepath.Base(filepath.Dir(path)), Dir: meta.Cwd, Title: meta.Title, Path: path}
	if meta.Created > 0 {
		s.Started = time.UnixMilli(meta.Created)
	}
	s.Updated = st.ModTime()
	if meta.Updated > 0 {
		s.Updated = time.UnixMilli(meta.Updated)
	}
	if s.Title == "" {
		if turns, _ := cursorTranscript(path); len(turns) > 0 && turns[0].Role == "user" {
			s.Title = turns[0].Text
		}
	}
	if s.Title == "" {
		return nil
	}
	s.Title = title(s.Title)
	return s
}

// cursorTranscriptPath is the transcript Cursor writes for the chat whose
// meta.json is meta, or "" when it wrote none.
func cursorTranscriptPath(meta string) string {
	id := filepath.Base(filepath.Dir(meta))
	home := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(meta)))) // …/.cursor
	found, _ := filepath.Glob(filepath.Join(home, "projects", "*", "agent-transcripts", id, id+".jsonl"))
	if len(found) == 0 {
		return ""
	}
	return found[0]
}

func cursorTranscript(meta string) ([]Turn, error) {
	path := cursorTranscriptPath(meta)
	if path == "" {
		return nil, fmt.Errorf("Cursor wrote no transcript conch can read for this chat")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var turns []Turn
	lines(f, 1<<30, func(b []byte) bool {
		var l struct {
			Role    string `json:"role"`
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(b, &l) != nil {
			return true
		}
		switch l.Role {
		case "user":
			turns = addTurn(turns, "user", userQuery(textParts(l.Message.Content)), time.Time{})
		case "assistant":
			turns = addTurn(turns, "assistant", textParts(l.Message.Content), time.Time{})
		}
		return true
	})
	return turns, nil
}
