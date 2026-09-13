package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
)

// sessionsData is a project's saved agent sessions, shared by the tree
// (counts), the project page and sessions views.
type sessionsData struct {
	list    []proto.SessionInfo
	err     string
	loading bool
	at      time.Time
}

// sessionsTTL is how long a loaded list is shown before reloading.
const sessionsTTL = 30 * time.Second

type sessionsMsg struct {
	key  string
	list []proto.SessionInfo
	err  error
}

func sessionsKey(mid, pid string) string { return mid + "|" + pid }

func (d *sessionsData) interrupted() int {
	n := 0
	for _, s := range d.list {
		if s.Interrupted {
			n++
		}
	}
	return n
}

// loadSessions fetches a project's sessions unless a fresh list is loaded
// or loading; force reloads anyway.
func (m *Model) loadSessions(mid, pid string, force bool) tea.Cmd {
	if m.sessions == nil {
		m.sessions = map[string]*sessionsData{}
	}
	c := m.clientOf(mid)
	if c == nil || len(c.MissingCapabilities([]string{"session.v1"})) > 0 {
		return nil
	}
	key := sessionsKey(mid, pid)
	d := m.sessions[key]
	if d == nil {
		d = &sessionsData{}
		m.sessions[key] = d
	}
	if d.loading || (!force && time.Since(d.at) < sessionsTTL) {
		return nil
	}
	d.loading = true
	return func() tea.Msg {
		var out proto.SessionList
		err := callCtx(c, proto.MethodSessionList, proto.SessionListParams{ProjectID: pid}, &out)
		return sessionsMsg{key: key, list: out.Sessions, err: err}
	}
}

func (m *Model) receiveSessions(msg sessionsMsg) {
	d := m.sessions[msg.key]
	if d == nil {
		return
	}
	d.loading, d.at = false, time.Now()
	if msg.err != nil {
		d.err = msg.err.Error()
		return
	}
	d.err, d.list = "", msg.list
}

// hasSessions reports whether a machine's server can list sessions.
func (m Model) hasSessions(mid string) bool {
	c := m.clientOf(mid)
	return c != nil && len(c.MissingCapabilities([]string{"session.v1"})) == 0
}

// resumeSession reopens a session, or shows the pane it is open in.
func (m *Model) resumeSession(mid, pid string, s proto.SessionInfo) tea.Cmd {
	if s.PaneID != "" {
		if p := m.pane(mid, s.PaneID); p != nil {
			info := *p
			return func() tea.Msg { return createdMsg{machine: mid, info: info} }
		}
	}
	mach := m.machine(mid)
	if mach != nil && mach.missingAgent(s.Agent) {
		return func() tea.Msg { return askInstallMsg{machine: mid, agent: s.Agent} }
	}
	cols, rows := m.paneArea()
	ref := proto.SessionRef{Agent: s.Agent, ID: s.ID, Dir: s.Dir, Cols: cols, Rows: rows}
	var info proto.PaneInfo
	key := sessionsKey(mid, pid)
	return m.callOn(mid, proto.MethodSessionResume, ref, &info, func() tea.Msg {
		return tea.BatchMsg{
			func() tea.Msg {
				return createdMsg{machine: mid, info: info}
			},
			func() tea.Msg { return sessionsStaleMsg{key: key} },
		}
	})
}

type sessionsStaleMsg struct{ key string }

// ---- the view ----

// sessionsView lists a project's sessions in the main area.
type sessionsView struct {
	machine, projectID string
	sel, scroll        int
	agent              string // filter; "" all
}

func (sv *sessionsView) data(m Model) *sessionsData {
	return m.sessions[sessionsKey(sv.machine, sv.projectID)]
}

// visible is the filtered list.
func (sv *sessionsView) visible(m Model) []proto.SessionInfo {
	d := sv.data(m)
	if d == nil {
		return nil
	}
	if sv.agent == "" {
		return d.list
	}
	var out []proto.SessionInfo
	for _, s := range d.list {
		if s.Agent == sv.agent {
			out = append(out, s)
		}
	}
	return out
}

