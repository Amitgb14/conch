package tui

import (
	"fmt"
	"path/filepath"
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

	// Search: query filters by title, agent, branch and ID at once; the
	// server's matches inside conversations (hits, with snippets) arrive
	// shortly after typing stops.
	query     string
	typing    bool
	hits      map[string]string // agent|id → snippet, for hitsFor
	hitsFor   string
	searching bool
	searchErr string
}

// searchDelay is how long typing must pause before searching conversations.
const searchDelay = 300 * time.Millisecond

type (
	sessionSearchTickMsg struct{ machine, projectID, query string }
	sessionSearchMsg     struct {
		machine, projectID, query string
		list                      []proto.SessionInfo
		err                       error
	}
)

func sessionKey(s proto.SessionInfo) string { return s.Agent + "|" + s.ID }

// matches reports whether a session's details contain every word.
func (sv *sessionsView) matches(s proto.SessionInfo, words []string) bool {
	text := strings.ToLower(strings.Join([]string{s.Title, s.Agent, agentLabel(s.Agent), s.Branch, s.ID}, " "))
	for _, w := range words {
		if !strings.Contains(text, w) {
			return false
		}
	}
	return true
}

// snippet is the conversation text a session matched, if any.
func (sv *sessionsView) snippet(s proto.SessionInfo) string {
	if sv.query == "" || sv.hitsFor != sv.query {
		return ""
	}
	return sv.hits[sessionKey(s)]
}

// searchKey edits the query while typing; handled is false for keys that
// end typing and act as usual (up, down).
func (sv *sessionsView) searchKey(m *Model, k tea.KeyMsg) (cmd tea.Cmd, handled bool) {
	switch k.Type {
	case tea.KeyEsc:
		sv.typing = false
		sv.setQuery("")
		return nil, true
	case tea.KeyEnter:
		sv.typing = false
		return nil, true
	case tea.KeyUp, tea.KeyDown, tea.KeyPgUp, tea.KeyPgDown:
		sv.typing = false
		return nil, false
	case tea.KeyBackspace:
		r := []rune(sv.query)
		if len(r) == 0 {
			sv.typing = false
			return nil, true
		}
		sv.setQuery(string(r[:len(r)-1]))
	case tea.KeyCtrlU:
		sv.setQuery("")
	case tea.KeySpace:
		sv.setQuery(sv.query + " ")
	case tea.KeyRunes:
		sv.setQuery(sv.query + string(k.Runes))
	default:
		return nil, true
	}
	return sv.scheduleSearch(m), true
}

func (sv *sessionsView) setQuery(q string) {
	if q != sv.query {
		sv.query, sv.sel, sv.scroll = q, 0, 0
	}
	if strings.TrimSpace(q) == "" {
		sv.hits, sv.hitsFor, sv.searching, sv.searchErr = nil, "", false, ""
	}
}

// scheduleSearch asks the server to search conversations once typing pauses.
func (sv *sessionsView) scheduleSearch(m *Model) tea.Cmd {
	if strings.TrimSpace(sv.query) == "" || !m.hasCapability(sv.machine, "session.search.v1") {
		return nil
	}
	sv.searching = true
	msg := sessionSearchTickMsg{machine: sv.machine, projectID: sv.projectID, query: sv.query}
	return tea.Tick(searchDelay, func(time.Time) tea.Msg { return msg })
}

// sessionsViews lists the sessions views of a project on screen or in tabs.
func (m *Model) sessionsViews(mid, pid string) []*sessionsView {
	var out []*sessionsView
	tabs := m.tabs
	if m.preview != nil {
		tabs = append(append([]*tab(nil), tabs...), m.preview)
	}
	for _, t := range tabs {
		for _, l := range t.root.leaves() {
			if sv := l.sessions; sv != nil && sv.machine == mid && sv.projectID == pid {
				out = append(out, sv)
			}
		}
	}
	return out
}

// searchSessions runs a search whose query is still current.
func (m *Model) searchSessions(msg sessionSearchTickMsg) tea.Cmd {
	current := false
	for _, sv := range m.sessionsViews(msg.machine, msg.projectID) {
		current = current || sv.query == msg.query
	}
	if !current {
		return nil
	}
	c := m.clientOf(msg.machine)
	if c == nil {
		return nil
	}
	return func() tea.Msg {
		var out proto.SessionList
		err := callCtx(c, proto.MethodSessionSearch, proto.SessionSearchParams{ProjectID: msg.projectID, Query: msg.query}, &out)
		return sessionSearchMsg{machine: msg.machine, projectID: msg.projectID, query: msg.query, list: out.Sessions, err: err}
	}
}

