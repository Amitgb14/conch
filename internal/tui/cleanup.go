package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
)

// The cleanup list: a project's leftover worktrees, the finished ones ticked,
// removed together with their branches.

const (
	cleanupCapability = "worktree.cleanup.v1"
	cleanupListRows   = 12
)

// cleanupListMsg carries a project's worktrees for the cleanup list.
type cleanupListMsg struct {
	machine, projectID string
	worktrees          []proto.StaleWorktree
}

// cleanupDoneMsg reports what a cleanup removed.
type cleanupDoneMsg struct {
	machine, projectID string
	result             proto.WorktreeCleanupResult
}

// openCleanup reads the selected project's worktrees for the cleanup list.
func (m *Model) openCleanup() tea.Cmd {
	pl := m.contextPlace()
	proj := m.project(pl.machine, pl.projectID)
	switch {
	case proj == nil || !proj.Git:
		m.setFlash("select a git project to clean up its worktrees", true)
		return nil
	case m.clientOf(pl.machine) == nil:
		m.setFlash(m.offlineText(pl.machine), true)
		return nil
	case !m.hasCapability(pl.machine, cleanupCapability):
		m.setFlash("the server there predates cleaning up worktrees; reload it", true)
		return nil
	}
	m.setFlash("reading the worktrees of "+proj.Name+"…", false)
	c, mid, pid := m.clientOf(pl.machine), pl.machine, proj.ID
	return func() tea.Msg {
		var out proto.WorktreeStale
		if err := callCtx(c, proto.MethodWorktreeStale, proto.ProjectRef{ID: pid}, &out); err != nil {
			return errMsg{err}
		}
		return cleanupListMsg{machine: mid, projectID: pid, worktrees: out.Worktrees}
	}
}

func (m *Model) receiveCleanupList(msg cleanupListMsg) {
	name := m.projectName(msg.machine, msg.projectID)
	if len(msg.worktrees) == 0 {
		m.setFlash(name+" has no worktrees besides its main checkout", false)
		return
	}
	m.flash = ""
	m.overlay = newCleanupDialog(msg)
}

type cleanupDialog struct {
	machine, projectID string
	items              []proto.StaleWorktree
	on                 []bool
	sel, scroll        int
	err                string
}

func newCleanupDialog(msg cleanupListMsg) *cleanupDialog {
	d := &cleanupDialog{machine: msg.machine, projectID: msg.projectID, items: msg.worktrees, on: make([]bool, len(msg.worktrees))}
	for i, w := range d.items {
		d.on[i] = w.Suggested
	}
	return d
}

// worktreeBlocked says why a worktree can't be removed at all, or "".
func worktreeBlocked(w proto.StaleWorktree) string {
	switch {
	case w.Panes:
		return "panes running"
	case w.Locked:
		return "locked"
	}
	return ""
}

// worktreeLoss says what removing a worktree loses, or "".
func worktreeLoss(w proto.StaleWorktree) string {
	var parts []string
	if w.Uncommitted > 0 {
		parts = append(parts, counted(w.Uncommitted, "uncommitted file"))
	}
	if w.Unmerged > 0 {
		parts = append(parts, counted(w.Unmerged, "commit")+" not merged or pushed")
	}
	return strings.Join(parts, " and ")
}

// worktreeName names a worktree by its branch, or its folder when detached.
func worktreeName(w proto.StaleWorktree) string {
	if w.Branch != "" {
		return w.Branch
	}
	return filepath.Base(w.Path) + " (detached)"
}

func (d *cleanupDialog) chosen() (items []proto.StaleWorktree) {
	for i, w := range d.items {
		if d.on[i] {
			items = append(items, w)
		}
	}
	return items
}

func (d *cleanupDialog) toggle(i int) {
	if i < 0 || i >= len(d.items) {
		return
	}
	if why := worktreeBlocked(d.items[i]); why != "" && !d.on[i] {
		d.err = worktreeName(d.items[i]) + " can't be removed: " + why
		return
	}
	d.on[i] = !d.on[i]
	d.err = ""
}

func (d *cleanupDialog) update(m *Model, msg tea.Msg) (bool, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return false, nil
	}
	switch k.String() {
	case "esc", "q":
		m.overlay = nil
		return true, nil
	case "up", "k":
		d.sel = max(d.sel-1, 0)
	case "down", "j":
		d.sel = min(d.sel+1, len(d.items)-1)
	case " ", "x":
		d.toggle(d.sel)
	case "a":
		// Tick the suggested ones, or untick everything when they already are.
		all := true
		for i, w := range d.items {
			all = all && (!w.Suggested || d.on[i])
		}
		for i, w := range d.items {
			d.on[i] = !all && w.Suggested
		}
		d.err = ""
	case "enter":
		return false, d.review(m)
	}
	if d.sel < d.scroll {
		d.scroll = d.sel
	}
	if d.sel >= d.scroll+cleanupListRows {
		d.scroll = d.sel - cleanupListRows + 1
	}
	return false, nil
}

// review confirms the removal, naming what it loses. Only worktrees whose
// loss was shown are forced, so work made since the list was read still
// stops the server.
func (d *cleanupDialog) review(m *Model) tea.Cmd {
	chosen := d.chosen()
	if len(chosen) == 0 {
		d.err = "tick at least one (space)"
		return nil
	}
	var names, losses []string
	var remove []proto.CleanupWorktree
	branches := 0
	for _, w := range chosen {
		names = append(names, worktreeName(w))
		l := worktreeLoss(w)
		if l != "" {
			losses = append(losses, worktreeName(w)+" ("+l+")")
		}
		if w.Branch != "" && !w.Base {
			branches++
		}
		remove = append(remove, proto.CleanupWorktree{Path: w.Path, Force: l != ""})
	}
	q := "Remove " + counted(len(chosen), "worktree")
	if branches > 0 {
		q += " and delete " + map[bool]string{true: "its branch", false: "their branches"}[len(chosen) == 1]
	}
	q += ": " + listSome(names, 8) + "?"
	c := newConfirm(q, func(m *Model) tea.Cmd { return m.runCleanup(d.machine, d.projectID, remove) })
	c.title = " Clean up worktrees "
	if len(losses) > 0 {
		c.text = append(c.text, "This loses "+strings.Join(losses, ", ")+", for good.")
		c.yesOnly = true
	}
	m.overlay = &cleanupConfirm{dialog: c, back: d}
	return nil
}

