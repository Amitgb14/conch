package tui

import (
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/proto"
)

// a4Diff is a two-hunk diff of file, with what each hunk adds.
func a4Diff(file, first, second string) string {
	return "diff --git a/" + file + " b/" + file + "\n" +
		"index 1111111..2222222 100644\n" +
		"--- a/" + file + "\n" +
		"+++ b/" + file + "\n" +
		"@@ -1,3 +1,3 @@\n context\n-old\n+" + first + "\n context\n" +
		"@@ -20,3 +20,3 @@\n context\n-old\n+" + second + "\n context\n"
}

// hunkView is a changes view with a.go's diff open, two files uncommitted.
func hunkView(t *testing.T, m *Model) *changesView {
	t.Helper()
	cv := &changesView{machine: localMachine, projectID: "r1", branch: "feat"}
	data := proto.Changes{ProjectID: "r1", Branch: "feat", Base: "main", Worktree: "/src/api-feat",
		Files: []proto.FileChange{{Path: "a.go", Code: "M"}, {Path: "b.go", Code: "M"}}}
	cv.receive(changesMsg{projectID: "r1", branch: "feat", data: data})
	cv.key(m, a2Key("enter")) // open a.go's diff
	cv.receive(diffMsg{projectID: "r1", branch: "feat", file: "a.go", diff: a4Diff("a.go", "one", "two")})
	return cv
}

func TestSplitHunks(t *testing.T) {
	header, hunks, at := splitHunks(strings.Split(strings.TrimRight(a4Diff("a.go", "one", "two"), "\n"), "\n"))
	if !strings.HasPrefix(header, "diff --git") || !strings.HasSuffix(header, "+++ b/a.go\n") {
		t.Fatalf("header %q", header)
	}
	if len(hunks) != 2 || at[0] != 4 || at[1] != 9 {
		t.Fatalf("hunks %d at %v", len(hunks), at)
	}
	if !strings.HasPrefix(hunks[0], "@@ -1,3") || !strings.Contains(hunks[0], "+one") || strings.Contains(hunks[0], "+two") {
		t.Fatalf("first hunk %q", hunks[0])
	}
	// A diff with no hunks (a binary file or a mode change) has no marks.
	header, hunks, at = splitHunks([]string{"diff --git a/x b/x", "Binary files differ"})
	if len(hunks) != 0 || len(at) != 0 || !strings.Contains(header, "Binary") {
		t.Fatalf("binary: %q %v", header, hunks)
	}
	if h, hs, _ := splitHunks(nil); h != "" || hs != nil {
		t.Fatal("empty diff")
	}
}

func TestHunkMarking(t *testing.T) {
	m, _ := harvestModel(t, harvestCapability, hunksCapability)
	cv := hunkView(t, m)
	if cv.diffFile != "a.go" || cv.hunkSel != 0 {
		t.Fatalf("diff open: %q %d", cv.diffFile, cv.hunkSel)
	}

	// space marks the hunk under the cursor and moves to the next.
	cv.key(m, a2Key(" "))
	if !cv.markedHunk(0) || cv.markedHunk(1) || cv.hunkSel != 1 {
		t.Fatalf("first mark: %v sel %d", cv.hunks["a.go"], cv.hunkSel)
	}
	sel := cv.selection()
	if sel.hunks != 1 || sel.inned != 1 || !strings.Contains(sel.patch, "+one") || strings.Contains(sel.patch, "+two") {
		t.Fatalf("selection: %+v\n%s", sel, sel.patch)
	}
	if !strings.HasPrefix(sel.patch, "diff --git a/a.go") || !strings.Contains(sel.patch, "+++ b/a.go\n@@ -1,3") {
		t.Fatalf("patch header:\n%s", sel.patch)
	}

	// Marking again unmarks; the cursor keys move between hunks.
	cv.key(m, a2Key("N"))
	cv.key(m, a2Key(" "))
	if cv.selection().patch != "" || len(cv.hunks) != 0 {
		t.Fatalf("unmarked: %+v", cv.hunks)
	}
	for _, step := range []struct {
		key  string
		want int
	}{{"n", 1}, {"n", 1}, {"N", 0}, {"[", 0}, {"]", 1}} {
		cv.key(m, a2Key(step.key))
		if cv.hunkSel != step.want {
			t.Fatalf("%s: hunk %d, want %d", step.key, cv.hunkSel, step.want)
		}
	}
	// Moving to a hunk scrolls it into view.
	cv.diffScroll = 0
	cv.toHunk(m, 1, 6) // a 6-row view shows 4 diff lines
	if cv.diffScroll != 9 {
		t.Fatalf("scrolled to %d", cv.diffScroll)
	}

	// Marks of two files build one patch, in the order the files are listed.
	cv.key(m, a2Key(" ")) // a.go's second hunk
	cv.key(m, a2Key("esc"))
	cv.sel = 1
	cv.key(m, a2Key("enter"))
	cv.receive(diffMsg{projectID: "r1", branch: "feat", file: "b.go", diff: a4Diff("b.go", "three", "four")})
	cv.key(m, a2Key(" "))
	sel = cv.selection()
	if sel.hunks != 2 || sel.inned != 2 {
		t.Fatalf("two files: %+v", sel)
	}
	if strings.Index(sel.patch, "b/a.go") > strings.Index(sel.patch, "b/b.go") {
		t.Fatalf("patch out of order:\n%s", sel.patch)
	}
	if strings.Count(sel.patch, "@@ -") != 2 || !strings.Contains(sel.patch, "+two") || !strings.Contains(sel.patch, "+three") {
		t.Fatalf("patch:\n%s", sel.patch)
	}

	// Marking a hunk of a file drops its whole-file mark, and the other way.
	cv.marked = map[string]bool{"b.go": true}
	cv.key(m, a2Key("N"))
	cv.key(m, a2Key(" ")) // mark b.go's first hunk too
	if cv.marked["b.go"] {
		t.Fatal("hunk marks left the whole-file mark")
	}
	cv.key(m, a2Key("esc"))
	cv.key(m, a2Key(" ")) // mark the file b.go as a whole
	if !cv.marked["b.go"] || cv.hunks["b.go"] != nil {
		t.Fatalf("file mark kept hunk marks: %v %+v", cv.marked, cv.hunks)
	}
}

