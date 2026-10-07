package tui

import (
	"maps"
	"slices"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
)

// Workspaces are separate sets of tabs and of the tree, picked along the
// sidebar's top border beside conch, as tmux has sessions. Each shows only
// what belongs to it: the projects added in it, the panes started in it,
// its machines, saved hosts and folders, with tabs of its own. A project can
// belong to several, and its agents and sessions then show in each.
//
// What belongs to no workspace — everything from before there were any,
// and what the conch command makes outside the TUI — is the first one's,
// so nothing is ever out of reach. Bringing something that already exists
// into another workspace keeps it in the first rather than taking it away.
// Panes keep running whichever workspace is on screen, and closing one
// moves what it holds to the first: nothing ends.

// space is one workspace. The one on screen keeps its tabs in the model;
// the rest keep theirs here until they are shown again.
type space struct {
	name     string
	projects map[string]bool // scoped(machine, project)
	panes    map[string]bool // paneKey: started here, outside its projects
	folders  map[string]bool // spaceFolder: folders made here, shown even empty
	machines map[string]bool // machine IDs added here, shown even with nothing on them
	hosts    map[string]bool // saved ssh hosts saved here

	tabs       []*tab
	activeTab  int
	preview    *tab
	previewing bool
	scopeTab   map[string]*tab
	lastTab    *tab
	cursor     string
	scroll     int
}

// savedSpace is a workspace in ui.json. The first one's tabs stay in the
// top-level tabs, which is what older builds read.
type savedSpace struct {
	Name      string     `json:"name,omitempty"`
	Projects  []string   `json:"projects,omitempty"`
	Panes     []string   `json:"panes,omitempty"`
	Folders   []string   `json:"folders,omitempty"`
	Machines  []string   `json:"machines,omitempty"`
	Hosts     []string   `json:"hosts,omitempty"`
	Tabs      []savedTab `json:"tabs,omitempty"`
	ActiveTab int        `json:"active_tab,omitempty"`
}

func newSpace() *space {
	return &space{projects: map[string]bool{}, panes: map[string]bool{}, folders: map[string]bool{},
		machines: map[string]bool{}, hosts: map[string]bool{}}
}

// cur is the workspace on screen, or nil while there is only the one and
// everything is in it.
func (m Model) cur() *space {
	if len(m.spaces) < 2 || m.activeSpace < 0 || m.activeSpace >= len(m.spaces) {
		return nil
	}
	return m.spaces[m.activeSpace]
}

// spaceCount is how many workspaces there are; with none made, the one.
func (m Model) spaceCount() int { return max(len(m.spaces), 1) }

// claimed is whether a workspace after the first has key in the set pick
// gives: something no later one has is the first's.
func (m Model) claimed(pick func(*space) map[string]bool, key string) bool {
	for _, s := range m.spaces[min(1, len(m.spaces)):] {
		if pick(s)[key] {
			return true
		}
	}
	return false
}

func projectsOf(s *space) map[string]bool { return s.projects }
func foldersOf(s *space) map[string]bool  { return s.folders }
func hostsOf(s *space) map[string]bool    { return s.hosts }

// hasProject is whether workspace i shows project key.
func (m Model) hasProject(i int, key string) bool {
	return m.spaces[i].projects[key] || i == 0 && !m.claimed(projectsOf, key)
}

// hasHost is whether workspace i shows saved host target.
func (m Model) hasHost(i int, target string) bool {
	return m.spaces[i].hosts[target] || i == 0 && !m.claimed(hostsOf, target)
}