func (m *Model) runCleanup(mid, pid string, remove []proto.CleanupWorktree) tea.Cmd {
	c := m.clientOf(mid)
	if c == nil {
		return func() tea.Msg { return errMsg{errString(m.offlineText(mid))} }
	}
	m.setFlash("removing "+counted(len(remove), "worktree")+"…", false)
	return func() tea.Msg {
		var res proto.WorktreeCleanupResult
		params := proto.WorktreeCleanupParams{ProjectID: pid, Remove: remove}
		if err := callCtxFor(c, proto.MethodWorktreeCleanup, params, &res, harvestTimeout); err != nil {
			return errMsg{err}
		}
		return cleanupDoneMsg{machine: mid, projectID: pid, result: res}
	}
}

func (m *Model) receiveCleanup(msg cleanupDoneMsg) {
	res := msg.result
	text := "removed " + counted(len(res.Removed), "worktree")
	if len(res.Failed) == 0 {
		m.setFlash(text, false)
		return
	}
	var why []string
	for _, f := range res.Failed {
		why = append(why, filepath.Base(f.Path)+": "+f.Error)
	}
	m.setFlash(text+"; kept "+strings.Join(why, "; "), true)
}

// cleanupConfirm is the yes/no step; declining goes back to the list.
type cleanupConfirm struct {
	*dialog
	back *cleanupDialog
}

func (c *cleanupConfirm) update(m *Model, msg tea.Msg) (bool, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "n", "N", "esc", "q":
			m.overlay = c.back
			return false, nil
		}
	}
	return c.dialog.update(m, msg)
}

func (c *cleanupConfirm) mouse(m *Model, msg tea.MouseMsg, b box) tea.Cmd {
	if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft && !b.contains(msg.X, msg.Y) {
		m.overlay = c.back
	}
	return nil
}

// worktreeNote is a worktree's right-hand detail: what blocks it, what it loses, or
// why it looks finished.
func worktreeNote(w proto.StaleWorktree) string {
	if why := worktreeBlocked(w); why != "" {
		return styleErr.Render(why)
	}
	if l := worktreeLoss(w); l != "" {
		return styleWarn.Render(l)
	}
	if len(w.Reasons) > 0 {
		return styleOK.Render(strings.Join(w.Reasons, " · "))
	}
	return styleMuted.Render("in use")
}

func (d *cleanupDialog) render(m Model) box {
	w := m.dialogWidth()
	name := m.projectName(d.machine, d.projectID)
	lines := []string{"", " " + styleBold.Render(ansi.Truncate(fmt.Sprintf("%d of %d worktrees of %s ticked", len(d.chosen()), len(d.items), name), w-2, "…"))}
	end := min(d.scroll+cleanupListRows, len(d.items))
	if d.scroll > 0 {
		lines = append(lines, styleMuted.Render(fmt.Sprintf("   … %d above", d.scroll)))
	}
	for i := d.scroll; i < end; i++ {
		it := d.items[i]
		box := "[ ]"
		if d.on[i] {
			box = "[x]"
		}
		row := fmt.Sprintf("   %s %s", box, worktreeName(it))
		detail := worktreeNote(it)
		line := spread(row, detail+" ", w)
		if i == d.sel {
			line = styleSel.Render(spread(ansi.Strip(row), ansi.Strip(detail)+" ", w))
		}
		lines = append(lines, line)
	}
	if end < len(d.items) {
		lines = append(lines, styleMuted.Render(fmt.Sprintf("   … %d more", len(d.items)-end)))
	}
	if d.sel < len(d.items) {
		it := d.items[d.sel]
		about := m.tildify(d.machine, it.Path)
		if !it.Committed.IsZero() {
			about += " · last commit " + ago(it.Committed)
		}
		if it.Base {
			about += " · the base branch is kept"
		}
		lines = append(lines, "", " "+styleMuted.Render(ansi.Truncate(about, w-2, "…")))
	}
	lines = append(lines, "")
	if d.err != "" {
		lines = append(lines, " "+styleErr.Render(ansi.Truncate(d.err, w-2, "…")))
	}
	for _, l := range wrap("space tick · a the finished ones · enter review · esc cancel", w-2) {
		lines = append(lines, " "+styleMuted.Render(l))
	}
	b := box{lines: frameLines(" Clean up worktrees ", lines, w, colorAccent)}
	b.x = max((m.width-b.width())/2, 0)
	b.y = max((m.height-len(b.lines))/4, 0)
	return b
}

func (d *cleanupDialog) mouse(m *Model, msg tea.MouseMsg, b box) tea.Cmd {
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return nil
	}
	if !b.contains(msg.X, msg.Y) {
		m.overlay = nil
		return nil
	}
	// Lines: border, blank, heading, [above], list rows.
	first := b.y + 3
	if d.scroll > 0 {
		first++
	}
	if i := d.scroll + msg.Y - first; msg.Y >= first && i < min(d.scroll+cleanupListRows, len(d.items)) {
		d.sel = i
		d.toggle(i)
	}
	return nil
}