func (m *Model) receiveSessionSearch(msg sessionSearchMsg) {
	for _, sv := range m.sessionsViews(msg.machine, msg.projectID) {
		if sv.query != msg.query {
			continue
		}
		sv.searching = false
		if msg.err != nil {
			sv.searchErr = msg.err.Error()
			continue
		}
		sv.searchErr, sv.hitsFor, sv.hits = "", msg.query, map[string]string{}
		for _, s := range msg.list {
			sv.hits[sessionKey(s)] = s.Snippet
		}
	}
}

// ---- sharing ----

// openShareMenu offers where to hand a session's conversation: a new agent
// in its folder, or an agent already running in the project.
func (m *Model) openShareMenu(sv *sessionsView, s proto.SessionInfo) {
	switch {
	case s.ID == "":
		m.setFlash("this interrupted run has no saved conversation to share", true)
		return
	case !m.hasCapability(sv.machine, "session.share.v1"):
		m.setFlash("the server there predates sharing sessions; reload it", true)
		return
	}
	mach := m.machine(sv.machine)
	if mach == nil {
		return
	}
	mid, pid := sv.machine, sv.projectID
	list := mach.agentList
	if len(list) == 0 {
		for _, name := range []string{"claude", "codex", "gemini", "opencode"} {
			list = append(list, proto.AgentAvailability{Name: name, Installed: true})
		}
	}
	var items []menuItem
	for _, a := range list {
		if !a.Installed {
			continue
		}
		label := a.Label
		if label == "" {
			label = agentLabel(a.Name)
		}
		key := ""
		if len(items) < 9 {
			key = fmt.Sprint(len(items) + 1)
		}
		to := a.Name
		items = append(items, menuItem{key, "Start " + label + " with it", func(m *Model) tea.Cmd {
			return m.shareSession(mid, pid, s, proto.SessionShareParams{To: to}, label)
		}})
	}
	for _, p := range mach.panes {
		if p.Agent == nil || p.State != proto.PaneRunning || p.ProjectID != pid || p.ID == s.PaneID {
			continue
		}
		id, name := p.ID, p.DisplayName()
		detail := ""
		if p.Branch != "" {
			detail = styleMuted.Render("  " + p.Branch)
		}
		items = append(items, menuItem{"", "Send to " + name + detail, func(m *Model) tea.Cmd {
			return m.shareSession(mid, pid, s, proto.SessionShareParams{PaneID: id}, name)
		}})
	}
	if len(items) == 0 {
		m.setFlash("no agent to share with: install one with c", true)
		return
	}
	title := "Share “" + ansi.Truncate(s.Title, 40, "…") + "”"
	m.overlay = &menu{title: title, items: items, x: max(m.width/2-25, 0), y: max(m.height/3, 0)}
}

