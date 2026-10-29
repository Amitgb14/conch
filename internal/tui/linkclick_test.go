package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amitgb14/conch/internal/proto"
)

func TestLinkUnderThePointer(t *testing.T) {
	lines := []string{
		"Open this to sign in: https://claude.ai/oauth?code=abc123 then come back",
		"nothing here at all",
		"│ https://example.com/wrapped/very/long/path/that/keeps/going/and/goin │",
		"│ g/until/here                                                        │",
	}
	// Anywhere inside the link finds the whole of it, from its start.
	for _, x := range []int{22, 30, 56} {
		if got := linkUnder(lines, x, 0); got != "https://claude.ai/oauth?code=abc123" {
			t.Fatalf("x=%d gave %q", x, got)
		}
	}
	// Outside it, nothing — the words around it are not links.
	for _, x := range []int{0, 5, 21, 60} {
		if got := linkUnder(lines, x, 0); got != "" {
			t.Fatalf("x=%d gave %q, want nothing", x, got)
		}
	}
	if got := linkUnder(lines, 4, 1); got != "" {
		t.Fatalf("a line with no link gave %q", got)
	}
	// A link wrapped over two rows comes back whole, borders and all gone.
	if got := linkUnder(lines, 10, 2); got != "https://example.com/wrapped/very/long/path/that/keeps/going/and/going/until/here" {
		t.Fatalf("a wrapped link gave %q", got)
	}
	// Off the screen, and off the end of a row.
	if got := linkUnder(lines, 5, 99); got != "" {
		t.Fatalf("a row that is not there gave %q", got)
	}
	if got := linkUnder(nil, 0, 0); got != "" {
		t.Fatalf("no lines gave %q", got)
	}
}

// linkEvent sends one mouse event at x,0 of a pane through paneMouse,
// the way the pointer reaches it.
func linkEvent(m *Model, pane string, x int, b tea.MouseButton, a tea.MouseAction, alt, ctrl bool) tea.Cmd {
	msg := tea.MouseMsg{X: x, Y: 0, Button: b, Action: a, Alt: alt, Ctrl: ctrl}
	press := a == tea.MouseActionPress
	wheel := b == tea.MouseButtonWheelUp || b == tea.MouseButtonWheelDown
	return m.paneMouse(localMachine, pane, msg, x, 0, press, wheel)
}

// linkOpened runs what a click handed back, then what the double-click
// window ends with, and says what was opened.
func linkOpened(t *testing.T, m *Model, cmd tea.Cmd) string {
	t.Helper()
	lastOpened, lastClipboard = "", ""
	for _, msg := range a2Run(cmd) {
		if open, ok := msg.(linkOpenMsg); ok {
			a2Run(m.openClickedLink(open))
		}
	}
	return lastOpened
}

func TestClickingALink(t *testing.T) {
	m, _ := a1Fixture(t, true)
	m.viewMachine, m.viewing = localMachine, "p1"
	plain := &proto.Frame{ID: "p1", Lines: []string{"go to https://example.com/x now"}}
	a2Frame(m, "p1", plain)
	left, press, release := tea.MouseButtonLeft, tea.MouseActionPress, tea.MouseActionRelease
	click := func(pane string, x int, alt, ctrl bool) string {
		linkEvent(m, pane, x, left, press, alt, ctrl)
		return linkOpened(t, m, linkEvent(m, pane, x, left, release, alt, ctrl))
	}

	// alt+click (option on a Mac) opens it and copies it — on the
	// release, once it is clear the press was a click.
	if got := click("p1", 10, true, false); got != "https://example.com/x" || lastClipboard != "https://example.com/x" {
		t.Fatalf("opened %q, copied %q", got, lastClipboard)
	}
	if !strings.Contains(m.flash, "opening https://example.com/x") {
		t.Fatalf("it said %q", m.flash)
	}
	m.linkQuiet = time.Time{} // as if the double-click window had passed

	// ctrl does too: some terminals keep alt+click for themselves.
	if got := click("p1", 10, false, true); got != "https://example.com/x" {
		t.Fatalf("ctrl+click opened %q", got)
	}
	m.linkQuiet = time.Time{}

	// A plain click never opens it: it is for selecting.
	if got := click("p1", 10, false, false); got != "" {
		t.Fatalf("a plain click opened %q", got)
	}
	m.linkQuiet = time.Time{}

	// An alt+click beside it is not a click on it.
	if got := click("p1", 2, true, false); got != "" {
		t.Fatalf("an alt+click on the words opened %q", got)
	}
	m.linkQuiet = time.Time{}

	// A right button and a wheel are not clicks on a link, alt or not.
	for _, b := range []tea.MouseButton{tea.MouseButtonRight, tea.MouseButtonWheelUp} {
		if cmd, took := m.linkMouse(localMachine, "p1", plain, tea.MouseMsg{X: 10, Button: b, Action: press, Alt: true}, 10, 0); took || cmd != nil || m.linkClick != nil {
			t.Fatalf("button %v took the click", b)
		}
	}
	// No frame at all.
	if _, took := m.linkMouse(localMachine, "p1", nil, tea.MouseMsg{X: 10, Button: left, Action: press, Alt: true}, 10, 0); took || m.linkClick != nil {
		t.Fatal("a pane with no screen took the click")
	}
	// A release with no press before it.
	if cmd, took := m.linkMouse(localMachine, "p1", plain, tea.MouseMsg{X: 10, Button: left, Action: release, Alt: true}, 10, 0); took || cmd != nil {
		t.Fatal("a release alone opened something")
	}

	// The frame is the pane under the pointer's, not the focused pane's.
	other := &proto.Frame{ID: "p2", Lines: []string{"see https://example.com/other for this one"}}
	a2Frame(m, "p2", other)
	if got := click("p2", 6, true, false); got != "https://example.com/other" {
		t.Fatalf("it opened %q, which is the focused pane's link", got)
	}
}

