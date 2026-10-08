package tui

import (
	"strconv"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/proto"
)

// The line a hunk starts changing, read from its header. Getting this
// wrong is worse than not opening the editor at all: it lands somebody on
// the wrong line of the right file and they trust it.
func TestHunkLine(t *testing.T) {
	for _, c := range []struct {
		what string
		hunk string
		want int
	}{
		{"a change at the top of the hunk", "@@ -1,3 +1,3 @@\n-old\n+new\n ctx", 1},
		{"context first, so the change is below it", "@@ -10,6 +12,6 @@\n ctx\n ctx\n-old\n+new", 14},
		{"a one-line hunk with no count", "@@ -5 +7 @@\n-old\n+new", 7},
		{"the function name git puts after the @@", "@@ -1,4 +20,4 @@ func main() {\n ctx\n-a\n+b", 21},
		{"an addition only", "@@ -0,0 +1,2 @@\n+a\n+b", 1},
	} {
		if got := hunkLine([]string{c.hunk}, 0); got != c.want {
			t.Errorf("%s: line %d, want %d", c.what, got, c.want)
		}
	}
	// Nothing to read gives nothing, rather than a line somebody would
	// trust: out of range, an empty list, and a header that is not one.
	for _, c := range []struct {
		what  string
		hunks []string
		i     int
	}{
		{"past the end", []string{"@@ -1 +1 @@"}, 5},
		{"before the start", []string{"@@ -1 +1 @@"}, -1},
		{"no hunks", nil, 0},
		{"not a header", []string{"just some text"}, 0},
		{"a header with no plus", []string{"@@ -1,2 @@"}, 0},
		{"a header with nonsense where the line goes", []string{"@@ -1 +x @@"}, 0},
		{"a line number of zero", []string{"@@ -1 +0 @@"}, 0},
	} {
		if got := hunkLine(c.hunks, c.i); got != 0 {
			t.Errorf("%s: gave line %d", c.what, got)
		}
	}
}

// The script that runs the editor. It asks the editor's own name for the
// flag because editors disagree and because $EDITOR belongs to the
// machine the worktree is on — which may not be this one.
func TestEditAtLineScriptPicksTheFlag(t *testing.T) {
	run := func(t *testing.T, editor string) string {
		t.Helper()
		// Run the script with a stand-in editor that prints what it got,
		// rather than running a real one.
		out := a4Shell(t, editor, "/work/main.go", "42")
		return strings.TrimSpace(out)
	}
	for _, c := range []struct{ editor, want string }{
		{"vim", "+42 /work/main.go"},
		{"nvim", "+42 /work/main.go"},
		{"nano", "+42 /work/main.go"},
		{"emacsclient", "+42 /work/main.go"},
		{"code", "--goto /work/main.go:42"},
		{"cursor", "--goto /work/main.go:42"},
		{"hx", "/work/main.go:42"},
		{"subl", "/work/main.go:42"},
		{"goland", "--line 42 /work/main.go"},
		// An editor nobody here has heard of opens the file and no more:
		// a flag it does not know would become a file called "+42".
		{"ed", "/work/main.go"},
		{"my-own-editor", "/work/main.go"},
	} {
		if got := run(t, c.editor); got != c.want {
			t.Errorf("%s was given %q, want %q", c.editor, got, c.want)
		}
	}
	// A full path to the editor is matched by its name, not its path.
	if got := run(t, "/usr/local/bin/nvim"); got != "+42 /work/main.go" {
		t.Errorf("a path: %q", got)
	}
	// An $EDITOR with its own flags keeps them, and is still recognised.
	if got := run(t, "code -w"); got != "-w --goto /work/main.go:42" {
		t.Errorf("an editor with flags: %q", got)
	}
}

// editAtHunk builds the pane that runs it: on the worktree's machine, in
// the worktree, at the selected hunk's line.
func TestEditAtHunkOpensAPane(t *testing.T) {
	a2Isolate(t)
	m := a2Model()
	c, peer := a1FakeClient(t, "pane.v1")
	m.machines[0].c = c
	cv := &changesView{machine: localMachine, projectID: "r1", branch: "feat", diffFile: "src/main.go",
		data: &proto.Changes{Worktree: "/work/feat"},
		diff: []string{"@@ -1,2 +1,2 @@", " ctx", "-old", "+new"},
	}
	a2Run(cv.editAtHunk(m))
	msg := peer.waitMethod(t, proto.MethodPaneCreate, "")
	params := string(msg.Params)
	for _, want := range []string{`"cwd":"/work/feat"`, "/work/feat/src/main.go", "VISUAL", "edit · main.go"} {
		if !strings.Contains(params, want) {
			t.Errorf("the pane does not carry %q: %s", want, params)
		}
	}
	// The line is the hunk's first change, not the hunk's first line: one
	// line of context sits above it.
	if !strings.Contains(params, `"`+strconv.Itoa(2)+`"`) {
		t.Errorf("the line it opens at: %s", params)
	}

	// A branch with no worktree has no file to open, and says so rather
	// than starting an editor on a path that is not there.
	cv.data = &proto.Changes{}
	if cmd := cv.editAtHunk(m); cmd != nil {
		t.Error("it opened an editor for a branch with no worktree")
	}
	if !strings.Contains(m.flash, "no worktree") {
		t.Errorf("flash %q", m.flash)
	}
	// And nothing at all to open is nothing to do.
	cv.diffFile = ""
	if cmd := cv.editAtHunk(m); cmd != nil {
		t.Error("it opened an editor with no file")
	}
}
