package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
)

// setupView shows what agents load in a directory — instructions, skills,
// commands, MCP servers, plugins — and, in a worktree, what it lacks
// compared with the main checkout.
type setupView struct {
	mid, dir string
	want     string // agent tab to show first
	tab      int
	scroll   int

	res     *proto.AgentSetupResult
	err     string
	loading bool
}

type (
	setupMsg struct {
		view *setupView
		res  proto.AgentSetupResult
		err  error
	}
	setupCopiedMsg struct {
		view *setupView
		res  proto.WorktreeFilesResult
		err  error
	}
)

// openSetup shows the agent setup of the selected row's directory.
func (m *Model) openSetup() tea.Cmd {
	r, _ := m.selectedRow()
	pl := m.contextPlace()
	if pl.dir == "" {
		m.setFlash("check the branch out first (c or n) to see its agent setup", true)
		return nil
	}
	agent := m.defaultAgent()
	if r.kind == kindPane {
		if p := m.pane(r.machine, r.paneID); p != nil && p.Agent != nil && p.Agent.Name != "" {
			agent = p.Agent.Name
		}
	}
	v := &setupView{mid: pl.machine, dir: pl.dir, want: agent}
	m.overlay = v
	return v.load(m)
}

func (v *setupView) load(m *Model) tea.Cmd {
	c := m.clientOf(v.mid)
	switch {
	case c == nil:
		v.err = m.offlineText(v.mid)
		return nil
	case len(c.MissingCapabilities([]string{"agent.setup.v1"})) > 0:
		v.err = "the server on this machine predates agent setup; restart it to use this build"
		return nil
	}
	v.loading = true
	params := proto.AgentSetupParams{Dir: v.dir}
	return func() tea.Msg {
		var res proto.AgentSetupResult
		err := callCtx(c, proto.MethodAgentSetup, params, &res)
		return setupMsg{view: v, res: res, err: err}
	}
}

func (v *setupView) copyMissing(m *Model) tea.Cmd {
	c := m.clientOf(v.mid)
	if v.res == nil || v.res.Worktree == "" || c == nil {
		return nil
	}
	params := proto.WorktreeFilesParams{ProjectID: v.res.ProjectID, Path: v.res.Worktree}
	return func() tea.Msg {
		var res proto.WorktreeFilesResult
		err := callCtx(c, proto.MethodWorktreeFiles, params, &res)
		return setupCopiedMsg{view: v, res: res, err: err}
	}
}

func (v *setupView) update(m *Model, msg tea.Msg) (bool, tea.Cmd) {
	switch msg := msg.(type) {
	case setupMsg:
		if msg.view != v {
			return false, nil
		}
		v.loading = false
		if msg.err != nil {
			v.err = msg.err.Error()
			return false, nil
		}
		first := v.res == nil
		v.res, v.err = &msg.res, ""
		if first {
			for i, a := range msg.res.Agents {
				if a.Agent == v.want {
					v.tab = i
				}
			}
		}
		return false, nil
	case setupCopiedMsg:
		if msg.view != v {
			return false, nil
		}
		if msg.err != nil {
			m.setFlash(msg.err.Error(), true)
			return false, nil
		}
		text := fmt.Sprintf("copied %d local file(s)", len(msg.res.Copied))
		if len(msg.res.Copied) == 0 {
			text = "nothing to copy"
		}
		m.setFlash(text, false)
		return false, v.load(m)
	case tea.KeyMsg:
		n := 1
		if v.res != nil {
			n = max(len(v.res.Agents), 1)
		}
		switch msg.String() {
		case "esc", "q", "i":
			m.overlay = nil
			return true, nil
		case "tab", "right", "l":
			v.tab, v.scroll = (v.tab+1)%n, 0
		case "shift+tab", "left", "h":
			v.tab, v.scroll = (v.tab+n-1)%n, 0
		case "1", "2", "3", "4", "5", "6":
			if t := int(msg.String()[0] - '1'); t < n {
				v.tab, v.scroll = t, 0
			}
		case "up", "k":
			v.scroll--
		case "down", "j":
			v.scroll++
		case "pgup":
			v.scroll -= m.setupListHeight()
		case "pgdown", " ":
			v.scroll += m.setupListHeight()
		case "g", "home":
			v.scroll = 0
		case "G", "end":
			v.scroll = 1 << 20
		case "r":
			return false, v.load(m)
		case "c":
			if v.res != nil && v.res.Worktree != "" {
				return false, v.copyMissing(m)
			}
		case "f":
			if proj := v.project(m); proj != nil && proj.Git {
				d := newLocalFilesDialog(*m, v.mid, *proj)
				m.overlay = d
				return true, d.focusCmd()
			}
		}
	}
	return false, nil
}

