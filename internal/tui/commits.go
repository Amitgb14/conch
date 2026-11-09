package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/gitx"
	"github.com/Amitgb14/conch/internal/proto"
)

// The commit graph: a project's history in a window over the screen, every
// branch and tag newest first, drawn as git log --graph draws it — a dot
// per commit on its branch's lane, lanes joining where branches merge — with
// each commit's short hash, its refs, subject, author and age. Opened from a
// project's or a branch's row menu (a branch's opens at its newest commit).
// The server reads the history (project.log); the layout is drawn here, as
// Conch.app draws the same layout (ConchKit/CommitGraph.swift).

// graphRow is one commit's row of the graph: its lane, and the lines
// through the row. The top half of a row runs from its top edge to the
// commit, the bottom half from there down.
type graphRow struct {
	c    proto.LogCommit
	lane int
	// through are lanes passing straight down, not touching the commit;
	// into are lanes from the top ending at it (its children); outOf are
	// lanes leaving it for the bottom (its parents).
	through, into, outOf []int
	width                int
	// colors is each lane's colour in the row, by lane: a branch keeps its
	// colour down the graph.
	colors map[int]int
}

func (r graphRow) merge() bool { return len(r.c.Parents) > 1 }

// commitGraph lays out a history (children before parents) as git log
// --graph does: each lane waits for the next commit it leads to; a commit
// takes the lane waiting for it (or a new one), other lanes waiting for it
// join it there, and its parents go on in its lane (the first) and in lanes
// of their own (a merge's others). A free lane is reused, so the graph stays
// as narrow as the history allows. A parent not in the list (a log cut
// short) runs off the bottom.
func commitGraph(commits []proto.LogCommit) []graphRow {
	var lanes []string // the commit each lane waits for; "" is free
	var laneColor []int
	next := 0
	free := func() int {
		for i, l := range lanes {
			if l == "" {
				return i
			}
		}
		lanes = append(lanes, "")
		laneColor = append(laneColor, 0)
		return len(lanes) - 1
	}
	rows := make([]graphRow, 0, len(commits))
	for _, c := range commits {
		before := append([]string(nil), lanes...)
		var waiting []int
		for i, l := range lanes {
			if l == c.Hash {
				waiting = append(waiting, i)
			}
		}
		lane := -1
		if len(waiting) > 0 {
			lane = waiting[0]
		} else {
			lane = free()
			laneColor[lane] = next
			next++
		}
		for _, j := range waiting {
			if j != lane {
				lanes[j] = ""
			}
		}
		var through []int
		for i, l := range before {
			if l != "" && l != c.Hash {
				through = append(through, i)
			}
		}
		var outOf []int
		lanes[lane] = ""
		if len(c.Parents) > 0 {
			lanes[lane] = c.Parents[0]
			outOf = append(outOf, lane)
		}
		for _, p := range c.Parents[min(1, len(c.Parents)):] {
			k := -1
			for i, l := range lanes {
				if l == p {
					k = i
					break
				}
			}
			if k < 0 {
				k = free()
				lanes[k] = p
				laneColor[k] = next
				next++
			}
			outOf = append(outOf, k)
		}
		colors := map[int]int{}
		for _, set := range [][]int{through, waiting, outOf, {lane}} {
			for _, i := range set {
				colors[i] = laneColor[i]
			}
		}
		width := max(len(before), len(lanes), lane+1)
		rows = append(rows, graphRow{c: c, lane: lane, through: through, into: waiting, outOf: outOf, width: width, colors: colors})
		for len(lanes) > 0 && lanes[len(lanes)-1] == "" {
			lanes = lanes[:len(lanes)-1]
			laneColor = laneColor[:len(laneColor)-1]
		}
	}
	return rows
}

// laneStyle is the colour a lane takes, in turn from the theme's accent
// and its other signal colours, so neighbouring branches differ.
func laneStyle(i int) lipgloss.Style {
	cols := laneColors()
	return lipgloss.NewStyle().Foreground(cols[((i%len(cols))+len(cols))%len(cols)])
}

