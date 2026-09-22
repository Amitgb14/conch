package tui

import (
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/remote"
)

func (m Model) handleKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.overlay != nil {
		_, cmd := m.overlay.update(&m, k)
		return m, cmd
	}
	if m.focus == focusMain {
		return m.handleMainKey(k)
	}
	if m.filtering {
		return m.handleFilterKey(k)
	}

	m.flash = ""
	r, ok := m.selectedRow()
	if cmd, handled := m.repeatResize(k.String()); handled {
		return m, cmd
	}
	if cmd, handled := m.numberKey(k.String()); handled {
		return m, cmd
	}
	if m.prefixArmed {
		m.prefixArmed = false
		if cmd, handled := m.layoutKey(k.String()); handled {
			return m, cmd
		}
		if k.String() == "z" {
			m.zoom = !m.zoom
			return m, m.syncView()
		}
		if k.String() == "r" && ok {
			return m, m.redrawPane(r)
		}
		return m, nil
	}
	if k.String() == m.cfg.Keys.Prefix {
		m.prefixArmed = true
		return m, nil
	}
	switch k.String() {
	case "q", "ctrl+c":
		return m, tea.Sequence(m.saveState(), tea.Quit)
	case "v", "s", "O":
		if !ok {
			break
		}
		switch k.String() {
		case "v":
			return m, m.split(splitRight, viewOf(r))
		case "s":
			return m, m.split(splitDown, viewOf(r))
		}
		return m, m.newTab(viewOf(r))
	case "up", "k":
		return m, m.moveCursor(-1)
	case "down", "j":
		return m, m.moveCursor(1)
	case "pgup":
		return m, m.moveCursor(-m.sidebarRowsVisible())
	case "pgdown":
		return m, m.moveCursor(m.sidebarRowsVisible())
	case "home", "g":
		return m, m.moveCursor(-len(m.rows))
	case "end", "G":
		return m, m.moveCursor(len(m.rows))
	case "right", "l":
		if ok && r.expandable() && !m.isOpen(r) {
			open := true
			return m, m.toggle(r, &open)
		}
		return m, m.moveCursor(1)
	case "left", "h":
		if ok && r.expandable() && m.isOpen(r) {
			closed := false
			return m, m.toggle(r, &closed)
		}
		if i := indexOfRow(m.rows, m.cursor); i >= 0 {
			if pid := parentID(m.rows, i); pid != "" {
				m.cursor = pid
				m.keepCursorVisible()
				return m, m.syncView()
			}
		}
	case " ":
		if ok {
			return m, m.toggle(r, nil)
		}
	case "enter":
		return m, m.activate(r)
	case "tab":
		if ok && (r.kind == kindPane || r.kind == kindBranch) {
			m.focus = focusMain
		}
	case "/":
		m.filtering = true
	case "esc":
		if m.filter != "" {
			m.filter = ""
			return m, m.rebuild()
		}
	case "n":
		return m, m.openHere(false)
	case "c", "A", "C":
		pl := m.contextPlace()
		mach := m.machine(pl.machine)
		if mach == nil || mach.c == nil {
			m.setFlash(m.offlineText(pl.machine), true)
			break
		}
		m.overlay = newAgentMenu(m, mach)
	case "t":
		return m, m.openTaskDialog()
	case "a":
		pl := m.contextPlace()
		if m.clientOf(pl.machine) == nil {
			m.setFlash(m.offlineText(pl.machine), true)
			break
		}
		mach := m.machine(pl.machine)
		if len(mach.c.MissingCapabilities([]string{"fs.v1"})) > 0 {
			m.overlay = newAddProjectDialog(m, pl.machine) // server too old to browse
			break
		}
		start := "~"
		if pl.machine == localMachine {
			start = cwdOrHome()
		}
		b, cmd := newBrowser(&m, pl.machine, start)
		m.overlay = b
		return m, cmd
	case "H":
		return m, m.openSSH()
	case "M":
		d := newAddMachineDialog(m)
		m.overlay = d
		return m, d.focusCmd()
	case "r":
		m.openRename()
	case "x":
		m.openRemove()
	case "R":
		if ok && r.kind == kindMachine {
			return m, m.reconnect(r.machine, false)
		}
		if pl := m.contextPlace(); pl.projectID != "" {
			if c := m.clientOf(pl.machine); c != nil {
				c.Notify(proto.MethodProjectRefresh, proto.ProjectRef{ID: pl.projectID})
				m.setFlash("refreshing", false)
			}
		}
	case "m":
		if ok {
			i := indexOfRow(m.rows, r.id) - m.scroll
			m.overlay = newRowMenu(m, r, min(m.sidebarW-2, 4+r.depth*2), 2+i)
		}
	case "Q":
		m.focus = focusMain
		return m, m.show(queueRow())
	case "!":
		return m, m.jumpToAttention()
	case "y":
		return m, m.copyRow(r)
	case "o":
		if r.kind == kindBranch {
			if pr := m.branchPR(r.machine, r.projectID, r.branch); pr != nil {
				return m, openURL(pr.URL)
			}
			m.setFlash("no pull request for "+r.branch, true)
		}
	case "z":
		m.zoom = !m.zoom
		return m, m.syncView()
	case "?":
		m.overlay = newHelp()
	case "i":
		return m, m.openSetup()
	case ":":
		return m, m.openAsk()
	case "S":
		return m, m.summarizeSelected()
	case "B":
		return m, m.openBroadcast()
	case "W":
		return m, m.openCleanup()
	case "F":
		pl := m.contextPlace()
		proj := m.project(pl.machine, pl.projectID)
		if proj == nil || !proj.Git {
			m.setFlash("select a git project to choose its local files", true)
			break
		}
		if c := m.clientOf(pl.machine); c == nil || len(c.MissingCapabilities([]string{"worktree.files.v1"})) > 0 {
			m.setFlash("restart the server on this machine to use local files", true)
			break
		}
		d := newLocalFilesDialog(m, pl.machine, *proj)
		m.overlay = d
		return m, d.focusCmd()
	case ",":
		s, cmd := newSettings(&m)
		m.overlay = s
		return m, cmd
	}
	return m, nil
}

