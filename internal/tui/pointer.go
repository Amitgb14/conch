package tui

import (
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// The pointer itself says a link is clickable, where the terminal can be
// asked. Hovering a link underlines it (linkclick.go), which is what conch
// can do by drawing; the arrow turning into a hand is the terminal's to
// do, and it is asked with OSC 22 — kitty's pointer-shape escape, which
// Ghostty understands too.
//
// Most terminals don't have it. iTerm2 and Terminal.app keep the pointer
// to themselves (iTerm2 shows a hand for a link of its own finding, with
// cmd held), and tmux does not pass the escape on. An unknown OSC is
// swallowed rather than printed, but a sequence nobody acts on is still a
// write per cell the pointer crosses, so it is sent only where it means
// something.
//
// The shape is remembered, so crossing a link writes twice — a hand on the
// way in and an arrow on the way out — and not once a motion.

const (
	pointerArrow = "default" // what the terminal had before conch asked
	pointerHand  = "pointer" // over something a click would open
)

// pointerShapesWork reports whether this terminal takes OSC 22. $CONCH_POINTER
// settles it either way — 0 for a terminal that paints something ugly,
// 1 for one that supports it and is not named here yet.
func pointerShapesWork() bool {
	switch os.Getenv("CONCH_POINTER") {
	case "0", "off", "false":
		return false
	case "1", "on", "true":
		return true
	}
	if os.Getenv("TMUX") != "" {
		return false // tmux does not forward it, and owns the pointer anyway
	}
	term, program := os.Getenv("TERM"), strings.ToLower(os.Getenv("TERM_PROGRAM"))
	return program == "ghostty" || strings.Contains(term, "ghostty") ||
		program == "kitty" || strings.Contains(term, "kitty")
}

// setPointerShape asks the terminal for a pointer shape. Like the
// clipboard's OSC 52 it reaches out of the process, so it is a variable
// the test suite replaces (main_test.go): a test that ran this for real
// would leave the developer's own pointer as a hand.
var setPointerShape = writePointerShape

// writePointerShape is the writing itself, named so a test can check the
// bytes past the stub that replaces the variable.
func writePointerShape(shape string) {
	if !pointerShapesWork() {
		return
	}
	_, _ = os.Stdout.WriteString("\x1b]22;" + shape + "\x1b\\")
}

// pointAt asks for a shape if it is not the one the terminal already has.
// Every path that changes what is under the pointer goes through here, so
// the shape cannot be left behind as a hand over nothing.
func (m *Model) pointAt(shape string) {
	// Before conch asks for anything the terminal is showing an arrow, so
	// the zero value is one: moving over plain text at start-up asks for
	// nothing.
	have := m.pointer
	if have == "" {
		have = pointerArrow
	}
	m.pointer = shape
	if have == shape {
		return
	}
	setPointerShape(shape)
}

// releasePointer puts the pointer back as conch found it, for leaving the
// screen to somebody else: quitting, or a program that takes the terminal.
// A hand left behind would outlive conch.
func (m *Model) releasePointer() {
	m.pointAt(pointerArrow)
}

// quitting is tea.Quit with the terminal left as conch found it: the
// pointer back to an arrow, whatever it was over. Every path out of the
// TUI goes through here or releasePointer, or a hand over a link would
// outlive the process that asked for it.
func (m *Model) quitting(before ...tea.Cmd) tea.Cmd {
	m.releasePointer()
	return tea.Sequence(append(before, tea.Quit)...)
}
