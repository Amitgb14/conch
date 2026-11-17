package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/sessions"
)

// importFixture is a small Conductor database: a Claude conversation in
// a workspace that is still there, one with nothing said, a Codex one,
// and a Claude one whose workspace and repository are both gone.
func importFixture(t *testing.T) (db, ws string) {
	t.Helper()
	bin, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("no sqlite3")
	}
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("CONCH_HOME", filepath.Join(dir, "conch"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(dir, "claude"))
	t.Setenv("CODEX_HOME", filepath.Join(dir, "codex"))
	db = filepath.Join(dir, "conductor.db")
	ws = filepath.Join(dir, "it's here")
	os.MkdirAll(ws, 0o755)
	sql := `CREATE TABLE repos (id TEXT, name TEXT, root_path TEXT);
CREATE TABLE workspaces (id TEXT, repository_id TEXT, directory_name TEXT, branch TEXT, workspace_path TEXT);
CREATE TABLE sessions (id TEXT, created_at TEXT, updated_at TEXT, workspace_id TEXT, title TEXT, agent_type TEXT);
CREATE TABLE session_messages (id TEXT, session_id TEXT, role TEXT, content TEXT, created_at TEXT);
INSERT INTO repos VALUES ('r1', 'My Shop', '/gone');
INSERT INTO workspaces VALUES ('w1', 'r1', 'victoria', 'fix/build', '` + strings.ReplaceAll(ws, "'", "''") + `'),
 ('w2', 'r1', 'lost', 'lost', '/gone/lost');
INSERT INTO sessions VALUES ('0123456789abcdef', '2026-07-10 15:19:00', '2026-07-10 15:21:00', 'w1', 'Fix the build: now!', 'claude'),
 ('empty', '2026-07-11 15:19:00', '2026-07-11 15:21:00', 'w1', 'Nothing', 'claude'),
 ('codex1', '2026-07-12 15:19:00', '2026-07-12 15:21:00', 'w1', 'Rename it', 'codex'),
 ('lost1', '2026-07-13 15:19:00', '2026-07-13 15:21:00', 'w2', 'Nowhere to go', 'claude');
INSERT INTO session_messages VALUES
 ('m1', '0123456789abcdef', 'user', 'Why does the build fail?', '2026-07-10 15:19:01'),
 ('m2', '0123456789abcdef', 'assistant', '{"type":"assistant","uuid":"u2","message":{"id":"msg_1","role":"assistant","content":[{"type":"text","text":"A missing header; fixed."}]}}', '2026-07-10 15:19:05'),
 ('m3', 'codex1', 'user', 'Rename the button', '2026-07-12 15:19:01'),
 ('m4', 'codex1', 'assistant', '{"type":"item.completed","item":{"type":"agent_message","text":"Renamed."}}', '2026-07-12 15:19:02'),
 ('m5', 'lost1', 'user', 'Where am I?', '2026-07-13 15:19:01');`
	if out, err := exec.Command(bin, db, sql).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3: %v: %s", err, out)
	}
	return db, ws
}

// importSessions are the Claude sessions an import wrote.
func importSessions(t *testing.T) []string {
	t.Helper()
	m, _ := filepath.Glob(filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "projects", "*", "*.jsonl"))
	return m
}