// spaceShown is a machine's projects and panes as workspace i has them:
// its projects with everything in them, the panes started in it, what
// those panes started in turn — a helper is in the workspace of the agent
// that asked for it — and, in the first, every pane no other has.
func (m Model) spaceShown(i int, mach *machine) ([]proto.ProjectInfo, []proto.PaneInfo) {
	s := m.spaces[i]
	known := map[string]bool{}
	var projs []proto.ProjectInfo
	for _, p := range mach.projects {
		known[p.ID] = true
		if m.hasProject(i, scoped(mach.id, p.ID)) {
			projs = append(projs, p)
		}
	}
	elsewhere := map[string]bool{}
	if i == 0 {
		for j := 1; j < len(m.spaces); j++ {
			_, panes := m.spaceShown(j, mach)
			for _, p := range panes {
				elsewhere[p.ID] = true
			}
		}
	}
	in := map[string]bool{}
	for _, p := range mach.panes {
		inProject := known[p.ProjectID] && m.hasProject(i, scoped(mach.id, p.ProjectID))
		if inProject || s.panes[paneKey(mach.id, p.ID)] || i == 0 && !elsewhere[p.ID] {
			in[p.ID] = true
		}
	}
	for grew := true; grew; {
		grew = false
		for _, p := range mach.panes {
			if !in[p.ID] && p.CreatedBy != "" && in[p.CreatedBy] {
				in[p.ID], grew = true, true
			}
		}
	}
	var out []proto.PaneInfo
	for _, p := range mach.panes {
		if in[p.ID] {
			out = append(out, p)
		}
	}
	return projs, out
}

// shown is a machine's projects and panes as the workspace on screen has
// them. Pages and counts read this, so a workspace never counts what its
// tree leaves out.
func (m Model) shown(mach *machine) ([]proto.ProjectInfo, []proto.PaneInfo) {
	if m.cur() == nil {
		return mach.projects, mach.panes
	}
	return m.spaceShown(m.activeSpace, mach)
}

// hasMachine is whether workspace i lists machine mach: this computer —
// somewhere to add a project or start an agent is needed from the start —
// the machines added in it, any it holds a project or pane on, and, in the
// first, any no other lists.
func (m Model) hasMachine(i int, mach *machine) bool {
	if mach.id == localMachine || m.spaces[i].machines[mach.id] {
		return true
	}
	if projects, panes := m.spaceShown(i, mach); len(projects) > 0 || len(panes) > 0 {
		return true
	}
	if i != 0 {
		return false
	}
	for j := 1; j < len(m.spaces); j++ {
		if m.hasMachine(j, mach) {
			return false
		}
	}
	return true
}

// showsMachine is whether the workspace on screen lists machine mach.
func (m Model) showsMachine(mach *machine) bool {
	return m.cur() == nil || m.hasMachine(m.activeSpace, mach)
}

// showsHost is whether target is a saved host the workspace on screen lists.
func (m Model) showsHost(target string) bool {
	return slices.Contains(m.savedSSH, target) && (m.cur() == nil || m.hasHost(m.activeSpace, target))
}

// shownHosts is the saved hosts the workspace on screen lists, in order.
func (m Model) shownHosts() []string {
	if m.cur() == nil {
		return m.savedSSH
	}
	var out []string
	for _, t := range m.savedSSH {
		if m.hasHost(m.activeSpace, t) {
			out = append(out, t)
		}
	}
	return out
}

// spaceFolder names a folder for a workspace's own list: its section's key
// and its name, which is unique in the section.
func spaceFolder(key, name string) string { return key + "\n" + name }

// hasFolder is whether workspace i shows folder f of section key: one made
// in it, one holding a pane or saved host it shows, and, in the first, one
// no other workspace made.
func (m Model) hasFolder(i int, key string, f savedFolder) bool {
	k := spaceFolder(key, f.Name)
	if m.spaces[i].folders[k] || i == 0 && !m.claimed(foldersOf, k) {
		return true
	}
	if mach := m.machine(folderKeyMachine(key)); mach != nil {
		if _, panes := m.spaceShown(i, mach); len(f.claim(panes, map[string]bool{})) > 0 {
			return true
		}
	}
	return slices.ContainsFunc(f.Members, func(mem savedMember) bool { return mem.Host != "" && m.hasHost(i, mem.Host) })
}

// showsFolder is whether the workspace on screen shows folder f of key.
func (m Model) showsFolder(key string, f savedFolder) bool {
	return m.cur() == nil || m.hasFolder(m.activeSpace, key, f)
}

