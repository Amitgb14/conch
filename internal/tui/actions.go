package tui

import (
	"fmt"
	"strconv"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
)

// Custom actions: the commands somebody puts in config.toml and reaches
// from the row menu. conch's part is the context — which machine, which
// checkout — so an action is written once and works on every branch.
//
// It runs as an ordinary terminal pane on the machine the row belongs to,
// which is what makes a remote machine work without anything new: the
// command runs where the worktree is, not on the computer the TUI happens
// to be on.

// actionPlace is the word `on` uses for a kind of row, or "" for a row
// with no directory to run a command in. Only the four kinds that stand
// for something on disk: a section header is not a place.
func actionPlace(k nodeKind) string {
	switch k {
	case kindPane:
		return config.ActionOnPane
	case kindBranch:
		return config.ActionOnBranch
	case kindProject:
		return config.ActionOnProject
	case kindMachine:
		return config.ActionOnMachine
	}
	return ""
}

// actionsFor is the actions offered on a row.
func (m Model) actionsFor(r row) []config.Action {
	place := actionPlace(r.kind)
	if place == "" {
		return nil
	}
	return m.cfg.ActionsOn(place)
}

// actionsMenuItem is the row menu's way into them, when there are any.
// One item rather than a command per action: the menus are long enough,
// and these are somebody's own commands rather than conch's.
func (m Model) actionsMenuItem(r row, x, y int) []menuItem {
	acts := m.actionsFor(r)
	if len(acts) == 0 {
		return nil
	}
	return []menuItem{{"", "Actions…", func(m *Model) tea.Cmd {
		back, _ := m.overlay.(*menu)
		m.overlay = newActionsMenu(*m, r, x, y, back)
		return nil
	}}}
}

// newActionsMenu lists the actions for a row, numbered: a menu of
// somebody's own commands is reached often enough to be worth a digit.
func newActionsMenu(m Model, r row, x, y int, back *menu) *menu {
	var items []menuItem
	for i, a := range m.actionsFor(r) {
		act := a
		key := ""
		if i < 9 {
			key = strconv.Itoa(i + 1)
		}
		items = append(items, menuItem{key, a.Name, func(m *Model) tea.Cmd { return m.runAction(act, r) }})
	}
	return &menu{title: "Actions", items: items, x: x, y: y, back: back}
}

// runAction starts an action's command in a terminal of its own, on the
// machine the row belongs to and in its checkout.
func (m *Model) runAction(a config.Action, r row) tea.Cmd {
	pl := m.placeOf(r)
	c := m.clientOf(pl.machine)
	if c == nil {
		m.setFlash(m.offlineText(pl.machine), true)
		return nil
	}
	if pl.dir == "" {
		// A branch nobody has checked out has no directory to run in, and
		// making a worktree is too much for a menu item to do by itself.
		if pl.branch != "" {
			m.setFlash(pl.branch+" has no worktree to run in — open a terminal on it first (n)", true)
			return nil
		}
		m.setFlash("nothing here to run "+a.Name+" in", true)
		return nil
	}
	cols, rows := m.paneArea()
	mid := pl.machine
	keep := a.Keeps()
	name := a.Name
	params := proto.PaneCreateParams{
		Name:      a.Name + " · " + m.actionWhere(pl),
		Command:   []string{"/bin/sh", "-lc", a.Run},
		Cwd:       pl.dir,
		Env:       m.actionEnv(r, pl),
		Cols:      cols,
		Rows:      rows,
		NoProject: pl.loose,
	}
	return func() tea.Msg {
		var info proto.PaneInfo
		if err := callCtx(c, proto.MethodPaneCreate, params, &info); err != nil {
			return errMsg{err}
		}
		return actionStartedMsg{machine: mid, info: info, name: name, keep: keep}
	}
}

// actionStartedMsg carries the terminal an action is running in, so a kept
// one can be recognised when it exits.
type actionStartedMsg struct {
	machine string
	info    proto.PaneInfo
	name    string
	keep    bool
}

func (m *Model) receiveActionStarted(msg actionStartedMsg) tea.Cmd {
	if msg.keep {
		if m.actionPanes == nil {
			m.actionPanes = map[string]string{}
		}
		m.actionPanes[paneKey(msg.machine, msg.info.ID)] = msg.name
	}
	return func() tea.Msg { return createdMsg{machine: msg.machine, info: msg.info} }
}

// actionExited reports whether a pane that has just exited was an action
// whose terminal is kept, and says how it went. Like a check's terminal,
// it is the only place the output lives: closing it is a person's to do.
func (m *Model) actionExited(machine string, info proto.PaneInfo) bool {
	name, ok := m.actionPanes[paneKey(machine, info.ID)]
	if !ok {
		return false
	}
	delete(m.actionPanes, paneKey(machine, info.ID))
	if info.ExitCode == 0 {
		m.setFlash(name+" finished", false)
	} else {
		m.setFlash(fmt.Sprintf("%s failed (exit %d) — its terminal has the output", name, info.ExitCode), true)
	}
	return true
}

// actionWhere names what the action was started on, for the terminal's
// title: the branch, the project, or the machine.
func (m Model) actionWhere(pl place) string {
	if pl.branch != "" {
		return pl.branch
	}
	if proj := m.project(pl.machine, pl.projectID); proj != nil {
		return proj.Name
	}
	if mach := m.machine(pl.machine); mach != nil {
		return mach.label
	}
	return pl.machine
}

// actionEnv is the context an action is given. The names are conch's own,
// so a script can be written once and used from any row: what is not
// there for this row is empty rather than missing, which a shell reads
// the same way and a `set -u` script does not trip over.
func (m Model) actionEnv(r row, pl place) []string {
	project, branch := "", pl.branch
	if proj := m.project(pl.machine, pl.projectID); proj != nil {
		project = proj.Name
		if branch == "" {
			// A project's row is not about a branch, but the directory
			// the command is handed is on one: saying which is more use
			// than leaving it empty for the script to work out.
			for _, wt := range proj.Worktrees {
				if wt.Path == pl.dir {
					branch = wt.Branch
				}
			}
		}
	}
	pane := ""
	if r.kind == kindPane {
		pane = r.paneID
	}
	return []string{
		"CONCH_MACHINE=" + pl.machine,
		"CONCH_PROJECT=" + project,
		"CONCH_BRANCH=" + branch,
		"CONCH_WORKTREE=" + pl.dir,
		"CONCH_PANE=" + pane,
	}
}
