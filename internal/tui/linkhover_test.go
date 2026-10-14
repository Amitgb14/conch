package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/Amitgb14/conch/internal/proto"
)

// TestLinkUnderThePointerLightsUp: a link is clickable, and nothing on a
// terminal says so until you have clicked. With [ui] hover on, the one the
// pointer is over is underlined in the accent while alt is held — when an
// alt+click would open it — so it is visibly the thing a click acts on.
func TestLinkUnderThePointerLightsUp(t *testing.T) {
	a2Isolate(t)
	was := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	applyTheme("conch", "")
	t.Cleanup(func() { lipgloss.SetColorProfile(was); applyTheme("conch", "") })
	m := a2Model()
	m.cfg.UI.Hover = true
	m.width, m.height = 120, 40
	line := "see https://example.com/x for this"
	a2Frame(m, "p1", &proto.Frame{ID: "p1", Lines: []string{line}})
	m.rebuild()
	a1Open(t, m, paneNodeID(localMachine, "p1"))
	m.syncView()

	// Where the pane's cells are on screen, so the pointer can be put on
	// the link rather than at a guess.
	rects, _ := m.leafRects()
	id := m.tab().focus
	in := m.inner(rects[id])
	at := func(col int) tea.MouseMsg {
		return tea.MouseMsg{X: in.x + col, Y: in.y, Action: tea.MouseActionMotion, Button: tea.MouseButtonNone, Alt: true}
	}

	// On the words: nothing lights.
	m.hoverAt(at(1))
	if m.hoverLink.pane != "" {
		t.Fatalf("the words lit: %+v", m.hoverLink)
	}
	drawn := m.leafLines(m.tab().focused(), in.w, in.h, true)
	if strings.Contains(drawn[0], styleLink.Render("https://example.com/x")) {
		t.Errorf("something is lit already: %q", drawn[0])
	}

	// On the link: it lights, and only its own columns.
	if !m.hoverAt(at(8)) {
		t.Fatal("moving onto a link changed nothing to draw")
	}
	h := m.hoverLink
	if h.pane != "p1" || h.machine != localMachine || h.y != 0 {
		t.Fatalf("hover %+v", h)
	}
	if h.from != 4 || h.to != 4+len("https://example.com/x") {
		t.Fatalf("it covers columns %d..%d, want the link's own", h.from, h.to)
	}
	drawn = m.leafLines(m.tab().focused(), in.w, in.h, true)
	if !strings.Contains(drawn[0], styleLink.Render("https://example.com/x")) {
		t.Fatalf("the link is not lit: %q", drawn[0])
	}
	if got := strings.TrimRight(ansi.Strip(drawn[0]), " "); got != line {
		t.Fatalf("lighting it changed the text: %q", got)
	}

	// Off the link again: it goes out, and that is worth a redraw.
	if !m.hoverAt(at(1)) {
		t.Fatal("moving off a link changed nothing to draw")
	}
	if m.hoverLink.pane != "" {
		t.Fatalf("it stayed lit: %+v", m.hoverLink)
	}
}

// TestLinkHoverOnlyWithAlt: a plain click opens no link, in a pane whose
// program took the mouse or one whose did not — so a link is not lit
// without alt or ctrl either, or conch would be promising something a
// click will not do. With either held the click *would* open it, so it
// lights, and letting go puts it out at the next move.
func TestLinkHoverOnlyWithAlt(t *testing.T) {
	for _, takesMouse := range []bool{false, true} {
		a2Isolate(t)
		m := a2Model()
		m.cfg.UI.Hover = true
		m.width, m.height = 120, 40
		a2Frame(m, "p1", &proto.Frame{ID: "p1", Mouse: takesMouse, Lines: []string{"see https://example.com/x for this"}})
		m.rebuild()
		a1Open(t, m, paneNodeID(localMachine, "p1"))
		m.syncView()
		rects, _ := m.leafRects()
		in := m.inner(rects[m.tab().focus])

		plain := tea.MouseMsg{X: in.x + 8, Y: in.y, Action: tea.MouseActionMotion, Button: tea.MouseButtonNone}
		m.hoverAt(plain)
		if m.hoverLink.pane != "" || m.pointer != pointerArrow {
			t.Fatalf("mouse=%v: it lit a link a plain click would not open (pointer %v)", takesMouse, m.pointer)
		}
		for _, held := range []tea.MouseMsg{{Alt: true}, {Ctrl: true}} {
			msg := plain
			msg.Alt, msg.Ctrl = held.Alt, held.Ctrl
			m.hoverAt(msg)
			if m.hoverLink.pane != "p1" || m.pointer != pointerHand {
				t.Fatalf("mouse=%v alt=%v ctrl=%v: a click opens it, so it should light (pointer %v)",
					takesMouse, held.Alt, held.Ctrl, m.pointer)
			}
			m.hoverAt(plain)
			if m.hoverLink.pane != "" || m.pointer != pointerArrow {
				t.Fatalf("mouse=%v: let go of the key, it stayed lit", takesMouse)
			}
		}
	}
}

// TestLinkHoverIsOffWithoutHover: hover costs a message per cell crossed,
// which is why it is a setting; with it off nothing is tracked and nothing
// is drawn differently.
func TestLinkHoverIsOffWithoutHover(t *testing.T) {
	a2Isolate(t)
	m := a2Model()
	m.cfg.UI.Hover = false
	m.width, m.height = 120, 40
	a2Frame(m, "p1", &proto.Frame{ID: "p1", Lines: []string{"see https://example.com/x for this"}})
	m.rebuild()
	a1Open(t, m, paneNodeID(localMachine, "p1"))
	m.syncView()
	rects, _ := m.leafRects()
	in := m.inner(rects[m.tab().focus])
	m.hoverAt(tea.MouseMsg{X: in.x + 8, Y: in.y, Action: tea.MouseActionMotion, Button: tea.MouseButtonNone})
	if m.hoverLink.pane != "" {
		t.Fatalf("hover is off, but it tracked %+v", m.hoverLink)
	}
}
