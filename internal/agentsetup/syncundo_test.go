package agentsetup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// isolateUndo gives a test its own home, conch folder and PATH, so
// nothing an undo is talked into doing can reach the developer's own.
func isolateUndo(t *testing.T) (home, conchHome string) {
	t.Helper()
	home, conchHome = t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CONCH_HOME", conchHome)
	t.Setenv("PATH", t.TempDir())
	for _, k := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "XDG_CONFIG_HOME"} {
		t.Setenv(k, "")
	}
	return home, conchHome
}

// plant writes a record into the checkout's own .conch, where a cloned
// repository can carry one.
func plant(t *testing.T, root string, u undoLog) string {
	t.Helper()
	b, err := json.Marshal(u)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, legacyUndoDir, u.Stamp+".json")
	write(t, path, string(b))
	return path
}

// A record a repository brings with it is not conch's: undoing it must not
// write, remove, link or run anything — not outside the checkout, not
// through a link, and not on a file no sync wrote.
func TestUndoIgnoresAPlantedRecord(t *testing.T) {
	home, conchHome := isolateUndo(t)
	root := syncRepo(t)
	outside := t.TempDir()
	write(t, filepath.Join(outside, "keep.txt"), "mine\n")
	write(t, filepath.Join(outside, "rules.md"), "mine\n")
	write(t, filepath.Join(root, "main.go"), "package main\n")
	write(t, filepath.Join(root, "AGENTS.md"), "my own\n")
	// A folder in the checkout that is a link out of it.
	if err := os.Symlink(outside, filepath.Join(root, ".codex")); err != nil {
		t.Fatal(err)
	}
	// A fake claude, so a command in the record would leave a mark.
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	marker := filepath.Join(t.TempDir(), "ran")
	write(t, filepath.Join(bin, "claude"), "#!/bin/sh\ntouch "+marker+"\n")
	if err := os.Chmod(filepath.Join(bin, "claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	libWrittenBefore := "untouched"
	write(t, writtenPath(), libWrittenBefore)

	record := plant(t, root, undoLog{Stamp: "20991231-235959", From: "claude", Root: outside,
		Library: true, Written: []libWritten{{Agent: "evil"}},
		Files: []undoEntry{
			{Path: "keep.txt", Absent: true},                                          // through Root
			{Path: filepath.Join(outside, "rules.md"), Before: "planted\n"},           // whole path
			{Path: "../" + filepath.Base(outside) + "/keep.txt", Absent: true},        // climbing out
			{Path: ".codex/config.toml", Before: "planted\n"},                         // through a link
			{Path: "main.go", Absent: true},                                           // never synced
			{Path: "AGENTS.md", Before: "planted\n"},                                  // a file sync writes
			{Path: ".claude/skills/x", Link: outside},                                 // a link
			{Path: "x", Command: []string{"claude", "mcp", "add-json", "evil", "{}"}}, // a command
		}})

	for _, stamp := range []string{"", "20991231-235959"} {
		res, _ := UndoSync(root, stamp)
		for _, c := range res.Changes {
			if c.Done {
				t.Fatalf("undo %q did %+v", stamp, c)
			}
		}
	}
	for p, want := range map[string]string{
		filepath.Join(outside, "keep.txt"): "mine\n",
		filepath.Join(outside, "rules.md"): "mine\n",
		filepath.Join(root, "main.go"):     "package main\n",
		filepath.Join(root, "AGENTS.md"):   "my own\n",
		writtenPath():                      libWrittenBefore,
	} {
		if got, err := os.ReadFile(p); err != nil || string(got) != want {
			t.Fatalf("%s is %q (%v), want %q", p, got, err, want)
		}
	}
	if _, err := os.Lstat(filepath.Join(outside, "config.toml")); !os.IsNotExist(err) {
		t.Fatal("undo wrote through a link out of the checkout")
	}
	if _, err := os.Lstat(filepath.Join(root, ".claude", "skills", "x")); !os.IsNotExist(err) {
		t.Fatal("undo made a link a record asked for")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("undo ran a command a record asked for")
	}
	if _, err := os.Stat(record); err != nil {
		t.Fatalf("the planted record was taken away: %v", err)
	}
	if home == "" || conchHome == "" {
		t.Fatal("not isolated")
	}
}

// A sync's record is kept in conch's folder, not the checkout, and undoing
// from it puts the checkout back as it was.
func TestSyncRecordIsKeptOutsideTheCheckout(t *testing.T) {
	isolateUndo(t)
	root := syncRepo(t)
	res, err := Sync(root, "claude", []string{"codex"}, true)
	if err != nil || res.Undo == "" {
		t.Fatalf("%v %+v", err, res)
	}
	if _, err := os.Lstat(filepath.Join(root, ".conch")); !os.IsNotExist(err) {
		t.Fatal("the record went into the checkout")
	}
	if !isFile(filepath.Join(checkoutRecordDir(root), res.Undo+".json")) {
		t.Fatal("the record is not in conch's folder")
	}
	// The same checkout reached through a link is the same checkout.
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	if got := SyncUndos(alias); len(got) != 1 || got[0] != res.Undo {
		t.Fatalf("undos through a link: %v", got)
	}
	undone, err := UndoSync(root, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range undone.Changes {
		if !c.Done || c.Error != "" {
			t.Fatalf("not undone: %+v", c)
		}
	}
	for _, p := range []string{"AGENTS.md", ".codex", ".agents"} {
		if _, err := os.Lstat(filepath.Join(root, p)); !os.IsNotExist(err) {
			t.Fatalf("%s is still there", p)
		}
	}
	if _, err := os.Lstat(checkoutRecordDir(root)); !os.IsNotExist(err) {
		t.Fatal("the checkout's empty record folder was left behind")
	}
	if got := SyncUndos(root); len(got) != 0 {
		t.Fatalf("still listed: %v", got)
	}
}

// Even conch's own record does no more than a sync of a checkout could
// have done: a folder that has since become a link out of the checkout is
// not written through, and a path no sync writes is left alone.
func TestUndoStaysInsideTheCheckout(t *testing.T) {
	isolateUndo(t)
	root := syncRepo(t)
	res, err := Sync(root, "claude", []string{"codex"}, true)
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	write(t, filepath.Join(outside, "config.toml"), "mine\n")
	if err := os.RemoveAll(filepath.Join(root, ".codex")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, ".codex")); err != nil {
		t.Fatal(err)
	}
	// And an entry nothing a sync writes, as if the record had been edited.
	recPath := filepath.Join(checkoutRecordDir(root), res.Undo+".json")
	var u undoLog
	if err := json.Unmarshal([]byte(read(t, recPath)), &u); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "main.go"), "package main\n")
	u.Files = append(u.Files, undoEntry{Path: "main.go", Absent: true}, undoEntry{Path: "AGENTS.md/../main.go", Absent: true})
	b, _ := json.Marshal(u)
	write(t, recPath, string(b))

	undone, err := UndoSync(root, res.Undo)
	if err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(outside, "config.toml")); got != "mine\n" {
		t.Fatalf("written through the link: %q", got)
	}
	if !isFile(filepath.Join(root, "main.go")) {
		t.Fatal("undo removed a file no sync writes")
	}
	skipped := 0
	for _, c := range undone.Changes {
		if strings.Contains(c.Path, "main.go") || c.Path == ".codex/config.toml" {
			if c.Action != ActionSkip || c.Done {
				t.Fatalf("%s was not left alone:\n%s", c.Path, changeLines(undone))
			}
			skipped++
		}
	}
	if skipped < 3 {
		t.Fatalf("skipped %d:\n%s", skipped, changeLines(undone))
	}
	// What was inside the checkout is still put back.
	if _, err := os.Lstat(filepath.Join(root, "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatal("AGENTS.md, which the sync made, is still there")
	}
}

