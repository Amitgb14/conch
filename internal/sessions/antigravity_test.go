package sessions

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The fixtures are shaped like agy 1.5.16's own files on a Mac: the
// summary table's columns as it creates them, a transcript's lines as it
// writes them; the words are made up.

// agy keeps it in WAL mode, and with agy closed there is no -shm beside
// it, which a read-only sqlite3 cannot open without.
const agySchema = "PRAGMA journal_mode=WAL; CREATE TABLE `conversation_summaries` (`conversation_id` text,`title` text NOT NULL DEFAULT \"\",`preview` text NOT NULL DEFAULT \"\",`step_count` integer NOT NULL DEFAULT 0,`last_modified_time` datetime NOT NULL,`workspace_uris` text NOT NULL,`status` text NOT NULL DEFAULT \"\",`source` text NOT NULL DEFAULT \"\",`project_id` text NOT NULL DEFAULT \"\",`agent_name` text NOT NULL DEFAULT \"\",`parent_conversation_id` text NOT NULL DEFAULT \"\",`nesting_depth` integer NOT NULL DEFAULT 0,`battle_id` text NOT NULL DEFAULT \"\",`winning_conversation_id` text NOT NULL DEFAULT \"\",`not_fully_idle` numeric NOT NULL DEFAULT false,`killed` numeric NOT NULL DEFAULT false,`last_user_input_time` datetime NOT NULL,`last_user_input_step_index` integer NOT NULL DEFAULT -1,`app_data_dir` text NOT NULL DEFAULT \"\",`raw_summary` blob,`group_id` text NOT NULL DEFAULT \"\",PRIMARY KEY (`conversation_id`));" +
	"CREATE VIRTUAL TABLE conversations_fts USING fts5(conversation_id UNINDEXED, chunk_index UNINDEXED, title, user_body, agent_body);" +
	"CREATE TABLE conversation_parents (conversation_id TEXT PRIMARY KEY, parent_id TEXT NOT NULL) WITHOUT ROWID;"

func agySQL(t *testing.T, db, sql string) {
	t.Helper()
	bin, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("no sqlite3")
	}
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(bin, db, sql+"PRAGMA wal_checkpoint(TRUNCATE);").CombinedOutput(); err != nil {
		t.Fatalf("sqlite3: %v: %s", err, out)
	}
	// As agy leaves it once closed: everything in the file, no log or
	// shared memory beside it (macOS's sqlite3 keeps both).
	for _, x := range []string{"-wal", "-shm"} {
		if err := os.Remove(db + x); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
}

func agyRow(id, title, preview, modified, uris, parent string) string {
	return "INSERT INTO conversation_summaries (conversation_id, title, preview, step_count, last_modified_time, workspace_uris, last_user_input_time, parent_conversation_id, nesting_depth, app_data_dir) VALUES (" +
		sqlQuote(id) + "," + sqlQuote(title) + "," + sqlQuote(preview) + ",2," + sqlQuote(modified) + "," + sqlQuote(uris) + "," + sqlQuote(modified) + "," + sqlQuote(parent) + "," +
		map[bool]string{true: "1", false: "0"}[parent != ""] + ",'antigravity-cli');" +
		"INSERT INTO conversations_fts (conversation_id, chunk_index, title, user_body, agent_body) VALUES (" + sqlQuote(id) + ",0,'t','u','a');"
}

// agyTranscript is a transcript as agy writes it: the person's words in
// <USER_REQUEST> among metadata of its own, a tool step between answers.
var agyTranscript = strings.Join([]string{
	`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-11-24T19:30:27Z","content":"<USER_REQUEST>\nRename the flag\n</USER_REQUEST>\n<ADDITIONAL_METADATA>\nThe current local time is: 2026-11-24T11:30:27-08:00.\n</ADDITIONAL_METADATA>"}`,
	`{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-11-24T19:30:28Z","model":"m","input_tokens":900,"cache_read_tokens":0,"output_tokens":28,"content":"Looking."}`,
	`{"step_index":2,"source":"MODEL","type":"RUN_COMMAND","status":"DONE","created_at":"2026-11-24T19:30:29Z","content":"grep -r flag"}`,
	`not json`,
	`{"step_index":3,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-11-24T19:30:30Z","model":"m","output_tokens":41,"content":"Renamed."}`,
}, "\n") + "\n"

