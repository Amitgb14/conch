package tui

import (
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// The palette (ctrl+b /, ⌘K in conch's own window) is one list of
// everywhere you can go and everything you can do, narrowed as you type:
// the agents and terminals of the workspace on screen, its projects and
// task branches, and the commands that otherwise need their key
// remembering. It adds no command of its own. Going somewhere does what
// enter on that row of the tree does, and a command is its key, sent as
// if typed — so the palette cannot drift from the keys, and what a key
// refuses (closing the last tab, a split on a pane that is gone) it
// refuses here too.

// paletteItem is one line of the palette.
type paletteItem struct {
	label  string // drawn, with its colours
	detail string // drawn muted after the label: where it is
	hint   string // the right column: its keys, or what kind of thing it is
	text   string // what typing is matched against, lower case
	run    func(m *Model) tea.Cmd
}

type cmdPalette struct {
	query string
	items []paletteItem
	shown []int // indexes into items, in the order drawn
	sel   int   // into shown
	top   int   // first of shown on screen
}

// paletteCommand is a command the palette lists: a key typed in the tree,
// or a key after the prefix.
type paletteCommand struct {
	label  string
	key    string
	prefix bool
}

// paletteCommands are the commands, in the order an empty palette lists
// them: what is reached for most first.
var paletteCommands = []paletteCommand{
	{"New task (branch, worktree and agent)", "t", false},
	{"Start an agent here", "c", false},
	{"New terminal here", "n", false},
	{"New tab", "c", true},
	{"Split right", "%", true},
	{"Split down", "\"", true},
	{"Close split", "x", true},
	{"Close tab", "&", true},
	{"Zoom split", "z", true},
	{"Next agent waiting for you", "!", true},
	{"Review queue", "Q", false},
	{"Next tab", "n", true},
	{"Previous tab", "p", true},
	{"Every tab", "w", true},
	{"Rename tab", ",", true},
	{"Equalize splits", "=", true},
	{"Next layout", " ", true},
	{"Type into every split", "S", true},
	{"Scroll history", "[", true},
	{"Open a link the pane printed", "u", true},
	{"Back to where the split was", "b", true},
	{"New workspace", "N", true},
	{"Next workspace", ")", true},
	{"Previous workspace", "(", true},
	{"Add a project", "a", false},
	{"Add a machine", "M", false},
	{"SSH to a host", "H", false},
	{"Broadcast to the selection", "B", false},
	{"Files", "f", false},
	{"Agent setup", "i", false},
	{"Pair a phone", "P", false},
	{"Ask conch", ":", true},
	{"Settings", ",", false},
	{"Help", "?", true},
	{"Detach (agents keep running)", "d", true},
}

// keyMsg is the key a command sends, as Bubble Tea would hand it over.
func keyMsg(k string) tea.KeyMsg {
	if k == " " {
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
}

// openPalette builds the list from what is on screen now.
func (m *Model) openPalette() tea.Cmd {
	p := &cmdPalette{items: m.paletteItems()}
	p.filter()
	m.overlay = p
	return nil
}

func (m Model) paletteItems() []paletteItem {
	var waiting, panes, places []paletteItem
	many := len(m.machines) > 1
	for _, mach := range m.machines {
		projects, shown := m.shown(mach)
		names := map[string]string{}
		for _, pr := range projects {
			names[pr.ID] = pr.Name
		}
		where := func(parts ...string) string {
			if many {
				parts = append(parts, mach.label)
			}
			return strings.Join(slices.DeleteFunc(parts, func(s string) bool { return s == "" }), " · ")
		}
		for _, p := range shown {
			if m.isOwnPane(mach.id, p.ID) {
				continue
			}
			mid, pane := mach.id, p
			name := p.DisplayName()
			mark, style := m.agentMark(p)
			kind := "terminal"
			label := "› " + name
			if p.Agent != nil {
				kind = p.Agent.Name
				label = style.Render(mark) + name
			}
			it := paletteItem{
				label:  label,
				detail: where(names[p.ProjectID], p.Branch),
				hint:   kind,
				run: func(m *Model) tea.Cmd {
					m.filter = ""
					if pane.ProjectID != "" {
						m.expanded[workspaceID(mid)] = true
					}
					m.revealPane(mid, pane)
					m.focus = focusMain
					return m.rebuild()
				},
			}
			it.text = strings.ToLower(name + " " + it.detail + " " + kind)
			if p.Agent.NeedsAttention() {
				it.label = styleWarn.Render("! ") + it.label
				waiting = append(waiting, it)
				continue
			}
			panes = append(panes, it)
		}
		for _, pr := range projects {
			mid, pid := mach.id, pr.ID
			it := paletteItem{
				label:  styleAccent.Render("◆ ") + pr.Name,
				detail: where(),
				hint:   "project",
				run: func(m *Model) tea.Cmd {
					m.filter = ""
					m.selectProject(mid, pid)
					m.pendingShow = m.cursor
					return m.rebuild()
				},
			}
			it.text = strings.ToLower(pr.Name + " " + it.detail + " project")
			places = append(places, it)
			// Task branches: the ones checked out in a worktree of their
			// own, which is where an agent's work is. Every branch of a
			// repository would bury them.
			for _, wt := range pr.Worktrees {
				if wt.Main || wt.Branch == "" {
					continue
				}
				b := wt.Branch
				it := paletteItem{
					label:  "◇ " + b,
					detail: where(pr.Name),
					hint:   "branch",
					run: func(m *Model) tea.Cmd {
						m.filter = ""
						m.selectProject(mid, pid)
						m.expanded[projectNodeID(mid, pid)] = true
						m.expanded[sectionID(mid, pid, "branches")] = true
						m.showAll[scoped(mid, pid)] = true
						m.cursor = branchNodeID(mid, pid, b)
						m.pendingShow = m.cursor
						m.focus = focusMain
						return m.rebuild()
					},
				}
				it.text = strings.ToLower(b + " " + it.detail + " branch")
				places = append(places, it)
			}
		}
	}
	var cmds []paletteItem
	for _, c := range paletteCommands {
		hint := c.key
		if c.key == " " {
			hint = "space"
		}
		if c.prefix {
			hint = m.cfg.Keys.Prefix + " " + hint
			if sc := appShortcutFor(c.key); sc != "" && m.appWindow {
				hint = sc + "  " + hint
			}
		}
		key, prefix := c.key, c.prefix
		cmds = append(cmds, paletteItem{
			label: c.label,
			hint:  hint,
			text:  strings.ToLower(c.label),
			run: func(m *Model) tea.Cmd {
				if prefix {
					m.prefixArmed = true
				} else {
					// A tree key acts on the tree's selection, as it does
					// when typed there.
					m.focus = focusSidebar
				}
				next, cmd := m.handleKey(keyMsg(key))
				*m = next.(Model)
				return cmd
			},
		})
	}
	return slices.Concat(waiting, panes, places, cmds)
}

// filter narrows the list to what matches the query: every word of it
// somewhere in the item, and an item whose name starts with the query
// before one that only contains it. An empty query lists everything.
func (p *cmdPalette) filter() {
	q := strings.ToLower(strings.TrimSpace(p.query))
	words := strings.Fields(q)
	type hit struct{ i, rank int }
	var hits []hit
	for i, it := range p.items {
		ok := true
		for _, w := range words {
			if !strings.Contains(it.text, w) {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		rank := 2
		switch name := strings.ToLower(ansi.Strip(it.label)); {
		case q == "":
			rank = 0
		case strings.Contains(name, " "+q) || strings.HasPrefix(name, q):
			rank = 0
		case strings.Contains(name, q):
			rank = 1
		}
		hits = append(hits, hit{i, rank})
	}
	slices.SortStableFunc(hits, func(a, b hit) int { return a.rank - b.rank })
	p.shown = p.shown[:0]
	for _, h := range hits {
		p.shown = append(p.shown, h.i)
	}
	p.sel, p.top = 0, 0
}

// rows is how many items fit on a screen of height h.
func (p *cmdPalette) rows(h int) int {
	return max(min(14, h-6), 1)
}

func (p *cmdPalette) move(d, h int) {
	if len(p.shown) == 0 {
		return
	}
	p.sel = clamp(p.sel+d, 0, len(p.shown)-1)
	n := p.rows(h)
	if p.sel < p.top {
		p.top = p.sel
	} else if p.sel >= p.top+n {
		p.top = p.sel - n + 1
	}
}

func (p *cmdPalette) run(m *Model) tea.Cmd {
	m.overlay = nil
	if p.sel < 0 || p.sel >= len(p.shown) {
		return nil
	}
	return p.items[p.shown[p.sel]].run(m)
}

func (p *cmdPalette) update(m *Model, msg tea.Msg) (bool, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return false, nil
	}
	switch k.Type {
	case tea.KeyEsc, tea.KeyCtrlC:
		m.overlay = nil
		return true, nil
	case tea.KeyEnter:
		return true, p.run(m)
	case tea.KeyUp, tea.KeyCtrlP, tea.KeyShiftTab:
		p.move(-1, m.height)
	case tea.KeyDown, tea.KeyCtrlN, tea.KeyTab:
		p.move(1, m.height)
	case tea.KeyPgUp:
		p.move(-p.rows(m.height), m.height)
	case tea.KeyPgDown:
		p.move(p.rows(m.height), m.height)
	case tea.KeyBackspace:
		if r := []rune(p.query); len(r) > 0 {
			p.query = string(r[:len(r)-1])
			p.filter()
		}
	case tea.KeyCtrlU:
		p.query = ""
		p.filter()
	case tea.KeyRunes, tea.KeySpace:
		if k.Paste {
			// A paste is text to look for, but one line of it.
			p.query += strings.Join(strings.Fields(string(k.Runes)), " ")
		} else {
			p.query += string(k.Runes)
		}
		if k.Type == tea.KeySpace {
			p.query += " "
		}
		p.filter()
	}
	return false, nil
}

// dimBackground fades the screen behind the palette, as the ask bar's
// does: while it is open nothing else takes a key.
func (p *cmdPalette) dimBackground() bool { return true }

// render draws the palette as a search box: what you type on the top line
// with how many match on the right of it, a rule, then the list — each
// item, where it is in a muted voice, and its key or kind at the end. No
// title in the frame: the box says what it is by being typed into.
func (p *cmdPalette) render(m Model) box {
	inner := max(min(72, m.width-2), 1)
	count := ""
	if p.query != "" || len(p.shown) > p.rows(m.height) {
		count = itoa(len(p.shown)) + " "
		if len(p.shown) > 0 {
			count = itoa(p.sel+1) + "/" + count
		}
	}
	input := " " + styleAccent.Render("›") + " " + p.query + styleSel.Render(" ")
	if p.query == "" {
		input += styleMuted.Render(" agents, branches, projects, commands")
	}
	gap := inner - ansi.StringWidth(input) - ansi.StringWidth(count)
	if gap < 1 {
		count, gap = "", 0
	}
	lines := []string{fit(input+strings.Repeat(" ", max(gap, 0))+styleMuted.Render(count), inner)}
	lines = append(lines, styleMuted.Render(strings.Repeat("─", inner)))
	n := p.rows(m.height)
	if len(p.shown) == 0 {
		lines = append(lines, fit(styleMuted.Render("   nothing matches "+`"`+p.query+`"`), inner))
	}
	for i := p.top; i < len(p.shown) && i < p.top+n; i++ {
		it := p.items[p.shown[i]]
		hint := it.hint
		left := " " + it.label
		if it.detail != "" {
			left += "  " + styleMuted.Render(it.detail)
		}
		room := max(inner-ansi.StringWidth(hint)-2, 1)
		if ansi.StringWidth(left) > room {
			left = ansi.Truncate(left, room, "…")
		}
		gap := max(inner-ansi.StringWidth(left)-ansi.StringWidth(hint)-1, 1)
		line := left + strings.Repeat(" ", gap) + styleMuted.Render(hint) + " "
		if i == p.sel {
			line = styleSel.Render(padRight(ansi.Strip(line), inner))
		}
		lines = append(lines, fit(line, inner))
	}
	b := box{lines: frameLines("", lines, inner, colorAccent)}
	b.x = max((m.width-b.width())/2, 0)
	// In conch's own window it opens under the search box in the header,
	// which is the top of the screen it is given; elsewhere a little
	// below the top.
	y := 2
	if m.headerRows > 0 {
		y = 0
	}
	b.y = clamp(y, 0, max(m.height-len(b.lines), 0))
	return b
}

func (p *cmdPalette) mouse(m *Model, msg tea.MouseMsg, b box) tea.Cmd {
	// Items start under the border, the query and the rule.
	i := p.top + msg.Y - b.y - 3
	in := b.contains(msg.X, msg.Y) && msg.Y-b.y-3 >= 0 && i < len(p.shown) && i < p.top+p.rows(m.height)
	switch {
	case msg.Button == tea.MouseButtonWheelUp:
		p.move(-1, m.height)
	case msg.Button == tea.MouseButtonWheelDown:
		p.move(1, m.height)
	case msg.Action == tea.MouseActionMotion && in:
		p.sel = i
	case msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft && in:
		p.sel = i
		return p.run(m)
	case msg.Action == tea.MouseActionPress && !b.contains(msg.X, msg.Y):
		m.overlay = nil
	}
	return nil
}
