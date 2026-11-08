package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// paneRef is how conch's commands name a pane: its ID here, and
// machine:ID on another machine — `conch read busybox:p3` — by the
// machine's label, or its ID when the label has a space or a colon in it
// and would not survive being typed.
func (m Model) paneRef(mid, pane string) string {
	if mid == "" || mid == localMachine {
		return pane
	}
	if l := m.machineLabel(mid); l != "" && !strings.ContainsAny(l, " \t:") {
		return l + ":" + pane
	}
	return mid + ":" + pane
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
