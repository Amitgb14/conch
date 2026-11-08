package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// The pane being worked in is named in the status bar as the commands
// take it, a click copies it, and its menu says and copies it too.
func TestPaneIDShownAndCopied(t *testing.T) {
	m := a2Model()
	m.width = 200
	m.rebuild()
	m.cursor = paneNodeID(localMachine, "p1")
	line, hits := m.layoutStatus()
	if !strings.Contains(a2Plain([]string{line}), "p1") {
		t.Fatalf("the status bar doesn't name p1:\n%s", line)
	}
	// A click on it copies it.
	plain := a2Plain([]string{line})
	i := strings.Index(plain, "  p1  ")
	if i < 0 {
		t.Fatalf("p1 isn't an item of its own:\n%s", plain)
	}
	x := ansi.StringWidth(plain[:i+2]) // columns, not bytes: ⚑ is one column of three
	var copied tea.Cmd
	for _, h := range hits {
		if x >= h.x0 && x < h.x1 {
			copied = h.act(m)
		}
	}
	if copied == nil {
		t.Fatal("no click on p1")
	}
	lastClipboard = ""
	if msg := copied(); lastClipboard != "p1" || !strings.Contains(string(msg.(flashMsg)), "conch read p1") {
		t.Fatalf("clicked: clipboard %q, said %v", lastClipboard, msg)
	}

	// The pane's menu: the ID in its title, and a Copy pane ID.
	r, _ := m.selectedRow()
	mu := newRowMenu(*m, r, 0, 0)
	if !strings.HasSuffix(mu.title, " · p1") {
		t.Errorf("menu title %q", mu.title)
	}
	found := false
	for _, it := range mu.items {
		if it.label == "Copy pane ID (p1)" {
			found = true
			lastClipboard = ""
			it.run(m)()
			if lastClipboard != "p1" {
				t.Errorf("Copy pane ID copied %q", lastClipboard)
			}
		}
	}
	if !found {
		t.Error("no Copy pane ID in the pane's menu")
	}

	// Not a pane: nothing named.
	m.cursor = machineID(localMachine)
	if ref := m.activePaneRef(); ref != "" {
		t.Errorf("a machine row names pane %q", ref)
	}
	// A narrow bar drops it before anything that matters more.
	m.cursor = paneNodeID(localMachine, "p1")
	for _, w := range []int{20, 40, 60} {
		m.width = w
		line, _ := m.layoutStatus()
		if got := ansi.StringWidth(a2Plain([]string{line})); got > w {
			t.Errorf("%d wide: %d", w, got)
		}
	}
}

// Another machine's pane is machine:ID, by its label when that can be
// typed, else by the machine's ID.
func TestPaneRefOnAnotherMachine(t *testing.T) {
	m := a2Model()
	bb := &machine{id: "busybox-1", label: "busybox"}
	odd := &machine{id: "lab-2", label: "lab box"}
	m.machines = append(m.machines, bb, odd)
	for _, c := range []struct{ mid, want string }{
		{localMachine, "p3"}, {"", "p3"}, {"busybox-1", "busybox:p3"}, {"lab-2", "lab-2:p3"}, {"gone", "gone:p3"},
	} {
		if got := m.paneRef(c.mid, "p3"); got != c.want {
			t.Errorf("%q: %q, want %q", c.mid, got, c.want)
		}
	}
}
