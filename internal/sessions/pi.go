package sessions

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Pi (pi.dev) keeps a session as one JSONL file,
// ~/.pi/agent/sessions/--<cwd with dashes>--/<time>_<id>.jsonl: a
// "session" line with its id and folder, then "message" lines whose
// message has a role and the content Pi sent or got. Read off pi 1.1.0's
// own files (session version 3); `pi --session <id>` reopens one.

func piSessionsDir(e Env) string { return filepath.Join(e.Home, ".pi", "agent", "sessions") }

func pi(e Env, dirs []string) []Session {
	files, _ := filepath.Glob(filepath.Join(piSessionsDir(e), "*", "*.jsonl"))
	var out []Session
	for _, f := range files {
		if s := parseCached(f, parsePi); s != nil && withinAny(s.Dir, dirs) && time.Since(s.Updated) <= maxAge {
			out = append(out, *s)
		}
	}
	return out
}

type piLine struct {
	Type      string    `json:"type"`
	ID        string    `json:"id"`
	Cwd       string    `json:"cwd"`
	Timestamp time.Time `json:"timestamp"`
	Message   *struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

func parsePi(path string, st os.FileInfo) *Session {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	s := &Session{Agent: "pi", Updated: st.ModTime(), Path: path}
	lines(f, 2000, func(b []byte) bool {
		var l piLine
		if json.Unmarshal(b, &l) != nil {
			return true
		}
		switch {
		case l.Type == "session" && s.ID == "":
			s.ID, s.Dir, s.Started = l.ID, l.Cwd, l.Timestamp
		case l.Type == "message" && l.Message != nil && l.Message.Role == "user":
			if t := textParts(l.Message.Content); realPrompt(t) {
				s.Title = title(t)
				return false
			}
		}
		return true
	})
	if s.ID == "" || s.Dir == "" || s.Title == "" {
		return nil
	}
	return s
}

func piTranscript(path string) ([]Turn, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var turns []Turn
	lines(f, 1<<30, func(b []byte) bool {
		var l piLine
		if json.Unmarshal(b, &l) != nil || l.Type != "message" || l.Message == nil {
			return true
		}
		if r := l.Message.Role; r == "user" || r == "assistant" {
			turns = addTurn(turns, r, textParts(l.Message.Content), l.Timestamp)
		}
		return true
	})
	return turns, nil
}
