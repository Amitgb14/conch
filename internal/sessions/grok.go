package sessions

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Grok (xAI's grok CLI) keeps a session as a folder,
// ~/.grok/sessions/<cwd, URL-escaped>/<id>/: summary.json says where it
// ran, its generated title and when it was last active, and
// chat_history.jsonl holds the conversation — the person's words wrapped
// in <user_query>, beside reminders Grok adds itself (synthetic_reason).
// Read off grok 1.0.77's own files; `grok --resume <id>` reopens one.

func grokSessionsDir(e Env) string { return filepath.Join(e.Home, ".grok", "sessions") }

func grok(e Env, dirs []string) []Session {
	files, _ := filepath.Glob(filepath.Join(grokSessionsDir(e), "*", "*", "summary.json"))
	var out []Session
	for _, f := range files {
		if s := parseCached(f, parseGrok); s != nil && withinAny(s.Dir, dirs) && time.Since(s.Updated) <= maxAge {
			out = append(out, *s)
		}
	}
	return out
}

func parseGrok(path string, st os.FileInfo) *Session {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var sum struct {
		Info struct {
			ID  string `json:"id"`
			Cwd string `json:"cwd"`
		} `json:"info"`
		Title      string    `json:"generated_title"`
		Summary    string    `json:"session_summary"`
		Branch     string    `json:"head_branch"`
		Created    time.Time `json:"created_at"`
		Updated    time.Time `json:"updated_at"`
		LastActive time.Time `json:"last_active_at"`
	}
	if json.Unmarshal(b, &sum) != nil {
		return nil
	}
	dir := filepath.Dir(path)
	s := &Session{Agent: "grok", ID: sum.Info.ID, Dir: sum.Info.Cwd, Branch: sum.Branch, Started: sum.Created, Path: path}
	if s.ID == "" {
		s.ID = filepath.Base(dir)
	}
	if s.Dir == "" {
		s.Dir, _ = url.PathUnescape(filepath.Base(filepath.Dir(dir)))
	}
	s.Updated = sum.LastActive
	if s.Updated.IsZero() {
		s.Updated = sum.Updated
	}
	if s.Updated.IsZero() {
		s.Updated = st.ModTime()
	}
	// A session nobody asked anything in has no prompt and is left out,
	// whatever its summary says.
	turns, _ := grokTranscript(path)
	if len(turns) == 0 || turns[0].Role != "user" || !filepath.IsAbs(s.Dir) {
		return nil
	}
	switch {
	case sum.Title != "":
		s.Title = title(sum.Title)
	case sum.Summary != "":
		s.Title = title(sum.Summary)
	default:
		s.Title = title(turns[0].Text)
	}
	return s
}

// grokTranscript reads the conversation beside a session's summary.json.
func grokTranscript(summary string) ([]Turn, error) {
	f, err := os.Open(filepath.Join(filepath.Dir(summary), "chat_history.jsonl"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var turns []Turn
	lines(f, 1<<30, func(b []byte) bool {
		var l struct {
			Type      string          `json:"type"`
			Content   json.RawMessage `json:"content"`
			Synthetic string          `json:"synthetic_reason"`
		}
		if json.Unmarshal(b, &l) != nil || l.Synthetic != "" {
			return true
		}
		switch l.Type {
		case "user":
			turns = addTurn(turns, "user", userQuery(textParts(l.Content)), time.Time{})
		case "assistant":
			turns = addTurn(turns, "assistant", textParts(l.Content), time.Time{})
		}
		return true
	})
	return turns, nil
}

// userQuery is what the person typed, when an agent wraps it in
// <user_query> among context of its own (Grok and Cursor both do); the
// text unchanged otherwise.
func userQuery(text string) string {
	const open, closing = "<user_query>", "</user_query>"
	i := strings.Index(text, open)
	if i < 0 {
		return text
	}
	rest := text[i+len(open):]
	if j := strings.Index(rest, closing); j >= 0 {
		rest = rest[:j]
	}
	return strings.TrimSpace(rest)
}
