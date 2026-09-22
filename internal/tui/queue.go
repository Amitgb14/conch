package tui

import (
	"fmt"
	"sort"
	"strings"
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
	items := m.queueItems()
	lines := []string{
		spread(styleBold.Render("Review queue"), styleMuted.Render("enter open · esc tree"), w),
	}
	if len(items) == 0 {
		return append(lines, "", styleMuted.Render(fit("  Nothing is waiting for you.", w)))
	}
	waiting, spansMachines := 0, false
	for _, it := range items {
		if it.band == bandWaiting {
			waiting++
		}
		// Name the machine on each row only when there is something to
		// tell apart; one machine's queue reads better without it.
		spansMachines = spansMachines || it.machine != items[0].machine
	}
	summary := count(len(items), "thing") + " to look at"
	if waiting > 0 {
		summary += fmt.Sprintf(" · %d waiting on you", waiting)
	}
	lines = append(lines, styleMuted.Render(fit("  "+summary, w)), "")

	// Follow the row the selection was on, if it is still here.
	if qv.selKey != "" {
		for i, it := range items {
			if it.key() == qv.selKey {
				qv.sel = i
				break
			}
		}
	}
	listH := max(h-queueListTop, 1)
	qv.sel = clamp(qv.sel, 0, len(items)-1)
	if qv.sel < qv.scroll {
		qv.scroll = qv.sel
	}
	if qv.sel >= qv.scroll+listH {
		qv.scroll = qv.sel - listH + 1
	}
	qv.scroll = clamp(qv.scroll, 0, max(len(items)-listH, 0))
	qv.selKey = items[qv.sel].key()

	for i := qv.scroll; i < min(qv.scroll+listH, len(items)); i++ {
		it := items[i]
		glyph, style := queueGlyph(it.band)
		name := it.branch
		if name == "" {
			name = "(no branch)"
		}
		// The branch names the row; the project and machine are context.
		// When the split is narrow, drop the context from the left rather
		// than truncate everything into uselessness.
		parts := []string{it.project, name}
		if spansMachines {
			parts = append([]string{it.machineLbl}, parts...)
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
		room := max(w-ansi.StringWidth(right)-8, 10)
		whereRoom := max(room*3/5, 10)
		where := strings.Join(parts, " · ")
		for len(parts) > 1 && ansi.StringWidth(where) > whereRoom {
			parts = parts[1:]
			where = strings.Join(parts, " · ")
		}
		where = ansi.Truncate(where, whereRoom, "…")
		detail = ansi.Truncate(detail, max(room-whereRoom, 6), "…")
		if i == qv.sel {
			// The cursor replaces the band's glyph. Built from its parts,
			// never by slicing the drawn line: the glyphs are multi-byte,
			// and cutting one in half makes a line the wrong width, which
			// wraps and leaves the row above drawn twice.
			sel := styleSel
			if m.focus != focusMain {
				sel = styleSelDim
			}
			lines = append(lines, sel.Render(fit(spread(" ▸ "+where+"  "+detail, ansi.Strip(right), w), w)))
			continue
		}
		left := fmt.Sprintf(" %s %s", style.Render(glyph), where)
		left += styleMuted.Render("  " + detail)
		lines = append(lines, spread(left, right, w))
	}
	return lines
}

func queueGlyph(band int) (string, lipgloss.Style) {
	switch band {
	case bandWaiting:
		return "!", styleWarn
	case bandDone:
		return "✓", styleOK
	case bandUnshipped:
		return "↑", styleMuted
	default:
		return "·", styleMuted
	}
}

func (qv *queueView) key(m *Model, k tea.KeyMsg) (back bool, cmd tea.Cmd) {
	items := m.queueItems()
	switch k.String() {
	case "esc", "q", "left", "h", "tab":
		return true, nil
	case "up", "k":
		qv.sel--
	case "down", "j":
		qv.sel++
	case "pgup":
		qv.sel -= 10
	case "pgdown":
		qv.sel += 10
	case "home", "g":
		qv.sel = 0
	case "end", "G":
		qv.sel = len(items) - 1
	case "enter", "right", "l":
		if qv.sel >= 0 && qv.sel < len(items) {
			return false, qv.open(m, items[qv.sel])
		}
	case "x":
		if qv.sel >= 0 && qv.sel < len(items) {
			it := items[qv.sel]
			if m.queueSeen == nil {
				m.queueSeen = map[string]string{}
			}
			m.queueSeen[it.key()] = it.state()
			m.setFlash("dismissed "+queueName(it)+"; it comes back if it changes", false)
			qv.selKey = "" // the row is gone; keep the position, not the row
		}
	}
	qv.sel = clamp(qv.sel, 0, max(len(items)-1, 0))
	if qv.sel < len(items) {
		qv.selKey = items[qv.sel].key()
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
	items := m.queueItems()
	switch {
	case msg.Button == tea.MouseButtonWheelUp:
		qv.sel--
	case msg.Button == tea.MouseButtonWheelDown:
		qv.sel++
	case msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft:
		if i := qv.scroll + y - queueListTop; y >= queueListTop && i >= 0 && i < len(items) {
			if i == qv.sel {
				return qv.open(m, items[i])
			}
			qv.sel = i
		}
	default:
		return nil
	}
	qv.sel = clamp(qv.sel, 0, max(len(items)-1, 0))
	if qv.sel < len(items) {
		qv.selKey = items[qv.sel].key()
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

func isQueueRow(v viewRef) bool { return v.Kind == kindReviewQueue }
