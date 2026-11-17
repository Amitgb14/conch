package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/sessions"
)

// importFixture is a small Conductor database: one conversation with
// something said in it, in a workspace that is still there, and one
// with nothing said.
func importFixture(t *testing.T) (db, ws string) {
	t.Helper()
	bin, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("no sqlite3")
	}
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("CONCH_HOME", filepath.Join(dir, "conch"))
	db = filepath.Join(dir, "conductor.db")
	ws = filepath.Join(dir, "it's here")
	os.MkdirAll(ws, 0o755)
	sql := `CREATE TABLE repos (id TEXT, name TEXT, root_path TEXT);
CREATE TABLE workspaces (id TEXT, repository_id TEXT, directory_name TEXT, branch TEXT, workspace_path TEXT);
CREATE TABLE sessions (id TEXT, created_at TEXT, updated_at TEXT, workspace_id TEXT, title TEXT, agent_type TEXT);
CREATE TABLE session_messages (id TEXT, session_id TEXT, role TEXT, content TEXT, created_at TEXT);
INSERT INTO repos VALUES ('r1', 'My Shop', '/gone');
INSERT INTO workspaces VALUES ('w1', 'r1', 'victoria', 'fix/build', '` + strings.ReplaceAll(ws, "'", "''") + `');
INSERT INTO sessions VALUES ('0123456789abcdef', '2026-07-10 15:19:00', '2026-07-10 15:21:00', 'w1', 'Fix the build: now!', 'claude'),
 ('empty', '2026-07-11 15:19:00', '2026-07-11 15:21:00', 'w1', 'Nothing', 'claude');
INSERT INTO session_messages VALUES
 ('m1', '0123456789abcdef', 'user', 'Why does the build fail?', '2026-07-10 15:19:01'),
 ('m2', '0123456789abcdef', 'assistant', '{"type":"assistant","message":{"content":[{"type":"text","text":"A missing header; fixed."}]}}', '2026-07-10 15:19:05');`
	if out, err := exec.Command(bin, db, sql).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3: %v: %s", err, out)
	}
	return db, ws
}

func TestImportConductor(t *testing.T) {
	db, ws := importFixture(t)
	out := filepath.Join(t.TempDir(), "out")
	var err error
	stdout, _ := a4Capture(t, "", func() { err = runImport([]string{"conductor", "-db", db, "-out", out}) })
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(out, "my-shop", "fix-the-build-now-01234567.md")
	doc, rerr := os.ReadFile(file)
	if rerr != nil {
		t.Fatalf("the conversation was not written where expected: %v\n%s", rerr, stdout)
	}
	for _, want := range []string{"Imported from Conductor", "# Handoff: Fix the build: now!", "Why does the build fail?", "A missing header; fixed.", "`fix/build`"} {
		if !strings.Contains(string(doc), want) {
			t.Errorf("the document lacks %q:\n%s", want, doc)
		}
	}
	if m, _ := filepath.Glob(filepath.Join(out, "*", "nothing-*")); len(m) > 0 {
		t.Errorf("a conversation with nothing said was written: %v", m)
	}
	if st, _ := os.Stat(file); st.Mode().Perm() != 0o600 {
		t.Errorf("the document is %v, want readable by its owner alone", st.Mode().Perm())
	}
	for _, want := range []string{"wrote 1 of Conductor's conversations", "Fix the build: now!  (Claude Code, 2 messages, My Shop · fix/build)", file,
		"conch new -agent claude -cwd " + shellWord(ws) + " -- 'Read " + file} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output lacks %q:\n%s", want, stdout)
		}
	}

	// Again: the same file, replaced rather than a second one.
	a4Capture(t, "", func() { err = runImport([]string{"conductor", "-db", db, "-out", out}) })
	if m, _ := filepath.Glob(filepath.Join(out, "my-shop", "*.md")); err != nil || len(m) != 1 {
		t.Errorf("importing twice: %v, files %v", err, m)
	}
}