// laneColors are the colours lanes take in turn: the accent, working, ok,
// warning, merged and error, each once. A theme may give two of them the
// same colour — GitHub Light's accent is its working blue, Monokai's and
// Everforest's their ok green — and taking both in turn drew two lanes
// side by side in one colour.
func laneColors() []lipgloss.TerminalColor {
	var out []lipgloss.TerminalColor
	seen := map[string]bool{}
	for _, c := range []lipgloss.TerminalColor{colorAccent, styleWork.GetForeground(), styleOK.GetForeground(),
		colorWarn, stylePRMerged.GetForeground(), styleErr.GetForeground()} {
		k := strings.ToUpper(fmt.Sprint(c))
		if !seen[k] {
			seen[k] = true
			out = append(out, c)
		}
	}
	return out
}

// graphCells draws a row's share of the graph in lanes*2 cells: a lane's
// line or the commit in its first cell, the space between lanes in its
// second, and a horizontal run where lanes join the commit or leave it.
func graphCells(r graphRow, lanes int) string {
	n := lanes * 2
	if n <= 0 {
		return ""
	}
	cells := make([]string, n)
	color := make([]int, n)
	for i := range cells {
		cells[i], color[i] = " ", -1
	}
	set := func(c int, s string, lane int) {
		if c >= 0 && c < n {
			cells[c], color[c] = s, r.colors[lane]
		}
	}
	has := func(set []int, i int) bool {
		for _, x := range set {
			if x == i {
				return true
			}
		}
		return false
	}
	for _, i := range r.through {
		set(2*i, "│", i)
	}
	// The lanes joined to the commit by a horizontal run.
	var ends []int
	for _, i := range r.into {
		if i != r.lane {
			ends = append(ends, i)
		}
	}
	for _, k := range r.outOf {
		if k != r.lane && !has(ends, k) {
			ends = append(ends, k)
		}
	}
	lo, hi := r.lane, r.lane
	for _, i := range ends {
		lo, hi = min(lo, i), max(hi, i)
	}
	// The run between the commit and an end takes that end's colour: the
	// nearest end beyond each cell, on its side of the commit.
	nearest := func(c int) int {
		best := -1
		for _, e := range ends {
			if (c > 2*r.lane && 2*e >= c || c < 2*r.lane && 2*e <= c) && (best < 0 || abs(2*e-c) < abs(2*best-c)) {
				best = e
			}
		}
		return best
	}
	// A graph clipped narrower than the row stops at its edge.
	for c := 2 * lo; c <= 2*hi && c < n; c++ {
		if c == 2*r.lane {
			continue
		}
		e := nearest(c)
		if e < 0 {
			continue
		}
		if cells[c] == "│" {
			cells[c] = "┼"
			continue
		}
		set(c, "─", e)
	}
	for _, i := range ends {
		top, bot, thr := has(r.into, i), has(r.outOf, i), has(r.through, i)
		right := i > r.lane
		// A lane the run reaches on its way to a farther one is a
		// junction, not the run's end.
		onward := right && i < hi || !right && i > lo
		var g string
		switch {
		case onward && ((thr || top) && bot):
			g = "┼"
		case onward && top:
			g = "┴"
		case onward:
			g = "┬"
		case (thr || top) && bot:
			g = map[bool]string{true: "┤", false: "├"}[right]
		case top:
			g = map[bool]string{true: "╯", false: "╰"}[right]
		default:
			g = map[bool]string{true: "╮", false: "╭"}[right]
		}
		set(2*i, g, i)
	}
	dot := "●"
	if headOf(r.c.Refs) != "" {
		dot = "◉"
	}
	set(2*r.lane, dot, r.lane)

	var b strings.Builder
	for i := 0; i < n; {
		j := i
		var run strings.Builder
		for j < n && color[j] == color[i] {
			run.WriteString(cells[j])
			j++
		}
		if color[i] < 0 {
			b.WriteString(run.String())
		} else {
			b.WriteString(laneStyle(color[i]).Render(run.String()))
		}
		i = j
	}
	return b.String()
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// headOf is the branch HEAD names among a commit's refs ("HEAD -> main"),
// "HEAD" when it is detached there, or "".
func headOf(refs []string) string {
	for _, r := range refs {
		if b, ok := strings.CutPrefix(r, "HEAD -> "); ok {
			return b
		}
		if r == "HEAD" {
			return r
		}
	}
	return ""
}

// localBranches are the branches among a commit's refs that are this
// repository's own, HEAD's first: what a commit can open the changes of.
// remotes are the repository's remotes (nil: origin and upstream).
func localBranches(refs, remotes []string) []string {
	var out []string
	if h := headOf(refs); h != "" && h != "HEAD" {
		out = append(out, h)
	}
	for _, r := range refs {
		if strings.HasPrefix(r, "HEAD") || strings.HasPrefix(r, "tag: ") || r == "grafted" || isRemoteRef(r, remotes) {
			continue
		}
		out = append(out, r)
	}
	return out
}

// isRemoteRef says whether a decoration is a remote's branch: one of the
// repository's remotes, then a slash (fork/x). A local branch may have a
// slash of its own (conch/x). An older server names no remotes: origin
// and upstream are taken then, which a remote of another name was not.
func isRemoteRef(r string, remotes []string) bool {
	remote, _, ok := strings.Cut(r, "/")
	if !ok {
		return false
	}
	if remotes == nil {
		return remote == "origin" || remote == "upstream"
	}
	return slicesContains(remotes, remote)
}

// refsText is a commit's refs as git writes them, each in its colour:
// HEAD's branch in the accent, branches in the working colour, remotes
// muted, tags in the warning colour. origin/HEAD is left out: the row
// already shows the branch it names.
//
// A ref's name is cleaned as it is drawn: git refuses controls below 0x20
// in one but takes the C1 range, and a server builds the list unchanged
// because a name is matched against branches.
func refsText(refs, remotes []string) string {
	var parts []string
	if h := headOf(refs); h != "" {
		label := "HEAD"
		if h != "HEAD" {
			label = "HEAD → " + gitx.Printable(h)
		}
		parts = append(parts, styleAccent.Render(label))
	}
	for _, r := range refs {
		switch {
		case strings.HasPrefix(r, "HEAD"), strings.HasSuffix(r, "/HEAD"):
		case strings.HasPrefix(r, "tag: "):
			parts = append(parts, lipgloss.NewStyle().Foreground(colorWarn).Render(gitx.Printable(r)))
		case r == "grafted": // a shallow clone's boundary, from an older server
		case isRemoteRef(r, remotes):
			parts = append(parts, styleMuted.Render(gitx.Printable(r)))
		default:
			parts = append(parts, styleWork.Render(gitx.Printable(r)))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return styleMuted.Render("(") + strings.Join(parts, styleMuted.Render(", ")) + styleMuted.Render(") ")
}

// The window.

const (
	commitsPage    = proto.LogLimit
	commitsMaxRead = proto.LogLimitMax
	// commitsMaxLanes is the widest the graph column grows: a history
	// wider than this is clipped rather than pushing the text off the row.
	commitsMaxLanes = 16
)

type commitsView struct {
	mid, pid string
	rows     []graphRow
	more     bool
	limit    int
	loading  bool
	err      string
	sel, top int
	// focus is a branch to select once the history is read.
	focus string
	// listH is how many rows the last render showed, for paging.
	listH int
	// remotes are the repository's, as the server named them.
	remotes []string
	// seq numbers the reads: only the newest one's reply is taken, or an
	// r pressed during a read for more could put a shorter history over
	// a longer one.
	seq int
}

type commitsMsg struct {
	v   *commitsView
	seq int
	res proto.LogResult
	err error
}

// openCommits shows a project's commit graph; branch, when given, is
// selected once it is read.
func (m *Model) openCommits(mid, pid, branch string) tea.Cmd {
	proj := m.project(mid, pid)
	switch {
	case proj == nil || !proj.Git:
		m.setFlash("a commit graph belongs to a git project", true)
		return nil
	case !m.hasCapability(mid, proto.CapProjectLog):
		m.setFlash("the server there predates the commit graph; reload it (conch server reload)", true)
		return nil
	}
	v := &commitsView{mid: mid, pid: pid, limit: commitsPage, focus: branch}
	m.overlay = v
	return v.load(m)
}

func (v *commitsView) load(m *Model) tea.Cmd {
	c := m.clientOf(v.mid)
	if c == nil {
		v.err = "not connected"
		return nil
	}
	v.loading = true
	v.seq++
	pid, limit, seq := v.pid, v.limit, v.seq
	return func() tea.Msg {
		var res proto.LogResult
		err := callCtx(c, proto.MethodProjectLog, proto.LogParams{ProjectID: pid, Limit: limit}, &res)
		return commitsMsg{v: v, seq: seq, res: res, err: err}
	}
}

// receive takes a read history in, keeping the commit picked if it is
// still there.
func (v *commitsView) receive(msg commitsMsg) {
	if msg.seq != v.seq {
		return // a newer read is on its way
	}
	v.loading = false
	if msg.err != nil {
		v.err = msg.err.Error()
		return
	}
	v.err = ""
	picked := ""
	if v.sel < len(v.rows) {
		picked = v.rows[v.sel].c.Hash
	}
	v.rows, v.more, v.remotes = commitGraph(msg.res.Commits), msg.res.More, msg.res.Remotes
	v.sel = 0
	for i, r := range v.rows {
		if picked != "" && r.c.Hash == picked {
			v.sel = i
		}
	}
	if v.focus != "" {
		for i, r := range v.rows {
			if slicesContains(localBranches(r.c.Refs, v.remotes), v.focus) {
				v.sel = i
				// In the middle of the window, not at its edge.
				v.top = max(i-v.listH/2, 0)
				break
			}
		}
		v.focus = ""
	}
}

func slicesContains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// move moves the pick by d, and reads older commits when it reaches the
// last of those read and there are more.
func (v *commitsView) move(m *Model, d int) tea.Cmd {
	if len(v.rows) == 0 {
		return nil
	}
	v.sel = clamp(v.sel+d, 0, len(v.rows)-1)
	if v.sel == len(v.rows)-1 && v.more && !v.loading && v.limit < commitsMaxRead {
		v.limit = min(v.limit+commitsPage, commitsMaxRead)
		return v.load(m)
	}
	return nil
}

func (v *commitsView) update(m *Model, msg tea.Msg) (bool, tea.Cmd) {
	switch msg := msg.(type) {
	case commitsMsg:
		if msg.v != v { // a window closed before its history arrived
			return false, nil
		}
		v.receive(msg)
		return false, nil
	case tea.KeyMsg:
		page := max(v.listH-1, 1)
		switch msg.String() {
		case "esc", "q", "ctrl+c":
			m.overlay = nil
			return true, nil
		case "up", "k":
			return false, v.move(m, -1)
		case "down", "j":
			return false, v.move(m, 1)
		case "pgup", "ctrl+u":
			return false, v.move(m, -page)
		case "pgdown", "ctrl+d", " ":
			return false, v.move(m, page)
		case "home", "g":
			return false, v.move(m, -len(v.rows))
		case "end", "G":
			return false, v.move(m, len(v.rows))
		case "r":
			return false, v.load(m)
		case "y":
			if v.sel < len(v.rows) {
				return false, copyText(v.rows[v.sel].c.Hash)
			}
		case "enter":
			if v.sel < len(v.rows) {
				if b := localBranches(v.rows[v.sel].c.Refs, v.remotes); len(b) > 0 {
					m.overlay = nil
					return true, m.openBranch(v.mid, v.pid, b[0])
				}
				m.setFlash("no branch of this repository points at that commit", false)
			}
		}
	}
	return false, nil
}

func (v *commitsView) mouse(m *Model, msg tea.MouseMsg, b box) tea.Cmd {
	switch {
	case msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonWheelUp:
		return v.move(m, -3)
	case msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonWheelDown:
		return v.move(m, 3)
	case msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft:
		if !b.contains(msg.X, msg.Y) {
			m.overlay = nil
			return nil
		}
		// The list starts below the frame's top border.
		// Through move, so picking the last row reads older commits as
		// the keys and the wheel do.
		if i := v.top + msg.Y - b.y - 1; msg.Y > b.y && i >= v.top && i < v.top+v.listH && i < len(v.rows) {
			return v.move(m, i-v.sel)
		}
	}
	return nil
}

// render draws the window nearly as big as the screen: the graph needs
// width, and history is read in bulk.
func (v *commitsView) render(m Model) box {
	outerW := max(min(m.width-2, 180), min(m.width, 12))
	outerH := max(min(m.height-2, 60), min(m.height, 5))
	w := max(outerW-2, 1)
	listH := max(outerH-3, 1) // the frame's two borders, and a footer
	v.listH = listH
	v.sel = clamp(v.sel, 0, max(len(v.rows)-1, 0))
	if v.sel < v.top {
		v.top = v.sel
	}
	if v.sel >= v.top+listH {
		v.top = v.sel - listH + 1
	}
	v.top = clamp(v.top, 0, max(len(v.rows)-listH, 0))

	lanes := 1
	for _, r := range v.rows {
		lanes = max(lanes, r.width)
	}
	lanes = min(lanes, commitsMaxLanes, max(w/4, 1))

	var lines []string
	switch {
	case v.err != "" && len(v.rows) == 0:
		lines = append(lines, " "+styleErr.Render("could not read the history: "+v.err))
	case v.loading && len(v.rows) == 0:
		lines = append(lines, " "+styleMuted.Render("reading the history…"))
	case len(v.rows) == 0:
		lines = append(lines, " "+styleMuted.Render("no commits yet"))
	}
	for i := v.top; i < len(v.rows) && len(lines) < listH; i++ {
		lines = append(lines, v.line(v.rows[i], lanes, w, i == v.sel))
	}
	for len(lines) < listH {
		lines = append(lines, "")
	}
	foot := "↑↓ move · enter its branch's changes · y copy hash · r read again · esc close"
	switch {
	case v.loading && len(v.rows) > 0:
		foot = "reading older commits…  " + foot
	case v.err != "":
		foot = "error: " + v.err + "  " + foot
	case v.more && v.limit >= commitsMaxRead:
		foot = fmt.Sprintf("newest %d (as many as are read)  ", len(v.rows)) + foot
	case v.more:
		foot = fmt.Sprintf("newest %d (more as you scroll)  ", len(v.rows)) + foot
	default:
		foot = fmt.Sprintf("%d commits  ", len(v.rows)) + foot
	}
	lines = append(lines, styleMuted.Render(" "+foot))

	title := " Commits · " + m.projectName(v.mid, v.pid) + " "
	b := box{lines: frameLines(title, lines, w, colorAccent)}
	b.x = max((m.width-b.width())/2, 0)
	b.y = max((m.height-len(b.lines))/2, 0)
	return b
}

// line is one commit's row, w wide: the graph, then its hash, refs and
// subject, with its author and age at the right. The row picked is drawn
// in the selection colours past the graph.
//
// Author and subject are cleaned again here, though a server of this
// build already did: an older one sends them as git keeps them, escapes
// and all, and drawn raw they would be obeyed by the terminal.
func (v *commitsView) line(r graphRow, lanes, w int, picked bool) string {
	graph := " " + graphCells(r, lanes)
	gw := ansi.StringWidth(graph)
	subject := gitx.Printable(r.c.Subject)
	right := ""
	if r.c.Author != "" || !r.c.Time.IsZero() {
		right = " " + gitx.Printable(r.c.Author)
		if !r.c.Time.IsZero() {
			right += " · " + ago(r.c.Time)
		}
		right += " "
	}
	textW := max(w-gw, 0)
	short := r.c.Hash
	if len(short) > 7 {
		short = short[:7]
	}
	if picked {
		left := " " + short + " " + ansi.Strip(refsText(r.c.Refs, v.remotes)) + subject
		if ansi.StringWidth(left)+ansi.StringWidth(right) > textW {
			right = ""
		}
		left = ansi.Truncate(left, max(textW-ansi.StringWidth(right), 0), "…")
		pad := strings.Repeat(" ", max(textW-ansi.StringWidth(left)-ansi.StringWidth(right), 0))
		return ansi.Truncate(graph, w, "") + styleSel.Render(left+pad+right)
	}
	left := " " + styleMuted.Render(short) + " " + refsText(r.c.Refs, v.remotes) + subject
	if ansi.StringWidth(left)+ansi.StringWidth(right) > textW {
		right = ""
	}
	left = ansi.Truncate(left, max(textW-ansi.StringWidth(right), 0), "…")
	pad := strings.Repeat(" ", max(textW-ansi.StringWidth(left)-ansi.StringWidth(right), 0))
	return ansi.Truncate(graph, w, "") + left + pad + styleMuted.Render(right)
}
