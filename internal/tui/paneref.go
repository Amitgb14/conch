package tui

import (
	"fmt"

	"github.com/Amitgb14/conch/internal/remote"
	tea "github.com/charmbracelet/bubbletea"
)

// paneRef is how conch's commands name a pane: its ID here, and
// machine:ID on another machine — `conch read busybox:p3` — by the
// machine's label when the commands would find this machine by it, or its
// ID: a label with a space or a colon would not survive being typed, and
// one another machine answers to too (two logins to one host are both
// "devbox") would reach whichever the catalog lists first
// (remote.RefName).
func (m Model) paneRef(mid, pane string) string {
	if mid == "" || mid == localMachine {
		return pane
	}
	return remote.RefName(m.knownMachines(), mid) + ":" + pane
}

// knownMachines is every machine the commands could take a name to mean:
// those in the tree, by the labels shown, and the rest of machines.json as
// last read — disabled ones are still looked up by name.
func (m Model) knownMachines() []remote.Machine {
	out := make([]remote.Machine, 0, len(m.machines)+len(m.catalog))
	seen := map[string]bool{}
	for _, mach := range m.machines {
		if mach.id != localMachine {
			out = append(out, remote.Machine{ID: mach.id, Label: mach.label})
			seen[mach.id] = true
		}
	}
	for _, sm := range m.catalog {
		if !seen[sm.ID] {
			out = append(out, sm)
		}
	}
	return out
}

// copyPaneRef puts a pane's ID on the clipboard and says which it was.
func copyPaneRef(ref string) tea.Cmd {
	return func() tea.Msg {
		putClipboard(ref)
		return flashMsg(fmt.Sprintf("copied %s: conch read %s, conch send %s …", ref, ref, ref))
	}
}

// activePaneRef is the pane being worked in, as the commands name it, or
// "" when that is not a pane.
func (m Model) activePaneRef() string {
	r := m.activeRow()
	if r.paneID == "" || m.pane(r.machine, r.paneID) == nil {
		return ""
	}
	return m.paneRef(r.machine, r.paneID)
}
