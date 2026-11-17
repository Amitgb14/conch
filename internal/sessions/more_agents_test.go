package sessions

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The four agents whose sessions conch learned to read after the first
// five: Copilot CLI, Cline, Qwen Code and Goose. Every fixture is in the
// shape the agent's own source writes (each store's comment says where).

func sessionsByKey(ss []Session) map[string]Session {
	got := map[string]Session{}
	for _, s := range ss {
		got[s.Agent+":"+s.ID] = s
	}
	return got
}

func TestCopilotSessions(t *testing.T) {
	home := t.TempDir()
	env := a6Env(home, nil)
	state := filepath.Join(home, ".copilot", "session-state")
	start := func(cwd string) string {
		return `{"id":"e0","timestamp":"2026-11-01T10:00:00Z","type":"session.start","data":{"sessionId":"SID","startTime":"2026-11-01T10:00:00Z","context":{"cwd":"` + cwd + `","branch":"main"}}}` + "\n"
	}
	// A session in the repo: a skill's hidden prompt comes first and is not
	// the title; what was typed is.
	write(t, filepath.Join(state, "a1", "events.jsonl"), strings.ReplaceAll(start("/src/api"), "SID", "a1")+
		`not json at all`+"\n"+
		`{"id":"e1","timestamp":"2026-11-01T10:00:01Z","type":"user.message","data":{"content":"<skill>pdf</skill>","source":"skill-pdf"}}`+"\n"+
		`{"id":"e2","timestamp":"2026-11-01T10:00:02Z","type":"user.message","data":{"content":"Fix the   flaky\nlogin test"}}`+"\n"+
		`{"id":"e3","timestamp":"2026-11-01T10:00:03Z","type":"assistant.message","data":{"content":"Looking","chunkIndex":0,"chunkCount":2}}`+"\n"+
		`{"id":"e4","timestamp":"2026-11-01T10:00:04Z","type":"assistant.message","data":{"content":"Fixed it","chunkIndex":1,"chunkCount":2}}`+"\n")
	// One in a subfolder, one in a sibling folder sharing the prefix, one
	// where nothing was typed, and one with no start at all.
	write(t, filepath.Join(state, "a2", "events.jsonl"), strings.ReplaceAll(start("/src/api/web"), "SID", "a2")+
		`{"type":"user.message","data":{"content":"Style the header"}}`+"\n")
	write(t, filepath.Join(state, "a3", "events.jsonl"), strings.ReplaceAll(start("/src/api-v2"), "SID", "a3")+
		`{"type":"user.message","data":{"content":"not this project"}}`+"\n")
	write(t, filepath.Join(state, "a4", "events.jsonl"), strings.ReplaceAll(start("/src/api"), "SID", "a4"))
	write(t, filepath.Join(state, "a5", "events.jsonl"), `{"type":"user.message","data":{"content":"where?"}}`+"\n")
	// A folder with no events file is not a session.
	os.MkdirAll(filepath.Join(state, "a6"), 0o755)

	got := sessionsByKey(List(env, []string{"/src/api"}, 0))
	s := got["copilot:a1"]
	if s.Title != "Fix the flaky login test" || s.Dir != "/src/api" || s.Branch != "main" || s.Started.IsZero() {
		t.Errorf("copilot a1: %+v", s)
	}
	if s := got["copilot:a2"]; s.Dir != "/src/api/web" || s.Title != "Style the header" {
		t.Errorf("copilot in a subfolder: %+v", s)
	}
	for _, id := range []string{"a3", "a4", "a5", "a6"} {
		if _, ok := got["copilot:"+id]; ok {
			t.Errorf("copilot %s was listed", id)
		}
	}

	turns, err := Transcript(context.Background(), env, got["copilot:a1"])
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 2 || turns[0].Role != "user" || turns[1].Text != "Looking\n\nFixed it" {
		t.Errorf("copilot transcript: %+v", turns)
	}

	// COPILOT_HOME moves the whole store.
	other := t.TempDir()
	write(t, filepath.Join(other, "session-state", "b1", "events.jsonl"), strings.ReplaceAll(start("/src/api"), "SID", "b1")+
		`{"type":"user.message","data":{"content":"elsewhere"}}`+"\n")
	if ids := a6IDs(List(a6Env(home, map[string]string{"COPILOT_HOME": other}), []string{"/src/api"}, 0)); ids != "copilot:b1" {
		t.Errorf("with COPILOT_HOME: %s", ids)
	}

	// A delete moves the session's folder, with everything in it.
	trash := t.TempDir()
	write(t, filepath.Join(state, "a1", "plan.md"), "the plan")
	if err := Delete(context.Background(), env, got["copilot:a1"], trash); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(state, "a1")); !os.IsNotExist(err) {
		t.Error("the session's folder is still there")
	}
	moved, _ := filepath.Glob(filepath.Join(trash, "copilot-*-a1", "plan.md"))
	if len(moved) != 1 {
		t.Errorf("the folder was not moved to the trash whole: %v", moved)
	}
	if _, ok := sessionsByKey(List(env, []string{"/src/api"}, 0))["copilot:a1"]; ok {
		t.Error("a deleted session is still listed")
	}
}

