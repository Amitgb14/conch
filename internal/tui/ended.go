package tui

import (
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
)

// An agent whose pane ended without anybody closing it stays where it was
// (pane.ended.v1, internal/server/ended.go): its pane, kept on its last
// screen, while the server still holds it, and after a restart a row of
// its own built from the record. Either way the row says why it ended,
// enter or r resumes it in the same place, and d or x dismisses it.

// endedPrefix marks the pane id of a row standing for an ended agent whose
// pane the server no longer holds: no pane has an id like it, so nothing
// sent with it reaches one.
const endedPrefix = "ended:"

type (
	endedMsg struct {
		machine string
		gen     int
		ended   []proto.EndedAgent
	}
	// endedResumedMsg is a resumed agent, back in pane info where pane
	// was.
	endedResumedMsg struct {
		machine, pane string
		info          proto.PaneInfo
	}
)

// loadEnded asks a server that keeps ended agents for them.
func (mach *machine) loadEnded() tea.Cmd {
	c, id, gen := mach.c, mach.id, mach.gen
	if c == nil || len(c.MissingCapabilities([]string{proto.CapPaneEnded})) > 0 {
		return nil
	}
	return func() tea.Msg {
		var list proto.EndedList
		if err := callCtx(c, proto.MethodPaneEndedList, nil, &list); err != nil {
			return nil
		}
		return endedMsg{machine: id, gen: gen, ended: list.Ended}
	}
}

// keepsEnded says whether the machine's server keeps ended agents: then
// an agent's pane that ends is left for its record rather than closed.
func (mach *machine) keepsEnded() bool {
	return mach != nil && mach.c != nil && len(mach.c.MissingCapabilities([]string{proto.CapPaneEnded})) == 0
}

// addEnded keeps a record from a pane.ended event, newest first.
func (mach *machine) addEnded(rec proto.EndedAgent) {
	mach.ended = slices.DeleteFunc(mach.ended, func(e proto.EndedAgent) bool { return e.ID == rec.ID })
	mach.ended = append([]proto.EndedAgent{rec}, mach.ended...)
}

func (mach *machine) dropEnded(id string) {
	mach.ended = slices.DeleteFunc(mach.ended, func(e proto.EndedAgent) bool { return e.ID == id })
}

// endedFor is the record a pane row stands for: a row of its own
// (endedPrefix), or a pane the server still holds that ended as the
// record says — not a live pane that was given the same id after a
// restart.
func (m Model) endedFor(mid, paneID string) *proto.EndedAgent {
	mach := m.machine(mid)
	if mach == nil || len(mach.ended) == 0 {
		return nil
	}
	if id, ok := strings.CutPrefix(paneID, endedPrefix); ok {
		for i := range mach.ended {
			if mach.ended[i].ID == id {
				return &mach.ended[i]
			}
		}
		return nil
	}
	p := mach.pane(paneID)
	if p == nil || p.State != proto.PaneExited {
		return nil
	}
	for i := range mach.ended {
		if r := &mach.ended[i]; r.Pane == paneID && !p.Created.After(r.Ended) {
			return r
		}
	}
	return nil
}

// endedPane is the row an ended agent with no pane is listed as.
func endedPane(r proto.EndedAgent) proto.PaneInfo {
	name := r.Name
	if name == "" {
		name = agentLabel(r.Agent)
	}
	return proto.PaneInfo{ID: endedPrefix + r.ID, Name: name, CustomName: true, Cwd: r.Dir, State: proto.PaneExited,
		ExitCode: r.ExitCode, Created: r.Started, Account: r.Account, ProjectID: r.ProjectID, Branch: r.Branch}
}

// withEnded adds a row for each ended agent whose pane is gone, in a
// project the tree shows (or none), to the panes the tree is built from.
// agents is copied before it is added to: it is the machine's own.
func (m Model) withEnded(mach *machine, projects []proto.ProjectInfo, panes []proto.PaneInfo, agents map[string]bool) ([]proto.PaneInfo, map[string]bool) {
	if len(mach.ended) == 0 {
		return panes, agents
	}
	held := map[string]bool{}
	for _, p := range mach.panes {
		if p.State == proto.PaneExited {
			held[p.ID] = true
		}
	}
	var extra []proto.PaneInfo
	for _, r := range mach.ended {
		if held[r.Pane] && m.endedFor(mach.id, r.Pane) != nil {
			continue // its own pane is its row
		}
		if r.ProjectID != "" && !slices.ContainsFunc(projects, func(p proto.ProjectInfo) bool { return p.ID == r.ProjectID }) {
			continue
		}
		extra = append(extra, endedPane(r))
	}
	if len(extra) == 0 {
		return panes, agents
	}
	marked := make(map[string]bool, len(agents)+len(extra))
	for k, v := range agents {
		marked[k] = v
	}
	for _, p := range extra {
		marked[p.ID] = true
	}
	return append(slices.Clone(panes), extra...), marked
}