// activate is enter (or a double click) on a row.
func (m *Model) activate(r row) tea.Cmd {
	switch r.kind {
	case kindPane, kindBranch, kindSessions:
		m.focus = focusMain
		return m.show(r)
	case kindMore:
		return m.toggle(r, nil)
	}
	return m.toggle(r, nil)
}

func (m Model) handleFilterKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.Type {
	case tea.KeyEsc:
		m.filtering, m.filter = false, ""
	case tea.KeyEnter:
		m.filtering = false
	case tea.KeyBackspace:
		if r := []rune(m.filter); len(r) > 0 {
			m.filter = string(r[:len(r)-1])
		}
	case tea.KeyUp, tea.KeyDown:
		m.filtering = false
		return m.handleKey(k)
	case tea.KeyRunes, tea.KeySpace:
		m.filter += string(k.Runes)
		if k.Type == tea.KeySpace {
			m.filter += " "
		}
	default:
		return m, nil
	}
	return m, m.rebuild()
}

// handleMainKey routes keys while the main area has focus: to the pane, or
// to the changes view.
func (m Model) handleMainKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Keys go to what the focused split shows. The tree's cursor can be
	// elsewhere — on Workspace while an agent's tab is clicked, say — and
	// following it sent the keys nowhere.
	r := m.activeRow()
	prefix := m.cfg.Keys.Prefix

	if cmd, handled := m.repeatResize(k.String()); handled {
		return m, cmd
	}
	if cmd, handled := m.numberKey(k.String()); handled {
		return m, cmd
	}
	// Q opens the review queue from a view as well as from the tree. Not
	// inside a pane: there every key belongs to the program.
	if k.String() == "Q" && r.kind != kindPane {
		return m, m.show(queueRow())
	}
	if m.prefixArmed {
		m.prefixArmed = false
		if cmd, handled := m.layoutKey(k.String()); handled {
			return m, cmd
		}
		switch k.String() {
		case prefix:
			if r.kind == kindPane {
				m.sendKey(r.machine, r.paneID, k)
			}
		case "[", "pgup":
			if r.kind == kindPane && m.frame != nil {
				m.enterScrollMode()
				if k.String() == "pgup" {
					m.scrollPane(m.page())
				}
			}
		case "z":
			m.zoom = !m.zoom
			return m, m.syncView()
		case "!":
			return m, m.jumpToAttention()
		case "r":
			return m, m.redrawPane(r)
		default:
			m.focus = focusSidebar
		}
		return m, nil
	}
	if k.String() == prefix {
		m.prefixArmed = true
		return m, nil
	}

	switch r.kind {
	case kindPane:
		p := m.pane(r.machine, r.paneID)
		if p == nil || p.State != proto.PaneRunning {
			m.focus = focusSidebar
			return m, nil
		}
		if m.scrollMode {
			return m, m.scrollKey(k)
		}
		m.sel = nil
		if m.offset > 0 {
			m.scrollPane(-m.offset) // typing returns to the live screen
		}
		if cmd, handled := m.dropFiles(r.machine, p.ID, k); handled {
			return m, cmd // files dropped on a remote pane go there first
		}
		m.sendKey(r.machine, p.ID, k)
		m.forwardSynced(r.machine, p.ID, k)
		return m, nil
	case kindReviewQueue:
		if m.queueView != nil {
			back, cmd := m.queueView.key(&m, k)
			if back {
				m.focus = focusSidebar
			}
			return m, cmd
		}
	case kindSessions:
		if m.sessionsView != nil {
			back, cmd := m.sessionsView.key(&m, k)
			if back {
				m.focus = focusSidebar
			}
			return m, cmd
		}
	case kindBranch:
		if m.changes != nil {
			if back, cmd := m.changes.key(&m, k); back {
				m.focus = focusSidebar
				return m, cmd
			} else {
				return m, cmd
			}
		}
	}
	m.focus = focusSidebar
	return m, nil
}