func TestImportConductor(t *testing.T) {
	db, ws := importFixture(t)
	out := filepath.Join(t.TempDir(), "out")
	var err error
	stdout, _ := a4Capture(t, "", func() { err = runImport([]string{"conductor", "-db", db, "-out", out}) })
	if err != nil {
		t.Fatal(err)
	}
	// The Claude conversation resumes in Claude Code, as a session of its own.
	sess := importSessions(t)
	if len(sess) != 1 || !strings.HasPrefix(sess[0], filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "projects", claudeFolder(ws))) {
		t.Fatalf("sessions written: %v\n%s", sess, stdout)
	}
	id := strings.TrimSuffix(filepath.Base(sess[0]), ".jsonl")
	body, _ := os.ReadFile(sess[0])
	if !strings.Contains(string(body), "Why does the build fail?") || !strings.Contains(string(body), `"aiTitle":"Fix the build: now!"`) {
		t.Errorf("the session:\n%s", body)
	}
	// No Markdown for it unless asked; the others, which cannot resume,
	// come over as Markdown and say why.
	if _, err := os.Stat(filepath.Join(out, "my-shop", "fix-the-build-now-01234567.md")); err == nil {
		t.Error("a resumable conversation got a Markdown copy nobody asked for")
	}
	for _, f := range []string{"rename-it-codex1.md", "nowhere-to-go-lost1.md"} {
		if _, err := os.Stat(filepath.Join(out, "my-shop", f)); err != nil {
			t.Errorf("%s was not written as Markdown: %v", f, err)
		}
	}
	for _, want := range []string{
		"imported 3 of Conductor's conversations",
		"Fix the build: now!  (Claude Code, 2 messages, My Shop · fix/build)",
		"resumes in Claude Code: conch new -agent claude -cwd " + shellWord(ws) + " -- --resume " + id,
		"Rename it  (Codex", "as Markdown: Codex conversations come over as Markdown for now",
		"Nowhere to go", "as Markdown: Claude Code cannot resume it: no folder to resume it in",
		"conch new -agent codex -cwd " + shellWord(ws) + " -- 'Read ",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output lacks %q:\n%s", want, stdout)
		}
	}
	st, serr := sessions.LoadImportState(filepath.Join(os.Getenv("CONCH_HOME"), "imports", "conductor", "state.json"))
	if serr != nil || len(st) != 1 || st["0123456789abcdef"].AgentSessionID != id || st["0123456789abcdef"].LastMessageID != "m2" {
		t.Errorf("state: %+v %v", st, serr)
	}

	// Again: nothing new, nothing written, and said so.
	stdout, _ = a4Capture(t, "", func() { err = runImport([]string{"conductor", "-db", db, "-out", out}) })
	if err != nil || len(importSessions(t)) != 1 || !strings.Contains(stdout, "already imported, nothing new") {
		t.Errorf("importing twice: %v %v\n%s", err, importSessions(t), stdout)
	}
	if m, _ := filepath.Glob(filepath.Join(out, "my-shop", "*.md")); len(m) != 2 {
		t.Errorf("Markdown files after importing twice: %v", m)
	}

	// Carried on in conch, and a new message in Conductor: left alone.
	f, _ := os.OpenFile(sess[0], os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("{}\n")
	f.Close()
	sqlite(t, db, `INSERT INTO session_messages VALUES ('m9', '0123456789abcdef', 'user', 'More?', '2026-07-10 15:30:00')`)
	stdout, _ = a4Capture(t, "", func() { err = runImport([]string{"conductor", "-db", db, "-out", out}) })
	if err != nil || !strings.Contains(stdout, "left alone: carried on in conch since it was imported") {
		t.Errorf("carried on: %v\n%s", err, stdout)
	}
}

// claudeFolder is Claude's project folder name for an ASCII path.
func claudeFolder(dir string) string {
	b := []byte(dir)
	for i, c := range b {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			b[i] = '-'
		}
	}
	return string(b)
}

func sqlite(t *testing.T, db, sql string) {
	t.Helper()
	if out, err := exec.Command("sqlite3", db, sql).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3: %v: %s", err, out)
	}
}

