package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/Amitgb14/conch/internal/proto"
)

func a6Motion(m *Model, x, y int) {
	next, _ := m.handleMouse(tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonNone, Action: tea.MouseActionMotion})
	*m = next.(Model)
}

func TestHoverOnlyWhenAskedFor(t *testing.T) {
	// Colours are off without a terminal, and the tint is the point here.
	was := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	applyTheme("conch", "")
	t.Cleanup(func() { lipgloss.SetColorProfile(was); applyTheme("conch", "") })
	m, _ := a1Fixture(t, false)
	m.hoverTab = -1

	// Off by default: a bare motion changes nothing at all.
	a6Motion(m, 3, 4)
	if m.hoverRow != "" || m.hovering(m.rows[2].id) {
		t.Fatalf("hover without asking: %q", m.hoverRow)
	}
	// On: the row under the pointer is remembered and drawn with a tint.
	m.cfg.UI.Hover = true
	a6Motion(m, 3, 4)
	want := m.rows[m.scroll+4-2].id
	if m.hoverRow != want {
		t.Fatalf("hovered %q, want %q", m.hoverRow, want)
	}
	hovered := m.rowLine(m.rows[m.scroll+2], 40)
	m.hoverRow = ""
	plain := m.rowLine(m.rows[m.scroll+2], 40)
	if hovered == plain || ansi.Strip(hovered) != ansi.Strip(plain) {
		t.Fatalf("the tint is missing or moved the text:\n%q\n%q", hovered, plain)
	}
	m.hoverRow = want
	// Moving within the same row says nothing changed; a new row does.
	if m.hoverAt(tea.MouseMsg{X: 5, Y: 4, Button: tea.MouseButtonNone, Action: tea.MouseActionMotion}) {
		t.Fatal("the same row counted as a change")
	}
	if !m.hoverAt(tea.MouseMsg{X: 5, Y: 5, Button: tea.MouseButtonNone, Action: tea.MouseActionMotion}) {
		t.Fatal("another row was not noticed")
	}
	// Above the rows (the header and the border) nothing is hovered.
	a6Motion(m, 3, 0)
	if m.hoverRow != "" {
		t.Fatalf("the header hovered %q", m.hoverRow)
	}
	// A tab under the pointer is remembered instead.
	a1Open(t, m, paneNodeID(localMachine, "p1"))
	mr := m.mainRect()
	_, hits := m.tabBar(mr.w)
	a6Motion(m, mr.x+hits[0].x0, mr.y)
	if m.hoverTab != hits[0].tab {
		t.Fatalf("hovered tab %d, want %d", m.hoverTab, hits[0].tab)
	}
	// An overlay takes everything: nothing is left hovered under it.
	m.overlay = newConfirm("really?", nil)
	a6Motion(m, 3, 4)
	if m.hoverRow != "" || m.hoverTab != -1 {
		t.Fatalf("hover survived a dialog: %q %d", m.hoverRow, m.hoverTab)
	}
}

// TestHoverNeverReachesAProgram is the rule that matters: a program in a
// pane asked for the mouse it had, not the one hover turns on.
func TestHoverNeverReachesAProgram(t *testing.T) {
	m, peer := a1Fixture(t, true)
	m.cfg.UI.Hover = true
	m.hoverTab = -1
	a1Open(t, m, paneNodeID(localMachine, "p1"))
	m.viewMachine, m.viewing = localMachine, "p1"
	m.frames[paneKey(localMachine, "p1")] = &proto.Frame{ID: "p1", Mouse: true, Lines: []string{"x"}}
	m.frame = m.frames[paneKey(localMachine, "p1")]
	rects, _ := m.leafRects()
	in := m.inner(rects[m.tab().root.leaves()[0].id])

	mouseCalls := func() int {
		n := 0
		for _, meth := range peer.methods() {
			if meth == proto.MethodPaneSendMouse {
				n++
			}
		}
		return n
	}
	before := mouseCalls()
	for i := 0; i < 5; i++ {
		a6Motion(m, in.x+i, in.y+1)
	}
	if got := mouseCalls(); got != before {
		t.Fatalf("a bare motion was sent to the program: %d mouse calls, was %d", got, before)
	}
	// A drag still reaches it, as it always did.
	next, _ := m.handleMouse(tea.MouseMsg{X: in.x, Y: in.y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	*m = next.(Model)
	if m.sel == nil && m.click == nil {
		t.Fatal("a press in a pane did nothing")
	}
}