// layoutKey handles the split and tab commands that follow the prefix.
func (m *Model) layoutKey(key string) (tea.Cmd, bool) {
	switch key {
	case "v", "%", "|":
		return m.split(splitRight, viewRef{}), true
	case "-", "\"", "_":
		return m.split(splitDown, viewRef{}), true
	case "x":
		return m.closeSplitAsk(), true
	case "left", "h":
		return m.moveFocus(-1, 0), true
	case "right":
		return m.moveFocus(1, 0), true
	case "l":
		return m.gotoLastTab(), true
	case ";":
		return m.lastSplit(), true
	case "{":
		return m.swapSplit(-1), true
	case "}":
		return m.swapSplit(1), true
	case "w", "s":
		m.openTabPicker()
		return nil, true
	case "d":
		return tea.Sequence(m.saveState(), tea.Quit), true
	case "?":
		m.overlay = newHelp()
		return nil, true
	case ":":
		return m.openAsk(), true
	case ".":
		return m.openMoveTab(), true
	case "<":
		return m.moveTab(-1), true
	case ">":
		return m.moveTab(1), true
	case "q":
		return m.showNumbers(), true
	case " ":
		return m.applyLayout(m.tab().layout), true // the one after the last applied
	case "alt+1", "alt+2", "alt+3", "alt+4", "alt+5":
		return m.applyLayout(int(key[4] - '1')), true
	case "S":
		m.promote()
		return m.toggleSync(), true
	case "up", "k":
		return m.moveFocus(0, -1), true
	case "down", "j":
		return m.moveFocus(0, 1), true
	case "o":
		ls := m.tab().root.leaves()
		for i, l := range ls {
			if l.id == m.tab().focus {
				return m.focusLeaf(ls[(i+1)%len(ls)].id), true
			}
		}
	case "c":
		return m.newTab(viewRef{}), true
	case "n":
		return m.stepTab(1), true
	case "p":
		return m.stepTab(-1), true
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		return m.gotoVisibleTab(int(key[0] - '1')), true
	case "0":
		return m.gotoVisibleTab(9), true
	case "&":
		if m.previewing {
			return nil, true
		}
		return m.closeTabAsk(m.activeTab), true
	case "=":
		m.tab().root.equalize()
		return tea.Batch(m.syncView(), m.saveState()), true
	case ",":
		m.promote()
		t := m.tab()
		d := newDialog(*m, " Rename tab ", []string{"Leave empty to name it after what it shows."}, []string{"Name"}, []string{t.name})
		d.submit = func(m *Model, v []string) tea.Cmd {
			t.name = strings.TrimSpace(v[0])
			return m.saveState()
		}
		m.overlay = d
		return d.focusCmd(), true
	}
	if dx, dy, ok := resizeKey(key); ok {
		return m.resizeFocus(dx, dy), true
	}
	return nil, false
}

