// Package tui is conch's Bubble Tea client: a tree of machines, projects,
// branches and agents beside the selected pane, branch changes or project.
package tui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/remote"
)

type focusArea int

const (
	focusSidebar focusArea = iota
	focusMain
)

const (
	defaultSidebarWidth = 36
	minSidebarWidth     = 24
	maxSidebarWidth     = 70
	doubleClickWindow   = 400 * time.Millisecond
	spinInterval        = 120 * time.Millisecond
)

// Model is the root Bubble Tea model.
type Model struct {
	cfg config.Config

	width, height int

	machines []*machine // local first

	// Sidebar tree.
	rows      []row
	cursor    string // selected row id
	scroll    int    // first visible row
	expanded  map[string]bool
	showAll   map[string]bool
	filter    string
	filtering bool // typing into the filter

	focus       focusArea
	zoom        bool
	prefixArmed bool
	sidebarW    int
	dragging    bool
	lastClickID string
	lastClickAt time.Time

	// The pane shown in the main area.
	viewMachine string
	viewing     string
	frame       *proto.Frame

	changes *changesView // the focused leaf's, when it shows a branch
	overlay overlay      // menu or dialog on top, if any

	// Tabs and splits in the main area.
	tabs        []*tab
	activeTab   int
	leafSeq     int
	frames      map[string]*proto.Frame // latest frame per visible pane (paneKey)
	subscribed  map[string]bool
	barDrag     *splitBar // a split boundary being dragged
	pendingShow string    // row to put on screen once the tree has it

	offset     int        // lines the viewed pane is scrolled back
	scrollMode bool       // keys move a cursor over the pane's history
	curX, curY int        // that cursor, in view cells
	sel        *selection // text selected in the viewed pane
	statePath  string     // where fold state is saved; "" disables saving

	flash      string
	flashIsErr bool
	flashUntil time.Time
	flashTimer bool // a flashExpiredMsg is on its way

	spin    int
	ticking bool

	brain *brainState // summaries and command bar history

	snoozeUntil time.Time // alerts are silenced until then

	sessions     map[string]*sessionsData // saved agent sessions per project (sessionsKey)
	sessionsView *sessionsView            // the focused leaf's, when it lists sessions
}

type (
	flashMsg string
	errMsg   struct{ err error }
	tickMsg  struct{}
	// createdMsg reports a new pane on a machine, to be selected.
	createdMsg struct {
		machine string
		info    proto.PaneInfo
		note    string // shown in the status bar, e.g. local files copied
	}
)

// New builds the TUI around a connected local client. Saved remote
// machines connect in the background once the program starts.
func New(local *client.Client, cfg config.Config) Model {
	applyTheme(cfg.UI.Theme, cfg.UI.Accent)
	path := uiStatePath()
	st := loadUIState(path)
	m := Model{
		cfg:        cfg,
		expanded:   st.Expanded,
		showAll:    st.ShowAll,
		sidebarW:   defaultSidebarWidth,
		statePath:  path,
		frames:     map[string]*proto.Frame{},
		subscribed: map[string]bool{},
		brain:      newBrainState(),
		sessions:   map[string]*sessionsData{},
	}
	m.restoreTabs(st.Tabs, st.ActiveTab)
	if st.SidebarWidth > 0 {
		m.sidebarW = st.SidebarWidth
	}
	lm := newMachine(localMachine, localMachine, "")
	lm.attach(local)
	if missing := local.MissingCapabilities(proto.Capabilities); len(missing) > 0 {
		lm.warning = "server is from an older build · restart it (conch server stop)"
	}
	m.machines = append(m.machines, lm)
	saved, _ := remote.Machines()
	for _, sm := range saved {
		if sm.Enabled {
			m.machines = append(m.machines, newMachine(sm.ID, sm.Label, sm.Target))
		}
	}
	return m
}

// Init starts every machine's connection work.
func (m Model) Init() tea.Cmd {
	var cmds []tea.Cmd
	for _, mach := range m.machines {
		if mach.c != nil {
			cmds = append(cmds, mach.listen()...)
		} else {
			cmds = append(cmds, mach.connect(false))
		}
	}
	return tea.Batch(cmds...)
}