// agents lists the agents present, for the filter cycle.
func (sv *sessionsView) agents(m Model) []string {
	d := sv.data(m)
	if d == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, s := range d.list {
		if !seen[s.Agent] {
			seen[s.Agent] = true
			out = append(out, s.Agent)
		}
	}
	return out
}

// listTop is the view row of the first session.
const sessionsListTop = 3

func (sv *sessionsView) key(m *Model, k tea.KeyMsg) (back bool, cmd tea.Cmd) {
	list := sv.visible(*m)
	switch k.String() {
	case "esc", "q", "left", "h", "tab":
		return true, nil
	case "up", "k":
		sv.sel--
	case "down", "j":
		sv.sel++
	case "pgup":
		sv.sel -= 10
	case "pgdown":
		sv.sel += 10
	case "g", "home":
		sv.sel = 0
	case "G", "end":
		sv.sel = len(list) - 1
	case "enter", "right", "l":
		if sv.sel >= 0 && sv.sel < len(list) {
			return false, m.resumeSession(sv.machine, sv.projectID, list[sv.sel])
		}
	case "a":
		agents := sv.agents(*m)
		next := ""
		for i, a := range agents {
			if a == sv.agent && i+1 < len(agents) {
				next = agents[i+1]
			}
		}
		if sv.agent == "" && len(agents) > 0 {
			next = agents[0]
		}
		sv.agent, sv.sel, sv.scroll = next, 0, 0
	case "x":
		if sv.sel >= 0 && sv.sel < len(list) && list[sv.sel].Interrupted {
			s := list[sv.sel]
			key := sessionsKey(sv.machine, sv.projectID)
			return false, m.callOn(sv.machine, proto.MethodSessionDismiss, proto.SessionRef{Agent: s.Agent, ID: s.ID, Dir: s.Dir}, nil,
				func() tea.Msg { return sessionsStaleMsg{key: key} })
		}
	case "I":
		var cmds []tea.Cmd
		for _, s := range list {
			if s.Interrupted && s.PaneID == "" {
				cmds = append(cmds, m.resumeSession(sv.machine, sv.projectID, s))
			}
		}
		if len(cmds) == 0 {
			m.setFlash("no interrupted sessions", false)
		}
		return false, tea.Sequence(cmds...)
	case "y":
		if sv.sel >= 0 && sv.sel < len(list) && list[sv.sel].ID != "" {
			return false, copyText(list[sv.sel].ID)
		}
	case "R":
		return false, m.loadSessions(sv.machine, sv.projectID, true)
	}
	sv.sel = clamp(sv.sel, 0, max(len(list)-1, 0))
	return false, nil
}