func TestHunkMarksFollowTheDiff(t *testing.T) {
	m, _ := harvestModel(t, harvestCapability, hunksCapability)
	cv := hunkView(t, m)
	cv.key(m, a2Key(" ")) // first hunk
	cv.key(m, a2Key(" ")) // second hunk
	if cv.selection().hunks != 2 {
		t.Fatal("both marked")
	}
	// A refresh where one hunk changed keeps the other's mark.
	cv.receive(diffMsg{projectID: "r1", branch: "feat", file: "a.go", diff: a4Diff("a.go", "one", "edited")})
	sel := cv.selection()
	if sel.hunks != 1 || !strings.Contains(sel.patch, "+one") || strings.Contains(sel.patch, "edited") {
		t.Fatalf("after a refresh: %+v\n%s", sel, sel.patch)
	}
	// A refresh that leaves no marked hunk forgets the file.
	cv.receive(diffMsg{projectID: "r1", branch: "feat", file: "a.go", diff: a4Diff("a.go", "changed", "edited")})
	if len(cv.hunks) != 0 || cv.selection().patch != "" {
		t.Fatalf("stale marks kept: %+v", cv.hunks)
	}
	// The cursor stays inside a diff that lost hunks.
	cv.hunkSel = 1
	cv.receive(diffMsg{projectID: "r1", branch: "feat", file: "a.go",
		diff: "--- a/a.go\n+++ b/a.go\n@@ -1,3 +1,3 @@\n context\n-old\n+only\n"})
	if cv.hunkSel != 0 {
		t.Fatalf("cursor %d", cv.hunkSel)
	}
	// Opening another file starts at its first hunk.
	cv.hunkSel = 0
	cv.key(m, a2Key("esc"))
	cv.sel = 1
	cv.key(m, a2Key("enter"))
	if cv.hunkSel != 0 || cv.diffFile != "b.go" {
		t.Fatalf("new diff: %q %d", cv.diffFile, cv.hunkSel)
	}
}