// A record an older build left in the checkout still loads and is listed —
// after conch's own — but undoing it only says what it names.
func TestLegacyRecordLoadsButIsNotActedOn(t *testing.T) {
	isolateUndo(t)
	root := syncRepo(t)
	write(t, filepath.Join(root, "AGENTS.md"), "synced\n")
	old := plant(t, root, undoLog{Stamp: "20260901-000000", From: "claude", Root: root,
		Files: []undoEntry{{Path: "AGENTS.md", Kind: SyncInstructions, Absent: true}}})
	if got := SyncUndos(root); len(got) != 1 || got[0] != "20260901-000000" {
		t.Fatalf("undos %v", got)
	}
	res, err := UndoSync(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Changes) != 1 || res.Changes[0].Done || res.Changes[0].Path != "AGENTS.md" ||
		res.Changes[0].Kind != SyncInstructions || len(res.Notes) == 0 {
		t.Fatalf("%+v", res)
	}
	if !isFile(filepath.Join(root, "AGENTS.md")) || !isFile(old) {
		t.Fatal("a record in the checkout was acted on")
	}
	// A newer sync's record comes first.
	sres, err := Sync(root, "claude", []string{"gemini"}, true)
	if err != nil || sres.Undo == "" {
		t.Fatal(err)
	}
	if got := SyncUndos(root); len(got) != 2 || got[0] != sres.Undo {
		t.Fatalf("undos %v", got)
	}
	// A record that cannot be read says so.
	write(t, filepath.Join(root, legacyUndoDir, "20250101-000000.json"), "{not json")
	if _, err := UndoSync(root, "20250101-000000"); err == nil {
		t.Fatal("a broken record was accepted")
	}
	if _, err := UndoSync(root, "19990101-000000"); err == nil {
		t.Fatal("a missing record was accepted")
	}
}

// A stamp names a record in conch's folder and nowhere else: one that
// climbs out would reach a record a checkout carries, and conch's own
// trust in its records would go to that one.
func TestUndoStampCannotClimbOut(t *testing.T) {
	isolateUndo(t)
	root := syncRepo(t)
	outside := t.TempDir()
	write(t, filepath.Join(outside, "keep.txt"), "mine\n")
	b, _ := json.Marshal(undoLog{Stamp: "evil", Root: outside, Files: []undoEntry{{Path: "keep.txt", Absent: true}}})
	write(t, filepath.Join(root, "evil.json"), string(b))
	climb, err := filepath.Rel(userRecordDir(), filepath.Join(root, "evil"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := UndoUserSync(climb); err == nil {
		t.Fatal("a stamp that climbs out was taken")
	}
	climb, _ = filepath.Rel(checkoutRecordDir(root), filepath.Join(root, "evil"))
	if _, err := UndoSync(root, climb); err == nil {
		t.Fatal("a stamp that climbs out was taken")
	}
	for _, bad := range []string{"..", "a/b", "/abs"} {
		if _, err := UndoSync(root, bad); err == nil {
			t.Fatalf("stamp %q was taken", bad)
		}
	}
	if read(t, filepath.Join(outside, "keep.txt")) != "mine\n" {
		t.Fatal("undo acted on a record outside conch's folder")
	}
}