func (sv *sessionsView) render(m Model, w, h int) []string {
	proj := m.project(sv.machine, sv.projectID)
	name := sv.projectID
	if proj != nil {
		name = proj.Name
	}
	lines := []string{spread(styleBold.Render("Sessions · "+name), styleMuted.Render("enter resume · a agent · R reload"), w)}
	d := sv.data(m)
	switch {
	case d == nil || (d.list == nil && d.loading):
		return append(lines, "", styleMuted.Render("loading…"))
	case d.err != "":
		return append(lines, "", styleErr.Render(d.err))
	}
	list := sv.visible(m)
	filter := "all agents"
	if sv.agent != "" {
		filter = agentLabel(sv.agent)
	}
	meta := fmt.Sprintf("%d saved · %s", len(list), filter)
	if n := d.interrupted(); n > 0 {
		meta += " · " + styleWarn.Render(fmt.Sprintf("⚠ %d interrupted", n)) + styleMuted.Render(" (I resumes all, x dismisses)")
	}
	lines = append(lines, styleMuted.Render(meta), "")
	if len(list) == 0 {
		return append(lines, styleMuted.Render("  no saved sessions for this project"), "",
			styleMuted.Render("  sessions of Claude Code, Codex, Gemini CLI and OpenCode run here or in its worktrees appear here"))
	}

	listH := max(h-sessionsListTop, 1)
	sv.sel = clamp(sv.sel, 0, len(list)-1)
	if sv.sel < sv.scroll {
		sv.scroll = sv.sel
	}
	if sv.sel >= sv.scroll+listH {
		sv.scroll = sv.sel - listH + 1
	}
	agentW := 0
	for _, s := range list {
		agentW = max(agentW, ansi.StringWidth(agentLabel(s.Agent)))
	}
	for i := sv.scroll; i < len(list) && i < sv.scroll+listH; i++ {
		s := list[i]
		mark, markStyle := "·", styleMuted
		switch {
		case s.PaneID != "":
			mark, markStyle = "●", styleOK
		case s.Interrupted:
			mark, markStyle = "⚠", styleWarn
		}
		var right []string
		if s.Branch != "" {
			right = append(right, s.Branch)
		} else if proj != nil && s.Dir != proj.Path {
			right = append(right, m.tildify(sv.machine, s.Dir))
		}
		switch {
		case s.PaneID != "":
			right = append(right, "open")
		case s.Interrupted:
			right = append(right, "interrupted")
		}
		right = append(right, ago(s.Updated))
		rightText := strings.Join(right, " · ")
		title := ansi.Truncate(s.Title, max(w-agentW-ansi.StringWidth(rightText)-8, 10), "…")
		if i == sv.sel {
			style := styleSelDim
			if m.focus == focusMain {
				style = styleSel
			}
			lines = append(lines, style.Render(spread(fmt.Sprintf("▸ %s %s  %s", mark, padRight(agentLabel(s.Agent), agentW), title), rightText, w)))
			continue
		}
		left := fmt.Sprintf("  %s %s  %s", markStyle.Render(mark), styleAccent.Render(padRight(agentLabel(s.Agent), agentW)), title)
		rs := styleMuted.Render(rightText)
		if s.Interrupted && s.PaneID == "" {
			rs = styleWarn.Render(rightText)
		}
		lines = append(lines, spread(left, rs, w))
	}
	return lines
}

func (sv *sessionsView) mouse(m *Model, msg tea.MouseMsg, x, y int) tea.Cmd {
	list := sv.visible(*m)
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		sv.sel = clamp(sv.sel-1, 0, max(len(list)-1, 0))
		return nil
	case tea.MouseButtonWheelDown:
		sv.sel = clamp(sv.sel+1, 0, max(len(list)-1, 0))
		return nil
	}
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return nil
	}
	if i := sv.scroll + y - sessionsListTop; y >= sessionsListTop && i >= 0 && i < len(list) {
		if i == sv.sel {
			return m.resumeSession(sv.machine, sv.projectID, list[i])
		}
		sv.sel = i
	}
	return nil
}

// projectSessionLines is the sessions block on a project page.
func (m Model) projectSessionLines(mid string, proj proto.ProjectInfo, w int) []string {
	d := m.sessions[sessionsKey(mid, proj.ID)]
	if d == nil || d.list == nil {
		return nil
	}
	head := styleBold.Render("Sessions") + styleMuted.Render(fmt.Sprintf("  %d", len(d.list)))
	if n := d.interrupted(); n > 0 {
		head += "  " + styleWarn.Render(fmt.Sprintf("⚠ %d interrupted", n))
	}
	lines := []string{"", head}
	for i, s := range d.list {
		if i == 5 {
			lines = append(lines, styleMuted.Render(fmt.Sprintf("  … %d more · select Sessions in the tree to resume", len(d.list)-5)))
			break
		}
		mark := styleMuted.Render("·")
		switch {
		case s.PaneID != "":
			mark = styleOK.Render("●")
		case s.Interrupted:
			mark = styleWarn.Render("⚠")
		}
		right := styleMuted.Render(joinNonEmpty(" · ", s.Branch, ago(s.Updated)))
		left := fmt.Sprintf("  %s %s  %s", mark, styleAccent.Render(agentLabel(s.Agent)), ansi.Truncate(s.Title, max(w/2, 20), "…"))
		lines = append(lines, spread(left, right, w))
	}
	return lines
}

func joinNonEmpty(sep string, parts ...string) string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, sep)
}