// endedRight is the right side of an ended agent's row, as short as a
// narrow sidebar needs: why it ended is in its split and title.
func endedRight(r proto.EndedAgent) string {
	style := styleWarn
	if r.Failed() {
		style = styleErr
	}
	when := ago(r.Ended)
	if when != "now" {
		when += " ago"
	}
	return style.Render("⚠ ended "+when) + styleMuted.Render(" · Resume")
}

// endedKey is a key on an ended agent's row: enter or r resumes it, d or
// x dismisses it. Anything else is the row's as for any pane.
func (m *Model) endedKey(r row, key string) (tea.Cmd, bool) {
	if r.kind != kindPane {
		return nil, false
	}
	rec := m.endedFor(r.machine, r.paneID)
	if rec == nil {
		return nil, false
	}
	switch key {
	case "enter", "r":
		return m.resumeEnded(r.machine, *rec), true
	case "d", "x":
		return m.dismissEnded(r.machine, *rec), true
	}
	return nil, false
}

// resumeEnded starts an ended agent again in its place.
func (m *Model) resumeEnded(mid string, rec proto.EndedAgent) tea.Cmd {
	mach := m.machine(mid)
	if mach != nil && mach.missingAgent(rec.Agent) {
		return func() tea.Msg { return askInstallMsg{machine: mid, agent: rec.Agent} }
	}
	cols, rows := m.paneArea()
	var info proto.PaneInfo
	m.setFlash("resuming "+agentLabel(rec.Agent)+"…", false)
	return m.callOn(mid, proto.MethodPaneEndedResume, proto.EndedResumeParams{ID: rec.ID, Cols: cols, Rows: rows}, &info, func() tea.Msg {
		return endedResumedMsg{machine: mid, pane: rec.Pane, info: info}
	})
}

func (m *Model) dismissEnded(mid string, rec proto.EndedAgent) tea.Cmd {
	if mach := m.machine(mid); mach != nil {
		mach.dropEnded(rec.ID) // the server says so too; the row goes now
	}
	return tea.Batch(m.rebuild(), m.callOn(mid, proto.MethodPaneEndedDismiss, proto.EndedRef{ID: rec.ID}, nil, nil))
}

// replacePane puts pane to where old was shown, in every split showing it;
// the old pane's row goes with it. Where to is already shown, old is only
// taken out.
func (m *Model) replacePane(mid, old, to string) {
	if old == "" || to == "" || old == to {
		return
	}
	from, into := paneNodeID(mid, old), paneNodeID(mid, to)
	shown := false
	for _, t := range m.allTabs() {
		for _, l := range t.root.leaves() {
			shown = shown || l.view.Row == into
		}
	}
	if shown {
		m.removeRow(from)
		return
	}
	for _, t := range m.allTabs() {
		for _, l := range t.root.leaves() {
			if l.view.Row == from {
				l.view.Row, l.view.PaneID = into, to
			}
		}
	}
	if m.cursor == from {
		m.cursor = into
	}
}

// allTabs is every tab and the preview.
func (m *Model) allTabs() []*tab {
	out := slices.Clone(m.tabs)
	if m.preview != nil {
		out = append(out, m.preview)
	}
	return out
}

// endedEvent takes pane.ended and pane.ended.gone.
func (m *Model) endedEvent(mach *machine, msg proto.Message) tea.Cmd {
	switch msg.Event {
	case proto.EventPaneEnded:
		var rec proto.EndedAgent
		if !decodeInto(msg, &rec) {
			return nil
		}
		mach.addEnded(rec)
	case proto.EventPaneEndedGone:
		var g proto.EndedGone
		if !decodeInto(msg, &g) {
			return nil
		}
		mach.dropEnded(g.ID)
		if g.Resumed != "" {
			m.replacePane(mach.id, g.Pane, g.Resumed)
			return tea.Batch(m.rebuild(), m.saveState())
		}
	}
	return m.rebuild()
}

// endedLines is what a split showing an ended agent with no pane draws:
// why it ended, how to bring it back, and its last screen.
func (m Model) endedLines(rec proto.EndedAgent, w, h int) []string {
	head := styleWarn.Render("⚠ "+agentLabel(rec.Agent)+" "+rec.Reason()) +
		styleMuted.Render(" · enter resumes it here · d dismisses")
	lines := []string{ansi.Truncate(head, w, "…"), ""}
	screen := rec.Screen
	if room := h - len(lines); len(screen) > room {
		screen = screen[len(screen)-max(room, 0):]
	}
	for _, l := range screen {
		lines = append(lines, styleMuted.Render(ansi.Truncate(l, w, "…")))
	}
	return lines
}