// enterScrollMode starts scroll mode with the cursor at the bottom left.
func (m *Model) enterScrollMode() {
	if m.frame == nil || m.viewing == "" {
		return
	}
	_, rows := m.paneArea()
	m.scrollMode, m.focus = true, focusMain
	m.curX, m.curY, m.sel = 0, rows-1, nil
}

// scrollKey handles keys in scroll mode: a cursor moves over the history
// (scrolling at the edges), v starts a selection and y or enter copies it.
func (m *Model) scrollKey(k tea.KeyMsg) tea.Cmd {
	cols, rows := m.paneArea()
	moveY := func(dy int) {
		m.curY += dy
		if m.curY < 0 {
			m.scrollPane(-m.curY) // above the top: scroll back
			m.curY = 0
		} else if m.curY >= rows {
			m.scrollPane(rows - 1 - m.curY)
			m.curY = rows - 1
		}
	}
	switch k.String() {
	case "up", "k":
		moveY(-1)
	case "down", "j":
		moveY(1)
	case "left", "h":
		m.curX = max(m.curX-1, 0)
	case "right", "l":
		m.curX = min(m.curX+1, cols-1)
	case "0", "home":
		m.curX = 0
	case "$", "end":
		m.curX = cols - 1
	case "pgup", "b", "ctrl+u":
		m.scrollPane(m.page())
	case "pgdown", "f", "ctrl+d":
		m.scrollPane(-m.page())
	case "g":
		m.scrollPane(m.frame.History)
		m.curY = 0
	case "v", " ":
		if m.sel != nil && m.sel.keyboard {
			m.sel = nil
		} else {
			m.sel = &selection{paneID: m.viewing, ax: m.curX, ay: m.curY, bx: m.curX, by: m.curY, hasContent: true, keyboard: true}
		}
	case "y", "enter":
		if m.sel == nil || m.frame == nil {
			return nil
		}
		text := m.sel.text(m.frame.Lines, cols)
		m.sel, m.scrollMode = nil, false
		m.scrollPane(-m.offset)
		return copyText(text)
	default: // G, esc, q or anything else: back to live
		m.sel = nil
		m.scrollPane(-m.offset)
		m.scrollMode = false
	}
	if m.sel != nil && m.sel.keyboard {
		m.sel.bx, m.sel.by = m.curX, m.curY
	}
	return nil
}

func (m Model) page() int {
	_, rows := m.paneArea()
	return max(rows-2, 1)
}