// callOn runs a request on a machine in the background; done builds the
// message sent on success, errors become errMsg.
func (m Model) callOn(mid, method string, params, out any, done func() tea.Msg) tea.Cmd {
	mach := m.machine(mid)
	if mach == nil || mach.c == nil {
		return func() tea.Msg { return errMsg{errString(m.offlineText(mid))} }
	}
	c := mach.c
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := c.Call(ctx, method, params, out); err != nil {
			return errMsg{err}
		}
		if done == nil {
			return nil
		}
		return done()
	}
}

func (m Model) offlineText(mid string) string {
	if mach := m.machine(mid); mach != nil {
		return mach.label + " is " + mach.state.String()
	}
	return "unknown machine " + mid
}

// Update handles messages.
func (m Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.sidebarW = clamp(m.sidebarW, minSidebarWidth, min(maxSidebarWidth, max(m.width/2, minSidebarWidth)))
		return m, m.rebuild()

	case machineEventMsg:
		mach := m.machine(msg.machine)
		if mach == nil || msg.gen != mach.gen {
			return m, nil // from a connection that has since been replaced
		}
		cmd := m.handleEvent(mach, msg.msg)
		return m, tea.Batch(mach.waitEvent(), cmd, m.startTicking())

	case machineClosedMsg:
		mach := m.machine(msg.machine)
		if mach == nil || msg.gen != mach.gen {
			return m, nil
		}
		mach.lost(msg.err)
		for key := range m.subscribed {
			if strings.HasPrefix(key, mach.id+"|") {
				delete(m.subscribed, key) // the server forgot them with the connection
				delete(m.frames, key)
			}
		}
		if m.viewMachine == mach.id {
			m.viewing, m.frame = "", nil
			if m.focus == focusMain {
				m.focus = focusSidebar
			}
		}
		return m, tea.Batch(m.rebuild(), mach.scheduleRetry())

	case machineRetryMsg:
		if mach := m.machine(msg.machine); mach != nil && msg.gen == mach.gen && mach.c == nil {
			return m, mach.connect(false)
		}
		return m, nil

	case machineConnectedMsg:
		mach := m.machine(msg.machine)
		if mach == nil || msg.gen != mach.gen {
			if msg.c != nil {
				msg.c.Close()
			}
			return m, nil
		}
		cmd := mach.connected(msg)
		for key, d := range m.sessions {
			if strings.HasPrefix(key, mach.id+"|") {
				d.at = time.Time{} // a new server may have found interrupted runs
			}
		}
		return m, tea.Batch(cmd, m.rebuild())

	case panesMsg:
		if mach := m.machine(msg.machine); mach != nil && msg.gen == mach.gen {
			mach.setPanes(msg.panes)
		}
		return m, tea.Batch(m.rebuild(), m.startTicking())

	case limitsMsg:
		if mach := m.machine(msg.machine); mach != nil && msg.gen == mach.gen {
			for _, l := range msg.limits {
				mach.setLimits(l)
			}
		}
		return m, nil

	case agentStatusMsg:
		if mach := m.machine(msg.machine); mach != nil && msg.gen == mach.gen {
			mach.available = map[string]proto.AgentAvailability{}
			mach.agentList = msg.agents
			for _, a := range msg.agents {
				mach.available[a.Name] = a
			}
		}
		return m, nil

	case projectsMsg:
		if mach := m.machine(msg.machine); mach != nil && msg.gen == mach.gen {
			mach.projects = msg.projects
		}
		return m, m.rebuild()

	case createdMsg:
		if mach := m.machine(msg.machine); mach != nil && mach.paneIndex(msg.info.ID) < 0 {
			mach.panes = append(mach.panes, msg.info)
		}
		m.revealPane(msg.machine, msg.info)
		m.focus = focusMain
		if msg.note != "" {
			m.setFlash(msg.note, false)
		}
		return m, tea.Batch(m.rebuild(), m.saveState())

	case askInstallMsg:
		mach := m.machine(msg.machine)
		if mach == nil {
			return m, nil
		}
		mid, agent := msg.machine, msg.agent
		m.overlay = newConfirm(fmt.Sprintf("%s isn't installed on %s. Install it now with the official installer? It runs in a pane you can watch; afterwards start it with c and log in.",
			agentLabel(agent), mach.label), func(m *Model) tea.Cmd { return m.installAgent(mid, agent) })
		return m, nil

	case installStartedMsg:
		mach := m.machine(msg.machine)
		if mach == nil {
			return m, nil
		}
		mach.installers[msg.info.ID] = msg.agent
		if mach.paneIndex(msg.info.ID) < 0 {
			mach.panes = append(mach.panes, msg.info)
		}
		m.revealPane(msg.machine, msg.info)
		var done tea.Cmd
		if p := mach.pane(msg.info.ID); p != nil && p.State == proto.PaneExited {
			done = m.installerDone(mach, *p) // it finished before we heard it started
		}
		return m, tea.Batch(m.rebuild(), done)

	case machineAddedMsg:
		mach := newMachine(msg.m.ID, msg.m.Label, msg.m.Target)
		for i, existing := range m.machines {
			if existing.id == mach.id {
				existing.close()
				m.machines = append(m.machines[:i], m.machines[i+1:]...)
				break
			}
		}
		m.machines = append(m.machines, mach)
		m.expanded[machineID(mach.id)] = true
		m.cursor = machineID(mach.id)
		m.setFlash("added "+mach.label, false)
		return m, tea.Batch(mach.connect(false), m.rebuild(), m.saveState())

	case changesMsg, diffMsg:
		for _, t := range m.tabs {
			for _, l := range t.root.leaves() {
				if l.changes != nil {
					l.changes.receive(msg)
				}
			}
		}
		return m, nil

	case errMsg:
		m.setFlash(msg.err.Error(), true)
		return m, nil

	case summaryMsg:
		m.receiveSummary(msg)
		return m, nil

	case sessionsMsg:
		m.receiveSessions(msg)
		return m, nil

	case sessionsStaleMsg:
		mid, pid, _ := strings.Cut(msg.key, "|")
		return m, m.loadSessions(mid, pid, true)

	case flashMsg:
		m.setFlash(string(msg), false)
		return m, nil

	case tickMsg:
		m.ticking = false
		m.spin++
		return m, m.startTicking()

	case tea.MouseMsg:
		return m.handleMouse(msg)

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	if m.overlay != nil {
		_, cmd := m.overlay.update(&m, msg)
		return m, cmd
	}
	return m, nil
}

