package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
)

// TestFilesSelectionKeepsToItsColumn: the explorer draws the list beside
// the preview, so a selection dragged through a file used to take the
// list's rows with it — pasting a file's text brought the file names too.
func TestFilesSelectionKeepsToItsColumn(t *testing.T) {
	fv := &filesView{wide: true, treeW: 30}

	// A drag that starts in the preview keeps to the preview.
	from, to := fv.columns(40, 100)
	if from != 30 || to != 100 {
		t.Fatalf("in the preview: %d..%d", from, to)
	}
	// One that starts in the list keeps to the list.
	if from, to := fv.columns(5, 100); from != 0 || to != 30 {
		t.Fatalf("in the list: %d..%d", from, to)
	}
	// Narrow: one column, so no bound at all.
	narrow := &filesView{wide: false, treeW: 30}
	if from, to := narrow.columns(40, 100); from != 0 || to != 0 {
		t.Fatalf("narrow: %d..%d", from, to)
	}
	// A width that makes no sense is no bound rather than an empty one.
	odd := &filesView{wide: true, treeW: 200}
	if from, to := odd.columns(10, 100); from != 0 || to != 0 {
		t.Fatalf("a tree wider than the window: %d..%d", from, to)
	}

	// And what that does to the text: two columns on every row, a drag
	// down the preview takes only the preview.
	lines := []string{
		"src/main.go                   package main",
		"src/util.go                   func main() {",
		"README.md                     }",
	}
	s := selection{leaf: 1, ax: 30, ay: 0, bx: 99, by: 2, colFrom: 30, colTo: 100}
	got := s.text(lines, 100)
	if strings.Contains(got, "src/main.go") || strings.Contains(got, "README.md") {
		t.Fatalf("the list came with it:\n%s", got)
	}
	if got != "package main\nfunc main() {\n}" {
		t.Fatalf("the preview copied as:\n%q", got)
	}
	// Without the bound, the old behaviour: everything on the row.
	plain := selection{leaf: 1, ax: 30, ay: 0, bx: 99, by: 2}
	if !strings.Contains(plain.text(lines, 100), "package main") {
		t.Fatal("an unbounded selection lost the preview")
	}
	// A selection in the list column keeps to the list.
	list := selection{leaf: 1, ax: 0, ay: 0, bx: 29, by: 1, colFrom: 0, colTo: 30}
	if got := list.text(lines, 100); got != "src/main.go\nsrc/util.go" {
		t.Fatalf("the list copied as %q", got)
	}
}

// TestFilesCopyLeavesTheLineNumbers: the preview numbers every line it
// draws. A drag down several of them took the numbers and the column rule
// with it — only the first row starts where the pointer did — so a command
// read out of a file pasted as "  12  go test" and ran nothing.
func TestFilesCopyLeavesTheLineNumbers(t *testing.T) {
	m, _, fv := filesFixture(t)
	filesSelect(t, fv, "main.go")
	fv.prev = filesPreview{rel: "main.go", res: &proto.FSReadResult{
		Data: "go test -race ./...\n  indented line\nlast\n"}}
	const w, h = 120, 20
	lines := fv.render(*m, w, h)

	y := -1
	for i, l := range lines {
		if strings.Contains(ansi.Strip(l), "go test -race ./...") {
			y = i
			break
		}
	}
	if y < 0 {
		t.Fatalf("the preview did not draw the file:\n%s", strings.Join(lines, "\n"))
	}
	st := ansi.Strip(lines[y])
	x := ansi.StringWidth(st[:strings.Index(st, "go test")]) // columns, not bytes
	from, to := fv.columns(x, w)
	s := selection{ax: x, ay: y, bx: w - 1, by: y + 2, colFrom: from, colTo: to}
	want := "go test -race ./...\n  indented line\nlast"
	if got := s.text(lines, w); got != want {
		t.Fatalf("copied\n %q\nwant\n %q", got, want)
	}

	// A drag that starts on the numbers takes them: somebody quoting a
	// file with its line numbers still can.
	if from, _ := fv.columns(fv.treeW+1, w); from != fv.treeW {
		t.Fatalf("started on the numbers: from %d, want %d", from, fv.treeW)
	}

	// Ten lines or more widen the gutter, and the bound follows it.
	narrow := fv.prevGutter
	fv.prev = filesPreview{rel: "main.go", res: &proto.FSReadResult{Data: strings.Repeat("x\n", 12)}}
	fv.render(*m, w, h)
	if fv.prevGutter != narrow+1 {
		t.Fatalf("gutter for 12 lines: %d, want %d", fv.prevGutter, narrow+1)
	}
	if from, _ := fv.columns(w-1, w); from != fv.treeW+1+fv.prevGutter {
		t.Fatalf("wide gutter: from %d, want %d", from, fv.treeW+1+fv.prevGutter)
	}

	// Nothing numbered — a binary, an unreadable file, an empty one — is
	// bounded to the preview as before, with no gutter to skip.
	for _, p := range []filesPreview{
		{rel: "main.go", res: &proto.FSReadResult{Binary: true, MIME: "image/png"}},
		{rel: "main.go", err: "permission denied"},
		{rel: "main.go", res: &proto.FSReadResult{}},
	} {
		fv.prev = p
		fv.render(*m, w, h)
		if fv.prevGutter != 0 {
			t.Fatalf("%+v drew a gutter of %d", p, fv.prevGutter)
		}
		if from, to := fv.columns(w-1, w); from != fv.treeW || to != w {
			t.Fatalf("%+v: %d..%d", p, from, to)
		}
	}
}