func (v *setupView) project(m *Model) *proto.ProjectInfo {
	if v.res == nil || v.res.ProjectID == "" {
		return nil
	}
	return m.project(v.mid, v.res.ProjectID)
}

func (m Model) setupListHeight() int { return clamp(m.height-9, 5, 40) }

func (v *setupView) tabLabels() []string {
	if v.res == nil {
		return nil
	}
	var out []string
	for i, a := range v.res.Agents {
		label := fmt.Sprintf(" %d %s ", i+1, a.Label)
		if miss := missingCount(a); miss > 0 {
			label = fmt.Sprintf(" %d %s ✗%d ", i+1, a.Label, miss)
		}
		out = append(out, label)
	}
	return out
}

func missingCount(a proto.AgentSetup) int {
	n := 0
	for _, g := range a.Groups {
		for _, it := range g.Items {
			if it.Missing {
				n++
			}
		}
	}
	return n
}

// body is the scrollable content for the current tab.
func (v *setupView) body(m Model, w int) []string {
	switch {
	case v.err != "":
		return wrapIndent(styleErr.Render(v.err), w)
	case v.res == nil:
		return []string{styleMuted.Render(" loading…")}
	}
	res := v.res
	var lines []string
	where := m.tildify(v.mid, res.Dir)
	if res.Main != "" {
		where += styleMuted.Render(" · worktree of " + m.tildify(v.mid, res.Main))
	}
	lines = append(lines, " "+where, "")

	proj := v.project(&m)
	switch {
	case res.Main != "":
		missing := 0
		for _, f := range res.LocalFiles {
			if f.State == proto.FileMissing {
				missing++
			}
		}
		hint := "f edit patterns"
		if missing > 0 {
			hint = "c copy missing · " + hint
		}
		lines = append(lines, spread(" "+styleBold.Render("Local files from the main checkout"), styleMuted.Render(hint), w))
		if len(res.LocalFiles) == 0 {
			lines = append(lines, styleMuted.Render("   no ignored files match the project's patterns"))
		}
		for _, f := range res.LocalFiles {
			switch f.State {
			case proto.FileMissing:
				lines = append(lines, "   "+styleErr.Render("✗ "+f.Path)+styleMuted.Render("  missing"))
			case proto.FileDiffers:
				lines = append(lines, "   "+styleWarn.Render("≠ "+f.Path)+styleMuted.Render("  differs"))
			case proto.FileNotIgnored:
				lines = append(lines, "   "+styleMuted.Render("· "+f.Path+"  not copied: git doesn't ignore it, so an agent could commit it"))
			default:
				lines = append(lines, "   "+styleOK.Render("✓")+" "+f.Path)
			}
		}
		lines = append(lines, "")
	case proj != nil && proj.Git:
		pats := strings.Join(proj.LocalFiles, "  ")
		if pats == "" {
			pats = "(none)"
		}
		lines = append(lines, spread(" "+styleBold.Render("New worktrees get the ignored files matching"), styleMuted.Render("f edit"), w))
		for _, l := range wrap(pats, w-4) {
			lines = append(lines, "   "+styleMuted.Render(l))
		}
		lines = append(lines, "")
	}

	if v.tab >= len(res.Agents) {
		return lines
	}
	a := res.Agents[v.tab]
	for _, n := range a.Notes {
		lines = append(lines, wrapIndent(styleWarn.Render("⚠ ")+n, w)...)
	}
	if len(a.Notes) > 0 {
		lines = append(lines, "")
	}
	nameW := 0
	for _, g := range a.Groups {
		for _, it := range g.Items {
			nameW = max(nameW, ansi.StringWidth(it.Name))
		}
	}
	nameW = min(nameW, max(w/3, 12))
	for _, g := range a.Groups {
		lines = append(lines, " "+styleBold.Render(g.Title)+styleMuted.Render(fmt.Sprintf(" %d", len(g.Items))))
		for _, it := range g.Items {
			mark, name := "  ", ansi.Truncate(it.Name, nameW, "…")
			detail := it.Detail
			if it.Missing {
				mark = styleErr.Render("✗ ")
				name = styleErr.Render(padRight(name, nameW))
				detail = "only in the main checkout"
			} else {
				name = padRight(name, nameW)
			}
			line := "   " + mark + name + "  " + styleMuted.Render(padRight(it.Scope, 9))
			if detail != "" {
				room := w - ansi.StringWidth(line) - 1
				line += " " + styleMuted.Render(ansi.Truncate(detail, max(room, 0), "…"))
			}
			lines = append(lines, line)
		}
		lines = append(lines, "")
	}
	return lines
}