func (m *Model) handleEvent(mach *machine, msg proto.Message) tea.Cmd {
	switch msg.Event {
	case proto.EventPaneFrame:
		var f proto.Frame
		if !decodeInto(msg, &f) || !m.subscribed[paneKey(mach.id, f.ID)] {
			return nil
		}
		m.frames[paneKey(mach.id, f.ID)] = &f
		if mach.id == m.viewMachine && f.ID == m.viewing {
			m.frame = &f
			m.offset = f.Offset // the server keeps it anchored as output arrives
		}
		return nil

	case proto.EventPaneCreated:
		var info proto.PaneInfo
		if decodeInto(msg, &info) && mach.paneIndex(info.ID) < 0 {
			mach.panes = append(mach.panes, info)
			return m.rebuild()
		}

	case proto.EventPaneUpdated, proto.EventPaneExited:
		var info proto.PaneInfo
		if !decodeInto(msg, &info) {
			return nil
		}
		i := mach.paneIndex(info.ID)
		if i < 0 {
			return nil
		}
		old := mach.panes[i]
		mach.panes[i] = info
		if info.Agent != nil {
			mach.agents[info.ID] = true
		}
		if info.State != proto.PaneRunning && m.isViewing(mach.id, info.ID) && m.focus == focusMain {
			m.focus = focusSidebar
		}
		var installed tea.Cmd
		if msg.Event == proto.EventPaneExited {
			installed = m.installerDone(mach, info)
		}
		return tea.Batch(m.rebuild(), m.notifyAttention(mach, old, info), installed, m.observeAgent(mach, old, info))

	case proto.EventPaneClosed:
		var ref proto.PaneRef
		if decodeInto(msg, &ref) {
			if i := mach.paneIndex(ref.ID); i >= 0 {
				mach.panes = append(mach.panes[:i], mach.panes[i+1:]...)
				delete(mach.sizes, ref.ID)
				delete(mach.agents, ref.ID)
				delete(m.subscribed, paneKey(mach.id, ref.ID))
				delete(m.frames, paneKey(mach.id, ref.ID))
				if m.isViewing(mach.id, ref.ID) && m.focus == focusMain {
					m.focus = focusSidebar
				}
				return m.rebuild()
			}
		}

	case proto.EventProjectUpdated:
		var info proto.ProjectInfo
		if !decodeInto(msg, &info) {
			return nil
		}
		found := false
		for i := range mach.projects {
			if mach.projects[i].ID == info.ID {
				mach.projects[i], found = info, true
			}
		}
		if !found {
			mach.projects = append(mach.projects, info)
		}
		cmds := []tea.Cmd{m.rebuild()}
		for _, l := range m.tab().root.leaves() {
			if cv := l.changes; cv != nil && cv.machine == mach.id && cv.projectID == info.ID {
				cmds = append(cmds, cv.reload(m))
			}
		}
		return tea.Batch(cmds...)

	case proto.EventAgentLimits:
		var l proto.PlanLimits
		if decodeInto(msg, &l) {
			mach.setLimits(l)
		}
		return nil

	case proto.EventProjectRemoved:
		var ref proto.ProjectRef
		if decodeInto(msg, &ref) {
			for i := range mach.projects {
				if mach.projects[i].ID == ref.ID {
					mach.projects = append(mach.projects[:i], mach.projects[i+1:]...)
					break
				}
			}
			return m.rebuild()
		}
	}
	return nil
}