// shownFolders is the folders the workspace on screen shows.
func (m Model) shownFolders() map[string][]savedFolder {
	if m.cur() == nil {
		return m.folders
	}
	out := map[string][]savedFolder{}
	for key, fs := range m.folders {
		for _, f := range fs {
			if m.hasFolder(m.activeSpace, key, f) {
				out[key] = append(out[key], f)
			}
		}
	}
	return out
}

// shownPanes is the panes the workspace on screen shows on machine mid.
func (m Model) shownPanes(mid string) []proto.PaneInfo {
	mach := m.machine(mid)
	if mach == nil {
		return nil
	}
	_, panes := m.shown(mach)
	return panes
}

// keepInFirst is called before something that already exists joins
// workspace i: the first keeps it, if it was showing it only for belonging
// to no workspace — joining another would otherwise take it away.
func (m *Model) keepInFirst(i int, pick func(*space) map[string]bool, key string, had bool) {
	if i > 0 && had {
		pick(m.spaces[0])[key] = true
	}
}

// joinSpace puts a pane just started, outside every project the workspace
// on screen has, in that workspace. In the first, a pane belonging to no
// workspace is already there.
func (m *Model) joinSpace(mid, projectID, paneID string) {
	s := m.cur()
	if s == nil || m.activeSpace == 0 || paneID == "" {
		return
	}
	if projectID != "" && m.project(mid, projectID) != nil && m.hasProject(m.activeSpace, scoped(mid, projectID)) {
		return
	}
	s.panes[paneKey(mid, paneID)] = true
}

// spaceProjectMsg is a project added from a workspace, to be put in it.
// known is the machine's projects when it was asked for, to tell one that
// was already there — shared, and kept where it was — from a new one.
type spaceProjectMsg struct {
	space   *space
	machine string
	info    proto.ProjectInfo
	flash   string
	known   map[string]bool
}

// addedProject is what a project add says when it returns: the project
// joins the workspace it was added from, even if another is shown by then.
func (m *Model) addedProject(mid string, info *proto.ProjectInfo, flash string) func() tea.Msg {
	s := m.cur()
	known := map[string]bool{}
	if mach := m.machine(mid); mach != nil {
		for _, p := range mach.projects {
			known[p.ID] = true
		}
	}
	return func() tea.Msg {
		return spaceProjectMsg{space: s, machine: mid, info: *info, flash: flash + info.Name, known: known}
	}
}

func (m *Model) receiveSpaceProject(msg spaceProjectMsg) tea.Cmd {
	m.setFlash(msg.flash, false)
	i := slices.Index(m.spaces, msg.space)
	if msg.space == nil || i < 0 || msg.info.ID == "" {
		return nil
	}
	key := scoped(msg.machine, msg.info.ID)
	m.keepInFirst(i, projectsOf, key, msg.known[msg.info.ID] && m.hasProject(0, key))
	msg.space.projects[key] = true
	m.expanded[machineID(msg.machine)] = true
	m.expanded[workspaceID(msg.machine)] = true
	return tea.Batch(m.rebuild(), m.saveState())
}

// otherHas is whether a workspace besides the one on screen has what has
// asks about, by index: x then takes it out of this one rather than out of
// conch.
func (m Model) otherHas(has func(i int) bool) bool {
	if m.cur() == nil {
		return false
	}
	for i := range m.spaces {
		if i != m.activeSpace && has(i) {
			return true
		}
	}
	return false
}

// otherHasProject, otherHasMachine and otherHasHost ask otherHas about one.
func (m Model) otherHasProject(mid, pid string) bool {
	return m.otherHas(func(i int) bool { return m.hasProject(i, scoped(mid, pid)) })
}

func (m Model) otherHasMachine(mach *machine) bool {
	return m.otherHas(func(i int) bool { return m.hasMachine(i, mach) })
}

func (m Model) otherHasHost(target string) bool {
	return m.otherHas(func(i int) bool { return m.hasHost(i, target) })
}