func TestHunkMarkingRefused(t *testing.T) {
	m, _ := harvestModel(t, harvestCapability, hunksCapability)
	// A branch that isn't checked out shows changes since the base, which
	// are already committed: there is nothing to mark.
	cv := &changesView{machine: localMachine, projectID: "r1", branch: "old"}
	cv.receive(changesMsg{projectID: "r1", branch: "old", data: proto.Changes{Files: []proto.FileChange{{Path: "a.go", Code: "M"}}}})
	cv.key(m, a2Key("enter"))
	cv.receive(diffMsg{projectID: "r1", branch: "old", file: "a.go", diff: a4Diff("a.go", "one", "two")})
	cv.key(m, a2Key(" "))
	if len(cv.hunks) != 0 || !strings.Contains(m.flash, "only a checked-out branch") {
		t.Fatalf("not checked out: %v %q", cv.hunks, m.flash)
	}
	if out := a2Plain(cv.renderDiff(*m, 80, 20)); strings.Contains(out, "space mark") || strings.Contains(out, "▸") {
		t.Fatalf("offered marking:\n%s", out)
	}

	// A binary file's diff has no hunks.
	cv2 := hunkView(t, m)
	cv2.receive(diffMsg{projectID: "r1", branch: "feat", file: "a.go", diff: "diff --git a/a.go b/a.go\nBinary files a/a.go and b/a.go differ\n"})
	m.flash = ""
	cv2.key(m, a2Key(" "))
	if len(cv2.hunks) != 0 || !strings.Contains(m.flash, "no hunks") {
		t.Fatalf("binary: %v %q", cv2.hunks, m.flash)
	}
}

func TestHunkCommit(t *testing.T) {
	m, peer := harvestModel(t, harvestCapability, hunksCapability)
	cv := hunkView(t, m)
	cv.key(m, a2Key(" "))
	m.tab().focused().changes = cv

	cv.key(m, a2Key("c"))
	d, ok := m.overlay.(*dialog)
	if !ok {
		t.Fatalf("no commit dialog: %T %q", m.overlay, m.flash)
	}
	if text := strings.Join(d.text, " "); text != "Commits the 1 hunk marked in 1 file in /src/api-feat. The rest of the changes, and the files themselves, stay as they are." {
		t.Fatalf("hunk text %q", text)
	}
	a2Type(m, d, "Just the first bit")
	peer.setResult(proto.MethodBranchCommit, proto.CommitResult{Hash: "9999999aaa"})
	msgs := submitDialog(t, m)
	p := lastParams[proto.BranchCommitParams](t, peer, proto.MethodBranchCommit)
	if !strings.Contains(p.Patch, "+one") || strings.Contains(p.Patch, "+two") || len(p.Files) != 0 || p.Message != "Just the first bit" {
		t.Fatalf("params %+v", p)
	}
	// The commit clears both kinds of mark.
	m.receiveHarvest(msgs[0].(harvestDoneMsg))
	if len(cv.hunks) != 0 || len(cv.marked) != 0 {
		t.Fatalf("marks after committing: %v %v", cv.hunks, cv.marked)
	}

	// Hunks and whole files together.
	cv.key(m, a2Key(" "))
	cv.key(m, a2Key("esc"))
	cv.sel = 1
	cv.key(m, a2Key(" ")) // mark b.go whole
	cv.sel = 0
	cv.key(m, a2Key("c"))
	d = m.overlay.(*dialog)
	if text := strings.Join(d.text, " "); !strings.Contains(text, "Commits the 1 hunk marked in 1 file, and 1 whole file in /src/api-feat: b.go.") ||
		!strings.Contains(text, "Anything already staged is committed too.") {
		t.Fatalf("mixed text %q", text)
	}
	a2Type(m, d, "Both")
	submitDialog(t, m)
	p = lastParams[proto.BranchCommitParams](t, peer, proto.MethodBranchCommit, `"Both"`)
	if p.Patch == "" || strings.Join(p.Files, ",") != "b.go" {
		t.Fatalf("mixed params %+v", p)
	}

	// An older server can't take a patch.
	old, _ := harvestModel(t, harvestCapability)
	cvOld := hunkView(t, old)
	cvOld.key(old, a2Key(" "))
	cvOld.key(old, a2Key("c"))
	if old.overlay != nil || !strings.Contains(old.flash, "predates committing single hunks") {
		t.Fatalf("old server: %T %q", old.overlay, old.flash)
	}
}

func TestHunkRender(t *testing.T) {
	m, _ := harvestModel(t, harvestCapability, hunksCapability)
	cv := hunkView(t, m)
	out := a2Plain(cv.renderDiff(*m, 80, 20))
	if !strings.Contains(out, "space mark · n next hunk · c commit") {
		t.Fatalf("hints:\n%s", out)
	}
	if !strings.Contains(out, "▸ @@ -1,3") || strings.Contains(out, "✓") {
		t.Fatalf("cursor:\n%s", out)
	}
	cv.key(m, a2Key(" "))
	out = a2Plain(cv.renderDiff(*m, 80, 20))
	if !strings.Contains(out, " ✓@@ -1,3") || !strings.Contains(out, "▸ @@ -20,3") || !strings.Contains(out, "1 of 2 hunks marked") {
		t.Fatalf("marked:\n%s", out)
	}
}
