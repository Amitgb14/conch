package sessions

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// conductorFixture makes a database shaped like Conductor's, as much of it
// as conch reads; old leaves out the columns older versions lacked.
func conductorFixture(t *testing.T, db, ws, root string, old bool) {
	t.Helper()
	bin, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("no sqlite3")
	}
	sessCols, wsCols := "id TEXT PRIMARY KEY, claude_session_id TEXT, created_at TEXT, updated_at TEXT, workspace_id TEXT, title TEXT DEFAULT 'Untitled', agent_type", "id TEXT PRIMARY KEY, repository_id TEXT, directory_name TEXT, branch TEXT, workspace_path TEXT"
	sessRows := `('s1', 'c1', '2026-07-10 15:19:00', '2026-07-10 15:21:00', 'w1', 'Fix the build', 'claude'),
 ('s2', NULL, '2026-07-11 09:00:00', '2026-07-11 09:05:00', 'w2', 'Untitled', 'codex'),
 ('s3', NULL, '2026-07-12 09:00:00', '2026-07-12 09:00:00', 'w1', 'Nothing said', 'claude')`
	wsRows := `('w1', 'r1', 'victoria', 'victoria', '` + ws + `'), ('w2', 'r1', 'buffalo', 'fix/it''s', '/gone/buffalo')`
	if old {
		sessCols, wsCols = "id TEXT PRIMARY KEY, claude_session_id TEXT, created_at TEXT, updated_at TEXT, workspace_id TEXT", "id TEXT PRIMARY KEY, repository_id TEXT, directory_name TEXT, branch TEXT"
		sessRows = `('s1', 'c1', '2026-07-10 15:19:00', '2026-07-10 15:21:00', 'w1')`
		wsRows = `('w1', 'r1', 'victoria', 'victoria')`
	}
	sql := `CREATE TABLE repos (id TEXT PRIMARY KEY, name TEXT, root_path TEXT);
CREATE TABLE workspaces (` + wsCols + `);
CREATE TABLE sessions (` + sessCols + `);
CREATE TABLE session_messages (id TEXT PRIMARY KEY, session_id TEXT, role TEXT, content TEXT, created_at TEXT);
INSERT INTO repos VALUES ('r1', 'shop', '` + root + `');
INSERT INTO workspaces VALUES ` + wsRows + `;
INSERT INTO sessions VALUES ` + sessRows + `;
INSERT INTO session_messages VALUES
 ('m1', 's1', 'user', 'Why does the build fail?', '2026-07-10 15:19:01'),
 ('m2', 's1', 'assistant', '{"type":"system","subtype":"init","session_id":"c1"}', '2026-07-10 15:19:02'),
 ('m3', 's1', 'assistant', '{"type":"assistant","message":{"role":"assistant","content":[{"type":"thinking","thinking":"hmm"},{"type":"text","text":"Let me look."},{"type":"tool_use","name":"Bash","input":{"command":"make"}}]}}', '2026-07-10 15:19:03'),
 ('m4', 's1', 'assistant', '{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"make: *** error"}]}}', '2026-07-10 15:19:04'),
 ('m5', 's1', 'assistant', '{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"A missing header; fixed."}]}}', '2026-07-10 15:19:05'),
 ('m6', 's1', 'assistant', 'not json at all', '2026-07-10 15:19:06'),
 ('m7', 's1', 'assistant', '{"no":"type"}', '2026-07-10 15:19:07'),
 ('m8', 's2', 'user', 'Rename the checkout button', '2026-07-11 09:00:01'),
 ('m9', 's2', 'assistant', '{"type":"item.completed","item":{"type":"agent_message","text":"Renamed it to Pay."}}', '2026-07-11 09:00:02'),
 ('m10', 'gone', 'user', 'a session that was deleted', '2026-07-11 09:00:03');`
	if old {
		sql = strings.ReplaceAll(sql, `,
 ('m8'`, `;
-- `)
		sql = sql[:strings.Index(sql, "-- ")] + "\n"
	}
	if out, err := exec.Command(bin, db, sql).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3: %v: %s", err, out)
	}
}