// TestDraggingOverALinkCopiesIt: alt or ctrl and a drag is how text is
// selected over a program that took the mouse, and the link is what
// people most want to copy. Opening on the press opened a browser at the
// start of every such drag.
func TestDraggingOverALinkCopiesIt(t *testing.T) {
	for _, mouse := range []bool{false, true} {
		m, _ := a1Fixture(t, true)
		m.viewMachine, m.viewing = localMachine, "p1"
		f := &proto.Frame{ID: "p1", Mouse: mouse, Lines: []string{"go to https://example.com/x now"}}
		a2Frame(m, "p1", f)
		left := tea.MouseButtonLeft
		lastOpened = ""

		if cmd := linkEvent(m, "p1", 6, left, tea.MouseActionPress, true, false); cmd != nil {
			if got := linkOpened(t, m, cmd); got != "" {
				t.Fatalf("mouse=%v: the press opened %q", mouse, got)
			}
		}
		if m.sel == nil {
			t.Fatalf("mouse=%v: the press began no selection", mouse)
		}
		linkEvent(m, "p1", 26, left, tea.MouseActionMotion, true, false)
		cmd := linkEvent(m, "p1", 26, left, tea.MouseActionRelease, true, false)
		if got := linkOpened(t, m, cmd); got != "" {
			t.Fatalf("mouse=%v: the drag opened %q", mouse, got)
		}
		if !strings.Contains(lastClipboard, "https://example.com/x") {
			t.Fatalf("mouse=%v: the drag copied %q", mouse, lastClipboard)
		}
		if m.linkClick != nil {
			t.Fatalf("mouse=%v: a link is still waiting to open", mouse)
		}

		// Pressed on the link and let go somewhere else, with nothing
		// dragged between: not a click on it either.
		m.linkQuiet = time.Time{}
		linkEvent(m, "p1", 10, left, tea.MouseActionPress, true, false)
		if cmd, took := m.linkMouse(localMachine, "p1", f, tea.MouseMsg{X: 11, Button: left, Action: tea.MouseActionRelease, Alt: true}, 11, 0); took || cmd != nil {
			t.Fatalf("mouse=%v: a release elsewhere opened the link", mouse)
		}
	}
}

// TestDoubleClickingALinkSelectsIt: a double click takes the word under
// it, and on a link that is the link — copied, not opened.
func TestDoubleClickingALinkSelectsIt(t *testing.T) {
	m, _ := a1Fixture(t, true)
	m.viewMachine, m.viewing = localMachine, "p1"
	a2Frame(m, "p1", &proto.Frame{ID: "p1", Lines: []string{"go to https://example.com/x now"}})
	left, press, release := tea.MouseButtonLeft, tea.MouseActionPress, tea.MouseActionRelease

	linkEvent(m, "p1", 10, left, press, true, false)
	first := linkEvent(m, "p1", 10, left, release, true, false) // the window starts
	linkEvent(m, "p1", 10, left, press, true, false)
	second := linkEvent(m, "p1", 10, left, release, true, false)
	// A third press, in the window still, is a triple click.
	linkEvent(m, "p1", 10, left, press, true, false)
	if m.linkClick != nil {
		t.Fatal("a triple click is waiting to open the link")
	}
	third := linkEvent(m, "p1", 10, left, release, true, false)
	// None of them opens it, and the first click's window ends with the
	// link not opened.
	for i, cmd := range []tea.Cmd{third, second, first} {
		if got := linkOpened(t, m, cmd); got != "" {
			t.Fatalf("click %d of three opened %q", 3-i, got)
		}
	}
}

// TestClickingALinkInAProgramThatTookTheMouse: the click was the link's,
// so the program is not sent it as well.
func TestClickingALinkInAProgramThatTookTheMouse(t *testing.T) {
	m, _ := a1Fixture(t, true)
	m.viewMachine, m.viewing = localMachine, "p1"
	a2Frame(m, "p1", &proto.Frame{ID: "p1", Mouse: true, Lines: []string{"go to https://example.com/x now"}})
	left := tea.MouseButtonLeft
	linkEvent(m, "p1", 10, left, tea.MouseActionPress, true, false)
	if m.click == nil {
		t.Fatal("the press did not wait to see whether it was a click")
	}
	cmd := linkEvent(m, "p1", 10, left, tea.MouseActionRelease, true, false)
	if m.click != nil || m.sel != nil {
		t.Fatalf("after the click: pending %v, selection %v", m.click, m.sel)
	}
	if got := linkOpened(t, m, cmd); got != "https://example.com/x" {
		t.Fatalf("opened %q", got)
	}
}
