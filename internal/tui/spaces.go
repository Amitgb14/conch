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
// sidebar's top border beside conch, as tmux has sessions. The first is
// everything — the tree and tabs conch always had. One made with + starts
// empty and shows only the projects added to it and the panes started in
// it, with tabs of its own. Panes keep running whichever is on screen: a
// workspace is a way of looking, so closing one ends nothing.

// space is one workspace. The one on screen keeps its tabs in the model;
// the rest keep theirs here until they are shown again.
type space struct {
	name     string
	projects map[string]bool // scoped(machine, project)
	panes    map[string]bool // paneKey: started here, outside its projects
	folders  map[string]bool // spaceFolder: folders made here, shown even empty
	machines map[string]bool // machine IDs added here, shown even with nothing on them

	tabs       []*tab
	activeTab  int
	preview    *tab
	previewing bool
	scopeTab   map[string]*tab
	lastTab    *tab
	cursor     string
	scroll     int
}

// savedSpace is a workspace after the first in ui.json. The first one's
// tabs stay in the top-level tabs, which is what older builds read.
type savedSpace struct {
	Name      string     `json:"name,omitempty"`
	Projects  []string   `json:"projects,omitempty"`
	Panes     []string   `json:"panes,omitempty"`
	Folders   []string   `json:"folders,omitempty"`
	Machines  []string   `json:"machines,omitempty"`
	Tabs      []savedTab `json:"tabs,omitempty"`
	ActiveTab int        `json:"active_tab,omitempty"`
}

func newSpace() *space {
	return &space{projects: map[string]bool{}, panes: map[string]bool{}, folders: map[string]bool{}, machines: map[string]bool{}}
}

// isolated is the workspace on screen when it is not the first: the one
// whose tree is narrowed to what was put in it. Nil means everything.
func (m Model) isolated() *space {
	if m.activeSpace <= 0 || m.activeSpace >= len(m.spaces) {
		return nil
	}
	return m.spaces[m.activeSpace]
}

// spaceCount is how many workspaces there are; with none made, the one.
func (m Model) spaceCount() int { return max(len(m.spaces), 1) }

// spaceFilter narrows a machine's projects and panes to a workspace's: its
// projects with everything in them, the panes started in it, and what those
// panes started in turn — a helper is in the workspace of the agent that
// asked for it.
func (s *space) filter(mid string, projects []proto.ProjectInfo, panes []proto.PaneInfo) ([]proto.ProjectInfo, []proto.PaneInfo) {
	var projs []proto.ProjectInfo
	for _, p := range projects {
		if s.projects[scoped(mid, p.ID)] {
			projs = append(projs, p)
		}
	}
	in := map[string]bool{}
	for _, p := range panes {
		if s.projects[scoped(mid, p.ProjectID)] || s.panes[paneKey(mid, p.ID)] {
			in[p.ID] = true
		}
	}
	for grew := true; grew; {
		grew = false
		for _, p := range panes {
			if !in[p.ID] && p.CreatedBy != "" && in[p.CreatedBy] {
				in[p.ID], grew = true, true
			}
		}
	}
	var out []proto.PaneInfo
	for _, p := range panes {
		if in[p.ID] {
			out = append(out, p)
		}
	}
	return projs, out
}

// shown is a machine's projects and panes as the workspace on screen has
// them: everything in the first, what was put in it in any other. Pages and
// counts read this, so a workspace never counts what its tree leaves out.
func (m Model) shown(mach *machine) ([]proto.ProjectInfo, []proto.PaneInfo) {
	if s := m.isolated(); s != nil {
		return s.filter(mach.id, mach.projects, mach.panes)
	}
	return mach.projects, mach.panes
}

// spaceFolder names a folder for a workspace's own list: its section's key
// and its name, which is unique in the section.
func spaceFolder(key, name string) string { return key + "\n" + name }

// filterFolders is the folders a workspace shows: the ones made in it, the
// ones holding a pane it shows, and the SSH section's that hold a saved
// host, since saved hosts are listed in every workspace. An empty folder
// made in another workspace is that workspace's own.
func (s *space) filterFolders(folders map[string][]savedFolder, panes map[string][]proto.PaneInfo) map[string][]savedFolder {
	out := map[string][]savedFolder{}
	for key, fs := range folders {
		for _, f := range fs {
			if s.showsFolder(key, f, panes[folderKeyMachine(key)]) {
				out[key] = append(out[key], f)
			}
		}
	}
	return out
}

// showsFolder is whether the workspace shows folder f of section key, given
// the panes it shows on that section's machine.
func (s *space) showsFolder(key string, f savedFolder, panes []proto.PaneInfo) bool {
	if s.folders[spaceFolder(key, f.Name)] || len(f.claim(panes, map[string]bool{})) > 0 {
		return true
	}
	return slices.ContainsFunc(f.Members, func(mem savedMember) bool { return mem.Host != "" })
}