func TestImportConductorNewMessages(t *testing.T) {
	db, _ := importFixture(t)
	out := filepath.Join(t.TempDir(), "out")
	var err error
	a4Capture(t, "", func() { err = runImport([]string{"conductor", "-db", db, "-out", out}) })
	if err != nil {
		t.Fatal(err)
	}
	sqlite(t, db, `INSERT INTO session_messages VALUES ('m9', '0123456789abcdef', 'user', 'And the tests?', '2026-07-10 15:30:00')`)
	stdout, _ := a4Capture(t, "", func() { err = runImport([]string{"conductor", "-db", db, "-out", out}) })
	if err != nil || !strings.Contains(stdout, "added 1 new message") {
		t.Errorf("a new message: %v\n%s", err, stdout)
	}
	sess := importSessions(t)
	body, _ := os.ReadFile(sess[0])
	if len(sess) != 1 || !strings.Contains(string(body), "And the tests?") {
		t.Errorf("the new message was not added to the session: %v", sess)
	}
}

// -markdown keeps today's handoff beside the session; -resumable=false is
// today's handoff alone.
func TestImportConductorMarkdown(t *testing.T) {
	db, _ := importFixture(t)
	out := filepath.Join(t.TempDir(), "out")
	var err error
	stdout, _ := a4Capture(t, "", func() { err = runImport([]string{"conductor", "-db", db, "-out", out, "-markdown"}) })
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(out, "my-shop", "fix-the-build-now-01234567.md")
	doc, rerr := os.ReadFile(file)
	if rerr != nil || len(importSessions(t)) != 1 {
		t.Fatalf("-markdown: %v, sessions %v\n%s", rerr, importSessions(t), stdout)
	}
	if !strings.Contains(stdout, "and as Markdown: "+file) {
		t.Errorf("-markdown output:\n%s", stdout)
	}

	out2 := filepath.Join(t.TempDir(), "out")
	stdout, _ = a4Capture(t, "", func() { err = runImport([]string{"conductor", "-db", db, "-out", out2, "-resumable=false"}) })
	if err != nil {
		t.Fatal(err)
	}
	if len(importSessions(t)) != 1 {
		t.Error("-resumable=false wrote a session")
	}
	doc, rerr = os.ReadFile(filepath.Join(out2, "my-shop", "fix-the-build-now-01234567.md"))
	if rerr != nil {
		t.Fatalf("-resumable=false: %v\n%s", rerr, stdout)
	}
	for _, want := range []string{"Imported from Conductor", "# Handoff: Fix the build: now!", "Why does the build fail?", "A missing header; fixed.", "`fix/build`"} {
		if !strings.Contains(string(doc), want) {
			t.Errorf("the document lacks %q:\n%s", want, doc)
		}
	}
	if st, _ := os.Stat(filepath.Join(out2, "my-shop", "fix-the-build-now-01234567.md")); st.Mode().Perm() != 0o600 {
		t.Errorf("the document is %v, want readable by its owner alone", st.Mode().Perm())
	}
	if m, _ := filepath.Glob(filepath.Join(out2, "*", "nothing-*")); len(m) > 0 {
		t.Errorf("a conversation with nothing said was written: %v", m)
	}
	if !strings.Contains(stdout, "imported 3 of Conductor's conversations") || strings.Contains(stdout, "resumes in Claude Code") {
		t.Errorf("-resumable=false output:\n%s", stdout)
	}
}

func TestImportConductorDryRunAndJSON(t *testing.T) {
	db, _ := importFixture(t)
	out := filepath.Join(t.TempDir(), "out")
	var err error
	stdout, _ := a4Capture(t, "", func() { err = runImport([]string{"conductor", "-db", db, "-out", out, "-n"}) })
	if err != nil || !strings.Contains(stdout, "would import 3") || !strings.Contains(stdout, "without -n imports them") ||
		!strings.Contains(stdout, "would resume in Claude Code") {
		t.Errorf("dry run: %v\n%s", err, stdout)
	}
	if _, serr := os.Stat(out); serr == nil {
		t.Error("a dry run wrote something")
	}
	if len(importSessions(t)) != 0 {
		t.Error("a dry run wrote a session")
	}
	if _, serr := os.Stat(filepath.Join(os.Getenv("CONCH_HOME"), "imports", "conductor", "state.json")); serr == nil {
		t.Error("a dry run wrote the state")
	}

	stdout, _ = a4Capture(t, "", func() { err = runImport([]string{"conductor", "-db", db, "-out", out, "-json"}) })
	var got []importedFile
	if err != nil || json.Unmarshal([]byte(stdout), &got) != nil || len(got) != 3 {
		t.Fatalf("-json: %v\n%s", err, stdout)
	}
	if got[0].Title != "Fix the build: now!" || got[0].Agent != "Claude Code" || got[0].Messages != 2 || got[0].Branch != "fix/build" ||
		got[0].Status != "written" || got[0].Session == "" || got[0].File != "" {
		t.Errorf("-json: %+v", got[0])
	}
	if _, serr := os.Stat(got[0].SessionFile); serr != nil {
		t.Errorf("-json names a session that is not there: %v", serr)
	}
	if got[1].Status != "markdown" || got[1].Why == "" || got[1].File == "" {
		t.Errorf("-json, Codex: %+v", got[1])
	}
}