func wrapIndent(text string, w int) []string {
	var out []string
	for _, l := range wrap(text, w-3) {
		out = append(out, " "+l)
	}
	return out
}

func (v *setupView) render(m Model) box {
	w := clamp(96, 40, max(m.width-4, 40))
	listH := m.setupListHeight()
	body := v.body(m, w)
	v.scroll = clamp(v.scroll, 0, max(len(body)-listH, 0))

	var tabs strings.Builder
	for i, label := range v.tabLabels() {
		if i == v.tab {
			tabs.WriteString(styleSel.Render(label))
		} else {
			tabs.WriteString(styleMuted.Render(label))
		}
		tabs.WriteString(" ")
	}
	lines := []string{tabs.String(), ""}
	for i := v.scroll; i < v.scroll+listH; i++ {
		if i < len(body) {
			lines = append(lines, body[i])
		} else {
			lines = append(lines, "")
		}
	}
	if more := len(body) - (v.scroll + listH); more > 0 {
		lines[len(lines)-1] = styleMuted.Render(fmt.Sprintf("  … %d more lines", more))
	}
	hint := " tab agent · ↑↓ scroll · r reload · esc close"
	if v.loading {
		hint = " loading…" + hint
	}
	lines = append(lines, "", styleMuted.Render(hint))
	b := box{lines: frameLines(" Agent setup ", lines, w, colorAccent)}
	b.x = max((m.width-b.width())/2, 0)
	b.y = max((m.height-len(b.lines))/4, 0)
	return b
}

func (v *setupView) mouse(m *Model, msg tea.MouseMsg, b box) tea.Cmd {
	if !b.contains(msg.X, msg.Y) {
		if msg.Action == tea.MouseActionPress {
			m.overlay = nil
		}
		return nil
	}
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		v.scroll -= 3
		return nil
	case tea.MouseButtonWheelDown:
		v.scroll += 3
		return nil
	}
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft || msg.Y-b.y-1 != 0 {
		return nil
	}
	x := msg.X - b.x - 1
	for i, label := range v.tabLabels() {
		width := ansi.StringWidth(label) + 1
		if x < width {
			v.tab, v.scroll = i, 0
			return nil
		}
		x -= width
	}
	return nil
}

// newLocalFilesDialog edits the untracked files copied into new worktrees.
func newLocalFilesDialog(m Model, mid string, proj proto.ProjectInfo) *dialog {
	value := strings.Join(proj.LocalFiles, " ")
	if proj.LocalFilesDefault {
		value = "default"
	}
	d := newDialog(m, " Local files · "+proj.Name+" ", []string{
		"Files git ignores that are copied from the main checkout into each new worktree,",
		"as git globs separated by spaces (e.g. .env config/*.local.json). \"default\"",
		"restores the built-in list; empty copies nothing.",
	}, []string{"Patterns"}, []string{value})
	id := proj.ID
	d.submit = func(m *Model, v []string) tea.Cmd {
		params := proto.ProjectFilesParams{ProjectID: id}
		if strings.TrimSpace(v[0]) == "default" {
			params.Reset = true
		} else {
			params.Patterns = strings.FieldsFunc(v[0], func(r rune) bool { return r == ' ' || r == ',' })
		}
		var info proto.ProjectInfo
		return m.callOn(mid, proto.MethodProjectFiles, params, &info, func() tea.Msg { return flashMsg("local files updated for " + info.Name) })
	}
	return d
}