// copyRow copies the most useful text for a tree row: a branch name, a
// project or pane directory.
func (m *Model) copyRow(r row) tea.Cmd {
	switch r.kind {
	case kindBranch:
		return copyText(r.branch)
	case kindPane:
		if p := m.pane(r.machine, r.paneID); p != nil {
			return copyText(p.Cwd)
		}
	case kindProject, kindBranches, kindAgents, kindTerminals, kindSSH, kindMore, kindSessions:
		if proj := m.project(r.machine, r.projectID); proj != nil {
			return copyText(proj.Path)
		}
	}
	return nil
}

func (m *Model) openRename() {
	r, _ := m.selectedRow()
	if r.kind == kindMachine {
		m.openRenameMachine(r.machine)
		return
	}
	if r.kind != kindPane {
		m.setFlash("select a machine, agent or terminal to rename", true)
		return
	}
	if p := m.pane(r.machine, r.paneID); p != nil {
		m.overlay = newRenameDialog(*m, r.machine, *p)
	}
}

// openRenameMachine asks for a machine's new label.
func (m *Model) openRenameMachine(mid string) {
	mach := m.machine(mid)
	switch {
	case mach == nil:
		return
	case mid == localMachine:
		m.setFlash("this computer is always called local", true)
		return
	}
	d := newDialog(*m, " Rename machine ", []string{"The name shown in the tree; the ssh target (" + mach.target + ") stays."}, []string{"Label"}, []string{mach.label})
	d.submit = func(m *Model, v []string) tea.Cmd {
		saved, err := remote.RenameMachine(mid, v[0])
		if err != nil {
			m.setFlash(err.Error(), true)
			return nil
		}
		if mach := m.machine(mid); mach != nil {
			mach.label = saved.Label
		}
		m.catalogStamp = catalogStamp() // our own change: nothing for the watcher to do
		m.setFlash("renamed to "+saved.Label, false)
		return m.rebuild()
	}
	m.overlay = d
}

// openRemove confirms the removal that fits the selected row.
func (m *Model) openRemove() {
	r, _ := m.selectedRow()
	switch r.kind {
	case kindMachine:
		if r.machine == localMachine {
			m.setFlash("this computer can't be removed", true)
			return
		}
		mid := r.machine
		label := m.machine(mid).label
		m.overlay = newConfirm(fmt.Sprintf("Remove %s from conch? Its server and panes keep running there.", label), func(m *Model) tea.Cmd {
			return m.removeMachine(mid)
		})
	case kindPane:
		p := m.pane(r.machine, r.paneID)
		if p == nil {
			return
		}
		id, mid := p.ID, r.machine
		m.overlay = newConfirm(fmt.Sprintf("Close %s? Its process is stopped.", p.DisplayName()), func(m *Model) tea.Cmd {
			return m.callOn(mid, proto.MethodPaneClose, proto.PaneRef{ID: id}, nil, nil)
		})
	case kindBranch:
		proj := m.project(r.machine, r.projectID)
		if proj == nil {
			return
		}
		for _, wt := range proj.Worktrees {
			if wt.Branch == r.branch && !wt.Main {
				params := proto.WorktreeRemoveParams{ProjectID: proj.ID, Path: wt.Path}
				mid := r.machine
				m.overlay = newConfirm(fmt.Sprintf("Remove worktree %s? The branch is kept; uncommitted changes block removal.", m.tildify(mid, wt.Path)),
					func(m *Model) tea.Cmd {
						return m.callOn(mid, proto.MethodWorktreeRemove, params, nil, func() tea.Msg { return flashMsg("worktree removed") })
					})
				return
			}
		}
		m.setFlash("only linked worktrees can be removed", true)
	case kindProject:
		proj := m.project(r.machine, r.projectID)
		if proj == nil {
			return
		}
		id, mid := proj.ID, r.machine
		m.overlay = newConfirm(fmt.Sprintf("Remove %s from the sidebar? Files are not touched.", proj.Name), func(m *Model) tea.Cmd {
			return m.callOn(mid, proto.MethodProjectRemove, proto.ProjectRef{ID: id}, nil, nil)
		})
	}
}

