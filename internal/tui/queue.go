package tui

import (
	"fmt"
	"sort"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
)

// The review queue answers "what needs me now?" across every machine. The
// tree maps the fleet; this orders it. Everything here is read from what
// the TUI already holds — panes, projects, branches, cost — so the view
// costs no server call and is never stale in its own right.

// Why a row is in the queue, most urgent first.
const (
	bandWaiting   = iota // an agent is blocked on an answer
	bandFailed           // the project's check failed on it
	bandDone             // an agent finished and nobody has looked
	bandUnshipped        // commits that are neither pushed nor merged
	bandDirty            // uncommitted work with no agent running
)

// queueItem is one thing waiting for a decision.
type queueItem struct {
	band                int
	machine, machineLbl string
	projectID, project  string
	branch              string
	paneID              string // set when an agent is involved
	agent               string
	detail              string    // why it is here, in words
	since               time.Time // when it started needing you
	cost                usage
	check               verifyState // what the project's check came to
	checkText           string
}

// key identifies a row across rebuilds, so the selection survives one.
func (it queueItem) key() string {
	return it.machine + "|" + it.projectID + "|" + it.branch + "|" + it.paneID
}

// state is what the row says right now. Dismissing remembers it, so the row
// comes back when it changes — another file edited, an agent finishing
// again — rather than being hidden for good.
func (it queueItem) state() string {
	return fmt.Sprintf("%d|%s|%d", it.band, it.detail, it.since.Unix())
}

// queueItems collects what needs a decision, most urgent first and, within
// a band, whatever has been waiting longest.
func (m Model) queueItems() []queueItem {
	var items []queueItem
	seen := map[string]bool{} // machine|project|branch, so a branch lands once
	for _, mach := range m.machines {
		if mach.state != stateOnline {
			continue // its panes and branches are a stale cache
		}
		// Agents first: they hold the branch's place in the queue.
		for _, p := range mach.panes {
			if p.Agent == nil || !p.Agent.NeedsAttention() {
				continue
			}
			band, detail := bandDone, "finished"
			if p.Agent.State == proto.AgentBlocked {
				band, detail = bandWaiting, "waiting for an answer"
			}
			if p.Agent.Failed {
				detail = "stopped with an error"
			}
			it := queueItem{
				band: band, machine: mach.id, machineLbl: m.machineLabel(mach.id),
				projectID: p.ProjectID, project: m.projectName(mach.id, p.ProjectID),
				branch: p.Branch, paneID: p.ID, agent: p.Agent.Name,
				detail: detail, since: p.Agent.Since, cost: paneUsage(p),
			}
			if it.since.IsZero() {
				it.since = p.Created
			}
			if p.Branch != "" {
				seen[mach.id+"|"+p.ProjectID+"|"+p.Branch] = true
				it.check, it.checkText = m.verifyOf(mach.id, p.ProjectID, p.Branch)
				it.band = failedFirst(it.band, it.check)
			}
			items = append(items, it)
		}
		// Then branches carrying work nobody has shipped or committed.
		for _, proj := range mach.projects {
			worktrees := map[string]*proto.WorktreeInfo{}
			for i := range proj.Worktrees {
				if w := &proj.Worktrees[i]; w.Branch != "" {
					worktrees[w.Branch] = w
				}
			}
			for _, b := range proj.Branches {
				if b.Name == proj.Base || seen[mach.id+"|"+proj.ID+"|"+b.Name] {
					continue
				}
				// Branches the tree has stopped listing are archaeology, not
				// a to-do list: a branch nobody has touched in a fortnight
				// and isn't checked out has stopped being a decision you owe
				// today. An agent's own row is never filtered this way.
				if b.Worktree == "" && !b.Committed.IsZero() && time.Since(b.Committed) > recentBranchAge {
					continue
				}
				it := queueItem{
					machine: mach.id, machineLbl: m.machineLabel(mach.id),
					projectID: proj.ID, project: proj.Name, branch: b.Name, since: b.Committed,
				}
				w := worktrees[b.Name]
				switch {
				// A pushed branch with an open pull request is somebody
				// else's move, not yours.
				case b.PR != nil && b.PR.State == "OPEN" && b.Ahead == 0:
					continue
				case b.BaseAhead > 0 && (b.Upstream == "" || b.Ahead > 0):
					it.band, it.detail = bandUnshipped, count(b.BaseAhead, "commit")+" not pushed"
					if b.Upstream != "" {
						it.detail = count(b.Ahead, "commit") + " ahead of " + b.Upstream
					}
				case b.BaseAhead > 0:
					it.band, it.detail = bandUnshipped, count(b.BaseAhead, "commit")+" not merged into "+proj.Base
				// A branch with no worktree has nothing uncommitted: there
				// is nowhere for the changes to be.
				case w != nil && !w.Status.Clean():
					it.band, it.detail = bandDirty, count(w.Status.Files, "file")+" uncommitted"
				default:
					continue
				}
				it.check, it.checkText = m.verifyOf(mach.id, proj.ID, b.Name)
				it.band = failedFirst(it.band, it.check)
				items = append(items, it)
			}
		}
	}
	if len(m.queueSeen) > 0 {
		kept := items[:0]
		for _, it := range items {
			if was, dismissed := m.queueSeen[it.key()]; !dismissed || was != it.state() {
				kept = append(kept, it)
			}
		}
		items = kept
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].band != items[j].band {
			return items[i].band < items[j].band
		}
		a, b := items[i].since, items[j].since
		if a.IsZero() != b.IsZero() {
			return b.IsZero() // something without a time goes last
		}
		return a.Before(b) // longest wait first
	})
	return items
}

