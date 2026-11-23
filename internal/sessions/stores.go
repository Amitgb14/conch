package sessions

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// ---- Claude Code ----

// claudeDirName is how Claude names a directory's project folder: every
// character other than a letter or digit becomes "-" — one for each UTF-16
// unit, as JavaScript's replace counts them, so "é" is one dash and an
// emoji two. Claude cuts a name longer than claudeDirMax there and adds a
// hash of its own (see claudeIn).
func claudeDirName(dir string) string {
	var b strings.Builder
	for _, c := range dir {
		switch {
		case c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9':
			b.WriteRune(c)
		case c > 0xffff:
			b.WriteString("--")
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

// claudeDirMax is the longest project folder name Claude Code writes whole.
const claudeDirMax = 200

func claude(e Env, dirs []string) []Session {
	root := filepath.Join(e.Home, ".claude")
	if d := e.get("CLAUDE_CONFIG_DIR"); d != "" {
		root = d
	}
	var out []Session
	for _, a := range e.roots("claude", root) {
		out = append(out, claudeIn(a.Dir, a.Name, dirs)...)
	}
	return out
}

// claudeIn lists the sessions in one Claude configuration folder.
func claudeIn(root, account string, dirs []string) []Session {
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
			if enc := claudeDirName(d); name == enc || strings.HasPrefix(name, enc+"-") ||
				len(enc) > claudeDirMax && strings.HasPrefix(name, enc[:claudeDirMax]+"-") {
				match = true
			}
		}
		if !match {
			continue
		}
		files, _ := filepath.Glob(filepath.Join(projects, name, "*.jsonl"))
		for _, f := range files {
			if s := parseCached(f, parseClaude); s != nil && withinAny(s.Dir, dirs) {
				c := *s
				c.Account = account
				out = append(out, c)
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
	s := &Session{Agent: "claude", ID: strings.TrimSuffix(filepath.Base(path), ".jsonl"), Updated: st.ModTime(), Path: path}
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
		TotalCost float64         `json:"totalCostUSD"`
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
	s.Cost = claudeCost(path)
	return s
}

// claudeCost is what Claude Code says the conversation has cost so far.
// The cost lines are few and far between — a 35 MB transcript can hold two
// dozen, none of them near the end — so the whole file is read, but only
// the bytes added since last time.
func claudeCost(path string) float64 {
	return scanCost(path, []byte(`"cost-state"`), func(b []byte) (float64, bool) {
		var l struct {
			Type      string  `json:"type"`
			TotalCost float64 `json:"totalCostUSD"`
		}
		if json.Unmarshal(b, &l) != nil || l.Type != "cost-state" || l.TotalCost <= 0 {
			return 0, false
		}
		return l.TotalCost, true
	})
}

// ---- Codex ----

func codex(e Env, dirs []string) []Session {
	home := filepath.Join(e.Home, ".codex")
	if d := e.get("CODEX_HOME"); d != "" {
		home = d
	}
	var out []Session
	for _, a := range e.roots("codex", home) {
		out = append(out, codexIn(a.Dir, a.Name, dirs)...)
	}
	return out
}

// CodexRollout is the file Codex keeps thread id in, in this
// environment's Codex home (CODEX_HOME, else ~/.codex), or "" when it has
// none: rollout-TIME-ID.jsonl under its sessions folder. Only that home,
// since `codex resume` started by conch reads only it.
func CodexRollout(e Env, id string) string {
	if id == "" || strings.ContainsAny(id, `/\`) {
		return ""
	}
	home := filepath.Join(e.Home, ".codex")
	if d := e.get("CODEX_HOME"); d != "" {
		home = d
	}
	found := ""
	_ = filepath.WalkDir(filepath.Join(home, "sessions"), func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && codexRolloutID(d.Name()) == id {
			found = path
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

// codexRolloutID is the thread id in a rollout's file name,
// rollout-2026-11-17T23-28-24-ID.jsonl: what follows the time, which is
// always as long, so an id is never matched by the end of a longer one.
func codexRolloutID(name string) string {
	const head = len("rollout-2026-11-17T23-28-24-")
	if !strings.HasPrefix(name, "rollout-") || !strings.HasSuffix(name, ".jsonl") || len(name) <= head+len(".jsonl") {
		return ""
	}
	return strings.TrimSuffix(name[head:], ".jsonl")
}

// codexIn lists the sessions in one Codex home.
func codexIn(home, account string, dirs []string) []Session {
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
			c := *s
			c.Account = account
			out = append(out, c)
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
	s := &Session{Agent: "codex", Updated: st.ModTime(), Path: path}
	lines(f, 300, func(b []byte) bool {
		var l struct {
			Type    string `json:"type"`
			Payload struct {
				Type      string     `json:"type"`
				ID        string     `json:"id"`
				Cwd       string     `json:"cwd"`
				Timestamp time.Time  `json:"timestamp"`
				Message   string     `json:"message"`
				Item      *codexItem `json:"item"`
				Git       struct {
					Branch string `json:"branch"`
				} `json:"git"`
			} `json:"payload"`
		}
		if json.Unmarshal(b, &l) != nil {
			return true
		}
		if l.Type == "session_meta" && s.ID == "" {
			s.ID, s.Dir, s.Started, s.Branch = l.Payload.ID, l.Payload.Cwd, l.Payload.Timestamp, l.Payload.Git.Branch
		} else if role, text := codexMessage(l.Type, l.Payload.Type, l.Payload.Message, l.Payload.Item); role == "user" && realPrompt(text) {
			s.Title = title(text)
			return false
		}
		return true
	})
	if s.ID == "" || s.Dir == "" || s.Title == "" {
		return nil
	}
	s.Output = codexOutput(f, st)
	return s
}

// codexItem is a message as Codex writes it from 0.158 on: an
// item_completed event's item, its text in typed parts ("text" for the
// person, "Text" for Codex). Older rollouts said the same in user_message
// and agent_message events, which newer ones no longer write at all.
type codexItem struct {
	Type    string          `json:"type"`
	Content json.RawMessage `json:"content"`
}

// codexMessage is who said what in a rollout line, from either kind of
// event; "" for anything else: tool calls, reasoning, and the
// response_items that repeat the conversation along with the instructions
// Codex adds to it.
func codexMessage(lineType, eventType, message string, item *codexItem) (role, text string) {
	if lineType != "event_msg" {
		return "", ""
	}
	switch eventType {
	case "user_message":
		return "user", message
	case "agent_message":
		return "assistant", message
	case "item_completed":
		if item == nil {
			return "", ""
		}
		switch item.Type {
		case "UserMessage":
			return "user", codexText(item.Content)
		case "AgentMessage":
			return "assistant", codexText(item.Content)
		}
	}
	return "", ""
}

// codexText joins an item's text parts, leaving out images and the rest.
func codexText(raw json.RawMessage) string {
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	var b strings.Builder
	for _, p := range parts {
		if strings.EqualFold(p.Type, "text") && strings.TrimSpace(p.Text) != "" {
			if b.Len() > 0 {
				b.WriteString("\n\n")
			}
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

// codexOutput reads the tokens a Codex session generated from the last
// token_count event, looking only at the end of the rollout. Codex reports
// no cost, so tokens are all there is to show.
func codexOutput(f *os.File, st os.FileInfo) int {
	if _, err := f.Seek(max(st.Size()-256*1024, 0), io.SeekStart); err != nil {
		return 0
	}
	out := 0
	lines(f, 1<<20, func(b []byte) bool {
		if !bytes.Contains(b, []byte(`"token_count"`)) {
			return true
		}
		var l struct {
			Payload struct {
				Type string `json:"type"`
				Info *struct {
					Total struct {
						Output int `json:"output_tokens"`
					} `json:"total_token_usage"`
				} `json:"info"`
			} `json:"payload"`
		}
		if json.Unmarshal(b, &l) == nil && l.Payload.Type == "token_count" && l.Payload.Info != nil {
			out = l.Payload.Info.Total.Output // the last one is the total
		}
		return true
	})
	return out
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
	s := &Session{Agent: "gemini", ID: rec.SessionID, Started: rec.StartTime, Updated: rec.LastUpdated, Title: rec.Summary, Path: path}
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

// sqlUnder are the conditions for col being the folder dir or inside it.
// SQLite's substr counts characters in text, not bytes, so the prefix's
// length is counted in runes: in bytes, /Users/josé/ is one longer than
// the substr it is compared with, and no session under it ever listed.
func sqlUnder(col, dir string) []string {
	prefix := strings.TrimSuffix(dir, "/") + "/"
	return []string{col + " = " + sqlQuote(dir),
		"substr(" + col + ", 1, " + strconv.Itoa(utf8.RuneCountInString(prefix)) + ") = " + sqlQuote(prefix)}
}

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
		conds = append(conds, sqlUnder("directory", d)...)
	}
	where := " FROM session WHERE parent_id IS NULL AND time_archived IS NULL AND (" +
		strings.Join(conds, " OR ") + ") ORDER BY time_updated DESC LIMIT 200"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Older stores have no cost columns; ask for them, and ask again
	// without rather than losing the whole list.
	res, err := exec.CommandContext(ctx, bin, "-readonly", "-json", db,
		"SELECT id, directory, title, time_created, time_updated, cost, tokens_output"+where).Output()
	if err != nil {
		res, err = exec.CommandContext(ctx, bin, "-readonly", "-json", db,
			"SELECT id, directory, title, time_created, time_updated"+where).Output()
	}
	if err != nil {
		return nil, false
	}
	var rows []struct {
		ID      string  `json:"id"`
		Dir     string  `json:"directory"`
		Title   string  `json:"title"`
		Cost    float64 `json:"cost"`
		Output  int     `json:"tokens_output"`
		Created int64   `json:"time_created"`
		Updated int64   `json:"time_updated"`
	}
	if len(bytes.TrimSpace(res)) > 0 && json.Unmarshal(res, &rows) != nil {
		return nil, false
	}
	for _, r := range rows {
		out = append(out, Session{Agent: "opencode", ID: r.ID, Dir: r.Dir, Title: title(r.Title),
			Started: time.UnixMilli(r.Created), Updated: time.UnixMilli(r.Updated), Path: db, Cost: r.Cost, Output: r.Output})
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
		Started: time.UnixMilli(rec.Time.Created), Updated: time.UnixMilli(rec.Time.Updated), Path: path}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// ---- Devin for Terminal ----

// devinTimeout bounds one `devin list`, which reads Devin's local session
// database.
const devinTimeout = 10 * time.Second

// devinBinary is the devin command: where its installer puts it, else PATH.
func devinBinary(e Env) string {
	if p := filepath.Join(e.Home, ".local", "bin", "devin"); isExecutable(p) {
		return p
	}
	// PATH from the environment the store was given, not this process's,
	// so tests with a scratch environment never reach a real devin.
	for _, d := range filepath.SplitList(e.get("PATH")) {
		if p := filepath.Join(d, "devin"); d != "" && isExecutable(p) {
			return p
		}
	}
	return ""
}

func isExecutable(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir() && st.Mode()&0o111 != 0
}

// devin lists Devin for Terminal's sessions. Devin keeps them in a local
// database whose layout isn't documented, so conch asks the CLI, which lists
// the sessions of the folder it runs in: `devin list --format json` in each
// of dirs.
func devin(e Env, dirs []string) []Session {
	bin := devinBinary(e)
	if bin == "" {
		return nil
	}
	// One `devin list` takes a few hundred milliseconds, and a project has a
	// folder per worktree: ask for them all at once.
	lists := make([][]Session, len(dirs))
	var wg sync.WaitGroup
	for i, dir := range dirs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lists[i] = devinList(e, bin, dir)
		}()
	}
	wg.Wait()
	seen := map[string]bool{}
	var out []Session
	for _, list := range lists {
		for _, s := range list {
			if seen[s.ID] || !withinAny(s.Dir, dirs) {
				continue
			}
			seen[s.ID] = true
			out = append(out, s)
		}
	}
	return out
}

func devinList(e Env, bin, dir string) []Session {
	ctx, cancel := context.WithTimeout(context.Background(), devinTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "list", "--format", "json")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "HOME="+e.Home)
	cmd.WaitDelay = time.Second
	b, err := cmd.Output()
	if err != nil {
		return nil // not signed in, an older devin, or a missing folder
	}
	var rows []struct {
		ID    string `json:"id"`
		Dir   string `json:"working_directory"`
		Last  int64  `json:"last_activity_at"`
		Title string `json:"title"`
	}
	if json.Unmarshal(b, &rows) != nil {
		return nil
	}
	var out []Session
	for _, r := range rows {
		if r.ID == "" || r.Dir == "" {
			continue
		}
		updated := time.Unix(r.Last, 0)
		if r.Last <= 0 || time.Since(updated) > maxAge {
			continue
		}
		// Devin reports no start time; its path is the command that manages it.
		out = append(out, Session{Agent: "devin", ID: r.ID, Dir: r.Dir, Title: title(r.Title),
			Started: updated, Updated: updated, Path: bin})
	}
	return out
}