func (m Model) isViewing(mid, paneID string) bool {
	return m.viewMachine == mid && m.viewing == paneID
}

func (m Model) notifyAttention(mach *machine, old, info proto.PaneInfo) tea.Cmd {
	if m.isViewing(mach.id, info.ID) || !info.Agent.NeedsAttention() ||
		(old.Agent != nil && old.Agent.State == info.Agent.State) {
		return nil
	}
	if (info.Agent.State == proto.AgentBlocked && !m.cfg.Notify.Waiting) ||
		(info.Agent.State == proto.AgentDone && !m.cfg.Notify.Done) {
		return nil
	}
	body := info.DisplayName() + " finished"
	if info.Agent.State == proto.AgentBlocked {
		body = info.DisplayName() + " is waiting for you"
		if info.Agent.Message != "" {
			body = info.Agent.Message
		}
	}
	title := "conch · " + info.Agent.Name
	if mach.id != localMachine {
		title += " on " + mach.label
	}
	if m.silenced(time.Now()) {
		return nil // the sidebar still shows it as waiting
	}
	return notify(m.cfg.Notify, title, body)
}

// rebuild recomputes the tree after data or expansion changed, keeps the
// cursor on the same node (or its nearest surviving neighbour) and syncs
// the main area with it.
func (m *Model) rebuild() tea.Cmd {
	prevIndex := indexOfRow(m.rows, m.cursor)
	in := treeInput{expanded: m.expanded, showAll: m.showAll, filter: m.filter, now: time.Now()}
	for _, mach := range m.machines {
		in.machines = append(in.machines, treeMachine{id: mach.id, panes: mach.panes, projects: mach.projects, agents: mach.agents,
			sessions: m.hasSessions(mach.id)})
	}
	m.rows = buildTree(in)
	if indexOfRow(m.rows, m.cursor) < 0 && len(m.rows) > 0 {
		m.cursor = m.rows[clamp(prevIndex, 0, len(m.rows)-1)].id
	}
	m.keepCursorVisible()
	// Session counts in the tree; each list reloads at most every sessionsTTL.
	var loads []tea.Cmd
	for _, mach := range m.machines {
		for _, proj := range mach.projects {
			loads = append(loads, m.loadSessions(mach.id, proj.ID, false))
		}
	}
	if m.pendingShow != "" {
		if i := indexOfRow(m.rows, m.pendingShow); i >= 0 {
			m.pendingShow = ""
			return tea.Batch(append(loads, m.show(m.rows[i]))...)
		}
	}
	return tea.Batch(append(loads, m.syncView())...)
}