// queueLine is one line of the list: a project heading, or a row under it.
// Headings and rows share an index so scrolling, clicking and the selection
// all work on the same list; the selection only ever rests on a row.
type queueLine struct {
	header string
	item   queueItem
	last   bool // the last row of its group, so groups can breathe
}

// queueLines groups the queue by project. A project appears once, and the
// projects themselves are ordered by their most urgent row, so the thing
// that needs answering first is still at the top.
func (m Model) queueLines() []queueLine {
	items := m.queueItems()
	if len(items) == 0 {
		return nil
	}
	spansMachines := false
	for _, it := range items {
		spansMachines = spansMachines || it.machine != items[0].machine
	}
	var order []string
	groups := map[string][]queueItem{}
	for _, it := range items {
		k := it.machine + "|" + it.projectID
		if _, ok := groups[k]; !ok {
			order = append(order, k) // items are sorted, so the first is the most urgent
		}
		groups[k] = append(groups[k], it)
	}
	var lines []queueLine
	for _, k := range order {
		in := groups[k]
		name := in[0].project
		if name == "" {
			name = "(no project)"
		}
		if spansMachines {
			name = in[0].machineLbl + " · " + name
		}
		lines = append(lines, queueLine{header: name})
		for i, it := range in {
			lines = append(lines, queueLine{item: it, last: i == len(in)-1})
		}
	}
	return lines
}

// onRow keeps the selection on a row: it starts at the top of the list,
// which is a heading, and a dismissal can leave it on one.
func (qv *queueView) onRow(list []queueLine) {
	if len(list) == 0 {
		return
	}
	qv.sel = clamp(qv.sel, 0, len(list)-1)
	if _, ok := itemAt(list, qv.sel); ok {
		return
	}
	if i := nextRow(list, qv.sel, 1); i >= 0 {
		qv.sel = i
	} else if i := nextRow(list, qv.sel, -1); i >= 0 {
		qv.sel = i
	}
}

// itemAt is the row at i, if i is a row rather than a heading.
func itemAt(lines []queueLine, i int) (queueItem, bool) {
	if i >= 0 && i < len(lines) && lines[i].header == "" {
		return lines[i].item, true
	}
	return queueItem{}, false
}

// nextRow is the next line at or after i in direction d that is a row.
func nextRow(lines []queueLine, i, d int) int {
	for ; i >= 0 && i < len(lines); i += d {
		if lines[i].header == "" {
			return i
		}
	}
	return -1
}

// failedFirst promotes a row whose check failed, whatever put it in the
// queue: a command that comes back non-zero is the clearest call for
// attention there is. An agent waiting on an answer still outranks it.
func failedFirst(band int, check verifyState) int {
	if check == verifyFailed && band > bandFailed {
		return bandFailed
	}
	return band
}

// count is "1 commit" / "3 commits", reusing the package's plural suffix.
func count(n int, what string) string {
	return fmt.Sprintf("%d %s%s", n, what, plural(n))
}

