package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// Each split's border ends with the ID of the pane in it — what conch's
// commands take — and a click on it copies it.
func TestSplitBorderNamesItsPane(t *testing.T) {
	m := splitModel()
	m.cursor = m.rows[0].id
	m.show(m.rows[0])
	m.split(splitRight, viewOf(m.rows[1]))
	screen := strings.Split(ansi.Strip(m.View()), "\n")

	find := func(ref string) (int, int) {
		for y, line := range screen {
			if i := strings.Index(line, " "+ref+" ─╮"); i >= 0 {
				return ansi.StringWidth(line[:i+1]), y
			}
		}
		t.Fatalf("no %s at the end of a border:\n%s", ref, strings.Join(screen[:4], "\n"))
		return 0, 0
	}
	for _, ref := range []string{"p1", "p2"} {
		x, y := find(ref)
		lastClipboard = ""
		_, cmd := m.handleMouse(tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
		if cmd == nil {
			t.Fatalf("a click on %s did nothing", ref)
		}
		cmd()
		if lastClipboard != ref {
			t.Fatalf("a click on %s copied %q", ref, lastClipboard)
		}
		// Beside it is still the border, which carries the split as before.
		lastClipboard = ""
		if _, cmd := m.handleMouse(tea.MouseMsg{X: x - 3, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}); cmd != nil {
			cmd()
		}
		if lastClipboard != "" {
			t.Fatalf("a click beside %s copied %q", ref, lastClipboard)
		}
	}
}

// The ID gives way to the title when a split is narrow, and the line is
// always exactly the frame's width.
func TestFrameRightLabel(t *testing.T) {
	for _, w := range []int{1, 5, 16, 17, 18, 30, 80} {
		lines := frameLinesLabeled(" claude · working · conch/pane-ids ", "p12", []string{"x"}, w, colorBorder, nil)
		top := ansi.Strip(lines[0])
		if got := ansi.StringWidth(top); got != w+2 {
			t.Errorf("w=%d: top is %d wide, want %d: %q", w, got, w+2, top)
		}
		has := strings.HasSuffix(top, " p12 ─╮")
		if want := frameRightAt(3, w) >= 0; has != want {
			t.Errorf("w=%d: label shown %v, want %v: %q", w, has, want, top)
		}
		if has && !strings.HasPrefix(top, "╭─ claude") {
			t.Errorf("w=%d: the title lost its start: %q", w, top)
		}
	}
	// No label: as before.
	if a, b := frameLinesLabeled(" t ", "", nil, 20, colorBorder, nil)[0], frameLinesBar(" t ", nil, 20, colorBorder, nil)[0]; a != b {
		t.Errorf("no label changed the frame: %q vs %q", a, b)
	}
}