func TestQwenSessions(t *testing.T) {
	home := t.TempDir()
	env := a6Env(home, nil)
	projects := filepath.Join(home, ".qwen", "projects")
	rec := func(cwd, typ, sub, text string) string {
		s := `{"uuid":"u","sessionId":"q1","timestamp":"2026-11-02T10:00:00Z","type":"` + typ + `","cwd":"` + cwd + `","gitBranch":"main"`
		if sub != "" {
			s += `,"subtype":"` + sub + `"`
		}
		if text != "" {
			s += `,"message":{"role":"user","parts":[{"text":"` + text + `"},{"text":"thinking","thought":true},{"functionCall":{"name":"x"}}]}`
		}
		return s + "}\n"
	}
	write(t, filepath.Join(projects, "-src-api", "chats", "11111111-2222-3333-4444-555555555555.jsonl"),
		rec("/src/api", "user", "agent_message", "from another agent")+
			"{broken\n"+
			rec("/src/api", "user", "", "Explain the   auth flow")+
			strings.Replace(rec("/src/api", "assistant", "", "It goes"), `"role":"user"`, `"role":"model"`, 1)+
			rec("/src/api", "tool_result", "", "")+
			strings.Replace(rec("/src/api", "assistant", "", "like this"), `"role":"user"`, `"role":"model"`, 1))
	// A title given later wins over the first prompt.
	write(t, filepath.Join(projects, "-src-api", "chats", "t1.jsonl"),
		strings.ReplaceAll(rec("/src/api", "user", "", "first words"), `"q1"`, `"t1"`)+
			`{"sessionId":"t1","type":"system","subtype":"custom_title","systemPayload":{"customTitle":"Old name"}}`+"\n"+
			`{"sessionId":"t1","type":"system","subtype":"custom_title","systemPayload":{"customTitle":"Auth rewrite"}}`+"\n")
	// A subfolder's folder shares the prefix; a sibling's does too and is
	// told apart by its cwd. One where nothing was typed is left out, and
	// archived ones stay archived.
	write(t, filepath.Join(projects, "-src-api-web", "chats", "w1.jsonl"),
		strings.ReplaceAll(rec("/src/api/web", "user", "", "Style the header"), `"q1"`, `"w1"`))
	write(t, filepath.Join(projects, "-src-api-v2", "chats", "v1.jsonl"),
		strings.ReplaceAll(rec("/src/api-v2", "user", "", "not this project"), `"q1"`, `"v1"`))
	write(t, filepath.Join(projects, "-src-api", "chats", "e1.jsonl"),
		strings.ReplaceAll(rec("/src/api", "system", "ui_telemetry", ""), `"q1"`, `"e1"`))
	write(t, filepath.Join(projects, "-src-api", "chats", "archive", "old.jsonl"),
		strings.ReplaceAll(rec("/src/api", "user", "", "put away"), `"q1"`, `"old"`))

	got := sessionsByKey(List(env, []string{"/src/api"}, 0))
	if s := got["qwen:q1"]; s.Title != "Explain the auth flow" || s.Dir != "/src/api" || s.Branch != "main" {
		t.Errorf("qwen q1: %+v", s)
	}
	if s := got["qwen:t1"]; s.Title != "Auth rewrite" {
		t.Errorf("qwen's last custom title should win: %+v", s)
	}
	if s := got["qwen:w1"]; s.Dir != "/src/api/web" {
		t.Errorf("qwen in a subfolder: %+v", s)
	}
	for _, id := range []string{"v1", "e1", "old"} {
		if _, ok := got["qwen:"+id]; ok {
			t.Errorf("qwen %s was listed", id)
		}
	}

	turns, err := Transcript(context.Background(), env, got["qwen:q1"])
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 2 || turns[0].Text != "Explain the   auth flow" || turns[1].Text != "It goes\n\nlike this" {
		t.Errorf("qwen transcript: %+v", turns)
	}

	// QWEN_HOME moves it, and QWEN_RUNTIME_DIR moves it further.
	for _, v := range []string{"QWEN_HOME", "QWEN_RUNTIME_DIR"} {
		other := t.TempDir()
		write(t, filepath.Join(other, "projects", "-src-api", "chats", "o1.jsonl"),
			strings.ReplaceAll(rec("/src/api", "user", "", "elsewhere"), `"q1"`, `"o1"`))
		if ids := a6IDs(List(a6Env(home, map[string]string{v: other}), []string{"/src/api"}, 0)); ids != "qwen:o1" {
			t.Errorf("with %s: %s", v, ids)
		}
	}

	// A delete moves the file.
	trash := t.TempDir()
	if err := Delete(context.Background(), env, got["qwen:q1"], trash); err != nil {
		t.Fatal(err)
	}
	if _, ok := sessionsByKey(List(env, []string{"/src/api"}, 0))["qwen:q1"]; ok {
		t.Error("a deleted session is still listed")
	}
}