func (m *Model) openTaskDialog() tea.Cmd {
	pl := m.contextPlace()
	proj := m.project(pl.machine, pl.projectID)
	if proj == nil || !proj.Git {
		m.setFlash("select a git project to start a task", true)
		return nil
	}
	d := newTaskDialog(*m, pl.machine, *proj)
	m.overlay = d
	return d.focusCmd()
}

// reconnect retries a machine now. With install, conch is installed or
// upgraded there first (remote) or the server is started (local).
func (m *Model) reconnect(mid string, install bool) tea.Cmd {
	mach := m.machine(mid)
	if mach == nil {
		return nil
	}
	mach.close()
	mach.failures = 0
	m.setFlash("connecting to "+mach.label+"…", false)
	return tea.Batch(mach.connect(install), m.rebuild())
}

// restartServer stops a machine's server; reconnecting starts the new build.
func (m *Model) restartServer(mid string) tea.Cmd {
	mach := m.machine(mid)
	if mach == nil || mach.c == nil {
		return nil
	}
	c := mach.c
	return func() tea.Msg {
		if err := callCtx(c, proto.MethodServerStop, nil, nil); err != nil {
			return errMsg{err}
		}
		return flashMsg("restarting the server on " + mach.label)
	}
}

// canReload reports whether a machine's server can reload without
// stopping its panes.
// redrawPane asks a pane's program to draw its screen again, clearing what
// a partial redraw left behind. The pane is told its size changed instead
// of being sent a keystroke, which would land in whatever it is typing.
func (m *Model) redrawPane(r row) tea.Cmd {
	if r.kind != kindPane {
		m.setFlash("select an agent or terminal to redraw", true)
		return nil
	}
	c := m.clientOf(r.machine)
	if c == nil {
		m.setFlash("machine is offline", true)
		return nil
	}
	if len(c.MissingCapabilities([]string{"pane.redraw.v1"})) > 0 {
		m.setFlash("this server predates redrawing panes", true)
		return nil
	}
	name := r.paneID
	if p := m.pane(r.machine, r.paneID); p != nil {
		name = p.DisplayName()
	}
	m.setFlash("redrawing "+name, false)
	return m.callOn(r.machine, proto.MethodPaneRedraw, proto.PaneRef{ID: r.paneID}, nil, nil)
}

func (m Model) canReload(mid string) bool {
	mach := m.machine(mid)
	return mach != nil && mach.c != nil && len(mach.c.MissingCapabilities([]string{"server.reload.v1"})) == 0
}

type reloadedMsg struct{ machine string }

// reloadServer runs the build installed on a machine without stopping its
// panes: the server execs its (new) executable and the TUI reconnects.
func (m *Model) reloadServer(mid string) tea.Cmd { return m.reloadServerInto(mid, "") }

// reloadServerInto reloads a machine's server into bin ("" for its own
// executable).
func (m *Model) reloadServerInto(mid, bin string) tea.Cmd {
	mach := m.machine(mid)
	if mach == nil || mach.c == nil {
		return nil
	}
	c, label := mach.c, mach.label
	m.setFlash("reloading the server on "+label+"…", false)
	return func() tea.Msg {
		var res proto.ServerReloadResult
		if err := callCtx(c, proto.MethodServerReload, proto.ServerReloadParams{Binary: bin}, &res); err != nil {
			return errMsg{err}
		}
		time.Sleep(500 * time.Millisecond) // the exec takes a moment
		return reloadedMsg{machine: mid}
	}
}

func (m *Model) removeMachine(mid string) tea.Cmd {
	for i, mach := range m.machines {
		if mach.id == mid {
			mach.close()
			m.machines = append(m.machines[:i], m.machines[i+1:]...)
			break
		}
	}
	if err := remote.RemoveMachine(mid); err != nil {
		m.setFlash(err.Error(), true)
	}
	return m.rebuild()
}

func cwdOrHome() string {
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	home, _ := os.UserHomeDir()
	return home
}
