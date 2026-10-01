package tui

import (
	"strings"
	"testing"
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