// A long session's title is appended at its end, past the lines read
// from the start.
func TestQwenTitleInTheTail(t *testing.T) {
	home := t.TempDir()
	var b strings.Builder
	b.WriteString(`{"sessionId":"big","timestamp":"2026-11-02T10:00:00Z","type":"user","cwd":"/src/api","message":{"parts":[{"text":"start"}]}}` + "\n")
	pad := strings.Repeat("x", 1000)
	for i := 0; i < 600; i++ {
		b.WriteString(`{"sessionId":"big","type":"assistant","cwd":"/src/api","message":{"parts":[{"text":"` + pad + `"}]}}` + "\n")
	}
	b.WriteString(`{"sessionId":"big","type":"system","subtype":"custom_title","systemPayload":{"customTitle":"Named late"}}` + "\n")
	write(t, filepath.Join(home, ".qwen", "projects", "-src-api", "chats", "big.jsonl"), b.String())
	if s := sessionsByKey(List(a6Env(home, nil), []string{"/src/api"}, 0))["qwen:big"]; s.Title != "Named late" {
		t.Errorf("title from the tail: %+v", s)
	}
}

func TestClineSessions(t *testing.T) {
	home := t.TempDir()
	env := a6Env(home, nil)
	sessions := filepath.Join(home, ".cline", "data", "sessions")
	manifest := func(id, cwd, source, prompt, title string) string {
		s := `{"version":1,"session_id":"` + id + `","source":"` + source + `","pid":1,"started_at":"2026-11-03T10:00:00.000Z","status":"completed","interactive":true,"provider":"p","model":"m","cwd":"` + cwd + `","workspace_root":"` + cwd + `","enable_tools":true,"enable_spawn":false,"enable_teams":false,"prompt":"` + prompt + `"`
		if title != "" {
			s += `,"metadata":{"title":"` + title + `"}`
		}
		return s + "}"
	}
	write(t, filepath.Join(sessions, "1_aaaaa", "1_aaaaa.json"), manifest("1_aaaaa", "/src/api", "cli", "Fix the login test", "Login test"))
	write(t, filepath.Join(sessions, "1_aaaaa", "1_aaaaa.messages.json"),
		`{"version":1,"messages":[{"role":"user","content":"Fix the login test"},{"role":"assistant","content":[{"type":"text","text":"Done"},{"type":"tool_use","name":"x"}]}]}`)
	write(t, filepath.Join(sessions, "1_aaaaa", "1_aaaaa.compaction.json"), `{}`)
	// The title falls back to the prompt; a subagent's session, a sibling
	// folder's and one where nothing was said are left out; a broken
	// manifest is skipped.
	write(t, filepath.Join(sessions, "2_bbbbb", "2_bbbbb.json"), manifest("2_bbbbb", "/src/api/web", "cli", "Style the header", ""))
	write(t, filepath.Join(sessions, "3_ccccc", "3_ccccc.json"), manifest("3_ccccc", "/src/api", "subagent", "helper", ""))
	write(t, filepath.Join(sessions, "4_ddddd", "4_ddddd.json"), manifest("4_ddddd", "/src/api-v2", "cli", "elsewhere", ""))
	write(t, filepath.Join(sessions, "5_eeeee", "5_eeeee.json"), manifest("5_eeeee", "/src/api", "cli", "", ""))
	write(t, filepath.Join(sessions, "6_fffff", "6_fffff.json"), `{"session_id":`)
	// The older messages file is the list alone.
	write(t, filepath.Join(sessions, "2_bbbbb", "2_bbbbb.messages.json"), `[{"role":"user","content":"Style the header"},{"role":"assistant","content":"Styled"}]`)
	// Written to as the conversation goes: the newer file is when it was used.
	old := time.Now().Add(-48 * time.Hour)
	os.Chtimes(filepath.Join(sessions, "1_aaaaa", "1_aaaaa.json"), old, old)

	got := sessionsByKey(List(env, []string{"/src/api"}, 0))
	s := got["cline:1_aaaaa"]
	if s.Title != "Login test" || s.Dir != "/src/api" || s.Started.IsZero() || time.Since(s.Updated) > time.Hour {
		t.Errorf("cline 1: %+v", s)
	}
	if s := got["cline:2_bbbbb"]; s.Title != "Style the header" {
		t.Errorf("cline title from the prompt: %+v", s)
	}
	for _, id := range []string{"3_ccccc", "4_ddddd", "5_eeeee", "6_fffff"} {
		if _, ok := got["cline:"+id]; ok {
			t.Errorf("cline %s was listed", id)
		}
	}
	for id, want := range map[string]string{"1_aaaaa": "Done", "2_bbbbb": "Styled"} {
		turns, err := Transcript(context.Background(), env, got["cline:"+id])
		if err != nil || len(turns) != 2 || turns[1].Text != want {
			t.Errorf("cline %s transcript: %+v %v", id, turns, err)
		}
	}
	// A session that has said nothing yet has no conversation file.
	os.Remove(filepath.Join(sessions, "2_bbbbb", "2_bbbbb.messages.json"))
	if turns, err := Transcript(context.Background(), env, got["cline:2_bbbbb"]); err != nil || len(turns) != 0 {
		t.Errorf("no messages yet: %+v %v", turns, err)
	}

	// The environment moves it, most specific first.
	for v, dir := range map[string]string{"CLINE_DIR": "data/sessions", "CLINE_DATA_DIR": "sessions", "CLINE_SESSION_DATA_DIR": ""} {
		other := t.TempDir()
		write(t, filepath.Join(other, dir, "9_zzzzz", "9_zzzzz.json"), manifest("9_zzzzz", "/src/api", "cli", "moved", ""))
		if ids := a6IDs(List(a6Env(home, map[string]string{v: other}), []string{"/src/api"}, 0)); ids != "cline:9_zzzzz" {
			t.Errorf("with %s: %s", v, ids)
		}
	}
}