func TestAntigravitySessions(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("no sqlite3")
	}
	home := t.TempDir()
	root := filepath.Join(home, ".gemini", "antigravity-cli")
	db := filepath.Join(root, "conversation_summaries.db")
	now := time.Now().UTC()
	at := func(d time.Duration) string { return now.Add(-d).Format("2006-01-02 15:04:05.999999-07:00") }
	agySQL(t, db, agySchema+
		agyRow("a1", "", "Rename   the flag", at(time.Hour), `["file:///w/proj"]`, "")+
		agyRow("a2", "Fix the parser", "fix it", at(2*time.Hour), `["file:///other","file:///w/proj/sub"]`, "")+
		// A subagent's conversation belongs to its parent's.
		agyRow("a3", "sub", "sub", at(time.Hour), `["file:///w/proj"]`, "a1")+
		// Elsewhere, no folder, a folder that is no file URI, and too old.
		agyRow("a4", "x", "x", at(time.Hour), `["file:///x"]`, "")+
		agyRow("a5", "x", "x", at(time.Hour), `[]`, "")+
		agyRow("a6", "x", "x", at(time.Hour), `not json`, "")+
		agyRow("a7", "x", "x", at(200*24*time.Hour), `["file:///w/proj"]`, "")+
		// A folder with a space, as a URI escapes it; no title and no preview.
		agyRow("a8", "", "", at(3*time.Hour), `["file:///w/proj/my%20dir"]`, "")+
		"INSERT INTO conversation_parents VALUES ('a3', 'a1');")
	write(t, filepath.Join(root, "brain", "a1", ".system_generated", "logs", "transcript.jsonl"), agyTranscript)
	write(t, filepath.Join(root, "conversations", "a1.db"), "state")
	write(t, filepath.Join(root, "conversations", "a1.db-wal"), "log")
	// What `agy --continue` reopens in each folder.
	last := filepath.Join(root, "cache", "last_conversations.json")
	write(t, last, `{"/w/proj": "a1", "/w/proj/sub": "a2"}`)

	env := a6Env(home, nil)
	got := List(env, []string{"/w/proj"}, 0)
	if a6IDs(got) != "antigravity:a1,antigravity:a2,antigravity:a8" {
		t.Fatalf("%s %+v", a6IDs(got), got)
	}
	if got[0].Output != 28+41 || got[1].Output != 0 {
		t.Errorf("output tokens: %d %d", got[0].Output, got[1].Output)
	}
	if got[0].Title != "Rename the flag" || got[0].Dir != "/w/proj" || got[0].Path != db {
		t.Errorf("preview is the title when there is none: %+v", got[0])
	}
	if got[1].Title != "Fix the parser" || got[1].Dir != "/w/proj/sub" {
		t.Errorf("the folder under the project is the one shown: %+v", got[1])
	}
	if got[2].Dir != "/w/proj/my dir" || got[2].Title != "" {
		t.Errorf("escaped folder: %+v", got[2])
	}
	if d := got[0].Updated.Sub(now.Add(-time.Hour)); d < -time.Second || d > time.Second {
		t.Errorf("updated %v, want %v", got[0].Updated, now.Add(-time.Hour))
	}
	if !Reads("antigravity") {
		t.Error("antigravity's sessions are read")
	}

	turns, err := Transcript(context.Background(), env, got[0])
	if err != nil || turnsText(turns) != "user: Rename the flag\nassistant: Looking.\n\nRenamed." {
		t.Fatalf("%q %v", turnsText(turns), err)
	}
	if turns[0].Time.IsZero() {
		t.Error("a turn keeps its time")
	}
	// A conversation whose transcript is gone has none to show.
	if _, err := Transcript(context.Background(), env, got[1]); err == nil {
		t.Error("a2 has no transcript")
	}

	trash := t.TempDir()
	if err := Delete(context.Background(), env, got[0], trash); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"antigravity-*-a1", "antigravity-*-a1.db", "antigravity-*-a1.db-wal"} {
		if moved, _ := filepath.Glob(filepath.Join(trash, p)); len(moved) != 1 {
			t.Errorf("%s not in the trash: %v", p, moved)
		}
	}
	if _, err := os.Stat(db); err != nil {
		t.Fatal("the summaries must not be moved")
	}
	if b, _ := os.ReadFile(last); strings.Contains(string(b), "a1") || !strings.Contains(string(b), `"/w/proj/sub": "a2"`) {
		t.Errorf("--continue still names it: %s", b)
	}
	if got := List(env, []string{"/w/proj"}, 0); a6IDs(got) != "antigravity:a2,antigravity:a8" {
		t.Fatalf("still listed: %s", a6IDs(got))
	}
	out, _ := exec.Command("sqlite3", db, "SELECT count(*) FROM conversations_fts WHERE conversation_id = 'a1'").Output()
	if strings.TrimSpace(string(out)) != "0" {
		t.Errorf("search rows left: %s", out)
	}
	// A conversation with no files of its own still deletes: its rows. A
	// cache agy cannot read is left as it is.
	write(t, last, "not json")
	if err := Delete(context.Background(), env, got[1], t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if got := List(env, []string{"/w/proj"}, 0); a6IDs(got) != "antigravity:a8" {
		t.Fatalf("a2 still listed: %s", a6IDs(got))
	}
	if b, _ := os.ReadFile(last); string(b) != "not json" {
		t.Errorf("rewrote a cache it could not read: %s", b)
	}
}