// queueView is the list itself; like the sessions view it keeps only the
// selection, since the rows are derived on every render.
type queueView struct {
	sel, scroll int
	selKey      string // keeps the selection on the same row across rebuilds
}

const queueListTop = 3 // header, count, blank

func (qv *queueView) render(m Model, w, h int) []string {
	lines := []string{
		spread(styleBold.Render("Review queue"), styleMuted.Render("enter open · v check · o output · x dismiss · esc tree"), w),
	}
	list := m.queueLines()
	if len(list) == 0 {
		return append(lines, "", styleMuted.Render(fit("  Nothing is waiting for you.", w)))
	}
	rows, waiting := 0, 0
	for _, l := range list {
		if l.header != "" {
			continue
		}
		rows++
		if l.item.band == bandWaiting {
			waiting++
		}
	}
	summary := count(rows, "thing") + " to look at"
	if waiting > 0 {
		summary += fmt.Sprintf(" · %d waiting on you", waiting)
	}
	lines = append(lines, styleMuted.Render(fit("  "+summary, w)), "")

	// Keep the selection on the row it was on, and never on a heading.
	if qv.selKey != "" {
		for i, l := range list {
			if l.header == "" && l.item.key() == qv.selKey {
				qv.sel = i
				break
			}
		}
	}
	qv.onRow(list)
	listH := max(h-queueListTop, 1)
	if qv.sel < qv.scroll {
		qv.scroll = qv.sel
	}
	if qv.sel >= qv.scroll+listH {
		qv.scroll = qv.sel - listH + 1
	}
	qv.scroll = clamp(qv.scroll, 0, max(len(list)-listH, 0))
	// Keep a group's heading on screen with the first of its rows, or the
	// rows below it belong to nothing you can see.
	if qv.scroll > 0 && qv.sel == qv.scroll && list[qv.scroll-1].header != "" {
		qv.scroll--
	}
	if it, ok := itemAt(list, qv.sel); ok {
		qv.selKey = it.key()
	}

	for i := qv.scroll; i < min(qv.scroll+listH, len(list)); i++ {
		l := list[i]
		if l.header != "" {
			lines = append(lines, styleBold.Render(fit(" "+ansi.Truncate(l.header, max(w-2, 4), "…"), w)))
			continue
		}
		it := l.item
		glyph, style := queueGlyph(it.band)
		name := it.branch
		if name == "" {
			name = "(no branch)"
		}
		right := ""
		if c := m.costChip(it.cost); c != "" {
			right = c + "  "
		}
		if !it.since.IsZero() {
			right += styleMuted.Render(ago(it.since))
		}
		detail := it.detail
		if it.agent != "" {
			detail = agentLabel(it.agent) + " " + detail
		}
		if it.checkText != "" {
			detail += " · " + it.checkText
		}
		// The project is in the heading now, so the row is the branch and
		// what it needs; the branch keeps the larger share when space runs out.
		room := max(w-ansi.StringWidth(right)-8, 10)
		nameRoom := max(room*3/5, 10)
		name = ansi.Truncate(name, nameRoom, "…")
		detail = ansi.Truncate(detail, max(room-nameRoom, 6), "…")
		if i == qv.sel {
			// Built from its parts: slicing the drawn line cut multi-byte
			// glyphs in half, which wrapped the row and doubled the one above.
			sel := styleSel
			if m.focus != focusMain {
				sel = styleSelDim
			}
			lines = append(lines, sel.Render(fit(spread("   ▸ "+name+"  "+detail, ansi.Strip(right), w), w)))
			continue
		}
		left := fmt.Sprintf("   %s %s", style.Render(glyph), name)
		left += styleMuted.Render("  " + detail)
		lines = append(lines, spread(left, right, w))
	}
	return lines
}

func queueGlyph(band int) (string, lipgloss.Style) {
	switch band {
	case bandWaiting:
		return "!", styleWarn
	case bandFailed:
		return "✗", styleErr
	case bandDone:
		return "✓", styleOK
	case bandUnshipped:
		return "↑", styleMuted
	default:
		return "·", styleMuted
	}
}