// Cline's own command deletes a session, since it also indexes it in a
// database and keeps checkpoints as git refs; without it conch says so.
func TestClineDelete(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	work := t.TempDir()
	s := Session{Agent: "cline", ID: "1_aaaaa", Dir: work, Path: filepath.Join(home, "1_aaaaa.json")}

	if err := Delete(ctx, a6Env(home, map[string]string{"PATH": t.TempDir()}), s, t.TempDir()); err == nil || !strings.Contains(err.Error(), "needs the cline command") {
		t.Fatalf("no cline: %v", err)
	}
	log := filepath.Join(t.TempDir(), "log")
	bin := a6Tool(t, "cline", `{ /bin/pwd; echo "$@"; } > `+log+"\n")
	if err := Delete(ctx, a6Env(home, map[string]string{"PATH": bin}), s, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(log)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 2 || lines[1] != "history delete --session-id 1_aaaaa" {
		t.Fatalf("cline call: %q", lines)
	}
	realWork, _ := filepath.EvalSymlinks(work)
	if got, _ := filepath.EvalSymlinks(lines[0]); got != realWork {
		t.Errorf("cline ran in %q, not the session's folder", lines[0])
	}
	bin = a6Tool(t, "cline", "echo 'session not found' >&2; exit 1\n")
	if err := Delete(ctx, a6Env(home, map[string]string{"PATH": bin}), s, t.TempDir()); err == nil || !strings.Contains(err.Error(), "session not found") {
		t.Fatalf("cline fails: %v", err)
	}
}

// A Cline session whose folder has since gone is still deleted — cline is
// run from home rather than failing to start in a folder that is not
// there — and cline is given the store's own Cline folders, not this
// process's, so it deletes where the list found the session.
func TestClineDeleteGoneFolderAndEnv(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	clineDir := filepath.Join(t.TempDir(), "cline-elsewhere")
	// This process's own: neither may reach cline.
	t.Setenv("CLINE_DATA_DIR", "/process/data")
	t.Setenv("CLINE_DIR", "/process/cline")
	gone := filepath.Join(t.TempDir(), "removed")
	s := Session{Agent: "cline", ID: "1_bbbbb", Dir: gone, Path: filepath.Join(home, "1_bbbbb.json")}
	log := filepath.Join(t.TempDir(), "log")
	bin := a6Tool(t, "cline", `{ /bin/pwd; echo "home=$HOME dir=${CLINE_DIR-unset} data=${CLINE_DATA_DIR-unset} sess=${CLINE_SESSION_DATA_DIR-unset}"; } > `+log+"\n")
	env := a6Env(home, map[string]string{"PATH": bin, "CLINE_DIR": clineDir})
	if err := Delete(ctx, env, s, t.TempDir()); err != nil {
		t.Fatalf("a session whose folder is gone: %v", err)
	}
	b, _ := os.ReadFile(log)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 2 {
		t.Fatalf("cline call: %q", lines)
	}
	realHome, _ := filepath.EvalSymlinks(home)
	if got, _ := filepath.EvalSymlinks(lines[0]); got != realHome {
		t.Errorf("cline ran in %q, not home", lines[0])
	}
	if want := "home=" + home + " dir=" + clineDir + " data=unset sess=unset"; lines[1] != want {
		t.Errorf("cline's environment: %q, want %q", lines[1], want)
	}
	// No folder named at all: home too.
	s.Dir = ""
	if err := Delete(ctx, env, s, t.TempDir()); err != nil {
		t.Fatalf("no folder: %v", err)
	}
}

// gooseFixture is a Goose database with the tables its delete touches.
func gooseFixture(t *testing.T, db string, newer bool) {
	t.Helper()
	bin, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("no sqlite3")
	}
	os.MkdirAll(filepath.Dir(db), 0o755)
	now := time.Now().UTC()
	ts := func(d time.Duration) string { return now.Add(-d).Format("2006-01-02 15:04:05") }
	cols := "id TEXT PRIMARY KEY, name TEXT NOT NULL DEFAULT '', session_type TEXT NOT NULL DEFAULT 'user', working_dir TEXT NOT NULL, created_at TIMESTAMP, updated_at TIMESTAMP"
	extra := ""
	if newer {
		cols += ", accumulated_cost REAL, archived_at TIMESTAMP"
		extra = `CREATE TABLE usage_ledger (id INTEGER PRIMARY KEY, session_id TEXT);
CREATE TABLE session_tags (session_id TEXT, key TEXT, value TEXT);
INSERT INTO usage_ledger (session_id) VALUES ('20261101_1');
INSERT INTO session_tags VALUES ('20261101_1', 'k', 'v');`
	}
	sql := `CREATE TABLE sessions (` + cols + `);
CREATE TABLE messages (id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT NOT NULL, role TEXT NOT NULL, content_json TEXT NOT NULL, created_timestamp INTEGER NOT NULL);
INSERT INTO sessions (id, name, session_type, working_dir, created_at, updated_at) VALUES
 ('20261101_1', 'Fix the build', 'user', '/src/it''s', '` + ts(3*time.Hour) + `', '` + ts(time.Hour) + `'),
 ('20261101_2', '', 'scheduled', '/src/it''s/sub', '` + ts(3*time.Hour) + `', '` + ts(2*time.Hour) + `'),
 ('20261101_3', 'helper', 'sub_agent', '/src/it''s', '` + ts(time.Hour) + `', '` + ts(time.Hour) + `'),
 ('20261101_4', 'sibling', 'user', '/src/it''s2', '` + ts(time.Hour) + `', '` + ts(time.Hour) + `'),
 ('20261101_5', '', 'user', '/src/it''s', '` + ts(time.Hour) + `', '` + ts(time.Hour) + `');
INSERT INTO messages (session_id, role, content_json, created_timestamp) VALUES
 ('20261101_1', 'user', '[{"type":"text","text":"Fix it"}]', 1),
 ('20261101_1', 'assistant', '[{"type":"text","text":"On it"},{"type":"toolRequest","id":"t"}]', 2),
 ('20261101_1', 'user', '[{"type":"toolResponse","id":"t"}]', 3),
 ('20261101_1', 'assistant', '[{"type":"text","text":"Fixed"}]', 4),
 ('20261101_2', 'user', '[{"type":"text","text":"Nightly   check"}]', 1);
` + extra
	if out, err := exec.Command(bin, db, sql).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
}