// showsMachine is whether the workspace on screen lists machine mach. The
// first lists every one. Another lists this computer — somewhere to add a
// project or start an agent is needed from the start — the machines added
// to it, and any it holds a project or a pane on.
func (m Model) showsMachine(mach *machine) bool {
	s := m.isolated()
	if s == nil || mach.id == localMachine || s.machines[mach.id] {
		return true
	}
	projects, panes := s.filter(mach.id, mach.projects, mach.panes)
	return len(projects) > 0 || len(panes) > 0
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

// joinMachine lists machine mid in the workspace on screen.
func (m *Model) joinMachine(mid string) {
	if s := m.isolated(); s != nil && mid != localMachine {
		s.machines[mid] = true
	}
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
			m.joinMachine(mid)
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
	if m.isolated() != nil && len(m.hiddenMachines()) > 0 {
		mu.items = append(mu.items, menuItem{"e", "Machine already in conch…", func(m *Model) tea.Cmd {
			m.openBringMachine(m.addMachineMenu())
			return nil
		}})
	}
	return mu
}

// leaveMachine takes a machine out of the workspace on screen, with its
// projects and the panes started there: what is on it is no longer this
// workspace's. Nothing ends, and every other workspace keeps it.
func (m *Model) leaveMachine(mid string) tea.Cmd {
	s := m.isolated()
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
	return tea.Batch(m.rebuild(), m.saveState())
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

// closeSpaceAsk closes the workspace on screen, after asking. Its panes go
// on running and are still in the first workspace; only its tabs go.
func (m *Model) closeSpaceAsk() tea.Cmd {
	i := m.activeSpace
	if i <= 0 || i >= len(m.spaces) {
		m.setFlash("the first workspace stays", false)
		return nil
	}
	m.overlay = newConfirm("Close workspace "+m.spaceLabel(i)+"? Its tabs close; agents and terminals keep running and stay in workspace 1.",
		func(m *Model) tea.Cmd { return m.closeSpace(i) })
	return nil
}

func (m *Model) closeSpace(i int) tea.Cmd {
	if i <= 0 || i >= len(m.spaces) {
		return nil
	}
	if i == m.activeSpace {
		m.enterSpace(i - 1)
	}
	m.spaces = slices.Delete(m.spaces, i, i+1)
	if m.activeSpace > i {
		m.activeSpace--
	}
	if len(m.spaces) == 1 {
		m.spaces, m.activeSpace = nil, 0 // just the one again
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

// joinSpace puts a project, and a pane started outside every project, in
// the workspace on screen. In the first one everything is already shown.
func (m *Model) joinSpace(mid, projectID, paneID string) {
	s := m.isolated()
	if s == nil {
		return
	}
	if projectID != "" && m.project(mid, projectID) != nil {
		s.projects[scoped(mid, projectID)] = true
		return
	}
	if paneID != "" {
		s.panes[paneKey(mid, paneID)] = true
	}
}

// spaceProjectMsg is a project added from a workspace, to be put in it.
type spaceProjectMsg struct {
	space   *space
	machine string
	info    proto.ProjectInfo
	flash   string
}

// addedProject is what a project add says when it returns: the project
// joins the workspace it was added from, even if another is shown by then.
func (m *Model) addedProject(mid string, info *proto.ProjectInfo, flash string) func() tea.Msg {
	s := m.isolated()
	return func() tea.Msg {
		return spaceProjectMsg{space: s, machine: mid, info: *info, flash: flash + info.Name}
	}
}

func (m *Model) receiveSpaceProject(msg spaceProjectMsg) tea.Cmd {
	m.setFlash(msg.flash, false)
	if msg.space == nil || !slices.Contains(m.spaces, msg.space) || msg.info.ID == "" {
		return nil
	}
	msg.space.projects[scoped(msg.machine, msg.info.ID)] = true
	m.expanded[machineID(msg.machine)] = true
	m.expanded[workspaceID(msg.machine)] = true
	return tea.Batch(m.rebuild(), m.saveState())
}

// leaveSpace takes a project out of the workspace on screen, leaving it on
// the machine and in every other workspace.
func (m *Model) leaveSpace(mid, projectID string) tea.Cmd {
	s := m.isolated()
	if s == nil {
		return nil
	}
	delete(s.projects, scoped(mid, projectID))
	return tea.Batch(m.rebuild(), m.saveState())
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
		out = append(out, savedSpace{
			Name:      s.name,
			Projects:  sortedKeys(s.projects),
			Panes:     sortedKeys(s.panes),
			Folders:   sortedKeys(s.folders),
			Machines:  sortedKeys(s.machines),
			Tabs:      saveTabs(tabs),
			ActiveTab: active,
		})
	}
	return out
}

// firstSpaceTabs is the first workspace's tabs, on screen or parked.
func (m Model) firstSpaceTabs() ([]*tab, int) {
	if m.activeSpace > 0 && m.activeSpace < len(m.spaces) {
		return m.spaces[0].tabs, m.spaces[0].activeTab
	}
	return m.tabs, m.activeTab
}

// restoreSpaces brings back the workspaces of the last run, after the
// first one's tabs are restored, and shows the one that was on screen.
func (m *Model) restoreSpaces(saved []savedSpace, active int) {
	if len(saved) == 0 {
		return
	}
	m.spaces = []*space{newSpace()}
	tabs, at, preview, previewing := m.tabs, m.activeTab, m.preview, m.previewing
	for _, ss := range saved {
		s := newSpace()
		s.name = ss.Name
		for _, k := range ss.Projects {
			s.projects[k] = true
		}
		for _, k := range ss.Panes {
			s.panes[k] = true
		}
		for _, k := range ss.Folders {
			s.folders[k] = true
		}
		for _, k := range ss.Machines {
			s.machines[k] = true
		}
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