// leaveSpace takes a project out of the workspace on screen, leaving it on
// the machine and in the other workspaces that have it.
func (m *Model) leaveSpace(mid, projectID string) tea.Cmd {
	s := m.cur()
	if s == nil {
		return nil
	}
	delete(s.projects, scoped(mid, projectID))
	return tea.Batch(m.rebuild(), m.saveState())
}

// hiddenMachines is the machines conch has that the workspace on screen
// does not list, for bringing one here.
func (m Model) hiddenMachines() []*machine {
	var out []*machine
	for _, mach := range m.machines {
		if !m.showsMachine(mach) {
			out = append(out, mach)
		}
	}
	return out
}

// joinMachine lists machine mid in the workspace on screen. had says it
// was already in conch, so the first keeps it if it listed it.
func (m *Model) joinMachine(mid string, had bool) {
	s := m.cur()
	if s == nil || mid == localMachine {
		return
	}
	if mach := m.machine(mid); had && mach != nil && m.activeSpace > 0 && m.hasMachine(0, mach) {
		m.spaces[0].machines[mid] = true
	}
	s.machines[mid] = true
}

// openBringMachine offers the machines this workspace does not list.
func (m *Model) openBringMachine(back *menu) {
	mu := &menu{title: "Machine already in conch", back: back}
	for i, mach := range m.hiddenMachines() {
		key := ""
		if i < 9 {
			key = itoa(i + 1)
		}
		mid := mach.id
		mu.items = append(mu.items, menuItem{key, mach.label, func(m *Model) tea.Cmd {
			m.joinMachine(mid, true)
			m.expanded[machineID(mid)] = true
			m.cursor = machineID(mid)
			return tea.Batch(m.rebuild(), m.saveState())
		}})
	}
	m.overlay = mu
}

// addMachineMenu is M's menu, with the machines conch already has that
// this workspace does not list as a way to bring one here.
func (m *Model) addMachineMenu() *menu {
	mu := newAddMenu()
	if m.cur() != nil && len(m.hiddenMachines()) > 0 {
		mu.items = append(mu.items, menuItem{"e", "Machine already in conch…", func(m *Model) tea.Cmd {
			m.openBringMachine(m.addMachineMenu())
			return nil
		}})
	}
	return mu
}

// leaveMachine takes a machine out of the workspace on screen, with its
// projects and the panes started there: what is on it is no longer this
// workspace's. Nothing ends, and the other workspaces keep it. The first
// cannot let go of what belongs to no other workspace, so a machine with
// some of that on it stays there, and says why.
func (m *Model) leaveMachine(mid string) tea.Cmd {
	s := m.cur()
	if s == nil {
		return nil
	}
	delete(s.machines, mid)
	for k := range s.projects {
		if strings.HasPrefix(k, scoped(mid, "")) { // mid~project
			delete(s.projects, k)
		}
	}
	for k := range s.panes {
		if strings.HasPrefix(k, mid+"|") {
			delete(s.panes, k)
		}
	}
	if mach := m.machine(mid); mach != nil && m.showsMachine(mach) {
		m.setFlash(mach.label+" stays: what is on it belongs to no other workspace", false)
	}
	return tea.Batch(m.rebuild(), m.saveState())
}

// joinHost lists saved host target in the workspace on screen. had says it
// was already saved, so the first keeps it if it listed it.
func (m *Model) joinHost(target string, had bool) {
	s := m.cur()
	if s == nil {
		return
	}
	m.keepInFirst(m.activeSpace, hostsOf, target, had && m.hasHost(0, target))
	s.hosts[target] = true
}

// renameHost carries a saved host's place in every workspace to its new
// target when the host itself is edited.
func (m *Model) renameHost(old, target string) {
	for _, s := range m.spaces {
		if s.hosts[old] {
			delete(s.hosts, old)
			s.hosts[target] = true
		}
	}
}

// leaveHost takes a saved host out of the workspace on screen; the other
// workspaces that have it keep it.
func (m *Model) leaveHost(target string) tea.Cmd {
	s := m.cur()
	if s == nil {
		return nil
	}
	delete(s.hosts, target)
	m.takeHostOutOfFolders(target)
	return tea.Batch(m.rebuild(), m.saveState())
}