func TestGooseSessions(t *testing.T) {
	for _, newer := range []bool{true, false} {
		home := t.TempDir()
		env := a6Env(home, nil)
		db := filepath.Join(home, ".local", "share", "goose", "sessions", "sessions.db")
		gooseFixture(t, db, newer)

		list := List(env, []string{"/src/it's"}, 0)
		// Newest first: the user's and the scheduled one; not the
		// subagent's, the sibling folder's, or the one where nothing was
		// said.
		if ids := a6IDs(list); ids != "goose:20261101_1,goose:20261101_2" {
			t.Fatalf("newer schema %v: %s", newer, ids)
		}
		if list[0].Title != "Fix the build" || list[1].Title != "Nightly check" || list[1].Dir != "/src/it's/sub" {
			t.Errorf("titles: %+v", list)
		}
		if list[0].Started.IsZero() || !list[0].Updated.After(list[0].Started) {
			t.Errorf("times: %+v", list[0])
		}
		turns, err := Transcript(context.Background(), env, list[0])
		if err != nil {
			t.Fatal(err)
		}
		if len(turns) != 2 || turns[0].Text != "Fix it" || turns[1].Text != "On it\n\nFixed" {
			t.Errorf("goose transcript: %+v", turns)
		}

		// A delete takes the session's rows and nobody else's.
		if err := Delete(context.Background(), env, list[0], t.TempDir()); err != nil {
			t.Fatal(err)
		}
		if ids := a6IDs(List(env, []string{"/src/it's"}, 0)); ids != "goose:20261101_2" {
			t.Errorf("after the delete: %s", ids)
		}
		count := func(sql string) string {
			out, _ := exec.Command("sqlite3", db, sql).Output()
			return strings.TrimSpace(string(out))
		}
		if n := count("SELECT count(*) FROM messages WHERE session_id = '20261101_1'"); n != "0" {
			t.Errorf("%s of its messages are left", n)
		}
		if n := count("SELECT count(*) FROM messages"); n != "1" {
			t.Errorf("another session's messages went too: %s left", n)
		}
		if newer {
			if n := count("SELECT (SELECT count(*) FROM usage_ledger) + (SELECT count(*) FROM session_tags)"); n != "0" {
				t.Errorf("its ledger and tags are left: %s", n)
			}
		}
		if _, err := os.Stat(db); err != nil {
			t.Error("the database itself must not be moved")
		}
	}
}