func TestImportConductorDefaultOut(t *testing.T) {
	db, _ := importFixture(t)
	var err error
	a4Capture(t, "", func() { err = runImport([]string{"conductor", "-db", db, "-resumable=false"}) })
	if err != nil {
		t.Fatal(err)
	}
	if m, _ := filepath.Glob(filepath.Join(os.Getenv("CONCH_HOME"), "imports", "conductor", "my-shop", "*.md")); len(m) != 3 {
		t.Errorf("by default the conversations go under conch's folder: %v", m)
	}
}

// A state file that cannot be read stops the import rather than import
// everything again.
func TestImportConductorBrokenState(t *testing.T) {
	db, _ := importFixture(t)
	state := filepath.Join(os.Getenv("CONCH_HOME"), "imports", "conductor", "state.json")
	os.MkdirAll(filepath.Dir(state), 0o700)
	os.WriteFile(state, []byte("{broken"), 0o600)
	var err error
	a4Capture(t, "", func() { err = runImport([]string{"conductor", "-db", db}) })
	if err == nil || !strings.Contains(err.Error(), "state.json") {
		t.Errorf("a broken state: %v", err)
	}
	if len(importSessions(t)) != 0 {
		t.Error("imported with a broken state")
	}
	if b, _ := os.ReadFile(state); string(b) != "{broken" {
		t.Error("the broken state was saved over")
	}
}

func TestImportErrors(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, args := range [][]string{nil, {"cursor"}, {"-db", "x"}} {
		if err := runImport(args); err == nil || !strings.Contains(err.Error(), "usage: conch import conductor") || !strings.Contains(err.Error(), "-markdown") {
			t.Errorf("runImport(%q) = %v, want the usage", args, err)
		}
	}
	if err := runImport([]string{"conductor", "extra"}); err == nil || !strings.Contains(err.Error(), "no arguments") {
		t.Errorf("an extra argument: %v", err)
	}
	if _, err := exec.LookPath("sqlite3"); err == nil {
		err := runImport([]string{"conductor", "-db", filepath.Join(t.TempDir(), "none.db")})
		if err == nil || !strings.Contains(err.Error(), "no Conductor database") || !strings.Contains(err.Error(), "-db") {
			t.Errorf("no database: %v", err)
		}
	}
}

func TestSlugify(t *testing.T) {
	for in, want := range map[string]string{
		"Fix the build: now!":   "fix-the-build-now",
		"  --  ":                "",
		"Ünïcode Wörds 2":       "ünïcode-wörds-2",
		"fix/it's":              "fix-it-s",
		strings.Repeat("a", 90): strings.Repeat("a", 60),
	} {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestShellWord(t *testing.T) {
	for in, want := range map[string]string{
		"/src/shop":  "/src/shop",
		"it's here":  `'it'\''s here'`,
		"":           "''",
		"a b":        "'a b'",
		"~/src/REPO": "~/src/REPO",
	} {
		if got := shellWord(in); got != want {
			t.Errorf("shellWord(%q) = %q, want %q", in, got, want)
		}
	}
}
