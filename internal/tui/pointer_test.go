package tui

import (
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amitgb14/conch/internal/proto"
)

// The pointer over a link. Underlining it is conch's own drawing; the
// arrow becoming a hand is the terminal's, asked for with OSC 22. What
// these tests hold to is that it is asked once per crossing, that it is
// always put back, and that it is not sent to a terminal that would do
// nothing with it — a write per cell the pointer crosses, for nothing.

// pointerSeen takes the shapes asked for since it was last called.
func pointerSeen() []string {
	got := pointerShapes
	pointerShapes = nil
	return got
}

// TestThePointerBecomesAHandOverALink: the thing people expect from a
// link, and the shape going back the moment the pointer leaves it.
func TestThePointerBecomesAHandOverALink(t *testing.T) {
	a2Isolate(t)
	t.Setenv("CONCH_POINTER", "1") // a terminal that takes it
	pointerSeen()
	m := a2Model()
	m.cfg.UI.Hover = true
	m.width, m.height = 120, 40
	a2Frame(m, "p1", &proto.Frame{ID: "p1", Lines: []string{"see https://example.com/x for this", "nothing here"}})
	m.rebuild()
	a1Open(t, m, paneNodeID(localMachine, "p1"))
	m.syncView()
	rects, _ := m.leafRects()
	in := m.inner(rects[m.tab().focus])
	at := func(col, row int) tea.MouseMsg {
		return tea.MouseMsg{X: in.x + col, Y: in.y + row, Action: tea.MouseActionMotion, Button: tea.MouseButtonNone}
	}

	// On the words beside it: nothing has been asked for yet, since an
	// arrow is what the terminal already shows.
	m.hoverAt(at(1, 0))
	if got := pointerSeen(); len(got) != 0 {
		t.Fatalf("over plain text it asked for %v", got)
	}

	// Onto the link: a hand, once.
	m.hoverAt(at(8, 0))
	if got := pointerSeen(); len(got) != 1 || got[0] != pointerHand {
		t.Fatalf("moving onto a link asked for %v", got)
	}
	// Along the link: the shape is already right, so nothing is sent. This
	// is the whole reason the shape is remembered — hover reports every
	// cell the pointer crosses.
	for col := 9; col < 20; col++ {
		m.hoverAt(at(col, 0))
	}
	if got := pointerSeen(); len(got) != 0 {
		t.Fatalf("moving along one link asked for %v", got)
	}

	// Off it: an arrow, once.
	m.hoverAt(at(1, 0))
	if got := pointerSeen(); len(got) != 1 || got[0] != pointerArrow {
		t.Fatalf("moving off a link asked for %v", got)
	}
	// And onto a row with nothing on it at all.
	m.hoverAt(at(5, 1))
	if got := pointerSeen(); len(got) != 0 {
		t.Fatalf("another line of text asked for %v", got)
	}
}

// TestThePointerIsPutBack: a hand asked for by conch would outlive conch,
// over whatever the person's shell draws next. So every way out puts it
// back: quitting, restarting onto a new build, and anything that clears
// what is under the pointer (a dialog opening over the screen).
func TestThePointerIsPutBack(t *testing.T) {
	a2Isolate(t)
	t.Setenv("CONCH_POINTER", "1")
	m := a2Model()
	m.cfg.UI.Hover = true
	m.width, m.height = 120, 40
	a2Frame(m, "p1", &proto.Frame{ID: "p1", Lines: []string{"see https://example.com/x for this"}})
	m.rebuild()
	a1Open(t, m, paneNodeID(localMachine, "p1"))
	m.syncView()
	rects, _ := m.leafRects()
	in := m.inner(rects[m.tab().focus])
	onLink := tea.MouseMsg{X: in.x + 8, Y: in.y, Action: tea.MouseActionMotion, Button: tea.MouseButtonNone}

	for name, leave := range map[string]func(m *Model){
		"clearing the hover": func(m *Model) { m.clearHover() },
		"quitting":           func(m *Model) { m.quitting() },
		"restarting":         func(m *Model) { m.handleUpdate(restartTUIMsg{}) },
	} {
		m.pointer = ""
		m.hoverAt(onLink)
		pointerSeen()
		if m.pointer != pointerHand {
			t.Fatalf("%s: the pointer was not a hand to begin with", name)
		}
		leave(m)
		if got := pointerSeen(); len(got) != 1 || got[0] != pointerArrow {
			t.Errorf("%s asked for %v, want the arrow back", name, got)
		}
		if m.pointer != pointerArrow {
			t.Errorf("%s left the shape as %q", name, m.pointer)
		}
	}
}