func TestGooseWhereAndWithout(t *testing.T) {
	// GOOSE_PATH_ROOT moves it, when it is absolute; XDG_DATA_HOME too.
	if got := gooseDB(a6Env("/h", map[string]string{"GOOSE_PATH_ROOT": "/g"})); got != "/g/data/sessions/sessions.db" {
		t.Errorf("GOOSE_PATH_ROOT: %s", got)
	}
	if got := gooseDB(a6Env("/h", map[string]string{"GOOSE_PATH_ROOT": "relative"})); got != "/h/.local/share/goose/sessions/sessions.db" {
		t.Errorf("a relative GOOSE_PATH_ROOT: %s", got)
	}
	if got := gooseDB(a6Env("/h", map[string]string{"XDG_DATA_HOME": "/x"})); got != "/x/goose/sessions/sessions.db" {
		t.Errorf("XDG_DATA_HOME: %s", got)
	}
	// No database, and no sqlite3: nothing listed, a delete that says why.
	home := t.TempDir()
	if ss := goose(a6Env(home, nil), []string{"/src"}); len(ss) != 0 {
		t.Errorf("no database: %v", ss)
	}
	db := filepath.Join(home, "sessions.db")
	write(t, db, "")
	a6NoTools(t)
	if ss := goose(a6Env(home, map[string]string{"GOOSE_PATH_ROOT": home}), []string{"/src"}); len(ss) != 0 {
		t.Errorf("no sqlite3: %v", ss)
	}
	if err := Delete(context.Background(), a6Env(home, nil), Session{Agent: "goose", ID: "x", Path: db}, t.TempDir()); err == nil || !strings.Contains(err.Error(), "needs the sqlite3 command") {
		t.Errorf("delete without sqlite3: %v", err)
	}
	if _, err := Transcript(context.Background(), a6Env(home, nil), Session{Agent: "goose", ID: "x", Path: db}); err == nil {
		t.Error("a transcript without sqlite3 should say so")
	}
}

