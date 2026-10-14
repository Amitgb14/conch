package tui

import (
	"strings"
	"testing"

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

func TestClickingALink(t *testing.T) {
	m, _ := a1Fixture(t, false)
	m.viewMachine, m.viewing = localMachine, "p1"
	plain := &proto.Frame{ID: "p1", Lines: []string{"go to https://example.com/x now"}}
	takesMouse := &proto.Frame{ID: "p1", Mouse: true, Lines: plain.Lines}
	a2Frame(m, "p1", plain)
	press := func(x int, alt bool) (tea.Cmd, bool) {
		return m.clickedLink(plain, tea.MouseMsg{X: x, Y: 0, Button: tea.MouseButtonLeft,
			Action: tea.MouseActionPress, Alt: alt}, x, 0)
	}

	// A plain click never opens a link, in a pane whose program takes no
	// mouse or one whose does: it is for selecting, or the program's.
	// Opening a browser on a click aimed at the words beside a link was
	// what people did not want.
	for _, f := range []*proto.Frame{plain, takesMouse} {
		if _, took := m.clickedLink(f, tea.MouseMsg{X: 10, Y: 0, Button: tea.MouseButtonLeft,
			Action: tea.MouseActionPress}, 10, 0); took {
			t.Fatalf("a plain click opened the link (mouse=%v)", f.Mouse)
		}
	}

	// alt+click (option on a Mac) opens it and copies it, in either.
	lastOpened, lastClipboard = "", ""
	cmd, took := press(10, true)
	if !took || cmd == nil {
		t.Fatal("alt+click on a link did nothing")
	}
	a2Run(cmd)
	if lastOpened != "https://example.com/x" || lastClipboard != "https://example.com/x" {
		t.Fatalf("opened %q, copied %q", lastOpened, lastClipboard)
	}
	if !strings.Contains(m.flash, "opening https://example.com/x") {
		t.Fatalf("it said %q", m.flash)
	}
	if _, took := m.clickedLink(takesMouse, tea.MouseMsg{X: 10, Y: 0, Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress, Alt: true}, 10, 0); !took {
		t.Fatal("alt+click did not open the link in a program that takes the mouse")
	}
	// ctrl does too: some terminals keep alt+click for themselves.
	if _, took := m.clickedLink(plain, tea.MouseMsg{X: 10, Y: 0, Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress, Ctrl: true}, 10, 0); !took {
		t.Fatal("ctrl+click did not open the link")
	}
	// An alt+click beside it is not a click on it.
	if _, took := press(2, true); took {
		t.Fatal("an alt+click on the words took the click")
	}
	// A release, a right button and a wheel are not clicks on a link,
	// alt or not.
	for _, msg := range []tea.MouseMsg{
		{X: 10, Y: 0, Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease, Alt: true},
		{X: 10, Y: 0, Button: tea.MouseButtonRight, Action: tea.MouseActionPress, Alt: true},
		{X: 10, Y: 0, Button: tea.MouseButtonWheelUp, Alt: true},
	} {
		if _, took := m.clickedLink(plain, msg, 10, 0); took {
			t.Fatalf("%v took the click", msg.Action)
		}
	}
	// No frame at all.
	if _, took := m.clickedLink(nil, tea.MouseMsg{X: 10, Y: 0, Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress, Alt: true}, 10, 0); took {
		t.Fatal("a pane with no screen took the click")
	}

	// The frame is the pane under the pointer's, not the focused pane's.
	// Clicking a link in another split read the focused screen's lines,
	// which opened the wrong link or none at all.
	other := &proto.Frame{ID: "p2", Lines: []string{"see https://example.com/other for this one"}}
	a2Frame(m, "p2", other)
	lastOpened = ""
	cmd, took = m.clickedLink(other, tea.MouseMsg{X: 6, Y: 0, Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress, Alt: true}, 6, 0)
	if !took {
		t.Fatal("a click in another split's link did nothing")
	}
	a2Run(cmd)
	if lastOpened != "https://example.com/other" {
		t.Fatalf("it opened %q, which is the focused pane's link", lastOpened)
	}
}
