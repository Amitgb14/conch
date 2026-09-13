package sessions

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ---- Claude Code ----

// claudeDirName is how Claude names a directory's project folder: every
// character other than a letter or digit becomes "-".
func claudeDirName(dir string) string {
	b := []byte(dir)
	for i, c := range b {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			b[i] = '-'
		}
	}
	return string(b)
}

func claude(e Env, dirs []string) []Session {
	root := filepath.Join(e.Home, ".claude")
	if d := e.get("CLAUDE_CONFIG_DIR"); d != "" {
		root = d
	}
	projects := filepath.Join(root, "projects")
	entries, err := os.ReadDir(projects)
	if err != nil {
		return nil
	}
	var out []Session
	for _, en := range entries {
		name := en.Name()
		match := false
		for _, d := range dirs {
			// Subdirectories share the prefix; the session's cwd decides.
			if enc := claudeDirName(d); name == enc || strings.HasPrefix(name, enc+"-") {
				match = true
			}
		}
		if !match {
			continue
		}
		files, _ := filepath.Glob(filepath.Join(projects, name, "*.jsonl"))
		for _, f := range files {
			if s := parseCached(f, parseClaude); s != nil && withinAny(s.Dir, dirs) {
				out = append(out, *s)
			}
		}
	}
	return out
}

func parseClaude(path string, st os.FileInfo) *Session {
	if time.Since(st.ModTime()) > maxAge {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	s := &Session{Agent: "claude", ID: strings.TrimSuffix(filepath.Base(path), ".jsonl"), Updated: st.ModTime()}
	var aiTitle, summary, prompt string
	type line struct {
		Type      string          `json:"type"`
		Cwd       string          `json:"cwd"`
		GitBranch string          `json:"gitBranch"`
		Timestamp time.Time       `json:"timestamp"`
		IsMeta    bool            `json:"isMeta"`
		Sidechain bool            `json:"isSidechain"`
		AITitle   string          `json:"aiTitle"`
		Summary   string          `json:"summary"`
		Message   json.RawMessage `json:"message"`
	}
	visit := func(b []byte) {
		var l line
		if json.Unmarshal(b, &l) != nil {
			return
		}
		switch l.Type {
		case "ai-title":
			aiTitle = l.AITitle
		case "summary":
			summary = l.Summary
		case "user", "assistant":
			if l.Sidechain {
				return
			}
			if s.Dir == "" && l.Cwd != "" {
				s.Dir, s.Branch = l.Cwd, l.GitBranch
			}
			if s.Started.IsZero() && !l.Timestamp.IsZero() {
				s.Started = l.Timestamp
			}
			if l.Type == "user" && prompt == "" && !l.IsMeta {
				var m struct {
					Content json.RawMessage `json:"content"`
				}
				if json.Unmarshal(l.Message, &m) == nil {
					if t := promptText(m.Content); realPrompt(t) {
						prompt = t
					}
				}
			}
		}
	}
	lines(f, 400, func(b []byte) bool {
		if bytes.Contains(b, []byte(`"type":"`)) {
			visit(b)
		}
		return true
	})
	// Titles are appended as the session goes on: check the tail too.
	if st.Size() > 256*1024 {
		if _, err := f.Seek(-256*1024, 2); err == nil {
			lines(f, 1<<20, func(b []byte) bool {
				if bytes.Contains(b, []byte(`"ai-title"`)) || bytes.Contains(b, []byte(`"summary"`)) {
					visit(b)
				}
				return true
			})
		}
	}
	if s.Dir == "" || prompt == "" && aiTitle == "" && summary == "" {
		return nil // nothing was said: not worth resuming
	}
	s.Title = title(firstNonEmpty(aiTitle, summary, prompt))
	return s
}

// ---- Codex ----

func codex(e Env, dirs []string) []Session {
	home := filepath.Join(e.Home, ".codex")
	if d := e.get("CODEX_HOME"); d != "" {
		home = d
	}
	var files []string
	_ = filepath.WalkDir(filepath.Join(home, "sessions"), func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(path, ".jsonl") {
			files = append(files, path)
		}
		return nil
	})
	sort.Sort(sort.Reverse(sort.StringSlice(files))) // dated paths: newest first
	var out []Session
	for i, f := range files {
		if i >= 3000 {
			break
		}
		s := parseCached(f, parseCodex)
		if s == nil {
			continue
		}
		if time.Since(s.Updated) > maxAge {
			break
		}
		if withinAny(s.Dir, dirs) {
			out = append(out, *s)
		}
	}
	return out
}

func parseCodex(path string, st os.FileInfo) *Session {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	s := &Session{Agent: "codex", Updated: st.ModTime()}
	lines(f, 300, func(b []byte) bool {
		var l struct {
			Type    string `json:"type"`
			Payload struct {
				Type      string    `json:"type"`
				ID        string    `json:"id"`
				Cwd       string    `json:"cwd"`
				Timestamp time.Time `json:"timestamp"`
				Message   string    `json:"message"`
				Git       struct {
					Branch string `json:"branch"`
				} `json:"git"`
			} `json:"payload"`
		}
		if json.Unmarshal(b, &l) != nil {
			return true
		}
		switch {
		case l.Type == "session_meta" && s.ID == "":
			s.ID, s.Dir, s.Started, s.Branch = l.Payload.ID, l.Payload.Cwd, l.Payload.Timestamp, l.Payload.Git.Branch
		case l.Type == "event_msg" && l.Payload.Type == "user_message" && realPrompt(l.Payload.Message):
			s.Title = title(l.Payload.Message)
			return false
		}
		return true
	})
	if s.ID == "" || s.Dir == "" || s.Title == "" {
		return nil
	}
	return s
}

