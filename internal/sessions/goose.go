package sessions

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ---- Goose ----
//
// Goose keeps every session in one SQLite database,
// <data>/sessions/sessions.db, where <data> is $GOOSE_PATH_ROOT/data or
// else $XDG_DATA_HOME/goose (~/.local/share/goose, on macOS too). A row of
// `sessions` is a session — id (20250921_1: a date and a count), name,
// working_dir, session_type, created_at, updated_at — and `messages` holds
// its conversation, role and content_json a row, the content Goose's list
// of typed blocks (crates/goose/src/session/session_manager.rs).
//
// It is read through the sqlite3 command, as OpenCode's is, so it is a
// slow store. The CLI's own `goose session remove` asks "Are you sure?" on
// the terminal, so a delete does what that command then does: the
// session's rows, in one transaction.

func gooseDB(e Env) string {
	if root := e.get("GOOSE_PATH_ROOT"); filepath.IsAbs(root) {
		return filepath.Join(root, "data", "sessions", "sessions.db")
	}
	data := filepath.Join(e.Home, ".local", "share")
	if x := e.get("XDG_DATA_HOME"); x != "" {
		data = x
	}
	return filepath.Join(data, "goose", "sessions", "sessions.db")
}

// gooseTypes are the sessions Goose's own list shows: the ones somebody
// started, and the scheduled ones. Subagents and hidden sessions are its
// own business.
const gooseTypes = "session_type IN ('user', 'scheduled')"

func goose(e Env, dirs []string) []Session {
	db := gooseDB(e)
	if _, err := os.Stat(db); err != nil || len(dirs) == 0 {
		return nil
	}
	bin, err := exec.LookPath("sqlite3")
	if err != nil {
		return nil
	}
	var conds []string
	for _, d := range dirs {
		prefix := strings.TrimSuffix(d, "/") + "/"
		conds = append(conds, "working_dir = "+sqlQuote(d), "substr(working_dir, 1, "+strconv.Itoa(len(prefix))+") = "+sqlQuote(prefix))
	}
	// The first thing typed, for a session nobody named.
	first := "(SELECT content_json FROM messages m WHERE m.session_id = s.id AND m.role = 'user' ORDER BY m.id LIMIT 1) AS first"
	where := " FROM sessions s WHERE " + gooseTypes + " AND (" + strings.Join(conds, " OR ") + ")"
	order := " ORDER BY updated_at DESC LIMIT 200"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Older databases have no archive or cost columns: ask with them, and
	// again without rather than losing the list.
	res, err := exec.CommandContext(ctx, bin, "-readonly", "-json", db,
		"SELECT id, name, working_dir, created_at, updated_at, accumulated_cost, "+first+where+" AND archived_at IS NULL"+order).Output()
	if err != nil {
		res, err = exec.CommandContext(ctx, bin, "-readonly", "-json", db,
			"SELECT id, name, working_dir, created_at, updated_at, "+first+where+order).Output()
	}
	if err != nil {
		return nil
	}
	var rows []struct {
		ID      string  `json:"id"`
		Name    string  `json:"name"`
		Dir     string  `json:"working_dir"`
		Created string  `json:"created_at"`
		Updated string  `json:"updated_at"`
		Cost    float64 `json:"accumulated_cost"`
		First   string  `json:"first"`
	}
	if len(bytes.TrimSpace(res)) > 0 && json.Unmarshal(res, &rows) != nil {
		return nil
	}
	var out []Session
	for _, r := range rows {
		t := r.Name
		if t == "" {
			t = textParts(json.RawMessage(r.First))
		}
		if r.ID == "" || r.Dir == "" || !realPrompt(t) {
			continue // nothing was said: not worth resuming
		}
		s := Session{Agent: "goose", ID: r.ID, Dir: r.Dir, Title: title(t), Path: db, Cost: r.Cost,
			Started: gooseTime(r.Created), Updated: gooseTime(r.Updated)}
		if s.Updated.IsZero() {
			s.Updated = s.Started
		}
		if time.Since(s.Updated) > maxAge {
			continue
		}
		out = append(out, s)
	}
	return out
}

// gooseTime reads a timestamp as SQLite's CURRENT_TIMESTAMP writes it
// (UTC, "2006-01-02 15:04:05") or as a chrono time is bound (RFC 3339).
func gooseTime(s string) time.Time {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05.999999999", "2006-01-02T15:04:05.999999999"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

func gooseTranscript(ctx context.Context, db, id string) ([]Turn, error) {
	bin, err := exec.LookPath("sqlite3")
	if err != nil {
		return nil, fmt.Errorf("reading Goose's sessions needs the sqlite3 command")
	}
	res, err := exec.CommandContext(ctx, bin, "-readonly", "-json", db,
		"SELECT role, content_json, created_timestamp FROM messages WHERE session_id = "+sqlQuote(id)+" ORDER BY id").Output()
	if err != nil {
		return nil, fmt.Errorf("sqlite3: %w", err)
	}
	var rows []struct {
		Role    string `json:"role"`
		Content string `json:"content_json"`
		Created int64  `json:"created_timestamp"`
	}
	if len(bytes.TrimSpace(res)) > 0 {
		if err := json.Unmarshal(res, &rows); err != nil {
			return nil, err
		}
	}
	var turns []Turn
	for _, r := range rows {
		if r.Role != "user" && r.Role != "assistant" {
			continue
		}
		turns = addTurn(turns, r.Role, textParts(json.RawMessage(r.Content)), time.Unix(r.Created, 0))
	}
	return turns, nil
}

// gooseDelete removes a session's rows as Goose's delete_session does, in
// one transaction. The ledger and tags tables are newer than the rest, so
// they are emptied only where they exist.
func gooseDelete(ctx context.Context, db, id string) error {
	bin, err := exec.LookPath("sqlite3")
	if err != nil {
		return fmt.Errorf("deleting a Goose session needs the sqlite3 command")
	}
	q := sqlQuote(id)
	ask := func(sql string) (string, error) {
		out, err := exec.CommandContext(ctx, bin, "-bail", db, sql).CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}
	tables, err := ask("SELECT name FROM sqlite_master WHERE type = 'table' AND name IN ('usage_ledger', 'session_tags')")
	if err != nil {
		return fmt.Errorf("sqlite3: %v: %s", err, tables)
	}
	sql := "BEGIN IMMEDIATE; DELETE FROM messages WHERE session_id = " + q + ";"
	for _, t := range strings.Fields(tables) {
		sql += " DELETE FROM " + t + " WHERE session_id = " + q + ";"
	}
	sql += " DELETE FROM sessions WHERE id = " + q + "; COMMIT;"
	if out, err := ask(sql); err != nil {
		return fmt.Errorf("sqlite3: %v: %s", err, out)
	}
	return nil
}