// park keeps what is on screen in workspace s; load puts s back.
func (m *Model) park(s *space) {
	s.tabs, s.activeTab, s.preview, s.previewing = m.tabs, m.activeTab, m.preview, m.previewing
	s.scopeTab, s.lastTab, s.cursor, s.scroll = m.scopeTab, m.lastTab, m.cursor, m.scroll
}

func (m *Model) load(s *space) {
	m.tabs, m.activeTab, m.preview, m.previewing = s.tabs, s.activeTab, s.preview, s.previewing
	m.scopeTab, m.lastTab, m.cursor, m.scroll = s.scopeTab, s.lastTab, s.cursor, s.scroll
	s.tabs, s.preview, s.scopeTab, s.lastTab = nil, nil, nil, nil
	m.seenTab, m.zoom, m.sel, m.scrollMode = nil, false, nil, false
	// The tab it was showing is shown again, not one picked for the cursor.
	m.keepTab, m.pickedFor = true, m.cursor
	// A pane that ended while the workspace was away leaves its tabs now:
	// only the tabs on screen hear about it when it happens.
	for _, t := range slices.Clone(m.tabs) {
		for _, l := range t.root.leaves() {
			v := l.view
			if v.Kind != kindPane {
				continue
			}
			if mach := m.machine(v.Machine); mach != nil && mach.state == stateOnline && mach.pane(v.PaneID) == nil {
				m.dropPane(v.Machine, v.PaneID)
			}
		}
	}
}

// enterSpace shows workspace i instead of the one on screen.
func (m *Model) enterSpace(i int) bool {
	if i < 0 || i >= len(m.spaces) || i == m.activeSpace {
		return false
	}
	m.park(m.spaces[m.activeSpace])
	m.activeSpace = i
	m.load(m.spaces[i])
	return true
}

func (m *Model) switchSpace(i int) tea.Cmd {
	if !m.enterSpace(i) {
		return nil
	}
	m.focus = focusSidebar
	return tea.Batch(m.rebuild(), m.saveState())
}

// newSpace makes an empty workspace after the last and shows it.
func (m *Model) newSpace() tea.Cmd {
	if len(m.spaces) == 0 {
		m.spaces = []*space{newSpace()}
		m.activeSpace = 0
	}
	m.spaces = append(m.spaces, newSpace())
	cmd := m.switchSpace(len(m.spaces) - 1)
	m.setFlash("workspace "+itoa(m.activeSpace+1)+": a adds a project, c starts an agent", false)
	return cmd
}

// stepSpace goes to the next or previous workspace, round the ends.
func (m *Model) stepSpace(d int) tea.Cmd {
	n := len(m.spaces)
	if n < 2 {
		m.setFlash("one workspace — the + beside conch makes another", false)
		return nil
	}
	return m.switchSpace(((m.activeSpace+d)%n + n) % n)
}

// closeSpaceAsk closes the workspace on screen, after asking. What it
// holds moves to the first: its agents and terminals keep running, and its
// projects, machines, hosts, folders and tabs are the first one's now.
func (m *Model) closeSpaceAsk() tea.Cmd {
	i := m.activeSpace
	if i <= 0 || i >= len(m.spaces) {
		m.setFlash("the first workspace stays", false)
		return nil
	}
	m.overlay = newConfirm("Close workspace "+m.spaceLabel(i)+"? Its agents, terminals, projects, hosts and tabs move to workspace 1; nothing ends.",
		func(m *Model) tea.Cmd { return m.closeSpace(i) })
	return nil
}