func (m *Model) selectedRow() (row, bool) {
	i := indexOfRow(m.rows, m.cursor)
	if i < 0 {
		return row{}, false
	}
	return m.rows[i], true
}

// revealPane expands the pane's ancestors and selects it.
func (m *Model) revealPane(mid string, p proto.PaneInfo) {
	m.expanded[machineID(mid)] = true
	if p.ProjectID != "" {
		m.expanded[projectNodeID(mid, p.ProjectID)] = true
		m.expanded[sectionID(mid, p.ProjectID, "agents")] = true
		m.expanded[sectionID(mid, p.ProjectID, "terminals")] = true
	}
	m.cursor = paneNodeID(mid, p.ID)
	m.pendingShow = m.cursor // shown once the next rebuild has its row
}

func (m *Model) moveCursor(delta int) tea.Cmd {
	i := indexOfRow(m.rows, m.cursor)
	if len(m.rows) == 0 {
		return nil
	}
	i = clamp(i+delta, 0, len(m.rows)-1)
	m.cursor = m.rows[i].id
	m.keepCursorVisible()
	return m.syncView()
}

// toggle expands or collapses a row; open forces a direction (nil toggles).
func (m *Model) toggle(r row, open *bool) tea.Cmd {
	if r.kind == kindMore {
		m.showAll[scoped(r.machine, r.projectID)] = true
		return tea.Batch(m.rebuild(), m.saveState())
	}
	if !r.expandable() {
		return nil
	}
	next := !m.isOpen(r)
	if open != nil {
		next = *open
	}
	m.expanded[r.id] = next
	return tea.Batch(m.rebuild(), m.saveState())
}

// isOpen reports whether a row's children are shown.
func (m Model) isOpen(r row) bool {
	i := indexOfRow(m.rows, r.id)
	return i >= 0 && i+1 < len(m.rows) && m.rows[i+1].depth > r.depth
}

func (m *Model) sidebarRowsVisible() int {
	_, h := m.sidebarInner()
	return max(h-1, 1) // first line is the header / filter
}

func (m *Model) keepCursorVisible() {
	i := indexOfRow(m.rows, m.cursor)
	visible := m.sidebarRowsVisible()
	if i < m.scroll {
		m.scroll = i
	}
	if i >= m.scroll+visible {
		m.scroll = i - visible + 1
	}
	m.scroll = clamp(m.scroll, 0, max(len(m.rows)-visible, 0))
}

// allPanes lists every pane on every machine.
func (m Model) allPanes() []scopedPane {
	var out []scopedPane
	for _, mach := range m.machines {
		for _, p := range mach.panes {
			out = append(out, scopedPane{machine: mach.id, PaneInfo: p})
		}
	}
	return out
}

func (m *Model) jumpToAttention() tea.Cmd {
	mid, id := nextAttention(m.allPanes(), scoped(m.viewMachine, m.viewing))
	if id == "" {
		m.setFlash("no agents need you", false)
		return nil
	}
	if p := m.pane(mid, id); p != nil {
		m.filter = ""
		m.revealPane(mid, *p)
	}
	return m.rebuild()
}

// clientOf is a machine's client, nil while it is disconnected.
func (m Model) clientOf(mid string) *client.Client {
	if mach := m.machine(mid); mach != nil {
		return mach.c
	}
	return nil
}

// viewClient is the client of the machine whose pane is shown.
func (m Model) viewClient() *client.Client {
	if mach := m.machine(m.viewMachine); mach != nil {
		return mach.c
	}
	return nil
}

// scrollPane moves the viewed pane's history view by delta lines (positive
// is back in time) and asks the server to render it.
func (m *Model) scrollPane(delta int) {
	c := m.viewClient()
	if m.frame == nil || m.viewing == "" || c == nil {
		return
	}
	next := clamp(m.offset+delta, 0, m.frame.History)
	if next == m.offset {
		return
	}
	if m.sel != nil && m.sel.keyboard {
		m.sel.ay += next - m.offset // the anchored text moves down as we scroll back
	} else {
		m.sel = nil
	}
	m.offset = next
	c.Notify(proto.MethodPaneScroll, proto.PaneScrollParams{ID: m.viewing, Offset: next})
}