func TestGooseTime(t *testing.T) {
	want := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	for _, s := range []string{"2026-11-01 10:00:00", "2026-11-01T10:00:00Z", "2026-11-01T10:00:00.000+00:00", "2026-11-01 10:00:00.000+00:00"} {
		if got := gooseTime(s); !got.Equal(want) {
			t.Errorf("%q: %v", s, got)
		}
	}
	if !gooseTime("").IsZero() || !gooseTime("yesterday").IsZero() {
		t.Error("a time that is not one should be zero")
	}
}

func TestHandoffNamesTheNewAgents(t *testing.T) {
	for agent, want := range map[string]string{"copilot": "Copilot CLI", "cline": "Cline", "qwen": "Qwen Code", "goose": "Goose"} {
		if got := AgentName(agent); got != want {
			t.Errorf("%s is named %q", agent, got)
		}
	}
}

// A Cline session's manifest is written when it starts and ends, its
// conversation as it goes: a session still in use moves up the list even
// though the file conch parses has not changed since it last looked.
func TestClineUpdatedFollowsTheConversation(t *testing.T) {
	home := t.TempDir()
	env := a6Env(home, nil)
	dir := filepath.Join(home, ".cline", "data", "sessions", "1_aaaaa")
	write(t, filepath.Join(dir, "1_aaaaa.json"), `{"session_id":"1_aaaaa","source":"cli","cwd":"/src/api","prompt":"go","started_at":"2026-11-03T10:00:00Z"}`)
	write(t, filepath.Join(dir, "1_aaaaa.messages.json"), `{"messages":[]}`)
	then := time.Now().Add(-3 * time.Hour)
	for _, f := range []string{"1_aaaaa.json", "1_aaaaa.messages.json"} {
		os.Chtimes(filepath.Join(dir, f), then, then)
	}
	first := List(env, []string{"/src/api"}, 0)
	if len(first) != 1 || first[0].Updated.After(then.Add(time.Second)) {
		t.Fatalf("first look: %+v", first)
	}
	now := time.Now()
	os.Chtimes(filepath.Join(dir, "1_aaaaa.messages.json"), now, now)
	if again := List(env, []string{"/src/api"}, 0); len(again) != 1 || again[0].Updated.Before(now.Add(-time.Second)) {
		t.Errorf("a conversation written to since is still dated %v", again[0].Updated)
	}
}