func TestReadConductor(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "conductor.db")
	ws := filepath.Join(dir, "workspaces", "shop", "victoria")
	root := filepath.Join(dir, "shop")
	os.MkdirAll(ws, 0o755)
	os.MkdirAll(root, 0o755)
	conductorFixture(t, db, ws, root, false)
	before, _ := os.ReadFile(db)

	all, err := ReadConductor(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("got %d conversations, want 3: %+v", len(all), all)
	}
	fix := all[0]
	if fix.Session.Title != "Fix the build" || fix.Session.Agent != "claude" || fix.Session.Branch != "victoria" || fix.Repo != "shop" {
		t.Errorf("first conversation: %+v", fix.Session)
	}
	if fix.Session.Dir != ws || fix.Workspace != ws {
		t.Errorf("dir = %q, workspace = %q; want the workspace, which is there", fix.Session.Dir, fix.Workspace)
	}
	// The prompt, then the agent's words joined into one answer: no
	// thinking, tool call, tool result or system note.
	if len(fix.Turns) != 2 || fix.Turns[0].Role != "user" || fix.Turns[0].Text != "Why does the build fail?" ||
		fix.Turns[1].Role != "assistant" || fix.Turns[1].Text != "Let me look.\n\nA missing header; fixed." {
		t.Errorf("turns = %+v", fix.Turns)
	}
	if fix.Turns[0].Time.IsZero() {
		t.Error("a turn lost its time")
	}
	if fix.Unread != 2 {
		t.Errorf("unread = %d, want 2 (the row that is not JSON and the one with no type)", fix.Unread)
	}

	codex := all[1]
	if codex.Session.Agent != "codex" || codex.Session.Title != "Rename the checkout button" {
		t.Errorf("an untitled conversation is named after its first prompt: %+v", codex.Session)
	}
	if codex.Session.Dir != root || codex.Workspace != "/gone/buffalo" {
		t.Errorf("a workspace that is gone: dir = %q (want the repository), workspace = %q", codex.Session.Dir, codex.Workspace)
	}
	if len(codex.Turns) != 2 || codex.Turns[1].Text != "Renamed it to Pay." {
		t.Errorf("codex turns = %+v", codex.Turns)
	}
	if len(all[2].Turns) != 0 {
		t.Errorf("a conversation with nothing said has turns: %+v", all[2].Turns)
	}

	after, _ := os.ReadFile(db)
	if !bytes.Equal(before, after) {
		t.Error("reading changed Conductor's database")
	}
	if m, _ := filepath.Glob(filepath.Join(dir, "conductor.db-*")); len(m) > 0 {
		t.Errorf("reading left files beside Conductor's database: %v", m)
	}
}

func TestReadConductorOlderDatabase(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "conductor.db")
	root := filepath.Join(dir, "shop")
	os.MkdirAll(root, 0o755)
	conductorFixture(t, db, "", root, true)
	all, err := ReadConductor(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].Session.Agent != "claude" || all[0].Session.Dir != root || len(all[0].Turns) != 2 {
		t.Fatalf("older database: %+v", all)
	}
	if all[0].Session.Title != "Why does the build fail?" {
		t.Errorf("title = %q, want the first prompt", all[0].Session.Title)
	}
}

func TestReadConductorWAL(t *testing.T) {
	bin, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("no sqlite3")
	}
	dir := t.TempDir()
	db := filepath.Join(dir, "conductor.db")
	conductorFixture(t, db, "", dir, false)
	// A message still in the write-ahead log, as an open Conductor leaves
	// it: the log is kept by turning off checkpoints on close.
	add := `PRAGMA journal_mode=WAL; PRAGMA wal_autocheckpoint=0;
INSERT INTO session_messages VALUES ('m99', 's3', 'user', 'Only in the log', '2026-07-12 09:00:01');`
	if out, err := exec.Command(bin, "-cmd", ".dbconfig no_ckpt_on_close on", db, add).CombinedOutput(); err != nil {
		t.Skipf("this sqlite3 cannot keep a log: %v: %s", err, out)
	}
	if _, err := os.Stat(db + "-wal"); err != nil {
		t.Skip("this sqlite3 checkpointed the log on close")
	}
	all, err := ReadConductor(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if len(all[2].Turns) != 1 || all[2].Turns[0].Text != "Only in the log" {
		t.Errorf("a message in the write-ahead log was not read: %+v", all[2].Turns)
	}
}

func TestReadConductorErrors(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("no sqlite3")
	}
	dir := t.TempDir()
	if _, err := ReadConductor(context.Background(), filepath.Join(dir, "none.db")); err == nil || !strings.Contains(err.Error(), "no Conductor database") {
		t.Errorf("missing database: %v", err)
	}
	notDB := filepath.Join(dir, "conductor.db")
	os.WriteFile(notDB, []byte("this is not a database"), 0o600)
	if _, err := ReadConductor(context.Background(), notDB); err == nil {
		t.Error("a file that is not a database was read")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	conductorFixture(t, filepath.Join(dir, "ok.db"), "", dir, false)
	if _, err := ReadConductor(ctx, filepath.Join(dir, "ok.db")); err == nil {
		t.Error("a cancelled read succeeded")
	}
}

func TestConductorMessage(t *testing.T) {
	for _, c := range []struct {
		role, content, who, text string
		known                    bool
	}{
		{"user", "plain words", "user", "plain words", true},
		{"user", `{"type":"user","message":{"content":"kept as a message"}}`, "user", "kept as a message", true},
		{"assistant", `{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}]}}`, "assistant", "hi", true},
		{"assistant", `{"type":"result","subtype":"success"}`, "", "", true},
		{"assistant", `{"type":"item.completed","item":{"type":"agent_message","text":"done"}}`, "assistant", "done", true},
		{"assistant", `{"type":"item.completed","item":{"type":"command_execution"}}`, "", "", true},
		{"assistant", `{}`, "", "", false},
		{"assistant", `nope`, "", "", false},
		{"assistant", ``, "", "", false},
	} {
		who, text, known := conductorMessage(c.role, c.content)
		if who != c.who || text != c.text || known != c.known {
			t.Errorf("conductorMessage(%q, %q) = %q, %q, %v; want %q, %q, %v", c.role, c.content, who, text, known, c.who, c.text, c.known)
		}
	}
}

func TestConductorDB(t *testing.T) {
	if got, want := ConductorDB("/Users/me"), "/Users/me/Library/Application Support/com.conductor.app/conductor.db"; got != want {
		t.Errorf("ConductorDB = %q, want %q", got, want)
	}
}