// setFlash shows a message in the status bar for a few seconds (errors a
// little longer).
func (m *Model) setFlash(s string, isErr bool) {
	m.flash, m.flashIsErr = s, isErr
	d := flashFor
	if isErr {
		d = 2 * flashFor
	}
	m.flashUntil = time.Now().Add(d)
}

const flashFor = 4 * time.Second

type flashExpiredMsg struct{}

// Update handles a message, then schedules clearing the status bar message
// when one is showing.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(flashExpiredMsg); ok {
		m.flashTimer = false
		if m.flash != "" && !time.Now().Before(m.flashUntil) {
			m.flash = ""
		}
	}
	next, cmd := m.update(msg)
	nm := next.(Model)
	if nm.flash != "" && !nm.flashTimer {
		nm.flashTimer = true
		wait := max(time.Until(nm.flashUntil), 100*time.Millisecond)
		cmd = tea.Batch(cmd, tea.Tick(wait, func(time.Time) tea.Msg { return flashExpiredMsg{} }))
	}
	return nm, cmd
}

func (m Model) machine(id string) *machine {
	for _, mach := range m.machines {
		if mach.id == id {
			return mach
		}
	}
	return nil
}

func (m Model) pane(mid, id string) *proto.PaneInfo {
	if mach := m.machine(mid); mach != nil {
		return mach.pane(id)
	}
	return nil
}

func (m Model) project(mid, id string) *proto.ProjectInfo {
	if mach := m.machine(mid); mach != nil {
		for i := range mach.projects {
			if mach.projects[i].ID == id {
				return &mach.projects[i]
			}
		}
	}
	return nil
}

// branchPR returns the pull request of a branch, if known.
func (m Model) branchPR(mid, projectID, branch string) *proto.PRInfo {
	if proj := m.project(mid, projectID); proj != nil {
		for _, b := range proj.Branches {
			if b.Name == branch {
				return b.PR
			}
		}
	}
	return nil
}

// startTicking schedules a spinner frame while any agent is working.
func (m *Model) startTicking() tea.Cmd {
	if m.ticking {
		return nil
	}
	if b, ok := m.overlay.(*askBar); ok && b.thinking() {
		m.ticking = true
		return tea.Tick(spinInterval, func(time.Time) tea.Msg { return tickMsg{} })
	}
	for _, sp := range m.allPanes() {
		if sp.Agent != nil && sp.Agent.State == proto.AgentWorking {
			m.ticking = true
			return tea.Tick(spinInterval, func(time.Time) tea.Msg { return tickMsg{} })
		}
	}
	return nil
}

// place describes where a new pane for the selected row should run.
type place struct {
	machine   string
	projectID string
	dir       string // "" when branch must first be checked out, or the machine's home
	branch    string
}

func (m Model) contextPlace() place {
	r, _ := m.selectedRow()
	mid := r.machine
	if mid == "" {
		mid = localMachine
	}
	switch r.kind {
	case kindPane:
		if p := m.pane(mid, r.paneID); p != nil {
			return place{machine: mid, projectID: p.ProjectID, dir: p.Cwd, branch: p.Branch}
		}
	case kindBranch:
		proj := m.project(mid, r.projectID)
		if proj == nil {
			break
		}
		for _, wt := range proj.Worktrees {
			if wt.Branch == r.branch {
				return place{machine: mid, projectID: proj.ID, dir: wt.Path, branch: r.branch}
			}
		}
		return place{machine: mid, projectID: proj.ID, branch: r.branch}
	case kindProject, kindBranches, kindAgents, kindTerminals, kindMore, kindSessions:
		if proj := m.project(mid, r.projectID); proj != nil {
			return place{machine: mid, projectID: proj.ID, dir: proj.Path}
		}
	}
	pl := place{machine: mid}
	if mid == localMachine {
		pl.dir, _ = os.Getwd()
	} else if mach := m.machine(mid); mach != nil {
		pl.dir = mach.server.Home
	}
	return pl
}

