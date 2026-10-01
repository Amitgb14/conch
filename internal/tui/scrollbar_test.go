package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
)

func TestScrollThumbAndOffset(t *testing.T) {
	// Nothing to scroll, no thumb — and the sizes a small pane gives.
	if _, size := scrollThumb(nil, 20); size != 0 {
		t.Fatal("a thumb with no frame")
	}
	if _, size := scrollThumb(&proto.Frame{History: 0}, 20); size != 0 {
		t.Fatal("a thumb with no history")
	}
	if _, size := scrollThumb(&proto.Frame{History: 100}, 2); size != 0 {
		t.Fatal("a thumb in a pane too short to draw one")
	}
	f := &proto.Frame{History: 100, Offset: 0}
	from, size := scrollThumb(f, 20)
	if size < 1 || from+size != 20 {
		t.Fatalf("live: thumb %d..%d of 20", from, from+size)
	}
	f.Offset = 100
	if from, _ := scrollThumb(f, 20); from != 0 {
		t.Fatalf("fully back: thumb starts at %d", from)
	}
	f.Offset = 50
	mid, _ := scrollThumb(f, 20)
	if mid == 0 || mid+size == 20 {
		t.Fatalf("half way: thumb at %d", mid)
	}
	// A history far longer than the pane still leaves a thumb to grab.
	if _, size := scrollThumb(&proto.Frame{History: 100000}, 20); size < 1 {
		t.Fatal("a huge history left nothing to grab")
	}

	// Rows map to offsets: the top is as far back as it goes, the bottom live.
	f = &proto.Frame{History: 100}
	if got := offsetAt(f, 20, 0); got != 100 {
		t.Fatalf("top row: %d", got)
	}
	if got := offsetAt(f, 20, 19); got != 0 {
		t.Fatalf("bottom row: %d", got)
	}
	if got := offsetAt(f, 20, -5); got != 100 {
		t.Fatalf("above the track: %d", got)
	}
	if got := offsetAt(f, 20, 99); got != 0 {
		t.Fatalf("below the track: %d", got)
	}
	if got := offsetAt(nil, 20, 3); got != 0 {
		t.Fatalf("no frame: %d", got)
	}
}

func TestScrollbarDrawnAndDragged(t *testing.T) {
	m, _ := a1Fixture(t, false)
	a1Open(t, m, paneNodeID(localMachine, "p1"))
	key := paneKey(localMachine, "p1")
	m.viewMachine, m.viewing = localMachine, "p1"
	m.frames[key] = &proto.Frame{ID: "p1", History: 200, Offset: 0, Lines: []string{"x"}}
	m.frame = m.frames[key]

	// The bar is drawn on the right border of a pane with history.
	if mark := m.scrollbarMark(viewRef{Kind: kindPane, Machine: localMachine, PaneID: "p1"}, 20); len(mark) == 0 {
		t.Fatal("no thumb on a pane with history")
	}
	// A branch's changes are not a pane, and a pane with no history has none.
	if mark := m.scrollbarMark(viewRef{Kind: kindBranch, Machine: localMachine, Branch: "feat"}, 20); mark != nil {
		t.Fatal("a thumb on something that is not a pane")
	}
	m.frames[key].History = 0
	if mark := m.scrollbarMark(viewRef{Kind: kindPane, Machine: localMachine, PaneID: "p1"}, 20); mark != nil {
		t.Fatal("a thumb with nothing to scroll")
	}
	m.frames[key].History = 200

	// Pressing the border grabs it, and the drag owns the mouse until it
	// is let go.
	rects, _ := m.leafRects()
	id := m.tab().root.leaves()[0].id
	r := rects[id]
	a1Mouse(t, m, r.x+r.w-1, r.y+1, a1Left, a1Press)
	if m.scrollDrag != id {
		t.Fatalf("the border did not grab: %d", m.scrollDrag)
	}
	a1Mouse(t, m, r.x+r.w-1, r.y+r.h-2, a1Left, a1Release)
	if m.scrollDrag != 0 {
		t.Fatal("the drag outlived the release")
	}
	// A press inside the pane is not the bar.
	in := m.inner(r)
	a1Mouse(t, m, in.x+1, in.y+1, a1Left, a1Press)
	if m.scrollDrag != 0 {
		t.Fatal("a click inside the pane grabbed the bar")
	}
}

// TestThumbIsOneCellInAnyFont guards the bug that scattered a whole screen:
// the thumb was █, whose width is Ambiguous — one cell by measurement, two
// in some fonts — so every line carrying it pushed the frame out a column.
// Whatever it is drawn with must measure one cell and be no glyph at all.
func TestThumbIsOneCellInAnyFont(t *testing.T) {
	applyTheme("conch", "")
	m, _ := a1Fixture(t, false)
	m.frames[paneKey(localMachine, "p1")] = &proto.Frame{ID: "p1", History: 300}
	mark := m.scrollbarMark(viewRef{Kind: kindPane, Machine: localMachine, PaneID: "p1"}, 20)
	if len(mark) == 0 {
		t.Fatal("no thumb to check")
	}
	for i, g := range mark {
		if w := ansi.StringWidth(g); w != 1 {
			t.Errorf("row %d: the thumb measures %d cells: %q", i, w, g)
		}
		for _, r := range ansi.Strip(g) {
			if r != ' ' {
				t.Errorf("row %d: the thumb draws %q — a glyph a font may widen", i, string(r))
			}
		}
	}
}

func TestFrameLinesBarDrawsTheThumb(t *testing.T) {
	lines := frameLinesBar(" title ", []string{"a", "b", "c"}, 10, colorBorder,
		map[int]string{1: styleThumb.Render(" ")})
	if len(lines) != 5 {
		t.Fatalf("lines: %d", len(lines))
	}
	if strings.HasSuffix(ansi.Strip(lines[2]), "│") {
		t.Fatalf("the marked row still draws the border: %q", ansi.Strip(lines[2]))
	}
	if !strings.HasSuffix(ansi.Strip(lines[1]), "│") || !strings.HasSuffix(ansi.Strip(lines[3]), "│") {
		t.Fatal("the border was lost where there is no thumb")
	}
	// Every line is the same width, thumb or not.
	w := ansi.StringWidth(lines[0])
	for i, l := range lines {
		if got := ansi.StringWidth(l); got != w {
			t.Fatalf("line %d is %d cells, want %d", i, got, w)
		}
	}
	// Without marks it is the plain frame.
	plain := frameLines(" title ", []string{"a"}, 10, colorBorder)
	if ansi.Strip(plain[1]) != ansi.Strip(frameLinesBar(" title ", []string{"a"}, 10, colorBorder, nil)[1]) {
		t.Fatal("frameLines and frameLinesBar disagree")
	}
	_ = tea.MouseMsg{}
}
