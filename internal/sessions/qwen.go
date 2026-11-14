package sessions

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ---- Qwen Code ----
//
// Qwen Code (a fork of Gemini CLI) keeps a session as JSON lines in
// <QWEN_HOME or ~/.qwen>/projects/<project dir, every character but a
// letter or digit made "-">/chats/<session id>.jsonl, one record a line:
// sessionId, timestamp, type (user, assistant, tool_result, system), cwd,
// gitBranch and a message of Gemini's shape ({role, parts: [{text}]}).
// A title the person or the model gave it is a system record with subtype
// custom_title, the last one winning (packages/core/src/services/
// chatRecordingService.ts and sessionService.ts).

func qwenHome(e Env) string {
	if d := e.get("QWEN_RUNTIME_DIR"); d != "" {
		return d
	}
	if d := e.get("QWEN_HOME"); d != "" {
		return d
	}
	return filepath.Join(e.Home, ".qwen")
}

// qwenDirName is how Qwen names a project's folder: Claude's rule, every
// character other than a letter or digit becomes "-".
func qwenDirName(dir string) string { return claudeDirName(dir) }

func qwen(e Env, dirs []string) []Session {
	projects := filepath.Join(qwenHome(e), "projects")
	entries, err := os.ReadDir(projects)
	if err != nil {
		return nil
	}
	var out []Session
	for _, en := range entries {
		name := en.Name()
		match := false
		for _, d := range dirs {
			// A subdirectory's folder shares the prefix; the record's cwd
			// decides.
			if enc := qwenDirName(d); name == enc || strings.HasPrefix(name, enc+"-") {
				match = true
			}
		}
		if !match {
			continue
		}
		// archive/ holds sessions the person put away; they are left there.
		files, _ := filepath.Glob(filepath.Join(projects, name, "chats", "*.jsonl"))
		for _, f := range files {
			if s := parseCached(f, parseQwen); s != nil && withinAny(s.Dir, dirs) {
				out = append(out, *s)
			}
		}
	}
	return out
}

// qwenRecord is one line of a Qwen session.
type qwenRecord struct {
	SessionID string    `json:"sessionId"`
	Timestamp time.Time `json:"timestamp"`
	Type      string    `json:"type"`
	Subtype   string    `json:"subtype"`
	Cwd       string    `json:"cwd"`
	GitBranch string    `json:"gitBranch"`
	Message   *struct {
		Role  string          `json:"role"`
		Parts json.RawMessage `json:"parts"`
	} `json:"message"`
	SystemPayload struct {
		CustomTitle string `json:"customTitle"`
	} `json:"systemPayload"`
}

// text is what a user or assistant record says, its tool calls and
// thoughts left out.
func (r qwenRecord) text() string {
	if r.Message == nil {
		return ""
	}
	var parts []struct {
		Text    string `json:"text"`
		Thought bool   `json:"thought"`
	}
	if json.Unmarshal(r.Message.Parts, &parts) != nil {
		return ""
	}
	var b strings.Builder
	for _, p := range parts {
		if p.Text != "" && !p.Thought {
			b.WriteString(p.Text + " ")
		}
	}
	return strings.TrimSpace(b.String())
}

func parseQwen(path string, st os.FileInfo) *Session {
	if time.Since(st.ModTime()) > maxAge {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	s := &Session{Agent: "qwen", ID: strings.TrimSuffix(filepath.Base(path), ".jsonl"), Updated: st.ModTime(), Path: path}
	var prompt, custom string
	visit := func(b []byte) {
		var r qwenRecord
		if json.Unmarshal(b, &r) != nil {
			return
		}
		if r.SessionID != "" {
			s.ID = r.SessionID
		}
		if s.Started.IsZero() && !r.Timestamp.IsZero() {
			s.Started = r.Timestamp
		}
		if s.Dir == "" && r.Cwd != "" {
			s.Dir, s.Branch = r.Cwd, r.GitBranch
		}
		switch {
		case r.Type == "system" && r.Subtype == "custom_title":
			custom = r.SystemPayload.CustomTitle
		case r.Type == "user" && r.Subtype == "" && prompt == "":
			// A subtype marks something conch did not type: a message
			// between agents, a mention expanded.
			if t := r.text(); realPrompt(t) {
				prompt = t
			}
		}
	}
	lines(f, 400, func(b []byte) bool {
		visit(b)
		return true
	})
	// A title is appended as the session goes on: read the tail for it.
	if st.Size() > 256*1024 {
		if _, err := f.Seek(-256*1024, 2); err == nil {
			lines(f, 1<<20, func(b []byte) bool {
				if bytes.Contains(b, []byte(`"custom_title"`)) {
					visit(b)
				}
				return true
			})
		}
	}
	if s.Dir == "" || prompt == "" && custom == "" {
		return nil // nothing was said: not worth resuming
	}
	s.Title = title(firstNonEmpty(custom, prompt))
	return s
}

func qwenTranscript(path string) ([]Turn, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var turns []Turn
	lines(f, 1<<22, func(b []byte) bool {
		var r qwenRecord
		if json.Unmarshal(b, &r) != nil {
			return true
		}
		switch {
		case r.Type == "user" && r.Subtype == "":
			turns = addTurn(turns, "user", r.text(), r.Timestamp)
		case r.Type == "assistant":
			turns = addTurn(turns, "assistant", r.text(), r.Timestamp)
		}
		return true
	})
	return turns, nil
}
