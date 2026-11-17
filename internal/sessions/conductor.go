package sessions

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ---- Conductor ----
//
// Conductor (conductor.build, a Mac app that ran Claude Code and Codex in
// parallel) keeps its conversations in its own SQLite database, not in the
// agents' stores: the agent's session file is gone with the workspace, or
// was never where the agent's own resume looks. So they are read from
// there and handed on as a handoff document (Handoff), which `conch import
// conductor` writes out. A row of `sessions` is a conversation (title,
// agent_type, workspace_id), `workspaces` and `repos` say where it ran, and
// `session_messages` holds it: a user row is the text typed, an assistant
// row one message of the agent's stream as JSON — for Claude the Agent
// SDK's {"type":"assistant","message":{"content":[…]}}.

// ConductorDB is where Conductor keeps its database on a Mac.
func ConductorDB(home string) string {
	return filepath.Join(home, "Library", "Application Support", "com.conductor.app", "conductor.db")
}

// Imported is one conversation read from another app's store.
type Imported struct {
	Session Session
	Repo    string // the repository's name
	Root    string // the repository's folder
	// Workspace is the folder the conversation ran in, which may be gone:
	// Session.Dir is the nearest of it and Root that is still there.
	Workspace string
	Turns     []Turn
	// Unread counts the agent's messages that were neither text nor a
	// tool call conch knows to leave out.
	Unread int
}

// ReadConductor reads every conversation in a Conductor database, oldest
// first. The database is copied before it is read, with its write-ahead
// log, so a Conductor that is still open is neither locked nor written to.
func ReadConductor(ctx context.Context, db string) ([]Imported, error) {
	bin, err := exec.LookPath("sqlite3")
	if err != nil {
		return nil, errors.New("reading Conductor's database needs the sqlite3 command")
	}
	if _, err := os.Stat(db); err != nil {
		return nil, fmt.Errorf("no Conductor database at %s", db)
	}
	tmp, err := os.MkdirTemp("", "conch-conductor-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	cp := filepath.Join(tmp, "conductor.db")
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := copyFile(db+suffix, cp+suffix); err != nil && !(suffix != "" && errors.Is(err, os.ErrNotExist)) {
			return nil, fmt.Errorf("copy Conductor's database: %w", err)
		}
	}
	query := func(q string, v any) error {
		out, err := exec.CommandContext(ctx, bin, "-json", cp, q).Output()
		if err != nil {
			var ee *exec.ExitError
			if errors.As(err, &ee) && len(ee.Stderr) > 0 {
				return fmt.Errorf("sqlite3: %s", strings.TrimSpace(string(ee.Stderr)))
			}
			return fmt.Errorf("sqlite3: %w", err)
		}
		if len(bytes.TrimSpace(out)) == 0 {
			return nil // no rows
		}
		return json.Unmarshal(out, v)
	}

	type row struct {
		ID        string `json:"id"`
		Title     string `json:"title"`
		Agent     string `json:"agent_type"`
		Created   string `json:"created_at"`
		Updated   string `json:"updated_at"`
		Workspace string `json:"workspace_path"`
		DirName   string `json:"directory_name"`
		Branch    string `json:"branch"`
		Repo      string `json:"name"`
		Root      string `json:"root_path"`
	}
	var rows []row
	// Older databases have no title, agent or workspace path: ask with
	// them, and again without rather than read nothing.
	const join = " FROM sessions s LEFT JOIN workspaces w ON w.id = s.workspace_id LEFT JOIN repos r ON r.id = w.repository_id ORDER BY s.created_at, s.id"
	if err := query("SELECT s.id, s.title, s.agent_type, s.created_at, s.updated_at, w.workspace_path, w.directory_name, w.branch, r.name, r.root_path"+join, &rows); err != nil {
		if err2 := query("SELECT s.id, s.created_at, s.updated_at, w.directory_name, w.branch, r.name, r.root_path"+join, &rows); err2 != nil {
			return nil, err
		}
	}
	var msgs []struct {
		Session string `json:"session_id"`
		Role    string `json:"role"`
		Content string `json:"content"`
		Created string `json:"created_at"`
	}
	if err := query("SELECT session_id, role, content, created_at FROM session_messages ORDER BY created_at, rowid", &msgs); err != nil {
		return nil, err
	}

	out := make([]Imported, 0, len(rows))
	at := map[string]int{}
	for _, r := range rows {
		agent := r.Agent
		if agent == "" {
			agent = "claude"
		}
		ws := r.Workspace
		dir := ""
		for _, d := range []string{ws, r.Root} {
			if st, err := os.Stat(d); d != "" && err == nil && st.IsDir() {
				dir = d
				break
			}
		}
		at[r.ID] = len(out)
		out = append(out, Imported{
			Session: Session{Agent: agent, ID: r.ID, Dir: dir, Branch: r.Branch, Title: r.Title, Path: db,
				Started: gooseTime(r.Created), Updated: gooseTime(r.Updated)},
			Repo: r.Repo, Root: r.Root, Workspace: ws,
		})
	}
	for _, m := range msgs {
		i, ok := at[m.Session]
		if !ok {
			continue
		}
		im := &out[i]
		role, text, known := conductorMessage(m.Role, m.Content)
		if !known {
			im.Unread++
			continue
		}
		im.Turns = addTurn(im.Turns, role, text, gooseTime(m.Created))
	}
	for i := range out {
		s := &out[i].Session
		if t := strings.TrimSpace(s.Title); t == "" || strings.EqualFold(t, "untitled") {
			s.Title = ""
			for _, t := range out[i].Turns {
				if t.Role == "user" {
					s.Title = title(t.Text)
					break
				}
			}
		}
	}
	return out, nil
}

// conductorMessage reads one row of session_messages: what was said and by
// whom, or known with no text for a row that says nothing to a reader (a
// tool's result, a progress note); known is false only for a row conch
// could not read at all.
func conductorMessage(role, content string) (who, text string, known bool) {
	var m struct {
		Type    string `json:"type"`
		Message struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"message"`
		// Codex's events: an item finished, with the agent's words.
		Item struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"item"`
	}
	trimmed := strings.TrimSpace(content)
	if !strings.HasPrefix(trimmed, "{") || json.Unmarshal([]byte(trimmed), &m) != nil {
		if role == "user" {
			return "user", content, true // what was typed, as it was typed
		}
		return "", "", false
	}
	switch {
	case role == "user":
		// A prompt kept as a message rather than as text.
		return "user", textParts(m.Message.Content), true
	case m.Type == "assistant":
		return "assistant", textParts(m.Message.Content), true
	case m.Item.Type == "agent_message":
		return "assistant", m.Item.Text, true
	case m.Type != "":
		// The stream's other messages — tool results echoed back as
		// "user", system notes, the result — are not the conversation.
		return "", "", true
	}
	return "", "", false
}

func copyFile(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