// openHere starts a shell or agent at the selected place, checking the
// branch out into a worktree first when needed.
func (m Model) openHere(withAgent bool) tea.Cmd {
	agent := ""
	if withAgent {
		agent = m.defaultAgent()
	}
	return m.openAgent(agent)
}

// defaultAgent is the agent c starts.
func (m Model) defaultAgent() string {
	if a := m.cfg.Agents.Default; a != "" {
		return a
	}
	return "claude"
}

// openAgent starts agent (or a shell when "") at the selected place.
func (m Model) openAgent(agent string) tea.Cmd {
	pl := m.contextPlace()
	mach := m.machine(pl.machine)
	if mach == nil || mach.c == nil {
		return func() tea.Msg { return errMsg{errString(m.offlineText(pl.machine))} }
	}
	if agent != "" && mach.missingAgent(agent) {
		return func() tea.Msg { return askInstallMsg{machine: pl.machine, agent: agent} }
	}
	cols, rows := m.paneArea()
	c := mach.c
	shellTheme := m.cfg.Shell.OMZTheme
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		dir, note := pl.dir, ""
		if dir == "" && pl.branch != "" {
			var wt proto.WorktreeResult
			err := c.Call(ctx, proto.MethodWorktreeAdd, proto.WorktreeAddParams{ProjectID: pl.projectID, Branch: pl.branch}, &wt)
			if err != nil {
				return errMsg{err}
			}
			dir = wt.Path
			if len(wt.Copied) > 0 {
				note = "worktree created with local files: " + strings.Join(wt.Copied, ", ")
			}
		}
		// No command: the server starts its machine's login shell.
		params := proto.PaneCreateParams{Cwd: dir, Cols: cols, Rows: rows}
		if agent != "" {
			params.Agent = agent
		} else {
			params.ShellTheme = shellTheme
		}
		var info proto.PaneInfo
		if err := c.Call(ctx, proto.MethodPaneCreate, params, &info); err != nil {
			return errMsg{err}
		}
		return createdMsg{machine: pl.machine, info: info, note: note}
	}
}

type (
	askInstallMsg struct {
		machine, agent string
	}
	installStartedMsg struct {
		machine, agent string
		info           proto.PaneInfo
	}
)

// agentLabels name agents for people; servers send labels too, these cover
// messages about agents a machine hasn't described.
var agentLabels = map[string]string{"claude": "Claude Code", "codex": "Codex", "gemini": "Gemini CLI", "opencode": "OpenCode"}

func agentLabel(agent string) string {
	if l, ok := agentLabels[agent]; ok {
		return l
	}
	return agent
}

// installerDone reports the outcome when an installer pane exits.
func (m *Model) installerDone(mach *machine, info proto.PaneInfo) tea.Cmd {
	agent, ok := mach.installers[info.ID]
	if !ok {
		return nil
	}
	delete(mach.installers, info.ID)
	if info.ExitCode == 0 {
		m.setFlash(fmt.Sprintf("%s installed on %s · press c to start it and log in", agentLabel(agent), mach.label), false)
	} else {
		m.setFlash(fmt.Sprintf("installing %s on %s failed (exit %d) · the pane shows why", agentLabel(agent), mach.label, info.ExitCode), true)
	}
	return mach.checkAgents()
}

// installAgent runs an agent's installer on a machine, in a pane.
func (m Model) installAgent(mid, agent string) tea.Cmd {
	cols, rows := m.paneArea()
	var info proto.PaneInfo
	return m.callOn(mid, proto.MethodAgentInstall, proto.AgentInstallParams{Agent: agent, Cols: cols, Rows: rows}, &info,
		func() tea.Msg { return installStartedMsg{machine: mid, agent: agent, info: info} })
}

// tildify shortens a path on machine mid with ~ for its home.
func (m Model) tildify(mid, p string) string {
	home := ""
	if mid == localMachine || mid == "" {
		home, _ = os.UserHomeDir()
	} else if mach := m.machine(mid); mach != nil {
		home = mach.server.Home
	}
	switch {
	case home == "":
		return p
	case p == home:
		return "~"
	case strings.HasPrefix(p, home+"/"):
		return "~" + p[len(home):]
	}
	return p
}

func clamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	return max(lo, min(v, hi))
}