func TestImportConductorDryRunAndJSON(t *testing.T) {
	db, _ := importFixture(t)
	out := filepath.Join(t.TempDir(), "out")
	var err error
	stdout, _ := a4Capture(t, "", func() { err = runImport([]string{"conductor", "-db", db, "-out", out, "-n"}) })
	if err != nil || !strings.Contains(stdout, "would write 1") || !strings.Contains(stdout, "without -n writes them") {
		t.Errorf("dry run: %v\n%s", err, stdout)
	}
	if _, serr := os.Stat(out); serr == nil {
		t.Error("a dry run wrote something")
	}

	stdout, _ = a4Capture(t, "", func() { err = runImport([]string{"conductor", "-db", db, "-out", out, "-json"}) })
	var got []importedFile
	if err != nil || json.Unmarshal([]byte(stdout), &got) != nil || len(got) != 1 {
		t.Fatalf("-json: %v\n%s", err, stdout)
	}
	if got[0].Title != "Fix the build: now!" || got[0].Agent != "Claude Code" || got[0].Messages != 2 || got[0].Branch != "fix/build" {
		t.Errorf("-json: %+v", got[0])
	}
	if _, serr := os.Stat(got[0].File); serr != nil {
		t.Errorf("-json names a file that is not there: %v", serr)
	}
}

func TestImportConductorDefaultOut(t *testing.T) {
	db, _ := importFixture(t)
	var err error
	a4Capture(t, "", func() { err = runImport([]string{"conductor", "-db", db}) })
	if err != nil {
		t.Fatal(err)
	}
	if m, _ := filepath.Glob(filepath.Join(os.Getenv("CONCH_HOME"), "imports", "conductor", "my-shop", "*.md")); len(m) != 1 {
		t.Errorf("by default the conversations go under conch's folder: %v", m)
	}
}

func TestImportErrors(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, args := range [][]string{nil, {"cursor"}, {"-db", "x"}} {
		if err := runImport(args); err == nil || !strings.Contains(err.Error(), "usage: conch import conductor") {
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
	scan := func() []importedFile {
		t.Helper()
		var err error
		stdout, _ := a4Capture(t, "", func() { err = runImport([]string{"conductor", "-db", db, "-out", out, "-n", "-json"}) })
		var got []importedFile
		if err != nil || json.Unmarshal([]byte(stdout), &got) != nil {
			t.Fatalf("scan: %v %q", err, stdout)
		}
		return got
	}
	got := scan()
	if len(got) != 1 || got[0].ID != "0123456789abcdef" || got[0].Where != "workspace" || got[0].Imported ||
		got[0].LastUsed.IsZero() || got[0].Dir != ws {
		t.Fatalf("scan: %+v", got)
	}

	var err error
	a4Capture(t, "", func() { err = runImport([]string{"conductor", "-db", db, "-out", out, "-ids", "nope"}) })
	if err == nil || !strings.Contains(err.Error(), `no conversation "nope"`) {
		t.Fatalf("an id Conductor does not have: %v", err)
	}
	// "empty" is a conversation with nothing said: chosen, it is skipped
	// rather than written as an empty document.
	stdout, _ := a4Capture(t, "", func() {
		err = runImport([]string{"conductor", "-db", db, "-out", out, "-ids", "0123456789abcdef,empty", "-json"})
	})
	var wrote []importedFile
	if err != nil || json.Unmarshal([]byte(stdout), &wrote) != nil || len(wrote) != 1 || !wrote[0].Imported {
		t.Fatalf("import of the chosen: %v %q", err, stdout)
	}
	if _, err := os.Stat(wrote[0].File); err != nil {
		t.Fatal(err)
	}
	if got := scan(); !got[0].Imported {
		t.Fatalf("imported already: %+v", got)
	}

	// The workspace gone, and the repository too: nowhere to carry it on.
	os.RemoveAll(ws)
	if got := scan(); got[0].Where != "gone" || got[0].Dir != "" {
		t.Fatalf("gone: %+v", got)
	}
}
