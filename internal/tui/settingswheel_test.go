package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amitgb14/conch/internal/proto"
)

// wheelItems is a settings list of n rows: a header every fifth, a choice
// otherwise.
func wheelItems(n int) []settingItem {
	items := make([]settingItem, n)
	for i := range items {
		items[i].label = fmt.Sprint("row", i)
		if i%5 == 0 {
			items[i].header = true
		} else {
			items[i].run = func(*Model) tea.Cmd { return nil }
		}
	}
	return items
}

// A swipe over the settings scrolls the list a line an event and leaves
// the selection where it is while it is in sight. The wheel used to move
// the selection instead: under a swipe it raced through the sections while
// the list sat still, then the list jumped when it reached the edge.
func TestSettingsWheelScrollsTheList(t *testing.T) {
	items := wheelItems(40)
	s := &settings{sel: 6}
	for i := 1; i <= 4; i++ {
		s.wheel(items, 1, 10)
		if s.scroll != i || s.sel != 6 {
			t.Fatalf("event %d: scroll %d sel %d", i, s.scroll, s.sel)
		}
	}
	// Off the top: the first choice in sight (row 7; row 5 is a header,
	// and so is 10, past it).
	s.wheel(items, 3, 10)
	if s.scroll != 7 || s.sel != 7 {
		t.Fatalf("off the top: scroll %d sel %d", s.scroll, s.sel)
	}
	// Back up: off the bottom it comes to the last choice in sight, and not
	// the last row, which says how many more there are.
	s.scroll, s.sel = 10, 18
	s.wheel(items, -3, 10)
	if s.scroll != 7 || s.sel != 14 {
		t.Fatalf("off the bottom: scroll %d sel %d", s.scroll, s.sel)
	}
}

// The wheel stops at either end of the list, and a list that fits does
// not scroll at all.
func TestSettingsWheelClamps(t *testing.T) {
	items := wheelItems(40)
	s := &settings{sel: 1}
	s.wheel(items, -3, 10)
	if s.scroll != 0 || s.sel != 1 {
		t.Fatalf("above the top: scroll %d sel %d", s.scroll, s.sel)
	}
	for range 50 {
		s.wheel(items, 3, 10)
	}
	if s.scroll != 30 || s.sel < 30 || items[s.sel].run == nil {
		t.Fatalf("past the end: scroll %d sel %d", s.scroll, s.sel)
	}
	short := wheelItems(6)
	s = &settings{sel: 4}
	s.wheel(short, 3, 10)
	if s.scroll != 0 || s.sel != 4 {
		t.Fatalf("a list that fits: scroll %d sel %d", s.scroll, s.sel)
	}
	s.wheel(nil, 3, 10)
	if s.scroll != 0 {
		t.Fatalf("no items: scroll %d", s.scroll)
	}
}

// With nothing to choose in sight the selection stays, and the list is
// shown around it again rather than the selection being lost.
func TestSettingsWheelNothingInSight(t *testing.T) {
	items := wheelItems(30)
	for i := 8; i < 20; i++ {
		items[i].run, items[i].header = nil, false // description lines
	}
	s := &settings{sel: 6}
	s.wheel(items, 9, 4) // rows 9–11 in sight, then "… more"
	if s.sel != 6 {
		t.Fatalf("selection moved to %d", s.sel)
	}
}

// Through the overlay: a swipe moves the list a line an event, and the
// selection is never left under the "… more" line.
func TestSettingsSwipeThroughTheOverlay(t *testing.T) {
	a2Isolate(t)
	defer applyTheme("conch", "")
	m := a2Model()
	m.height = 20 // a list of 12 rows
	s := &settings{shell: &proto.ShellThemes{OMZ: true}}
	for i := range 30 {
		s.shell.Themes = append(s.shell.Themes, fmt.Sprint("theme", i))
	}
	m.overlay = s
	b := s.render(*m)
	sel := s.sel
	swipe(4, func() { s.mouse(m, tea.MouseMsg{X: b.x + 2, Y: b.y + 4, Button: tea.MouseButtonWheelDown}, b) })
	if s.scroll != 3+1+1+1 {
		t.Fatalf("swipe: scroll %d", s.scroll)
	}
	b = s.render(*m)
	if s.scroll != 6 || s.sel < 6 {
		t.Fatalf("render moved it: scroll %d sel %d (was %d)", s.scroll, s.sel, sel)
	}
	a2CheckBox(t, b, *m)

	// Down by keys to the last row before "… more": the list scrolls so
	// the selection stays in sight.
	s.scroll, s.sel = 0, 1
	listH := m.settingsListHeight()
	for range listH {
		s.update(m, a2Key("down"))
		b = s.render(*m)
		items := s.items(m)
		if s.sel >= s.scroll+settingsShown(s.scroll, listH, len(items)) ||
			!strings.Contains(a2Plain(b.lines), items[s.sel].label) {
			t.Fatalf("selection %d %q not in sight (scroll %d):\n%s", s.sel, items[s.sel].label, s.scroll, a2Plain(b.lines))
		}
	}
	if !strings.Contains(a2Plain(b.lines), "more") {
		t.Fatalf("no footer:\n%s", a2Plain(b.lines))
	}
}
