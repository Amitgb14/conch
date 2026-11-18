package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/proto"
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
	// No server unless a test starts its own: adding projects must not
	// reach the developer's.
	t.Setenv("CONCH_SOCKET", filepath.Join(dir, "no.sock"))
	t.Setenv("CONCH_PANE_ID", "")
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
	if !strings.Contains(stdout, "and as Markdown: "+file) || !strings.Contains(string(doc), "# Handoff: Fix the build: now!") {
		t.Errorf("-markdown output:\n%s\n%s", stdout, doc)
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

// Two conversations of one title whose ids share their first eight
// characters (Conductor's are time-ordered) went to one file, the second
// overwriting the first. The oldest keeps the short name, so a file from
// an import before the other existed is still the one replaced; the other
// is named by its whole id, the same every time. One not written takes no
// name, and the order they come in changes nothing.
func TestImportedNamesDoNotCollide(t *testing.T) {
	out := t.TempDir()
	said := []sessions.Turn{{Role: "user", Text: "hi"}}
	conv := func(id, title, repo string, turns []sessions.Turn) sessions.Imported {
		return sessions.Imported{Session: sessions.Session{ID: id, Title: title, Agent: "claude"}, Repo: repo, Turns: turns}
	}
	all := []sessions.Imported{
		conv("aaaaaaaa-2", "Fix it", "shop", said),
		conv("aaaaaaaa-1", "Fix it", "shop", said),
		conv("aaaaaaaa-0", "Fix it", "shop", nil),          // nothing said: never written
		conv("aaaaaaaa-3", "Fix it", "other", said),        // another repository's folder
		conv("aaaaaaaa-4", "Something else", "shop", said), // another title
		conv("b", "", "", said),
	}
	// Markdown only: the names are the documents'.
	writeImported := func(all []sessions.Imported, out string, dry bool) ([]importedFile, error) {
		imp := importer{out: out, dry: dry, state: sessions.ImportState{}}
		return imp.run(context.Background(), all)
	}
	files, err := writeImported(all, out, false)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, f := range files {
		rel, _ := filepath.Rel(out, f.File)
		got[f.ID] = rel
	}
	want := map[string]string{
		"aaaaaaaa-1": "shop/fix-it-aaaaaaaa.md",
		"aaaaaaaa-2": "shop/fix-it-aaaaaaaa-2.md",
		"aaaaaaaa-3": "other/fix-it-aaaaaaaa.md",
		"aaaaaaaa-4": "shop/something-else-aaaaaaaa.md",
		"b":          "unknown-repository/conversation-b.md",
	}
	if len(got) != len(want) {
		t.Fatalf("files %v, want %v", got, want)
	}
	for id, name := range want {
		if got[id] != name {
			t.Errorf("%s: %q, want %q", id, got[id], name)
		}
	}
	// Each its own file, none overwritten.
	seen := map[string]bool{}
	for _, f := range files {
		if seen[f.File] {
			t.Fatalf("two conversations in %s", f.File)
		}
		seen[f.File] = true
		if _, err := os.Stat(f.File); err != nil {
			t.Fatal(err)
		}
	}
	// The other order names them the same.
	rev := append([]sessions.Imported(nil), all...)
	slices.Reverse(rev)
	again, err := writeImported(rev, out, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range again {
		if rel, _ := filepath.Rel(out, f.File); rel != want[f.ID] {
			t.Errorf("reversed, %s: %q", f.ID, rel)
		}
	}
	// One alone keeps the short name it always had.
	alone, _ := writeImported(all[:1], out, true)
	if rel, _ := filepath.Rel(out, alone[0].File); rel != "shop/fix-it-aaaaaaaa.md" {
		t.Errorf("alone: %q", rel)
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

// What Conch.app's Import sheet is drawn from (-n -json), and its Import
// button (-ids): each conversation by its whole id, where it carries on,
// when it was last used, and whether it was brought in already.
func TestImportConductorChoose(t *testing.T) {
	db, ws := importFixture(t)
	out := filepath.Join(t.TempDir(), "out")
	scan := func() map[string]importedFile {
		t.Helper()
		var err error
		stdout, _ := a4Capture(t, "", func() { err = runImport([]string{"conductor", "-db", db, "-out", out, "-n", "-json"}) })
		var got []importedFile
		if err != nil || json.Unmarshal([]byte(stdout), &got) != nil {
			t.Fatalf("scan: %v %q", err, stdout)
		}
		byID := map[string]importedFile{}
		for _, f := range got {
			byID[f.ID] = f
		}
		return byID
	}
	got := scan()
	fix := got["0123456789abcdef"]
	if len(got) != 3 || fix.Where != "workspace" || fix.Imported || fix.LastUsed.IsZero() || fix.Dir != ws ||
		fix.Status != string(sessions.ImportWritten) || got["lost1"].Where != "gone" || got["codex1"].Status != "markdown" {
		t.Fatalf("scan: %+v", got)
	}

	var err error
	a4Capture(t, "", func() { err = runImport([]string{"conductor", "-db", db, "-out", out, "-ids", "nope"}) })
	if err == nil || !strings.Contains(err.Error(), `no conversation "nope"`) {
		t.Fatalf("an id Conductor does not have: %v", err)
	}
	// "empty" is a conversation with nothing said: chosen, it is skipped
	// rather than written. Only the chosen are written: the Claude one as
	// a session, nothing for the Codex one.
	stdout, _ := a4Capture(t, "", func() {
		err = runImport([]string{"conductor", "-db", db, "-out", out, "-ids", "0123456789abcdef,empty", "-json"})
	})
	var wrote []importedFile
	if err != nil || json.Unmarshal([]byte(stdout), &wrote) != nil || len(wrote) != 1 || !wrote[0].Imported ||
		wrote[0].Session == "" || wrote[0].File != "" {
		t.Fatalf("import of the chosen: %v %q", err, stdout)
	}
	if _, err := os.Stat(wrote[0].SessionFile); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(out); err == nil {
		t.Errorf("the Codex conversation was not chosen, yet %s was written", out)
	}
	got = scan()
	if f := got["0123456789abcdef"]; !f.Imported || f.Status != string(sessions.ImportUnchanged) || f.Session != wrote[0].Session {
		t.Fatalf("imported already: %+v", f)
	}
	if got["codex1"].Imported {
		t.Errorf("codex1 was never imported: %+v", got["codex1"])
	}

	// The workspace gone, and the repository too: nowhere to carry it on.
	os.RemoveAll(ws)
	if f := scan()["0123456789abcdef"]; f.Where != "gone" || f.Dir != "" {
		t.Fatalf("gone: %+v", f)
	}
}

// After an import the repository is in conch: project.add for each
// repository still on this machine, once each, never for one that is gone
// and never with -n; the JSON names the project. Without a server, or
// with -project=false, nothing is asked and the import still stands, and
// the text says how to add it.
func TestImportConductorAddsTheProject(t *testing.T) {
	db, ws := importFixture(t)
	repo := filepath.Join(filepath.Dir(ws), "shop repo")
	os.MkdirAll(repo, 0o755)
	if out, err := exec.Command("sqlite3", db, "UPDATE repos SET root_path = '"+repo+"'; INSERT INTO repos VALUES ('r2', 'Gone', '/gone/away'); "+
		"INSERT INTO workspaces VALUES ('w3', 'r2', 'x', 'x', '/gone/away/x'); "+
		"INSERT INTO sessions VALUES ('g1', '2026-07-14 15:19:00', '2026-07-14 15:21:00', 'w3', 'Away', 'claude'); "+
		"INSERT INTO session_messages VALUES ('m9', 'g1', 'user', 'hello', '2026-07-14 15:19:01');").CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	out := filepath.Join(t.TempDir(), "out")

	// No server: imported all the same, and told how to add it.
	stdout, _ := a4Capture(t, "", func() {
		if err := runImport([]string{"conductor", "-db", db, "-out", out, "-ids", "codex1"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(stdout, "Not added to conch: no conch server is running") || !strings.Contains(stdout, "conch project add '"+repo+"'") {
		t.Fatalf("no server:\n%s", stdout)
	}

	sockDir, err := os.MkdirTemp("", "imp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(sockDir) })
	sock := filepath.Join(sockDir, "s.sock")
	t.Setenv("CONCH_SOCKET", sock)
	srv := startA4Server(t, sock)
	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		if msg.Method == proto.MethodProjectAdd {
			return proto.ProjectInfo{ID: "shop1", Name: "shop repo", Path: repo, Git: true}, nil
		}
		return nil, nil
	})
	adds := func() int {
		n := 0
		for _, m := range srv.methods() {
			if m == proto.MethodProjectAdd {
				n++
			}
		}
		return n
	}

	a4Capture(t, "", func() { runImport([]string{"conductor", "-db", db, "-out", out, "-n"}) })
	a4Capture(t, "", func() { runImport([]string{"conductor", "-db", db, "-out", out, "-project=false"}) })
	if n := adds(); n != 0 {
		t.Fatalf("-n or -project=false asked project.add %d times", n)
	}

	stdout, _ = a4Capture(t, "", func() {
		if err := runImport([]string{"conductor", "-db", db, "-out", out, "-json"}); err != nil {
			t.Fatal(err)
		}
	})
	var got []importedFile
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("%v %q", err, stdout)
	}
	// Three conversations in the shop repository, one in a repository
	// that is gone: project.add once, for the shop.
	var p proto.ProjectAddParams
	if n := adds(); n != 1 || !srv.params(t, proto.MethodProjectAdd, &p) || p.Path != repo {
		t.Fatalf("project.add %d times, %+v", n, p)
	}
	for _, f := range got {
		if want := map[bool]string{true: "", false: "shop1"}[f.ID == "g1"]; f.Project != want {
			t.Errorf("%s: project %q, want %q", f.ID, f.Project, want)
		}
	}

	stdout, _ = a4Capture(t, "", func() { runImport([]string{"conductor", "-db", db, "-out", out}) })
	if !strings.Contains(stdout, "In conch now") || !strings.Contains(stdout, repo) || strings.Contains(stdout, "Not added") {
		t.Fatalf("text:\n%s", stdout)
	}

	// A server that refuses: the import stands and says so.
	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		if msg.Method == proto.MethodProjectAdd {
			return nil, proto.Errorf(proto.ErrBadRequest, "no")
		}
		return nil, nil
	})
	stdout, _ = a4Capture(t, "", func() {
		if err := runImport([]string{"conductor", "-db", db, "-out", out}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(stdout, "Not added to conch") || !strings.Contains(stdout, "conch project add") {
		t.Fatalf("refused:\n%s", stdout)
	}
}

// The list -n prints is what somebody chooses from, so it names each
// conversation's id — what -ids takes — and says which can no longer be
// carried on where they ran, or were imported already.
func TestImportConductorDryRunNamesIDs(t *testing.T) {
	db, ws := importFixture(t)
	out := filepath.Join(t.TempDir(), "out")
	dry := func() string {
		t.Helper()
		var err error
		stdout, _ := a4Capture(t, "", func() { err = runImport([]string{"conductor", "-db", db, "-out", out, "-n"}) })
		if err != nil {
			t.Fatal(err)
		}
		return stdout
	}
	got := dry()
	for _, want := range []string{"    id 0123456789abcdef\n", "-ids ID,… only those"} {
		if !strings.Contains(got, want) {
			t.Errorf("-n lacks %q:\n%s", want, got)
		}
	}
	// The fixture's lost1 really is gone; the one in its workspace is not.
	if fix := got[strings.Index(got, "Fix the build"):]; strings.Contains(fix[:strings.Index(fix, "\n")], "imported already") ||
		strings.Contains(fix[:strings.Index(fix, "\n")], "gone") || strings.Contains(got, "removed") {
		t.Errorf("-n says more than is so:\n%s", got)
	}
	var err error
	a4Capture(t, "", func() { err = runImport([]string{"conductor", "-db", db, "-out", out}) })
	if err != nil {
		t.Fatal(err)
	}
	if got := dry(); !strings.Contains(got, "fix/build, imported already)") {
		t.Errorf("-n after an import:\n%s", got)
	}
	os.RemoveAll(ws)
	if got := dry(); !strings.Contains(got, "worktree and repository gone, imported already)") {
		t.Errorf("-n once the worktree and repository are gone:\n%s", got)
	}
	// Only a dry run names ids: what was written is named by its file.
	stdout, _ := a4Capture(t, "", func() { err = runImport([]string{"conductor", "-db", db, "-out", out}) })
	if err != nil || strings.Contains(stdout, "id 0123456789abcdef") {
		t.Errorf("an import names ids: %v\n%s", err, stdout)
	}
}

func TestImportedWhereWords(t *testing.T) {
	for where, want := range map[string]string{"workspace": "", "repository": "worktree removed", "gone": "worktree and repository gone", "": "worktree and repository gone"} {
		if got := whereWords(where); got != want {
			t.Errorf("whereWords(%q) = %q, want %q", where, got, want)
		}
	}
}

// The command an import ends with carries on a conversation that can be:
// the newest written whose worktree or repository is still here, not one
// whose folders are gone, which only had a placeholder to offer.
func TestImportedCarryOnSkipsGone(t *testing.T) {
	here := t.TempDir()
	written := []importedFile{
		{Title: "older", Dir: here, Where: "workspace", File: "/x/older.md", Status: "markdown", AgentName: "claude"},
		{Title: "newer", Dir: here, Where: "repository", File: "/x/newer.md", Status: "markdown", AgentName: "claude"},
		{Title: "gone", Where: "gone", File: "/x/gone.md", Status: "markdown", AgentName: "claude"},
	}
	stdout, _ := a4Capture(t, "", func() { printImported(written, nil, false) })
	if !strings.Contains(stdout, "conch new -agent claude -cwd "+here+" -- 'Read /x/newer.md") {
		t.Errorf("did not carry on the newest that can be:\n%s", stdout)
	}
	// Every one gone: the placeholder, since there is nothing better.
	stdout, _ = a4Capture(t, "", func() { printImported(written[2:], nil, false) })
	if !strings.Contains(stdout, "-cwd ~/src/REPO -- 'Read /x/gone.md") {
		t.Errorf("all gone:\n%s", stdout)
	}
}

// Resume In…: a Claude conversation whose workspace and repository are
// gone is written as a session in a folder the person chose (-dir), is
// remembered there, so the next scan offers Resume from it, and resumes
// with that folder. -dir is refused without -ids, with -resumable=false,
// for a folder that is not there, and for a conversation imported to
// resume somewhere else already.
func TestImportConductorResumeIn(t *testing.T) {
	db, ws := importFixture(t)
	out := filepath.Join(t.TempDir(), "out")
	run := func(args ...string) (string, error) {
		t.Helper()
		var err error
		stdout, _ := a4Capture(t, "", func() { err = runImport(append([]string{"conductor", "-db", db, "-out", out}, args...)) })
		return stdout, err
	}
	scan := func() map[string]importedFile {
		t.Helper()
		stdout, err := run("-n", "-json")
		var got []importedFile
		if err != nil || json.Unmarshal([]byte(stdout), &got) != nil {
			t.Fatalf("scan: %v %q", err, stdout)
		}
		byID := map[string]importedFile{}
		for _, f := range got {
			byID[f.ID] = f
		}
		return byID
	}
	// A folder reached through a link is named as Claude Code will see
	// it: links resolved.
	real := filepath.Join(t.TempDir(), "clone")
	os.MkdirAll(real, 0o755)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	real, _ = filepath.EvalSymlinks(real)

	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"-dir", link}, "-dir places only the conversations -ids names"},
		{[]string{"-ids", "lost1", "-dir", link, "-resumable=false"}, "-resumable=false writes only Markdown"},
		{[]string{"-ids", "lost1", "-dir", filepath.Join(real, "nope")}, "-dir"},
		{[]string{"-ids", "lost1", "-dir", filepath.Join(ws, "..", "conductor.db")}, "is not a folder"},
	} {
		if _, err := run(c.args...); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: %v, want %q", c.args, err, c.want)
		}
	}
	if len(importSessions(t)) != 0 {
		t.Fatal("a refused -dir wrote a session")
	}

	if f := scan()["lost1"]; f.Where != "gone" || f.Status != "markdown" {
		t.Fatalf("before: %+v", f)
	}
	stdout, err := run("-ids", "lost1", "-dir", link, "-json")
	var wrote []importedFile
	if err != nil || json.Unmarshal([]byte(stdout), &wrote) != nil || len(wrote) != 1 {
		t.Fatalf("-dir: %v %q", err, stdout)
	}
	w := wrote[0]
	if w.Status != string(sessions.ImportWritten) || w.Where != "chosen" || w.Dir != real || w.Session == "" || w.File != "" {
		t.Fatalf("written: %+v", w)
	}
	if !strings.HasPrefix(w.SessionFile, filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "projects", claudeFolder(real))) {
		t.Errorf("session filed under %s, want the chosen folder's project", w.SessionFile)
	}
	body, _ := os.ReadFile(w.SessionFile)
	if !strings.Contains(string(body), `"cwd":"`+real+`"`) {
		t.Errorf("the session does not run in the chosen folder:\n%s", body)
	}

	// The next scan, as the app's page makes, still knows the folder.
	f := scan()["lost1"]
	if f.Where != "chosen" || f.Dir != real || f.Session != w.Session || f.Status != string(sessions.ImportUnchanged) || !f.Imported {
		t.Fatalf("after: %+v", f)
	}
	if got := whereWords(f.Where); !strings.Contains(got, "a folder you chose") {
		t.Errorf("whereWords(chosen) = %q", got)
	}
	// The same folder again is no change; another one is refused, since
	// the session there is where it carries on now.
	if _, err := run("-ids", "lost1", "-dir", real); err != nil {
		t.Errorf("the same folder again: %v", err)
	}
	other := t.TempDir()
	other, _ = filepath.EvalSymlinks(other)
	if _, err := run("-ids", "lost1", "-dir", other); err == nil || !strings.Contains(err.Error(), "imported already and resumes in "+real) {
		t.Errorf("another folder: %v", err)
	}
	if _, err := run("-ids", "0123456789abcdef"); err != nil {
		t.Fatal(err)
	}
	if _, err := run("-ids", "0123456789abcdef", "-dir", other); err == nil || !strings.Contains(err.Error(), "resumes in "+ws) {
		t.Errorf("a conversation imported where it ran: %v", err)
	}
	if n := len(importSessions(t)); n != 2 {
		t.Errorf("sessions: %d, want 2", n)
	}

	// The chosen folder removed: nowhere to resume again, and said so;
	// another folder may be chosen now, and it is written there anew.
	os.RemoveAll(real)
	if f := scan()["lost1"]; f.Where != "gone" || f.Dir != "" {
		t.Errorf("chosen folder gone: %+v", f)
	}
	stdout, err = run("-ids", "lost1", "-dir", other, "-json")
	wrote = nil
	if err != nil || json.Unmarshal([]byte(stdout), &wrote) != nil || len(wrote) != 1 ||
		wrote[0].Status != string(sessions.ImportWritten) || wrote[0].Dir != other || wrote[0].Session == w.Session {
		t.Fatalf("a new folder once the chosen one is gone: %v %q", err, stdout)
	}
	if f := scan()["lost1"]; f.Where != "chosen" || f.Dir != other {
		t.Errorf("after choosing again: %+v", f)
	}
	// Written where it ran, with that folder still there: refused.
	if _, err := run("-ids", "0123456789abcdef", "-dir", other); err == nil || !strings.Contains(err.Error(), "resumes in "+ws) {
		t.Errorf("a conversation whose workspace is there: %v", err)
	}
}