func (qv *queueView) key(m *Model, k tea.KeyMsg) (back bool, cmd tea.Cmd) {
	list := m.queueLines()
	qv.onRow(list) // keys can arrive before the first render
	move := func(d, n int) {
		for ; n > 0; n-- {
			i := nextRow(list, qv.sel+d, d)
			if i < 0 {
				return // already at the first or last row
			}
			qv.sel = i
		}
	}
	switch k.String() {
	case "esc", "q", "left", "h", "tab":
		return true, nil
	case "up", "k":
		move(-1, 1)
	case "down", "j":
		move(1, 1)
	case "pgup":
		move(-1, 10)
	case "pgdown":
		move(1, 10)
	case "home", "g":
		if i := nextRow(list, 0, 1); i >= 0 {
			qv.sel = i
		}
	case "end", "G":
		if i := nextRow(list, len(list)-1, -1); i >= 0 {
			qv.sel = i
		}
	case "enter", "right", "l":
		if it, ok := itemAt(list, qv.sel); ok {
			return false, qv.open(m, it)
		}
	case "o":
		// The check's own terminal, where its output is.
		if it, ok := itemAt(list, qv.sel); ok {
			if pane := m.verifyPane(it.machine, it.projectID, it.branch); pane != "" {
				return false, m.openPaneRow(it.machine, pane)
			}
			switch state, _ := m.verifyOf(it.machine, it.projectID, it.branch); state {
			case verifyNone:
				m.setFlash("nothing has checked "+queueName(it)+" yet — v runs its project's check", true)
			default:
				m.setFlash("that check's terminal has been closed — v runs it again", true)
			}
		}
	case "v":
		if it, ok := itemAt(list, qv.sel); ok {
			if m.verifyCommand(it.projectID) == "" {
				return false, m.askVerifyCommand(it.machine, it.projectID, it.branch)
			}
			if cmd := m.startVerify(it.machine, it.projectID, it.branch); cmd != nil {
				m.setFlash("checking "+queueName(it)+"…", false)
				return false, cmd
			}
			m.setFlash("nothing to check "+queueName(it)+" in: it has no worktree", true)
		}
	case "x":
		if it, ok := itemAt(list, qv.sel); ok {
			if m.queueSeen == nil {
				m.queueSeen = map[string]string{}
			}
			m.queueSeen[it.key()] = it.state()
			m.setFlash("dismissed "+queueName(it)+"; it comes back if it changes", false)
			qv.selKey = "" // the row is gone; keep the position, not the row
		}
	}
	if it, ok := itemAt(list, qv.sel); ok {
		qv.selKey = it.key()
	}
	return false, nil
}

// open goes where the row's answer is: an agent waiting needs its pane, and
// anything else needs the diff.
func (qv *queueView) open(m *Model, it queueItem) tea.Cmd {
	if it.band == bandWaiting && it.paneID != "" {
		return m.openPaneRow(it.machine, it.paneID)
	}
	if it.branch != "" {
		return m.openBranch(it.machine, it.projectID, it.branch)
	}
	if it.paneID != "" {
		return m.openPaneRow(it.machine, it.paneID)
	}
	return nil
}

func (qv *queueView) mouse(m *Model, msg tea.MouseMsg, x, y int) tea.Cmd {
	list := m.queueLines()
	qv.onRow(list)
	switch {
	case msg.Button == tea.MouseButtonWheelUp:
		if i := nextRow(list, qv.sel-1, -1); i >= 0 {
			qv.sel = i
		}
	case msg.Button == tea.MouseButtonWheelDown:
		if i := nextRow(list, qv.sel+1, 1); i >= 0 {
			qv.sel = i
		}
	case msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft:
		i := qv.scroll + y - queueListTop
		if y < queueListTop || i < 0 || i >= len(list) {
			return nil
		}
		it, ok := itemAt(list, i)
		if !ok {
			return nil // a heading is not a row to open
		}
		if i == qv.sel {
			return qv.open(m, it)
		}
		qv.sel = i
	default:
		return nil
	}
	if it, ok := itemAt(list, qv.sel); ok {
		qv.selKey = it.key()
	}
	return nil
}

// queueName is a row in a sentence: the branch, or the project when a row
// has no branch of its own.
func queueName(it queueItem) string {
	if it.branch != "" {
		return it.branch
	}
	return it.project
}

// queueRow is the synthetic row the queue is shown through; it has no place
// in the tree, so it borrows the focused split.
func queueRow() row { return row{id: "queue", kind: kindReviewQueue} }