func (m *Model) closeSpace(i int) tea.Cmd {
	if i <= 0 || i >= len(m.spaces) {
		return nil
	}
	if i == m.activeSpace {
		m.enterSpace(0)
	}
	gone, first := m.spaces[i], m.spaces[0]
	for _, pick := range []func(*space) map[string]bool{projectsOf, foldersOf, hostsOf,
		func(s *space) map[string]bool { return s.panes }, func(s *space) map[string]bool { return s.machines }} {
		maps.Copy(pick(first), pick(gone))
	}
	if m.activeSpace == 0 {
		m.tabs = append(m.tabs, gone.tabs...)
	} else {
		first.tabs = append(first.tabs, gone.tabs...)
	}
	m.spaces = slices.Delete(m.spaces, i, i+1)
	if m.activeSpace > i {
		m.activeSpace--
	}
	if len(m.spaces) == 1 {
		m.spaces, m.activeSpace = nil, 0 // just the one again: everything is in it
	}
	return tea.Batch(m.rebuild(), m.saveState())
}

func (m *Model) renameSpaceAsk() tea.Cmd {
	if len(m.spaces) == 0 {
		m.spaces = []*space{newSpace()}
	}
	s := m.spaces[m.activeSpace]
	d := newDialog(*m, " Rename workspace ", []string{"Leave empty to number it."}, []string{"Name"}, []string{s.name})
	d.submit = func(m *Model, v []string) tea.Cmd {
		s.name = strings.TrimSpace(v[0])
		return m.saveState()
	}
	m.overlay = d
	return d.focusCmd()
}

// spaceLabel is what the header calls workspace i: its number, and its
// name when it has one.
func (m Model) spaceLabel(i int) string {
	if i >= 0 && i < len(m.spaces) && m.spaces[i].name != "" {
		return itoa(i+1) + " " + m.spaces[i].name
	}
	return itoa(i + 1)
}

// Header buttons, where a spaceHit's space is otherwise its index.
const (
	spaceHitPlus  = -1
	spaceHitClose = -2
)

type spaceHit struct {
	x0, x1 int
	space  int
}

// spaceHeader is the sidebar's title: conch, then the workspaces and a +,
// fitted to w columns, with where each is. The + is kept before any
// workspace is: it is the one way to make another. When the names don't
// fit the workspaces are numbered, and when the numbers don't either, the
// one on screen is kept with those after it.
func (m Model) spaceHeader(w int) (string, []spaceHit) {
	const conch = " ◆ conch "
	const plus = " + "
	title := styleSel.Render(conch)
	x0 := ansi.StringWidth(conch)
	if x0+len(plus) > w {
		return title, nil // the frame shortens it; no room for buttons
	}
	type chip struct {
		label string
		space int
	}
	chips := func(named bool, from int) ([]chip, int) {
		var out []chip
		width := 0
		if len(m.spaces) < 2 {
			return nil, 0
		}
		for i := from; i < len(m.spaces); i++ {
			label := " " + itoa(i+1) + " "
			if named {
				label = " " + m.spaceLabel(i) + " "
			}
			out = append(out, chip{label, i})
			width += ansi.StringWidth(label)
			if i == m.activeSpace && i > 0 {
				out = append(out, chip{"× ", spaceHitClose})
				width += 2
			}
		}
		return out, width
	}
	room := w - x0 - len(plus)
	list, width := chips(true, 0)
	if width > room {
		list, width = chips(false, 0)
	}
	for from := 1; width > room && from <= m.activeSpace; from++ {
		list, width = chips(false, from)
	}
	var b strings.Builder
	b.WriteString(title)
	var hits []spaceHit
	x := x0
	for _, c := range list {
		sw := ansi.StringWidth(c.label)
		if x+sw > w-len(plus) {
			break
		}
		style := styleMuted
		if c.space == m.activeSpace {
			style = styleSel
		}
		b.WriteString(style.Render(c.label))
		hits = append(hits, spaceHit{x0: x, x1: x + sw, space: c.space})
		x += sw
	}
	b.WriteString(styleAccent.Render(plus))
	hits = append(hits, spaceHit{x0: x, x1: x + len(plus), space: spaceHitPlus})
	return b.String(), hits
}

