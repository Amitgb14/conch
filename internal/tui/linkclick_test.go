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
	m.frame = &proto.Frame{ID: "p1", Lines: []string{"go to https://example.com/x now"}}
	press := func(x int, alt bool) (tea.Cmd, bool) {
		return m.clickedLink(tea.MouseMsg{X: x, Y: 0, Button: tea.MouseButtonLeft,
			Action: tea.MouseActionPress, Alt: alt}, x, 0, false)
	}

	// A plain click on the link opens it and copies it, in a pane whose
	// program takes no mouse.
	lastOpened, lastClipboard = "", ""
	cmd, took := press(10, false)
	if !took || cmd == nil {
		t.Fatal("a click on a link did nothing")
	}
	a2Run(cmd)
	if lastOpened != "https://example.com/x" || lastClipboard != "https://example.com/x" {
		t.Fatalf("opened %q, copied %q", lastOpened, lastClipboard)
	}
	if !strings.Contains(m.flash, "opening https://example.com/x") {
		t.Fatalf("it said %q", m.flash)
	}
	// A click beside it is not a click on it.
	if _, took := press(2, false); took {
		t.Fatal("a click on the words took the click")
	}
	// A program that takes the mouse is owed plain clicks; alt opens it.
	if _, took := m.clickedLink(tea.MouseMsg{X: 10, Y: 0, Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress}, 10, 0, true); took {
		t.Fatal("a plain click was taken from the program")
	}
	if _, took := m.clickedLink(tea.MouseMsg{X: 10, Y: 0, Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress, Alt: true}, 10, 0, true); !took {
		t.Fatal("alt+click did not open the link")
	}
	// A release, a right button and a wheel are not clicks on a link.
	for _, msg := range []tea.MouseMsg{
		{X: 10, Y: 0, Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease},
		{X: 10, Y: 0, Button: tea.MouseButtonRight, Action: tea.MouseActionPress},
		{X: 10, Y: 0, Button: tea.MouseButtonWheelUp},
	} {
		if _, took := m.clickedLink(msg, 10, 0, false); took {
			t.Fatalf("%v took the click", msg.Action)
		}
	}
	// No frame at all.
	m.frame = nil
	if _, took := press(10, false); took {
		t.Fatal("a pane with no screen took the click")
	}
}
