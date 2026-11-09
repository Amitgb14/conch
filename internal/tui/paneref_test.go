package tui

import (
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/remote"
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

// A label is used only when the commands would find this machine by it:
// two logins to one host labelled alike, a machine labelled "local" or
// with another's ID, or one that clashes with a disabled machine (still
// in machines.json, so still looked up) is named by its ID, which always
// reaches it.
func TestPaneRefAmbiguousLabels(t *testing.T) {
	m := a2Model()
	m.machines = append(m.machines,
		&machine{id: "devbox", label: "devbox"},
		&machine{id: "devbox-2", label: "devbox"},
		&machine{id: "loc", label: "local"},
		&machine{id: "bb", label: "busybox"},
		&machine{id: "x", label: "bb"},
		&machine{id: "tab", label: "lab\tbox"},
		&machine{id: "nl", label: "lab\nbox"},
		&machine{id: "gpu-1", label: "gpu"},
		&machine{id: "f1", label: "fresh"},
	)
	m.catalog = []remote.Machine{
		{ID: "old", Label: "gpu", Enabled: false},      // disabled, not in the tree
		{ID: "f1", Label: "stale", Enabled: true},      // the tree's label is newer
		{ID: "devbox", Label: "devbox", Enabled: true}, // in both: counted once
	}
	for _, c := range []struct{ mid, want string }{
		{"devbox", "devbox:p3"}, {"devbox-2", "devbox-2:p3"},
		{"loc", "loc:p3"},
		{"bb", "busybox:p3"}, {"x", "x:p3"},
		{"tab", "tab:p3"}, {"nl", "nl:p3"},
		{"gpu-1", "gpu-1:p3"},
		{"f1", "fresh:p3"},
	} {
		if got := m.paneRef(c.mid, "p3"); got != c.want {
			t.Errorf("%q: %q, want %q", c.mid, got, c.want)
		}
	}
	// Without the disabled one, gpu's label is its own.
	m.catalog = nil
	if got := m.paneRef("gpu-1", "p3"); got != "gpu:p3" {
		t.Errorf("gpu alone: %q", got)
	}
}