// spaceAt is what is at column x of the sidebar's top border: a workspace's
// index, spaceHitPlus, spaceHitClose, or ok false for nothing.
func (m Model) spaceAt(x int) (int, bool) {
	sw, _ := m.sidebarInner()
	_, hits := m.spaceHeader(max(sw-2, 0))
	x -= 2 // the corner and the line before the title
	for _, h := range hits {
		if x >= h.x0 && x < h.x1 {
			return h.space, true
		}
	}
	return 0, false
}

// clickSpace acts on a press on the sidebar's top border.
func (m *Model) clickSpace(x int) tea.Cmd {
	sp, ok := m.spaceAt(x)
	switch {
	case !ok:
		return nil
	case sp == spaceHitPlus:
		return m.newSpace()
	case sp == spaceHitClose:
		return m.closeSpaceAsk()
	}
	return m.switchSpace(sp)
}

func saveSpace(s *space, tabs []*tab, active int) savedSpace {
	return savedSpace{
		Name:      s.name,
		Projects:  sortedKeys(s.projects),
		Panes:     sortedKeys(s.panes),
		Folders:   sortedKeys(s.folders),
		Machines:  sortedKeys(s.machines),
		Hosts:     sortedKeys(s.hosts),
		Tabs:      saveTabs(tabs),
		ActiveTab: active,
	}
}

func loadSpace(ss savedSpace) *space {
	s := newSpace()
	s.name = ss.Name
	for set, keys := range map[*map[string]bool][]string{&s.projects: ss.Projects, &s.panes: ss.Panes,
		&s.folders: ss.Folders, &s.machines: ss.Machines, &s.hosts: ss.Hosts} {
		for _, k := range keys {
			(*set)[k] = true
		}
	}
	return s
}

// savedSpaces is every workspace after the first, for ui.json, wherever
// its tabs are just now.
func (m Model) savedSpaces() []savedSpace {
	var out []savedSpace
	for i, s := range m.spaces {
		if i == 0 {
			continue
		}
		tabs, active := s.tabs, s.activeTab
		if i == m.activeSpace {
			tabs, active = m.tabs, m.activeTab
		}
		out = append(out, saveSpace(s, tabs, active))
	}
	return out
}

// savedFirstSpace is what the first workspace holds, for ui.json; its tabs
// are the top-level ones. Nil while it is the only one.
func (m Model) savedFirstSpace() *savedSpace {
	if len(m.spaces) < 2 {
		return nil
	}
	ss := saveSpace(m.spaces[0], nil, 0)
	ss.Tabs = nil
	return &ss
}

// firstSpaceTabs is the first workspace's tabs, on screen or parked.
func (m Model) firstSpaceTabs() ([]*tab, int) {
	if m.activeSpace > 0 && m.activeSpace < len(m.spaces) {
		return m.spaces[0].tabs, m.spaces[0].activeTab
	}
	return m.tabs, m.activeTab
}

// restoreSpaces brings back the workspaces of the last run, after the
// first one's tabs are restored, and shows the one that was on screen. A
// file from before the first one kept anything of its own gives it
// nothing: everything belonging to no workspace is its anyway.
func (m *Model) restoreSpaces(first *savedSpace, saved []savedSpace, active int) {
	if len(saved) == 0 {
		return
	}
	m.spaces = []*space{newSpace()}
	if first != nil {
		m.spaces[0] = loadSpace(*first)
	}
	tabs, at, preview, previewing := m.tabs, m.activeTab, m.preview, m.previewing
	for _, ss := range saved {
		s := loadSpace(ss)
		m.tabs, m.preview, m.previewing = nil, nil, false
		m.restoreTabs(ss.Tabs, ss.ActiveTab)
		s.tabs, s.activeTab, s.preview, s.previewing = m.tabs, m.activeTab, m.preview, m.previewing
		m.spaces = append(m.spaces, s)
	}
	m.tabs, m.activeTab, m.preview, m.previewing = tabs, at, preview, previewing
	if active > 0 && active < len(m.spaces) {
		m.enterSpace(active)
	}
}

func sortedKeys(set map[string]bool) []string {
	keys := slices.Collect(maps.Keys(set))
	sort.Strings(keys)
	return keys
}