// ---- Gemini CLI ----

func gemini(e Env, dirs []string) []Session {
	home := filepath.Join(e.Home, ".gemini")
	var registry struct {
		Projects map[string]string `json:"projects"`
	}
	if b, err := os.ReadFile(filepath.Join(home, "projects.json")); err == nil {
		_ = json.Unmarshal(b, &registry)
	}
	var out []Session
	seen := map[string]bool{}
	for _, d := range dirs {
		sum := sha256.Sum256([]byte(d))
		for _, name := range []string{registry.Projects[d], hex.EncodeToString(sum[:])} {
			if name == "" {
				continue
			}
			chats := filepath.Join(home, "tmp", name, "chats")
			files, _ := filepath.Glob(filepath.Join(chats, "session-*.json*"))
			for _, f := range files {
				if seen[f] {
					continue
				}
				seen[f] = true
				if s := parseCached(f, parseGemini); s != nil {
					c := *s
					c.Dir = d // the store is keyed by the project directory
					out = append(out, c)
				}
			}
		}
	}
	return out
}

func parseGemini(path string, st os.FileInfo) *Session {
	if time.Since(st.ModTime()) > maxAge {
		return nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var rec struct {
		SessionID   string    `json:"sessionId"`
		StartTime   time.Time `json:"startTime"`
		LastUpdated time.Time `json:"lastUpdated"`
		Summary     string    `json:"summary"`
		Kind        string    `json:"kind"`
		Messages    []struct {
			Type    string          `json:"type"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if json.Unmarshal(b, &rec) != nil {
		// A JSONL record: the header is the first line.
		first, _, _ := bytes.Cut(b, []byte("\n"))
		if json.Unmarshal(first, &rec) != nil {
			return nil
		}
	}
	if rec.SessionID == "" || rec.Kind == "subagent" {
		return nil
	}
	s := &Session{Agent: "gemini", ID: rec.SessionID, Started: rec.StartTime, Updated: rec.LastUpdated, Title: rec.Summary}
	if s.Updated.IsZero() {
		s.Updated = st.ModTime()
	}
	if s.Title == "" {
		for _, m := range rec.Messages {
			if t := promptText(m.Content); m.Type == "user" && realPrompt(t) {
				s.Title = t
				break
			}
		}
	}
	if s.Title == "" {
		return nil
	}
	s.Title = title(s.Title)
	return s
}

// ---- OpenCode ----

func opencodeData(e Env) string {
	if x := e.get("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, "opencode")
	}
	return filepath.Join(e.Home, ".local", "share", "opencode")
}

func opencode(e Env, dirs []string) []Session {
	data := opencodeData(e)
	if out, ok := opencodeDB(filepath.Join(data, "opencode.db"), dirs); ok {
		return out
	}
	// Older releases kept one JSON file per session.
	files, _ := filepath.Glob(filepath.Join(data, "storage", "session", "*", "ses_*.json"))
	var out []Session
	for _, f := range files {
		if s := parseCached(f, parseOpenCodeFile); s != nil && withinAny(s.Dir, dirs) {
			out = append(out, *s)
		}
	}
	return out
}

func sqlQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// opencodeDB queries the sqlite store through the sqlite3 command; ok is
// false when there is no database or no sqlite3 to read it.
func opencodeDB(db string, dirs []string) (out []Session, ok bool) {
	if _, err := os.Stat(db); err != nil {
		return nil, false
	}
	bin, err := exec.LookPath("sqlite3")
	if err != nil || len(dirs) == 0 {
		return nil, false
	}
	var conds []string
	for _, d := range dirs {
		prefix := strings.TrimSuffix(d, "/") + "/"
		conds = append(conds, "directory = "+sqlQuote(d), "substr(directory, 1, "+strconv.Itoa(len(prefix))+") = "+sqlQuote(prefix))
	}
	query := "SELECT id, directory, title, time_created, time_updated FROM session WHERE parent_id IS NULL AND time_archived IS NULL AND (" +
		strings.Join(conds, " OR ") + ") ORDER BY time_updated DESC LIMIT 200"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := exec.CommandContext(ctx, bin, "-readonly", "-json", db, query).Output()
	if err != nil {
		return nil, false
	}
	var rows []struct {
		ID      string `json:"id"`
		Dir     string `json:"directory"`
		Title   string `json:"title"`
		Created int64  `json:"time_created"`
		Updated int64  `json:"time_updated"`
	}
	if len(bytes.TrimSpace(res)) > 0 && json.Unmarshal(res, &rows) != nil {
		return nil, false
	}
	for _, r := range rows {
		out = append(out, Session{Agent: "opencode", ID: r.ID, Dir: r.Dir, Title: title(r.Title),
			Started: time.UnixMilli(r.Created), Updated: time.UnixMilli(r.Updated)})
	}
	return out, true
}

func parseOpenCodeFile(path string, st os.FileInfo) *Session {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var rec struct {
		ID       string `json:"id"`
		ParentID string `json:"parentID"`
		Dir      string `json:"directory"`
		Title    string `json:"title"`
		Time     struct {
			Created int64 `json:"created"`
			Updated int64 `json:"updated"`
		} `json:"time"`
	}
	if json.Unmarshal(b, &rec) != nil || rec.ID == "" || rec.ParentID != "" {
		return nil
	}
	return &Session{Agent: "opencode", ID: rec.ID, Dir: rec.Dir, Title: title(rec.Title),
		Started: time.UnixMilli(rec.Time.Created), Updated: time.UnixMilli(rec.Time.Updated)}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
