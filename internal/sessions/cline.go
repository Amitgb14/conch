package sessions

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ---- Cline ----
//
// Cline's CLI keeps each session in a folder of its own,
// <CLINE_SESSION_DATA_DIR, or <CLINE_DATA_DIR, or <CLINE_DIR or ~/.cline>/data>/sessions>/<id>/,
// not filed by project: <id>.json is the manifest (session_id, source,
// started_at, ended_at, cwd, prompt, metadata.title) and
// <id>.messages.json the conversation, {messages: [{role, content}]}
// (sdk/packages/core/src/session/models/session-manifest.ts and
// services/session-artifacts.ts). Ids are a millisecond time and five
// random characters, 1731500000000_a1b2c.
//
// It also indexes them in a database and keeps checkpoints as git refs in
// the project, so a delete is left to its own `cline history delete`,
// which removes all three.

func clineSessionsDir(e Env) string {
	if d := e.get("CLINE_SESSION_DATA_DIR"); d != "" {
		return d
	}
	if d := e.get("CLINE_DATA_DIR"); d != "" {
		return filepath.Join(d, "sessions")
	}
	base := filepath.Join(e.Home, ".cline")
	if d := e.get("CLINE_DIR"); d != "" {
		base = d
	}
	return filepath.Join(base, "data", "sessions")
}

func cline(e Env, dirs []string) []Session {
	files, _ := filepath.Glob(filepath.Join(clineSessionsDir(e), "*", "*.json"))
	var out []Session
	for _, f := range files {
		// The manifest is the one named for its folder; the conversation
		// and compaction files sit beside it.
		if filepath.Base(f) != filepath.Base(filepath.Dir(f))+".json" {
			continue
		}
		s := parseCached(f, parseCline)
		if s == nil || !withinAny(s.Dir, dirs) {
			continue
		}
		// The parse is cached by the manifest, which is written when the
		// session starts and ends; the conversation beside it is written
		// as it goes, so when it was last used is asked every time.
		c := *s
		if fi, err := os.Stat(clineMessages(f, clineManifest{MessagesPath: s.messages})); err == nil && fi.ModTime().After(c.Updated) {
			c.Updated = fi.ModTime()
		}
		if time.Since(c.Updated) > maxAge {
			continue
		}
		out = append(out, c)
	}
	return out
}

type clineManifest struct {
	ID           string    `json:"session_id"`
	Source       string    `json:"source"`
	Started      time.Time `json:"started_at"`
	Ended        time.Time `json:"ended_at"`
	Cwd          string    `json:"cwd"`
	Prompt       string    `json:"prompt"`
	MessagesPath string    `json:"messages_path"`
	Metadata     struct {
		Title string `json:"title"`
	} `json:"metadata"`
}

func parseCline(path string, st os.FileInfo) *Session {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var m clineManifest
	if json.Unmarshal(b, &m) != nil || m.ID == "" || m.Cwd == "" {
		return nil
	}
	if m.Source == "subagent" {
		return nil // a session an agent started for itself, under its own
	}
	t := firstNonEmpty(m.Metadata.Title, m.Prompt)
	if !realPrompt(t) {
		return nil // nothing was said: not worth resuming
	}
	return &Session{Agent: "cline", ID: m.ID, Dir: m.Cwd, Title: title(t), Started: m.Started, Updated: st.ModTime(), Path: path,
		messages: m.MessagesPath}
}

// clineMessages is where a manifest's conversation is: where it says, or
// beside it.
func clineMessages(manifest string, m clineManifest) string {
	if m.MessagesPath != "" {
		return m.MessagesPath
	}
	return strings.TrimSuffix(manifest, ".json") + ".messages.json"
}

func clineTranscript(path string) ([]Turn, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m clineManifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(clineMessages(path, m))
	if os.IsNotExist(err) {
		return nil, nil // started, and nothing said yet
	}
	if err != nil {
		return nil, err
	}
	type message struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	var file struct {
		Messages []message `json:"messages"`
	}
	if json.Unmarshal(raw, &file) != nil {
		// Older files are the list alone.
		if err := json.Unmarshal(raw, &file.Messages); err != nil {
			return nil, err
		}
	}
	var turns []Turn
	for _, msg := range file.Messages {
		if msg.Role == "user" || msg.Role == "assistant" {
			turns = addTurn(turns, msg.Role, textParts(msg.Content), time.Time{})
		}
	}
	return turns, nil
}

// clineBinary is the cline command, found in the PATH the store was given
// rather than this process's, so tests never reach a real one.
func clineBinary(e Env) string {
	for _, d := range filepath.SplitList(e.get("PATH")) {
		if p := filepath.Join(d, "cline"); d != "" && isExecutable(p) {
			return p
		}
	}
	return ""
}

func clineDelete(ctx context.Context, e Env, s Session) error {
	bin := clineBinary(e)
	if bin == "" {
		return fmt.Errorf("deleting a Cline session needs the cline command")
	}
	cmd := exec.CommandContext(ctx, bin, "history", "delete", "--session-id", s.ID)
	cmd.Dir = s.Dir
	cmd.Env = append(os.Environ(), "HOME="+e.Home)
	cmd.WaitDelay = time.Second
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("cline history delete: %v: %s", err, strings.TrimSpace(string(out)))
	}
	cache.Delete(s.Path)
	return nil
}