func TestAntigravityNoStore(t *testing.T) {
	home := t.TempDir()
	env := a6Env(home, nil)
	// No folder at all.
	if got := antigravity(env, []string{"/w/proj"}); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
	// A database that is not one, and one with no such table.
	root := filepath.Join(home, ".gemini", "antigravity-cli")
	write(t, filepath.Join(root, "conversation_summaries.db"), "not a database")
	if got := antigravity(env, []string{"/w/proj"}); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
	if _, err := exec.LookPath("sqlite3"); err == nil {
		other := filepath.Join(t.TempDir(), "empty.db")
		agySQL(t, other, "CREATE TABLE x (y);")
		b, _ := os.ReadFile(other)
		write(t, filepath.Join(root, "conversation_summaries.db"), string(b))
		if got := antigravity(env, []string{"/w/proj"}); len(got) != 0 {
			t.Fatalf("%+v", got)
		}
	}
	// No folders asked about.
	if got := antigravity(env, nil); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
	// Without sqlite3 nothing is listed, and a delete says what it needs
	// before it moves anything.
	a6NoTools(t)
	if got := antigravity(env, []string{"/w/proj"}); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
	brain := filepath.Join(root, "brain", "a1")
	write(t, filepath.Join(brain, "x"), "")
	s := Session{Agent: "antigravity", ID: "a1", Path: filepath.Join(root, "conversation_summaries.db")}
	if err := Delete(context.Background(), env, s, t.TempDir()); err == nil || !strings.Contains(err.Error(), "sqlite3") {
		t.Fatalf("no sqlite3: %v", err)
	}
	if _, err := os.Stat(brain); err != nil {
		t.Fatal("nothing is moved when the rows cannot be removed")
	}
}

func TestAntigravityLockedDB(t *testing.T) {
	bin, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("no sqlite3")
	}
	home := t.TempDir()
	db := filepath.Join(home, ".gemini", "antigravity-cli", "conversation_summaries.db")
	agySQL(t, db, agySchema+agyRow("a1", "t", "p", time.Now().UTC().Format("2006-01-02 15:04:05-07:00"), `["file:///w/proj"]`, ""))
	// agy holding a write lock: a delete fails and says so rather than
	// hanging, and leaves the files where they are.
	hold := exec.Command(bin, db)
	in, err := hold.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := hold.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := hold.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { in.Close(); _ = hold.Wait() }()
	if _, err := in.Write([]byte("BEGIN EXCLUSIVE; SELECT 'locked';\n")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64)
	if n, _ := out.Read(buf); !strings.Contains(string(buf[:n]), "locked") {
		t.Fatalf("lock not taken: %q", buf[:n])
	}
	brain := filepath.Join(home, ".gemini", "antigravity-cli", "brain", "a1")
	write(t, filepath.Join(brain, "x"), "")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err = Delete(ctx, a6Env(home, nil), Session{Agent: "antigravity", ID: "a1", Path: db}, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "locked") {
		t.Fatalf("a locked database: %v", err)
	}
	if _, err := os.Stat(brain); err != nil {
		t.Fatal("nothing is moved when the rows cannot be removed")
	}
}

func TestAntigravityOutputFollowsTheFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "transcript.jsonl")
	step := func(i, out int) string {
		return `{"step_index":` + strconv.Itoa(i) + `,"type":"PLANNER_RESPONSE","model":"m","output_tokens":` + strconv.Itoa(out) + `}` + "\n"
	}
	if n := antigravityOutput(f); n != 0 {
		t.Fatalf("no file: %d", n)
	}
	write(t, f, step(1, 5)+`{"step_index":2,"type":"RUN_COMMAND","output_tokens":99}`+"\n")
	if n := antigravityOutput(f); n != 5 {
		t.Fatalf("first read: %d", n)
	}
	fh, err := os.OpenFile(f, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	// The same step again, further along, and half of the next.
	line := step(3, 7)
	fh.WriteString(step(1, 6) + line[:10])
	if n := antigravityOutput(f); n != 6 {
		t.Fatalf("half a line counted, or a step twice: %d", n)
	}
	fh.WriteString(line[10:])
	fh.Close()
	if n := antigravityOutput(f); n != 13 {
		t.Fatalf("appended: %d", n)
	}
	// Rewritten shorter: read again from the start.
	write(t, f, step(1, 2))
	if n := antigravityOutput(f); n != 2 {
		t.Fatalf("rewritten: %d", n)
	}
}

// An account of agy's (internal/accounts) is a folder given as
// --gemini_dir, with its own antigravity-cli inside: its conversations
// are listed as that account's, their chat read from that folder, and the
// same folder twice is read once.
func TestAntigravityAccounts(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("no sqlite3")
	}
	home := t.TempDir()
	work := filepath.Join(t.TempDir(), "work")
	now := time.Now().UTC().Add(-time.Hour).Format("2006-01-02 15:04:05.999999-07:00")
	agySQL(t, filepath.Join(home, ".gemini", "antigravity-cli", "conversation_summaries.db"), agySchema+agyRow("own1", "mine", "", now, `["file:///w/proj"]`, ""))
	agySQL(t, filepath.Join(work, "antigravity-cli", "conversation_summaries.db"), agySchema+agyRow("work1", "theirs", "", now, `["file:///w/proj"]`, ""))
	write(t, filepath.Join(work, "antigravity-cli", "brain", "work1", ".system_generated", "logs", "transcript.jsonl"), agyTranscript)

	env := a6Env(home, nil)
	env.Accounts = []Account{
		{Agent: "antigravity", Name: "work", Dir: work},
		{Agent: "antigravity", Name: "again", Dir: work},
		{Agent: "claude", Name: "other", Dir: t.TempDir()},
	}
	got := List(env, []string{"/w/proj"}, 0)
	if a6IDs(got) != "antigravity:own1,antigravity:work1" {
		t.Fatalf("%s", a6IDs(got))
	}
	var w Session
	for _, s := range got {
		if s.ID == "work1" {
			w = s
		} else if s.Account != "" {
			t.Errorf("own conversation on an account: %+v", s)
		}
	}
	if w.Account != "work" || w.Output != 28+41 {
		t.Errorf("%+v", w)
	}
	turns, err := Transcript(context.Background(), env, w)
	if err != nil || turnsText(turns) != "user: Rename the flag\nassistant: Looking.\n\nRenamed." {
		t.Fatalf("%q %v", turnsText(turns), err)
	}
	if up, err := ChatSince(context.Background(), env, w, "", 0); err != nil || len(up.Turns) == 0 {
		t.Fatalf("chat: %+v %v", up, err)
	}
	if err := Delete(context.Background(), env, w, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(work, "antigravity-cli", "brain", "work1")); !os.IsNotExist(err) {
		t.Errorf("the account's files stayed: %v", err)
	}
}
