package pane

import (
	"strings"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
)

// startPrinter runs script in a 40x8 pane and waits for its marker, which
// the script prints last.
func startPrinter(t *testing.T, script, marker string) *Pane {
	t.Helper()
	p, err := Start(Options{ID: "s1", Command: []string{"/bin/sh", "-c", script + "; sleep 30"}, Env: []string{"ENV="}, Cols: 40, Rows: 8})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	waitScreen(t, p, marker)
	return p
}

// forty prints line-1 … line-40, so most of them are history in an 8-row
// pane, then a marker.
const forty = `i=1; while [ $i -le 40 ]; do echo "line-$i"; i=$((i+1)); done; echo Mark-End`

// lineText is the text of line i as Search counts lines.
func lineText(t *testing.T, p *Pane, i int) string {
	t.Helper()
	lines, _ := p.textLines()
	if i < 0 || i >= len(lines) {
		t.Fatalf("line %d outside %d lines", i, len(lines))
	}
	return lines[i]
}

func TestSearchFindsHistoryBackward(t *testing.T) {
	p := startPrinter(t, forty, "Mark-End")
	lines, history := p.textLines()
	if history == 0 {
		t.Fatal("expected history")
	}
	// From the bottom, backward: the nearest line-3x is line-39.
	r := p.Search("line-3", len(lines), 0, true)
	if !r.Found || r.Wrapped || lineText(t, p, r.Line) != "line-39" || r.Col != 0 || r.Width != 6 || r.History != history {
		t.Fatalf("got %+v (%q)", r, lineText(t, p, r.Line))
	}
	// Again from there: line-38, and on to line-3 itself.
	r = p.Search("line-3", r.Line, r.Col, true)
	if lineText(t, p, r.Line) != "line-38" {
		t.Fatalf("next = %q", lineText(t, p, r.Line))
	}
	r = p.Search("line-1", 0, 0, true) // nothing above the first line: round to the bottom
	if !r.Found || !r.Wrapped || lineText(t, p, r.Line) != "line-19" {
		t.Fatalf("wrap backward = %+v (%q)", r, lineText(t, p, r.Line))
	}
}

func TestSearchForwardAndWrap(t *testing.T) {
	p := startPrinter(t, forty, "Mark-End")
	r := p.Search("line-4", -1, 0, false) // before the first line: the oldest match
	if !r.Found || r.Wrapped || lineText(t, p, r.Line) != "line-4" {
		t.Fatalf("got %+v (%q)", r, lineText(t, p, r.Line))
	}
	r = p.Search("line-4", r.Line, r.Col, false)
	if lineText(t, p, r.Line) != "line-40" || r.Wrapped {
		t.Fatalf("next = %+v (%q)", r, lineText(t, p, r.Line))
	}
	r = p.Search("line-4", r.Line, r.Col, false) // past the last: round to the top
	if lineText(t, p, r.Line) != "line-4" || !r.Wrapped {
		t.Fatalf("wrap = %+v (%q)", r, lineText(t, p, r.Line))
	}
}

func TestSearchSmartCase(t *testing.T) {
	p := startPrinter(t, `echo "one ERROR two error"; echo Mark-End`, "Mark-End")
	lines, _ := p.textLines()
	// Lower case matches either: the last on the line going backward.
	r := p.Search("error", len(lines), 0, true)
	if !r.Found || r.Col != 14 {
		t.Fatalf("lower = %+v", r)
	}
	r = p.Search("error", r.Line, r.Col, true)
	if !r.Found || r.Col != 4 || r.Wrapped {
		t.Fatalf("lower, previous = %+v", r)
	}
	// A capital asks for that case exactly.
	r = p.Search("Error", len(lines), 0, true)
	if r.Found {
		t.Fatalf("Error matched %+v", r)
	}
	r = p.Search("ERROR", len(lines), 0, true)
	if !r.Found || r.Col != 4 {
		t.Fatalf("ERROR = %+v", r)
	}
}

func TestSearchOnlyMatchComesRound(t *testing.T) {
	p := startPrinter(t, `echo "unique-word"; echo Mark-End`, "Mark-End")
	lines, _ := p.textLines()
	r := p.Search("unique", len(lines), 0, true)
	if !r.Found {
		t.Fatal("not found")
	}
	again := p.Search("unique", r.Line, r.Col, true)
	if !again.Found || !again.Wrapped || again.Line != r.Line || again.Col != r.Col {
		t.Fatalf("the one match should come round again: %+v then %+v", r, again)
	}
}

func TestSearchNothing(t *testing.T) {
	p := startPrinter(t, `echo hello; echo Mark-End`, "Mark-End")
	for _, q := range []string{"", "absent-text"} {
		if r := p.Search(q, 0, 0, true); r.Found || r.Wrapped {
			t.Fatalf("%q: %+v", q, r)
		}
	}
	// Longer than any line.
	if r := p.Search(strings.Repeat("x", 100), 0, 0, false); r.Found {
		t.Fatalf("overlong: %+v", r)
	}
}

func TestSearchWideCharactersCountCells(t *testing.T) {
	p := startPrinter(t, `echo "日本 target"; echo Mark-End`, "Mark-End")
	lines, _ := p.textLines()
	r := p.Search("target", len(lines), 0, true)
	if !r.Found || r.Col != 5 { // two wide characters, then a space
		t.Fatalf("got %+v; line %q", r, lineText(t, p, r.Line))
	}
	r = p.Search("本", len(lines), 0, true)
	if !r.Found || r.Col != 2 || r.Width != 2 {
		t.Fatalf("wide query: %+v", r)
	}
}

func TestSearchAlternateScreenHistory(t *testing.T) {
	// A full-screen program that scrolls: what went off the top is kept
	// (altscroll.go), and search reaches it. The pause keeps the switch to
	// the alternate screen out of the read that scrolls it (see
	// TestAltScrollKeepsLinesFromTheWriteThatEntersIt).
	p := startPrinter(t, `printf '\033[?1049h'; sleep 0.3; i=1; while [ $i -le 30 ]; do echo "alt-$i"; i=$((i+1)); done; echo Alt-End`, "Alt-End")
	deadline := time.Now().Add(5 * time.Second)
	for p.History() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	lines, history := p.textLines()
	if history == 0 {
		t.Fatal("no history kept on the alternate screen")
	}
	r := p.Search("alt-2", len(lines), 0, true)
	if !r.Found || lineText(t, p, r.Line) != "alt-29" {
		t.Fatalf("got %+v", r)
	}
	r = p.Search("alt-1", 0, -1, false)
	if !r.Found || r.Line >= history {
		t.Fatalf("alt-1 should be in the kept history: %+v (history %d)", r, history)
	}
}

func TestCellText(t *testing.T) {
	wide := &uv.Cell{Content: "日", Width: 2}
	row := []*uv.Cell{wide, {}, {Content: "a", Width: 1}, nil, {Content: "b", Width: 1}, {}, nil}
	got := cellText(len(row), func(x int) *uv.Cell { return row[x] })
	if got != "日a b" {
		t.Fatalf("got %q", got)
	}
	if got := cellText(0, nil); got != "" {
		t.Fatalf("empty row = %q", got)
	}
	blank := cellText(3, func(int) *uv.Cell { return nil })
	if blank != "" {
		t.Fatalf("blank row = %q", blank)
	}
}
