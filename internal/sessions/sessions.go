// Package sessions finds the conversations agents saved for a directory, so
// conch can list and resume them. Each agent keeps its own store; this
// package only reads them:
//
//   - Claude Code: ~/.claude/projects/<encoded dir>/<session id>.jsonl
//   - Codex: ~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl (session_meta first)
//   - Gemini CLI: ~/.gemini/tmp/<project>/chats/session-*.json
//   - OpenCode: ~/.local/share/opencode/opencode.db (sqlite), or the older
//     storage/session/*/*.json files
package sessions

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Session is one saved agent conversation.
type Session struct {
	Agent   string
	ID      string
	Dir     string // where it ran
	Branch  string // when the agent recorded it
	Title   string
	Started time.Time
	Updated time.Time
	// Path is the agent's session file, or its database (OpenCode).
	Path string
}

// Env is where agents keep their data.
type Env struct {
	Home   string
	Getenv func(string) string
}

// CurrentEnv is this process's environment.
func CurrentEnv() Env {
	home, _ := os.UserHomeDir()
	return Env{Home: home, Getenv: os.Getenv}
}

func (e Env) get(k string) string {
	if e.Getenv == nil {
		return ""
	}
	return e.Getenv(k)
}

// maxAge bounds how far back stores are searched.
const maxAge = 180 * 24 * time.Hour

// List returns the sessions that ran in any of dirs (or below them), newest
// first, at most limit.
func List(e Env, dirs []string, limit int) []Session {
	var out []Session
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, find := range []func(Env, []string) []Session{claude, codex, gemini, opencode} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			found := find(e, dirs)
			mu.Lock()
			out = append(out, found...)
			mu.Unlock()
		}()
	}
	wg.Wait()
	sort.SliceStable(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// within reports whether path is dir or inside it.
func within(path, dir string) bool {
	return path == dir || strings.HasPrefix(path, strings.TrimSuffix(dir, "/")+"/")
}

func withinAny(path string, dirs []string) bool {
	for _, d := range dirs {
		if within(path, d) {
			return true
		}
	}
	return false
}

// ---- parse cache: stores are append-only files, so reparse on change ----

type cached struct {
	mod  time.Time
	size int64
	s    *Session
}

var cache sync.Map // path → cached

func parseCached(path string, parse func(string, os.FileInfo) *Session) *Session {
	st, err := os.Stat(path)
	if err != nil {
		return nil
	}
	if v, ok := cache.Load(path); ok {
		c := v.(cached)
		if c.mod.Equal(st.ModTime()) && c.size == st.Size() {
			return c.s
		}
	}
	s := parse(path, st)
	cache.Store(path, cached{mod: st.ModTime(), size: st.Size(), s: s})
	return s
}

// lines calls fn for up to max JSON lines of r, stopping when fn returns
// false. Overlong lines are skipped.
func lines(r io.Reader, max int, fn func([]byte) bool) {
	br := bufio.NewReaderSize(r, 64*1024)
	for i := 0; i < max; i++ {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 && len(line) < 8<<20 && !fn(bytes.TrimSpace(line)) {
			return
		}
		if err != nil {
			return
		}
	}
}

// title cleans a first prompt for display.
func title(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 120 {
		s = string(r[:119]) + "…"
	}
	return s
}

// promptText extracts text from a message content that is either a string
// or a list of parts with text.
func promptText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) == nil {
		var b strings.Builder
		for _, p := range parts {
			if p.Text != "" {
				b.WriteString(p.Text + " ")
			}
		}
		return strings.TrimSpace(b.String())
	}
	return ""
}

// realPrompt is false for injected context rather than something typed.
func realPrompt(s string) bool {
	s = strings.TrimSpace(s)
	return s != "" && !strings.HasPrefix(s, "<")
}

// Delete removes a saved session: files move to trashDir (so a mistake can
// be undone), OpenCode's database entries are deleted through its CLI.
func Delete(ctx context.Context, e Env, s Session, trashDir string) error {
	if s.Path == "" {
		return fmt.Errorf("no file for this %s session", s.Agent)
	}
	if s.Agent == "opencode" && strings.HasSuffix(s.Path, ".db") {
		bin, err := exec.LookPath("opencode")
		if err != nil {
			return fmt.Errorf("deleting an OpenCode session needs the opencode command")
		}
		cmd := exec.CommandContext(ctx, bin, "session", "delete", s.ID)
		cmd.Dir = s.Dir
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("opencode session delete: %v: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	paths := []string{s.Path}
	if s.Agent == "claude" {
		// Claude keeps a session's subagent transcripts and tool output in a
		// directory named like the session.
		if dir := strings.TrimSuffix(s.Path, ".jsonl"); dir != s.Path {
			if st, err := os.Stat(dir); err == nil && st.IsDir() {
				paths = append(paths, dir)
			}
		}
	}
	if err := os.MkdirAll(trashDir, 0o700); err != nil {
		return err
	}
	stamp := time.Now().Format("20060102-150405")
	for _, p := range paths {
		dst := filepath.Join(trashDir, s.Agent+"-"+stamp+"-"+filepath.Base(p))
		if err := os.Rename(p, dst); err != nil {
			return fmt.Errorf("move %s to the trash: %w", p, err)
		}
		cache.Delete(p)
	}
	return nil
}
