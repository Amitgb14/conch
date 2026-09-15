package tui

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amitgb14/conch/internal/remote"
)

// The TUI reads machines.json at start; conch machine add and rm change it
// from other processes, so it is watched and the tree follows.

const catalogEvery = 3 * time.Second

type catalogTickMsg struct{}

func catalogTick() tea.Cmd {
	return tea.Tick(catalogEvery, func(time.Time) tea.Msg { return catalogTickMsg{} })
}

func catalogStamp() string {
	st, err := os.Stat(remote.CatalogPath())
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%d:%d", st.ModTime().UnixNano(), st.Size())
}

// syncCatalog applies changes to machines.json made outside this TUI.
func (m *Model) syncCatalog() tea.Cmd {
	stamp := catalogStamp()
	if stamp == m.catalogStamp {
		return nil
	}
	saved, err := remote.Machines()
	if err != nil {
		return nil // a half-written or broken file: keep what is shown and retry
	}
	m.catalogStamp = stamp
	return m.applyCatalog(saved)
}

// applyCatalog makes the machines match the saved ones: new machines are
// added and connected, removed or disabled ones closed, renamed ones
// relabelled, and a changed target reconnects.
func (m *Model) applyCatalog(saved []remote.Machine) tea.Cmd {
	want := map[string]remote.Machine{}
	for _, sm := range saved {
		if sm.Enabled && sm.ID != localMachine {
			want[sm.ID] = sm
		}
	}
	var cmds []tea.Cmd
	var added, removed []string
	kept := m.machines[:0:0]
	for _, mach := range m.machines {
		sm, ok := want[mach.id]
		switch {
		case mach.id == localMachine:
			kept = append(kept, mach)
		case !ok:
			mach.close()
			removed = append(removed, mach.label)
			if m.viewMachine == mach.id {
				m.viewMachine, m.viewing, m.frame = "", "", nil
			}
		case sm.Target != mach.target:
			mach.close()
			fresh := newMachine(sm.ID, sm.Label, sm.Target)
			kept = append(kept, fresh)
			cmds = append(cmds, fresh.connect(false))
		default:
			mach.label = sm.Label
			kept = append(kept, mach)
		}
		delete(want, mach.id)
	}
	for _, sm := range saved { // in the file's order
		if _, ok := want[sm.ID]; !ok {
			continue
		}
		mach := newMachine(sm.ID, sm.Label, sm.Target)
		kept = append(kept, mach)
		m.expanded[machineID(mach.id)] = true
		added = append(added, mach.label)
		cmds = append(cmds, mach.connect(false))
	}
	changed := len(added) > 0 || len(removed) > 0 || len(kept) != len(m.machines) ||
		slices.ContainsFunc(kept, func(k *machine) bool { return !slices.Contains(m.machines, k) })
	m.machines = kept
	if !changed {
		return tea.Batch(append(cmds, m.rebuild())...) // labels may have changed
	}
	var parts []string
	if len(added) > 0 {
		parts = append(parts, "added "+strings.Join(added, ", "))
	}
	if len(removed) > 0 {
		parts = append(parts, "removed "+strings.Join(removed, ", "))
	}
	if len(parts) > 0 {
		m.setFlash(strings.Join(parts, " · "), false)
	}
	return tea.Batch(append(cmds, m.rebuild(), m.saveState())...)
}
