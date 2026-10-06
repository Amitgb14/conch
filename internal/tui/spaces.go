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
	Tabs      []savedTab `json:"tabs,omitempty"`
	ActiveTab int        `json:"active_tab,omitempty"`
}

func newSpace() *space {
	return &space{projects: map[string]bool{}, panes: map[string]bool{}}
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
		out = append(out, savedSpace{Name: s.name, Projects: sortedKeys(s.projects), Panes: sortedKeys(s.panes),
			Tabs: saveTabs(tabs), ActiveTab: active})
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
