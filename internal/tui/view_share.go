package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
)

// An agent in a pane reaches the server, never the TUI, so for it to see
// the screen or ask for something to be put in front of the person the
// server carries both ways: this publishes what is on screen (view.state)
// and acts on the requests that come back (view.show).
//
// Each machine is told about its own panes only. A layout spans machines
// — a tab can hold a pane from here beside one from busybox — but an
// agent asking its own server "what is on screen?" wants an answer in
// terms of panes it could address, so a tab is published to a machine
// with that machine's panes in it, and left out where it has none.
//
// A request is a request, and it gathers rather than rearranges: the
// panes asked for end up side by side in one tab and nothing else is
// touched. They do *move* — a pane is shown in exactly one place
// (tabs.go), which is conch's rule and not something this introduces —
// so conch's own break-pane shortcut reuses a tab already holding one of
// them alone rather than leaving an empty one behind. One pane is a
// reveal, exactly as ! is for a waiting agent.

// viewStateFor is what machine mid is shown of the layout.
func (m Model) viewStateFor(mid string) proto.ViewState {
	v := proto.ViewState{Client: "conch-tui"}
	if f := m.tab(); f != nil {
		if fl := f.focused(); fl != nil && fl.view.Kind == kindPane && fl.view.Machine == mid {
			v.Focus = fl.view.PaneID
		}
	}
	for i, t := range m.tabs {
		tab := proto.ViewTab{Name: ansi.Strip(m.tabLabel(t)), Active: i == m.activeTab}
		for _, l := range t.root.leaves() {
			if l.view.Kind == kindPane && l.view.Machine == mid && l.view.PaneID != "" {
				tab.Panes = append(tab.Panes, l.view.PaneID)
			}
		}
		if len(tab.Panes) > 0 {
			v.Tabs = append(v.Tabs, tab)
		}
	}
	return v
}

// viewSig is a layout as a string, for telling a change from a redraw:
// published only when it differs, since syncView runs constantly.
func viewSig(v proto.ViewState) string {
	var b strings.Builder
	b.WriteString(v.Focus)
	for _, t := range v.Tabs {
		b.WriteString("\n" + t.Name + "\t" + strings.Join(t.Panes, ","))
		if t.Active {
			b.WriteString("\t*")
		}
	}
	return b.String()
}

// publishView tells every machine what of the layout is its own, when
// that has changed. A server too old to know view.state is not told:
// the notification would be ignored, and saying so once a keystroke is
// worse than silence.
func (m *Model) publishView() {
	if m.viewSigs == nil {
		m.viewSigs = map[string]string{}
	}
	for _, mach := range m.machines {
		if mach.c == nil || len(mach.c.MissingCapabilities([]string{proto.CapView})) > 0 {
			continue
		}
		state := m.viewStateFor(mach.id)
		if sig := viewSig(state); sig != m.viewSigs[mach.id] {
			m.viewSigs[mach.id] = sig
			mach.c.Notify(proto.MethodViewState, state)
		}
	}
}

// showPanes acts on a view.show: the panes that are still there, on the
// machine that asked, put in front of the person. One is revealed where
// it already is; several open a tab of their own, side by side.
func (m *Model) showPanes(mid string, p proto.ViewShowParams) tea.Cmd {
	var panes []proto.PaneInfo
	for _, id := range p.Panes {
		if info := m.pane(mid, id); info != nil && !m.isOwnPane(mid, id) {
			panes = append(panes, *info)
		}
	}
	who := p.Why
	if who == "" {
		who = "an agent"
	}
	switch len(panes) {
	case 0:
		m.setFlash(who+" asked for panes that are not here any more", true)
		return nil
	case 1:
		m.revealPane(mid, panes[0])
		m.setFlash(who+" brought up "+panes[0].DisplayName(), false)
		return m.rebuild()
	}
	// Gathered into one tab; newTab moves a pane that is already alone in
	// one rather than making a second place for it.
	cmds := []tea.Cmd{m.newTab(m.paneView(mid, panes[0]))}
	for _, info := range panes[1:] {
		cmds = append(cmds, m.split(splitRight, m.paneView(mid, info)))
	}
	if p.Name != "" {
		m.tab().name = p.Name
	}
	m.setFlash(fmt.Sprintf("%s put %d panes side by side", who, len(panes)), false)
	return tea.Batch(append(cmds, m.rebuild(), m.saveState())...)
}

// paneView is the view a pane is shown through, as opening its row would
// give: the tree may not have that row — folded away, or filtered out —
// so it is built rather than looked up.
func (m Model) paneView(mid string, p proto.PaneInfo) viewRef {
	return viewRef{Row: paneNodeID(mid, p.ID), Kind: kindPane, Machine: mid,
		PaneID: p.ID, ProjectID: p.ProjectID, Branch: p.Branch}
}