// TestQuittingStillEnds: putting the pointer back must not have cost the
// quit itself, or `q` would leave the TUI sitting there.
func TestQuittingStillEnds(t *testing.T) {
	a2Isolate(t)
	m := a2Model()
	if cmd := m.quitting(); cmd == nil {
		t.Fatal("quitting returned no command, so the TUI would not end")
	}
	if cmd := m.quitting(m.saveState()); cmd == nil {
		t.Fatal("quitting with a save returned no command")
	}
	// What `q` itself does, where the two are put together.
	m2 := a2Model()
	if _, cmd := m2.handleKey(a2Key("q")); cmd == nil {
		t.Fatal("q does not end the TUI")
	}
}

// TestOnlyTerminalsThatTakeItAreAsked: iTerm2 and Terminal.app do nothing
// with OSC 22 and tmux does not pass it on, so they are not written to at
// all; $CONCH_POINTER settles it either way for a terminal not named here.
func TestOnlyTerminalsThatTakeItAreAsked(t *testing.T) {
	for _, c := range []struct {
		name string
		env  map[string]string
		want bool
	}{
		{"ghostty", map[string]string{"TERM_PROGRAM": "ghostty", "TERM": "xterm-ghostty"}, true},
		{"ghostty by TERM alone", map[string]string{"TERM": "xterm-ghostty"}, true},
		{"kitty", map[string]string{"TERM": "xterm-kitty"}, true},
		{"iTerm2", map[string]string{"TERM_PROGRAM": "iTerm.app", "TERM": "xterm-256color"}, false},
		{"Terminal.app", map[string]string{"TERM_PROGRAM": "Apple_Terminal", "TERM": "xterm-256color"}, false},
		{"nothing at all", map[string]string{}, false},
		{"ghostty inside tmux", map[string]string{"TERM_PROGRAM": "ghostty", "TMUX": "/tmp/tmux-501/default,1,0"}, false},
		{"forced on", map[string]string{"TERM_PROGRAM": "iTerm.app", "CONCH_POINTER": "1"}, true},
		{"forced off", map[string]string{"TERM": "xterm-kitty", "CONCH_POINTER": "0"}, false},
		{"forced on inside tmux", map[string]string{"TMUX": "x", "CONCH_POINTER": "on"}, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			for _, k := range []string{"TERM", "TERM_PROGRAM", "TMUX", "CONCH_POINTER"} {
				t.Setenv(k, "")
				os.Unsetenv(k)
			}
			for k, v := range c.env {
				t.Setenv(k, v)
			}
			if got := pointerShapesWork(); got != c.want {
				t.Errorf("pointerShapesWork() = %v, want %v for %+v", got, c.want, c.env)
			}
		})
	}
}

// TestThePointerEscapeIsTheOneTerminalsRead: the sequence itself, since a
// wrong one would print into the person's screen instead of being acted
// on — OSC 22, the shape, and a string terminator.
func TestThePointerEscapeIsTheOneTerminalsRead(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = w
	t.Setenv("CONCH_POINTER", "1")
	writePointerShape(pointerHand)
	writePointerShape(pointerArrow)
	t.Setenv("CONCH_POINTER", "0")
	writePointerShape(pointerHand) // a terminal that does not take it is not written to
	os.Stdout = stdout
	w.Close()
	buf := make([]byte, 256)
	n, _ := r.Read(buf)
	r.Close()

	got := string(buf[:n])
	if want := "\x1b]22;pointer\x1b\\\x1b]22;default\x1b\\"; got != want {
		t.Fatalf("it wrote %q, want %q", got, want)
	}
	if strings.Count(got, "\x1b]22;") != 2 {
		t.Errorf("writes: %q", got)
	}
}