// shareSession hands the conversation over and shows the receiving pane.
func (m *Model) shareSession(mid, pid string, s proto.SessionInfo, p proto.SessionShareParams, to string) tea.Cmd {
	p.Agent, p.ID, p.Dir = s.Agent, s.ID, s.Dir
	p.Cols, p.Rows = m.paneArea()
	var res proto.SessionShareResult
	key := sessionsKey(mid, pid)
	return m.callOn(mid, proto.MethodSessionShare, p, &res, func() tea.Msg {
		note := "shared with " + to
		if res.Path != "" {
			note += " · " + filepath.Base(res.Path)
		}
		return tea.BatchMsg{
			func() tea.Msg { return createdMsg{machine: mid, info: res.Pane, note: note} },
			func() tea.Msg { return sessionsStaleMsg{key: key} },
		}
	})
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
	words := strings.Fields(strings.ToLower(sv.query))
	if sv.agent == "" && len(words) == 0 {
		return d.list
	}
	var out []proto.SessionInfo
	for _, s := range d.list {
		if sv.agent != "" && s.Agent != sv.agent {
			continue
		}
		if len(words) > 0 && !sv.matches(s, words) {
			if _, hit := sv.hits[sessionKey(s)]; sv.hitsFor != sv.query || !hit || s.ID == "" {
				continue
			}
		}
		out = append(out, s)
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
	if sv.typing {
		if cmd, handled := sv.searchKey(m, k); handled {
			return false, cmd
		}
	}
	list := sv.visible(*m)
	switch k.String() {
	case "/":
		sv.typing = true
		return false, nil
	case "esc":
		if sv.query != "" { // first esc clears the search
			sv.setQuery("")
			return false, nil
		}
		return true, nil
	case "q", "left", "h", "tab":
		return true, nil
	case "s":
		if sv.sel >= 0 && sv.sel < len(list) {
			m.openShareMenu(sv, list[sv.sel])
		}
		return false, nil
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
	case "d", "delete":
		if sv.sel < 0 || sv.sel >= len(list) {
			return false, nil
		}
		s := list[sv.sel]
		switch {
		case s.PaneID != "":
			m.setFlash("that session is open in a pane; close it first", true)
			return false, nil
		case s.ID == "":
			m.setFlash("this interrupted run has no saved conversation; x dismisses it", true)
			return false, nil
		case !m.hasCapability(sv.machine, "session.delete.v1"):
			m.setFlash("the server there predates deleting sessions; reload it", true)
			return false, nil
		}
		mid, key := sv.machine, sessionsKey(sv.machine, sv.projectID)
		where := "moves its files to the Trash"
		if s.Agent == "opencode" {
			where = "OpenCode deletes it"
		}
		m.overlay = newConfirm(fmt.Sprintf("Delete the %s session “%s”? %s.", agentLabel(s.Agent), ansi.Truncate(s.Title, 50, "…"), where),
			func(m *Model) tea.Cmd {
				ref := proto.SessionRef{Agent: s.Agent, ID: s.ID, Dir: s.Dir}
				return m.callOn(mid, proto.MethodSessionDelete, ref, nil, func() tea.Msg {
					return tea.BatchMsg{
						func() tea.Msg { return flashMsg("session deleted") },
						func() tea.Msg { return sessionsStaleMsg{key: key} },
					}
				})
			})
		return false, nil
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
	lines := []string{spread(styleBold.Render("Sessions · "+name), styleMuted.Render("enter resume · / search · s share · d delete · a agent"), w)}
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
	if sv.typing || sv.query != "" {
		cursor := ""
		if sv.typing {
			cursor = "█"
		}
		status := fmt.Sprintf("%d found", len(list))
		switch {
		case sv.searchErr != "":
			status = styleErr.Render("conversations: " + sv.searchErr)
		case sv.searching:
			status += " · searching conversations…"
		}
		meta = styleAccent.Render("/ ") + sv.query + cursor + styleMuted.Render("  "+status)
		lines = append(lines, meta, "")
	} else {
		lines = append(lines, styleMuted.Render(meta), "")
	}
	if len(list) == 0 {
		if sv.query != "" {
			return append(lines, styleMuted.Render("  no sessions match · esc clears the search"))
		}
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
		if chip := sessionUsage(s).chip(); chip != "" {
			right = append(right, chip)
		}
		right = append(right, ago(s.Updated))
		rightText := strings.Join(right, " · ")
		room := max(w-agentW-ansi.StringWidth(rightText)-8, 10)
		title := ansi.Truncate(s.Title, room, "…")
		snip := ""
		if text := sv.snippet(s); text != "" && ansi.StringWidth(title)+4 < room {
			snip = "  " + ansi.Truncate("“"+text+"”", room-ansi.StringWidth(title)-2, "…")
		}
		if i == sv.sel {
			style := styleSelDim
			if m.focus == focusMain {
				style = styleSel
			}
			lines = append(lines, style.Render(spread(fmt.Sprintf("▸ %s %s  %s%s", mark, padRight(agentLabel(s.Agent), agentW), title, snip), rightText, w)))
			continue
		}
		left := fmt.Sprintf("  %s %s  %s%s", markStyle.Render(mark), styleAccent.Render(padRight(agentLabel(s.Agent), agentW)), title, styleMuted.Render(snip))
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

// hasCapability reports whether a machine's server supports cap.
func (m Model) hasCapability(mid, cap string) bool {
	c := m.clientOf(mid)
	return c != nil && len(c.MissingCapabilities([]string{cap})) == 0
}
