package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/remote"
)

const statusHeight = 1

// sidebarInner is the content size inside the sidebar border.
func (m Model) sidebarInner() (w, h int) {
	return max(m.sidebarW-2, 1), max(m.height-statusHeight-2, 1)
}

// View renders the whole screen.
func (m Model) View() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	rows := m.height - statusHeight
	screen := make([]string, rows)
	if !m.zoom {
		sw, sh := m.sidebarInner()
		sideColor := colorAccent
		if m.focus == focusMain {
			sideColor = colorBorder
		}
		header, _ := m.spaceHeader(max(sw-2, 0))
		left := frameLines(header, exactly(m.sidebarLines(sw, sh), sh), sw, sideColor)
		for i := range screen {
			if i < len(left) {
				screen[i] = left[i]
			}
			screen[i] = padRight(screen[i], m.sidebarW)
		}
		if len(screen) > 0 {
			bar, _ := m.tabBar(m.mainRect().w)
			screen[0] += bar
		}
	}

	rects, _ := m.leafRects()
	t := m.tab()
	numbers := m.numbersShown()
	for n, l := range t.root.leaves() {
		r, ok := rects[l.id]
		if !ok {
			continue
		}
		focused := l.id == t.focus
		in := m.inner(r)
		content := exactly(m.leafLines(l, in.w, in.h, focused), in.h)
		if numbers {
			content = numberBadge(content, n+1, in.w)
		}
		var lines []string
		if m.zoom {
			for _, c := range content {
				lines = append(lines, fit(c, in.w))
			}
		} else {
			color := colorBorder
			switch {
			case m.leafDrag == l.id:
				color = colorWarn // being dragged: let go on another split
			case m.leafDrag != 0 && m.leafDrop == l.id:
				color = colorAccent // where it would land, while held
			case m.swapped(l.id):
				color = colorInput // the two that just swapped, for a moment
			case t.sync && l.view.Kind == kindPane:
				color = colorWarn // typing goes to every pane in the tab
			case focused && m.focus == focusMain:
				color = colorInput
			case focused && len(t.root.leaves()) > 1:
				color = colorAccent
			}
			lines = frameLinesBar(m.leafTitle(l), content, in.w, color, m.scrollbarMark(l.view, len(content)))
		}
		for i, line := range lines {
			if y := r.y + i; y >= 0 && y < rows {
				screen[y] = splice(screen[y], line, r.x, m.width)
			}
		}
	}
	screen = append(screen, m.statusBar())

	if m.overlay != nil {
		if d, ok := m.overlay.(dimmer); ok && d.dimBackground() {
			// Fade what's behind so only the dialog stands out.
			faded := lipgloss.NewStyle().Foreground(colorBorder)
			for i, l := range screen {
				screen[i] = faded.Render(ansi.Strip(l))
			}
		}
		b := m.overlay.render(m)
		for i, l := range b.lines {
			if y := b.y + i; y >= 0 && y < len(screen) {
				screen[y] = splice(screen[y], l, b.x, m.width)
			}
		}
	}
	for i, l := range screen { // a screen narrower than the sidebar or a dialog
		if ansi.StringWidth(l) > m.width {
			screen[i] = ansi.Truncate(l, m.width, "")
		}
	}
	return strings.Join(screen, "\n")
}

// splice draws s over base starting at column x, keeping what is either side.
func splice(base, s string, x, width int) string {
	base = padRight(base, x)
	return ansi.Cut(base, 0, x) + "\x1b[0m" + s + "\x1b[0m" + ansi.Cut(base, x+ansi.StringWidth(s), width)
}

// ---- sidebar ----

func (m Model) sidebarLines(w, h int) []string {
	header := styleMuted.Render("MACHINES")
	if m.filtering || m.filter != "" {
		cursor := ""
		if m.filtering {
			cursor = "█"
		}
		header = styleAccent.Render("/ ") + m.filter + cursor
		if strings.TrimSpace(m.filter) == waitingFilter {
			header += styleMuted.Render("  agents waiting for you")
		}
	}
	lines := []string{header}
	for i := m.scroll; i < len(m.rows) && len(lines) < h; i++ {
		lines = append(lines, m.rowLine(m.rows[i], w))
	}
	if len(m.rows) <= 1 && (m.isolated() != nil || len(m.allPanes()) == 0 && len(m.machines[0].projects) == 0) {
		lines = append(lines, "", styleMuted.Render(" a  add a project"), styleMuted.Render(" c  start an agent"), styleMuted.Render(" n  open a terminal"))
	}
	return lines
}

func (m Model) rowLine(r row, w int) string {
	indent := strings.Repeat("  ", r.depth)
	expander := "  "
	if r.expandable() {
		if m.isOpen(r) {
			expander = "▾ "
		} else {
			expander = "▸ "
		}
	}
	glyph, glyphStyle, label, labelStyle, right := m.rowParts(r)
	// Which agent, between the glyph that says what it is doing and the
	// name somebody gave it. It is drawn apart from the label so it can
	// keep the agent's own colour, which the label's style would take.
	mark, markStyle := "", styleMuted
	if r.kind == kindPane {
		if p := m.pane(r.machine, r.paneID); p != nil {
			mark, markStyle = m.agentMark(*p)
		}
	}
	selected := r.id == m.cursor
	// While a row is carried to a tab's section, the section it would land
	// in is marked, as a split's drop target is on the bar.
	if m.rowDrag != "" && m.rowDrop == r.id {
		plain := indent + expander + glyph
		if glyph != "" {
			plain += " "
		}
		return styleSel.Render(spread(plain+mark+label, ansi.Strip(right), w))
	}

	if selected {
		plain := indent + expander + glyph
		if glyph != "" {
			plain += " "
		}
		plain += mark + label
		style := styleSelDim
		if m.focus == focusSidebar && m.overlay == nil {
			style = styleSel
		}
		rightPlain := ansi.Strip(right)
		return style.Render(spread(plain, rightPlain, w))
	}
	left := styleMuted.Render(indent+expander) + glyphStyle.Render(glyph)
	if glyph != "" {
		left += " "
	}
	left += markStyle.Render(mark) + labelStyle.Render(label)
	line := spread(left, right, w)
	if m.hovering(r.id) {
		// A tint under what the pointer is on, keeping the row's own
		// colours: it says "this is what you would click", nothing more.
		return styleHover.Render(line)
	}
	return line
}

// rowParts describes how a row looks: a status glyph, a label and a
// right-aligned detail.
func (m Model) rowParts(r row) (glyph string, glyphStyle lipgloss.Style, label string, labelStyle lipgloss.Style, right string) {
	glyphStyle, labelStyle = lipgloss.NewStyle(), lipgloss.NewStyle()
	switch r.kind {
	case kindMachine:
		mach := m.machine(r.machine)
		if mach == nil {
			return "?", styleMuted, r.machine, labelStyle, ""
		}
		badge := joinRight(m.costChip(m.machineUsage(mach.id)), m.attentionBadge(mach.id, ""))
		switch {
		case mach.busy != "":
			return spinner[m.spin%len(spinner)], styleWork, mach.label, styleMuted, styleMuted.Render(mach.busy)
		case mach.state == stateAttention && mach.sandboxState != "":
			right := styleMuted.Render(string(mach.sandboxState))
			if spend := m.sandboxSpend(mach, time.Now()); spend != "" {
				right = joinRight(styleMuted.Render(spend), right)
			}
			return "■", styleMuted, mach.label, styleMuted, right
		}
		switch mach.state {
		case stateOnline:
			if spend := m.sandboxSpend(mach, time.Now()); spend != "" {
				badge = joinRight(styleMuted.Render(spend), badge)
			}
			if mach.warning != "" {
				return "●", styleWarn, mach.label, styleBold, joinRight(styleWarn.Render("outdated"), badge)
			}
			return "●", styleOK, mach.label, styleBold, badge
		case stateConnecting:
			return spinner[m.spin%len(spinner)], styleWork, mach.label, styleMuted, styleMuted.Render("connecting")
		case stateAttention:
			return "!", styleWarn, mach.label, styleBold, styleWarn.Render("setup")
		}
		return "○", styleErr, mach.label, styleMuted, styleErr.Render("offline")
	case kindProject:
		proj := m.project(r.machine, r.projectID)
		if proj == nil {
			return "◆", styleAccent, r.projectID, labelStyle, ""
		}
		if proj.Error != "" {
			right = styleErr.Render("git error")
		}
		right = joinRight(right, m.costChip(m.projectUsage(r.machine, proj.ID)))
		return "◆", styleAccent, proj.Name, styleBold, joinRight(right, m.attentionBadge(r.machine, proj.ID))
	case kindBranches:
		return "", glyphStyle, "Branches", styleMuted, styleMuted.Render(fmt.Sprint(r.count))
	case kindAgents:
		u := m.projectUsage(r.machine, r.projectID)
		if r.projectID == "" {
			u = m.looseUsage(r.machine)
		}
		return "", glyphStyle, "Agents", styleMuted, joinRight(m.costChip(u), styleMuted.Render(fmt.Sprint(r.count)))
	case kindTerminals:
		return "", glyphStyle, "Terminals", styleMuted, styleMuted.Render(fmt.Sprint(r.count))
	case kindTab:
		return "", glyphStyle, r.label, styleMuted, styleMuted.Render(fmt.Sprint(r.count))
	case kindFolder:
		// A mark of its own, not an arrow: the expander is already one, and
		// a second read as two. It also puts the name where the names of
		// the panes beside it are — a folder and a pane in no folder are
		// the same thing at the same level, and should line up.
		return "▪", glyphStyle, r.label, styleMuted, styleMuted.Render(fmt.Sprint(r.count))
	case kindSSH:
		return "", glyphStyle, "SSH", styleMuted, styleMuted.Render(fmt.Sprint(r.count))
	case kindSavedSSH:
		target := savedSSHTarget(r.id)
		return "○", styleMuted, sshDisplay(target, m.sshInfo[target]), styleMuted, styleMuted.Render("saved")
	case kindCLI:
		return "❯", styleAccent, "CLI", styleBold, styleMuted.Render(fmt.Sprint(r.count))
	case kindWorkspace:
		return "▤", styleAccent, "Workspace", styleBold, styleMuted.Render(fmt.Sprint(r.count))
	case kindSandboxes:
		return "▤", styleAccent, "Sandboxes", styleBold, styleMuted.Render(fmt.Sprint(r.count))
	case kindSandboxProvider:
		return "", glyphStyle, providerLabel(r.branch), styleBold, styleMuted.Render(fmt.Sprint(r.count))
	case kindMore:
		return "", glyphStyle, fmt.Sprintf("… %d more", r.count), styleMuted, ""
	case kindSessions:
		d := m.sessions[sessionsKey(r.machine, r.projectID)]
		if d == nil || d.list == nil {
			return "", glyphStyle, "Sessions", styleMuted, ""
		}
		right = styleMuted.Render(fmt.Sprint(len(d.list)))
		if n := d.interrupted(); n > 0 {
			right = joinRight(styleWarn.Render(fmt.Sprintf("⚠%d", n)), right)
		}
		return "", glyphStyle, "Sessions", styleMuted, right
	case kindFiles:
		return "", glyphStyle, "Files", styleMuted, ""
	case kindBranch:
		return m.branchParts(r)
	case kindPane:
		p := m.pane(r.machine, r.paneID)
		if p == nil {
			return "?", styleMuted, r.paneID, labelStyle, ""
		}
		g, state, style := m.paneGlyph(*p)
		if mach := m.machine(r.machine); mach != nil && mach.state != stateOnline {
			style, labelStyle = styleMuted, styleMuted // last seen; not live
		}
		if p.State == proto.PaneExited {
			right = style.Render(state)
		} else if p.Branch != "" {
			right = styleMuted.Render(p.Branch)
		} else if state != "" && p.Agent != nil {
			right = style.Render(state)
		}
		// Who started it, and what it started: a helper reads as somebody's
		// helper, and the agent that asked says how many it has out.
		right = joinRight(right, m.helperChip(r.machine, p.ID))
		// Listed under the pane that started it, the indent already says
		// whose it is; the chip is for a helper listed anywhere else.
		if who := m.forWhom(r.machine, *p); who != "" && !r.nested {
			right = joinRight(styleMuted.Render("↳"+who), right)
		}
		return g, style, p.DisplayName(), labelStyle, joinRight(right, m.costChip(paneUsage(*p)))
	case kindSubagent:
		return m.subagentParts(r)
	}
	return "", glyphStyle, r.id, labelStyle, ""
}

func (m Model) branchParts(r row) (string, lipgloss.Style, string, lipgloss.Style, string) {
	proj := m.project(r.machine, r.projectID)
	glyph, glyphStyle := "·", styleMuted
	if proj == nil {
		return glyph, glyphStyle, r.branch, lipgloss.NewStyle(), ""
	}
	var info proto.BranchInfo
	for _, b := range proj.Branches {
		if b.Name == r.branch {
			info = b
		}
	}
	var wt *proto.WorktreeInfo
	for i := range proj.Worktrees {
		if proj.Worktrees[i].Branch == r.branch {
			wt = &proj.Worktrees[i]
		}
	}
	labelStyle := lipgloss.NewStyle()
	if wt != nil {
		glyph, glyphStyle = "◇", styleAccent
		if wt.Main {
			glyph, glyphStyle, labelStyle = "●", styleOK, styleBold
		}
	}

	var parts []string
	// Agents working on this branch, worst state first.
	if g, style, ok := m.branchAgentGlyph(r.machine, proj.ID, r.branch); ok {
		parts = append(parts, style.Render(g))
	}
	if wt != nil && wt.Status != nil {
		switch {
		case wt.Status.Conflicts > 0:
			parts = append(parts, styleErr.Render(fmt.Sprintf("⚠%d", wt.Status.Conflicts)))
		case !wt.Status.Clean():
			stat := diffStat(wt.Status.Added, wt.Status.Deleted)
			if stat == "" {
				stat = styleWarn.Render(fmt.Sprintf("~%d", wt.Status.Files))
			}
			parts = append(parts, stat)
		}
	}
	if info.PR != nil {
		parts = append(parts, prBadge(info.PR))
	}
	if info.Gone {
		parts = append(parts, styleErr.Render("gone"))
	} else if r.branch != proj.Base && (info.BaseAhead > 0 || info.BaseBehind > 0) {
		ab := ""
		if info.BaseAhead > 0 {
			ab += fmt.Sprintf("↑%d", info.BaseAhead)
		}
		if info.BaseBehind > 0 {
			ab += fmt.Sprintf("↓%d", info.BaseBehind)
		}
		parts = append(parts, styleMuted.Render(ab))
	} else if r.branch == proj.Base && (info.Ahead > 0 || info.Behind > 0) {
		parts = append(parts, styleMuted.Render(fmt.Sprintf("⇡%d⇣%d", info.Ahead, info.Behind)))
	}
	return glyph, glyphStyle, r.branch, labelStyle, strings.Join(parts, " ")
}

// prBadge is a compact pull request marker: number plus checks.
func prBadge(pr *proto.PRInfo) string {
	num := fmt.Sprintf("#%d", pr.Number)
	switch {
	case pr.State == "MERGED":
		return stylePRMerged.Render(num)
	case pr.State == "CLOSED":
		return styleMuted.Render(num + "×")
	case pr.Draft:
		return styleMuted.Render(num)
	}
	switch pr.Checks {
	case "pass":
		return styleOK.Render(num + "✓")
	case "fail":
		return styleErr.Render(num + "✗")
	case "pending":
		return styleWarn.Render(num + "●")
	}
	return styleWork.Render(num)
}

// prSummary describes a pull request in words.
func prSummary(pr *proto.PRInfo) string {
	parts := []string{strings.ToLower(pr.State)}
	if pr.Draft {
		parts[0] = "draft"
	}
	switch pr.Checks {
	case "pass":
		parts = append(parts, styleOK.Render(fmt.Sprintf("checks passing %d/%d", pr.Passed, pr.Total)))
	case "fail":
		parts = append(parts, styleErr.Render(fmt.Sprintf("checks failing (%d/%d passed)", pr.Passed, pr.Total)))
	case "pending":
		parts = append(parts, styleWarn.Render(fmt.Sprintf("checks running %d/%d", pr.Passed, pr.Total)))
	}
	switch pr.Review {
	case "APPROVED":
		parts = append(parts, styleOK.Render("approved"))
	case "CHANGES_REQUESTED":
		parts = append(parts, styleErr.Render("changes requested"))
	case "REVIEW_REQUIRED":
		parts = append(parts, styleMuted.Render("review required"))
	}
	return strings.Join(parts, " · ")
}

// branchAgentGlyph summarises agents on a branch by the state most in need
// of attention.
func (m Model) branchAgentGlyph(mid, projectID, branch string) (string, lipgloss.Style, bool) {
	best, bestRank := proto.PaneInfo{}, -1
	rank := map[string]int{proto.AgentIdle: 0, proto.AgentWorking: 1, proto.AgentDone: 2, proto.AgentBlocked: 3}
	mach := m.machine(mid)
	if mach == nil {
		return "", lipgloss.Style{}, false
	}
	for _, p := range mach.panes {
		if p.ProjectID == projectID && p.Branch == branch && p.Agent != nil && rank[p.Agent.State] > bestRank {
			best, bestRank = p, rank[p.Agent.State]
		}
	}
	if bestRank < 0 {
		return "", lipgloss.Style{}, false
	}
	g, _, style := m.paneGlyph(best)
	return g, style, true
}

func (m Model) attentionBadge(mid, projectID string) string {
	waiting, done, working := 0, 0, 0
	mach := m.machine(mid)
	if mach == nil {
		return ""
	}
	_, panes := m.shown(mach)
	for _, p := range panes {
		if projectID != "" && p.ProjectID != projectID || p.Agent == nil {
			continue
		}
		switch p.Agent.State {
		case proto.AgentBlocked:
			waiting++
		case proto.AgentDone:
			done++
		case proto.AgentWorking:
			working++
		}
	}
	var parts []string
	if working > 0 {
		parts = append(parts, styleWork.Render(fmt.Sprintf("%s%d", spinner[m.spin%len(spinner)], working)))
	}
	if waiting > 0 {
		parts = append(parts, styleWarn.Render(fmt.Sprintf("⚑%d", waiting)))
	}
	if done > 0 {
		parts = append(parts, styleOK.Render(fmt.Sprintf("✓%d", done)))
	}
	return strings.Join(parts, " ")
}

func joinRight(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}
	return a + " " + b
}

var spinner = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// paneGlyph returns the status glyph, a short state label and its style.
func (m Model) paneGlyph(p proto.PaneInfo) (glyph, label string, style lipgloss.Style) {
	switch {
	case p.State == proto.PaneExited && p.ExitCode == 0:
		return "○", "exited", styleMuted
	case p.State == proto.PaneExited:
		return "✗", fmt.Sprintf("exit %d", p.ExitCode), styleErr
	case p.Agent == nil && p.Alert == proto.AlertActivity:
		return "#", "output", styleWarn // tmux's flags: # activity, ~ silence
	case p.Agent == nil && p.Alert == proto.AlertSilence:
		return "~", "quiet", styleWarn
	case p.Agent == nil:
		return "›", "", styleMuted
	}
	switch {
	case p.Agent.Failed && (p.Agent.State == proto.AgentDone || p.Agent.State == proto.AgentIdle):
		return "✗", "failed", styleErr
	}
	switch p.Agent.State {
	case proto.AgentWorking:
		return spinner[m.spin%len(spinner)], "working", styleWork
	case proto.AgentBlocked:
		return "!", "waiting", styleWarn
	case proto.AgentDone:
		return "✓", "done", styleOK
	default:
		return "○", "idle", styleMuted
	}
}

// inboxCount is how many agents need you, told apart: waiting ones have
// stopped to ask something, done ones finished while nobody watched and
// only want looking at.
func (m Model) inboxCount() (waiting, done int) {
	for _, p := range m.shownPanesAll() {
		switch {
		case p.Agent == nil:
		case p.Agent.State == proto.AgentBlocked:
			waiting++
		case p.Agent.State == proto.AgentDone:
			done++
		}
	}
	return waiting, done
}

// inboxLabel is the status bar's count of them: "⚑ 1 waiting · 1 done",
// either half alone, or nothing.
func inboxLabel(waiting, done int) string {
	switch {
	case waiting > 0 && done > 0:
		return styleWarn.Render(fmt.Sprintf("⚑ %d waiting", waiting)) + styleMuted.Render(" · ") + styleOK.Render(fmt.Sprintf("%d done", done))
	case waiting > 0:
		return styleWarn.Render(fmt.Sprintf("⚑ %d waiting", waiting))
	case done > 0:
		return styleOK.Render(fmt.Sprintf("✓ %d done", done))
	}
	return ""
}

// ---- main area ----

func (m Model) leafTitle(l *leaf) string {
	v := l.view
	mach := m.machine(v.Machine)
	switch v.Kind {
	case kindPane:
		p := m.pane(v.Machine, v.PaneID)
		if p == nil {
			return " closed "
		}
		t := " " + p.DisplayName() + " "
		if v.Machine != localMachine && mach != nil {
			t = " " + mach.label + " · " + p.DisplayName() + " "
		}
		if p.Agent != nil {
			t += "· " + p.Agent.State + " "
		} else if p.State == proto.PaneExited {
			t += fmt.Sprintf("· exited %d ", p.ExitCode)
		}
		if p.Branch != "" {
			t += "· " + p.Branch + " "
		}
		if p.Agent != nil && p.Agent.Tokens != nil {
			t += "· " + tokenSummary(p.Agent.Tokens) + " "
		}
		if sum := m.summaryText(v.Machine, v.PaneID); sum != "" {
			t += "· ✦ " + sum + " "
		}
		if f := m.frames[paneKey(v.Machine, v.PaneID)]; f != nil && f.Offset > 0 {
			t += fmt.Sprintf("· ↑ %d/%d lines back ", f.Offset, f.History)
		}
		return t
	case kindBranch:
		return " changes · " + v.Branch + " "
	case kindSessions:
		if proj := m.project(v.Machine, v.ProjectID); proj != nil {
			return " sessions · " + proj.Name + " "
		}
	case kindReviewQueue:
		return " review queue "
	case kindFiles:
		if proj := m.project(v.Machine, v.ProjectID); proj != nil {
			t := " files · " + proj.Name + " "
			if v.Branch != "" {
				t += "· " + v.Branch + " "
			}
			return t
		}
	case kindBranches:
		if proj := m.project(v.Machine, v.ProjectID); proj != nil {
			return " branches · " + proj.Name + " "
		}
	case kindAgents, kindTerminals, kindSSH:
		what := map[nodeKind]string{kindAgents: "agents", kindTerminals: "terminals", kindSSH: "ssh"}[v.Kind]
		if proj := m.project(v.Machine, v.ProjectID); proj != nil {
			return " " + what + " · " + proj.Name + " "
		}
		return " " + what + " · CLI "
	case kindSavedSSH:
		target := savedSSHTarget(v.Row)
		if name := m.sshInfo[target].Name; name != "" {
			return " " + name + " "
		}
		return " ssh · " + sshName(target) + " "
	case kindProject, kindMore:
		if proj := m.project(v.Machine, v.ProjectID); proj != nil {
			return " " + proj.Name + " "
		}
	case kindWorkspace:
		if mach != nil {
			return " " + mach.label + " · Workspace "
		}
	case kindSandboxes:
		return " sandboxes "
	case kindSandboxProvider:
		return " " + providerLabel(v.Branch) + " sandboxes "
	}
	if mach != nil {
		return " " + mach.label + " "
	}
	return " empty "
}

// tokenSummary is a short usage label: context size and output so far.
func tokenSummary(t *proto.Tokens) string {
	parts := []string{}
	switch {
	case t.Context > 0 && t.ContextSize > 0:
		parts = append(parts, fmt.Sprintf("ctx %s/%s", humanCount(t.Context), humanCount(t.ContextSize)))
	case t.Context > 0:
		parts = append(parts, "ctx "+humanCount(t.Context))
	}
	parts = append(parts, "out "+humanCount(t.Output))
	if t.CostUSD > 0 {
		parts = append(parts, usd(t.CostUSD))
	}
	return strings.Join(parts, " · ")
}

func usd(v float64) string {
	if v < 0.01 {
		return "<$0.01"
	}
	return fmt.Sprintf("$%.2f", v)
}

func humanCount(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 10_000:
		return fmt.Sprintf("%dk", n/1000)
	case n >= 1000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprint(n)
}

// leafLines renders what a leaf shows into w×h cells, with text selected
// on its page drawn over it.
func (m Model) leafLines(l *leaf, w, h int, focused bool) []string {
	lines := m.leafBody(l, w, h, focused)
	if m.sel != nil && m.sel.leaf == l.id && l.view.Kind != kindPane {
		lines = m.sel.highlight(exactly(lines, h), w)
	}
	return lines
}

// leafBody is what a leaf shows, before a page selection is drawn on it.
func (m Model) leafBody(l *leaf, w, h int, focused bool) []string {
	v := l.view
	if v.empty() {
		return centered(w, h, styleMuted.Render("Pick something in the tree for this split"), "",
			styleMuted.Render(m.cfg.Keys.Prefix+" x closes it"))
	}
	mach := m.machine(v.Machine)
	if mach != nil && mach.state != stateOnline && v.Kind != kindMachine {
		return m.machineLines(mach, w, h)
	}
	switch v.Kind {
	case kindPane:
		if m.pane(v.Machine, v.PaneID) == nil {
			return centered(w, h, styleMuted.Render("This pane was closed"), "", styleMuted.Render("pick another in the tree"))
		}
		f := m.frames[paneKey(v.Machine, v.PaneID)]
		if f == nil {
			return centered(w, h, styleMuted.Render("connecting…"))
		}
		lines := m.linkLines(v.Machine, v.PaneID, f.Lines, w)
		if focused && m.sel != nil && m.sel.leaf == 0 && m.sel.paneID == m.viewing {
			lines = m.sel.highlight(lines, w)
		}
		if focused && m.scrollMode {
			lines = m.searchMatches(exactly(lines, h), w)
			cursor := selection{ax: m.curX, ay: m.curY, bx: m.curX, by: m.curY}
			lines = m.searchPrompt(cursor.highlight(lines, w), w)
		}
		return lines
	case kindBranch:
		if l.changes != nil {
			return l.changes.render(m, w, h)
		}
	case kindSessions:
		if l.sessions != nil && mach != nil && mach.state == stateOnline {
			return l.sessions.render(m, w, h)
		}
	case kindReviewQueue:
		if l.queue != nil {
			return l.queue.render(m, w, h)
		}
	case kindFiles:
		if l.files != nil {
			return l.files.render(m, w, h)
		}
	case kindBranches:
		if proj := m.project(v.Machine, v.ProjectID); proj != nil {
			return m.branchesLines(v.Machine, *proj, w)
		}
	case kindAgents, kindTerminals, kindSSH:
		return m.sectionLines(v.Machine, v.ProjectID, v.Kind, w)
	case kindSavedSSH:
		target := savedSSHTarget(v.Row)
		return savedSSHLines(target, m.sshInfo[target], m.hostFolder(target), w)
	case kindProject, kindMore:
		if proj := m.project(v.Machine, v.ProjectID); proj != nil {
			return m.projectLines(v.Machine, *proj, w)
		}
	case kindWorkspace:
		if mach != nil {
			return m.workspaceLines(mach, w)
		}
	case kindSandboxes:
		return m.sandboxesLines("", w)
	case kindSandboxProvider:
		return m.sandboxesLines(v.Branch, w)
	}
	if mach == nil {
		return centered(w, h, styleMuted.Render("This machine was removed"))
	}
	return m.machineLines(mach, w, h)
}

// branchesLines is the page of a project's Branches row: every branch, not
// only the ones the tree lists, with where it is checked out and how it
// stands against the base.
func (m Model) branchesLines(mid string, proj proto.ProjectInfo, w int) []string {
	lines := []string{
		fit(styleBold.Render("Branches")+styleMuted.Render(fmt.Sprintf("  %d in %s", len(proj.Branches), proj.Name)), w),
		styleMuted.Render(ansi.Truncate(m.tildify(mid, proj.Path)+" · base "+proj.Base, w, "…")),
		"",
	}
	if len(proj.Branches) == 0 {
		return append(lines, styleMuted.Render("  no branches yet"))
	}
	worktree := map[string]*proto.WorktreeInfo{}
	for i := range proj.Worktrees {
		worktree[proj.Worktrees[i].Branch] = &proj.Worktrees[i]
	}
	var panes []proto.PaneInfo
	if mach := m.machine(mid); mach != nil {
		panes = mach.panes
	}
	branches, _ := listedBranches(proj, panes, true, time.Now())
	for _, b := range branches {
		glyph, style := styleMuted.Render("·"), styleMuted
		right := []string{}
		if wt := worktree[b.Name]; wt != nil {
			glyph, style = styleAccent.Render("◇"), lipgloss.NewStyle()
			if wt.Main {
				glyph, style = styleOK.Render("●"), styleBold
			}
			if wt.Status != nil && !wt.Status.Clean() {
				right = append(right, diffStat(wt.Status.Added, wt.Status.Deleted))
			}
		}
		if g, gs, ok := m.branchAgentGlyph(mid, proj.ID, b.Name); ok {
			right = append(right, gs.Render(g))
		}
		if b.PR != nil {
			right = append(right, prBadge(b.PR))
		}
		switch {
		case b.Gone:
			right = append(right, styleErr.Render("gone"))
		case b.Name != proj.Base && (b.BaseAhead > 0 || b.BaseBehind > 0):
			ab := ""
			if b.BaseAhead > 0 {
				ab += fmt.Sprintf("↑%d", b.BaseAhead)
			}
			if b.BaseBehind > 0 {
				ab += fmt.Sprintf("↓%d", b.BaseBehind)
			}
			right = append(right, styleMuted.Render(ab))
		}
		if !b.Committed.IsZero() {
			right = append(right, styleMuted.Render(ago(b.Committed)))
		}
		left := "  " + glyph + " " + style.Render(b.Name)
		if wt := worktree[b.Name]; wt != nil {
			left += "  " + styleMuted.Render(m.tildify(mid, wt.Path))
		}
		lines = append(lines, fit(spread(left, strings.Join(right, styleMuted.Render(" · ")), w), w))
	}
	hint := "click a branch for its changes · t new task · c start an agent · n terminal"
	return append(lines, "", styleMuted.Render(ansi.Truncate(hint, w, "…")))
}

// sectionPanes are the panes an Agents, Terminals or SSH row lists: a project's,
// or a machine's own when the row belongs to no project, in the order the
// tree shows them.
func (m Model) sectionPanes(mid, projectID string, kind nodeKind) []proto.PaneInfo {
	mach := m.machine(mid)
	if mach == nil {
		return nil
	}
	var out []proto.PaneInfo
	for _, p := range mach.panes {
		known := p.ProjectID != "" && m.project(mid, p.ProjectID) != nil
		if projectID == "" && known || projectID != "" && p.ProjectID != projectID {
			continue
		}
		if m.paneSection(mid, p.ID) == kind {
			out = append(out, p)
		}
	}
	return out
}

// sectionLines is the page of an Agents, Terminals or SSH row: what runs in
// it, each openable with a click.
func (m Model) sectionLines(mid, projectID string, kind nodeKind, w int) []string {
	what, hint := "Agents", "click an agent to open it · c start an agent · B broadcast · n terminal"
	switch kind {
	case kindTerminals:
		what, hint = "Terminals", "click a terminal to open it · n new terminal · H ssh to a host · B broadcast"
	case kindSSH:
		what, hint = "SSH", "click a session to open it · H ssh to a host · B broadcast"
	}
	where := "CLI"
	if proj := m.project(mid, projectID); proj != nil {
		where = proj.Name
	}
	panes := m.sectionPanes(mid, projectID, kind)
	lines := []string{
		fit(styleBold.Render(what)+styleMuted.Render(fmt.Sprintf("  %d in %s", len(panes), where)), w),
		styleMuted.Render(ansi.Truncate(m.machineLabel(mid), w, "…")),
		"",
	}
	if len(panes) == 0 {
		return append(lines, styleMuted.Render("  none yet"), "", styleMuted.Render(ansi.Truncate(hint, w, "…")))
	}
	for _, p := range panes {
		glyph, state, style := m.paneGlyph(p)
		right := []string{}
		if p.Branch != "" {
			right = append(right, styleMuted.Render(p.Branch))
		}
		if state != "" {
			right = append(right, style.Render(state))
		}
		if p.Agent != nil && p.Agent.Tokens != nil {
			right = append(right, styleMuted.Render(tokenSummary(p.Agent.Tokens)))
		}
		left := "  " + style.Render(glyph) + " " + p.DisplayName()
		lines = append(lines, fit(spread(left, strings.Join(right, styleMuted.Render(" · ")), w), w))
		if sum := m.summaryText(mid, p.ID); sum != "" {
			lines = append(lines, fit("    "+styleAccent.Render("✦ ")+styleMuted.Render(ansi.Truncate(sum, max(w-6, 10), "…")), w))
		}
	}
	return append(lines, "", styleMuted.Render(ansi.Truncate(hint, w, "…")))
}

// machineLabel names a machine for a page heading.
func (m Model) machineLabel(mid string) string {
	if mach := m.machine(mid); mach != nil {
		return mach.label
	}
	return mid
}

func (m Model) projectLines(mid string, proj proto.ProjectInfo, w int) []string {
	lines := []string{styleBold.Render(proj.Name), styleMuted.Render(m.tildify(mid, proj.Path))}
	if proj.Git {
		lines[1] += styleMuted.Render(" · base " + proj.Base)
	}
	if proj.Error != "" {
		lines = append(lines, styleErr.Render(proj.Error))
	}
	lines = append(lines, "")

	var agents []proto.PaneInfo
	mach := m.machine(mid)
	for _, p := range mach.panes {
		if p.ProjectID == proj.ID && (p.Agent != nil || mach.agents[p.ID]) {
			agents = append(agents, p)
		}
	}
	head := styleBold.Render("Agents") + styleMuted.Render(fmt.Sprintf("  %d", len(agents)))
	if line := m.usageLine(usageOf(agents), "usage"); line != "" {
		head = spread(head, styleMuted.Render(line), w)
	}
	lines = append(lines, head)
	if len(agents) == 0 {
		lines = append(lines, styleMuted.Render("  none yet · t starts a task on its own branch"))
	}
	for _, p := range agents {
		g, state, style := m.paneGlyph(p)
		left := fmt.Sprintf("  %s %s", style.Render(g), p.DisplayName())
		right := joinRight(styleMuted.Render(p.Branch), style.Render(state))
		lines = append(lines, spread(left, joinRight(right, m.costChip(paneUsage(p))), w))
		if sum := m.summaryText(mid, p.ID); sum != "" {
			lines = append(lines, "    "+styleAccent.Render("✦ ")+styleMuted.Render(ansi.Truncate(sum, max(w-6, 10), "…")))
		}
	}

	if proj.Git {
		lines = append(lines, "", styleBold.Render("Worktrees")+styleMuted.Render(fmt.Sprintf("  %d", len(proj.Worktrees))))
		for _, wt := range proj.Worktrees {
			name := wt.Branch
			if wt.Detached {
				name = "(detached " + wt.Head[:min(7, len(wt.Head))] + ")"
			}
			state := styleMuted.Render("clean")
			if wt.Status != nil && !wt.Status.Clean() {
				state = fmt.Sprintf("%s %s", diffStat(wt.Status.Added, wt.Status.Deleted), styleMuted.Render(fmt.Sprintf("%d files", wt.Status.Files)))
			}
			glyph := styleAccent.Render("◇")
			if wt.Main {
				glyph = styleOK.Render("●")
			}
			lines = append(lines, spread(fmt.Sprintf("  %s %s  %s", glyph, name, styleMuted.Render(m.tildify(mid, wt.Path))), state, w))
		}
	}
	if proj.Git {
		open, failing := 0, 0
		for _, b := range proj.Branches {
			if b.PR != nil && b.PR.State == "OPEN" {
				open++
				if b.PR.Checks == "fail" {
					failing++
				}
			}
		}
		line := styleBold.Render("Pull requests") + styleMuted.Render(fmt.Sprintf("  %d open", open))
		if failing > 0 {
			line += "  " + styleErr.Render(fmt.Sprintf("%d failing checks", failing))
		}
		if proj.PRStatus != "" {
			line += "  " + styleMuted.Render(proj.PRStatus)
		}
		lines = append(lines, "", line)
		for _, b := range proj.Branches {
			if b.PR != nil && b.PR.State == "OPEN" {
				left := fmt.Sprintf("  %s %s", prBadge(b.PR), b.PR.Title)
				lines = append(lines, spread(left, styleMuted.Render(b.Name), w))
			}
		}
	}
	lines = append(lines, m.projectSessionLines(mid, proj, w)...)
	lines = append(lines, "", styleMuted.Render("t new task · c start an agent · n terminal · m menu · x remove from sidebar"))
	return lines
}

// workspaceLines is the page of a machine's Workspace row: its projects,
// each with what runs in it.
func (m Model) workspaceLines(mach *machine, w int) []string {
	shownProjects, panes := m.shown(mach)
	lines := []string{styleBold.Render("Workspace") + styleMuted.Render(fmt.Sprintf("  %d projects on %s", len(shownProjects), mach.label)), ""}
	projects := append([]proto.ProjectInfo(nil), shownProjects...)
	sort.SliceStable(projects, func(i, j int) bool {
		return strings.ToLower(projects[i].Name) < strings.ToLower(projects[j].Name)
	})
	for _, proj := range projects {
		agents, terms := 0, 0
		for _, p := range panes {
			if p.ProjectID != proj.ID {
				continue
			}
			if p.Agent != nil || mach.agents[p.ID] {
				agents++
			} else {
				terms++
			}
		}
		right := styleMuted.Render(fmt.Sprintf("%d agents · %d terminals", agents, terms))
		if proj.Error != "" {
			right = styleErr.Render("git error")
		}
		right = joinRight(m.attentionBadge(mach.id, proj.ID), right)
		left := "  " + styleAccent.Render("◆") + " " + proj.Name + "  " + styleMuted.Render(m.tildify(mach.id, proj.Path))
		lines = append(lines, spread(left, right, w))
	}
	if len(projects) == 0 {
		lines = append(lines, styleMuted.Render("  no projects yet"))
	}
	lines = append(lines, "", styleMuted.Render("a add a project · t new task · B broadcast to project agents · m menu"))
	return lines
}

// sandboxesLines is the page of the Sandboxes row, or of one provider's:
// what conch made, where it is and what runs in it. It says what a
// sandbox costs to leave running, because nothing else does.
func (m Model) sandboxesLines(provider string, w int) []string {
	title := "Sandboxes"
	if provider != "" {
		title = providerLabel(provider) + " sandboxes"
	}
	var boxes []*machine
	for _, mach := range m.machines {
		p, _, ok := remote.ParseSandboxTarget(mach.target)
		if ok && (provider == "" || p == provider) && m.showsMachine(mach) {
			boxes = append(boxes, mach)
		}
	}
	sort.SliceStable(boxes, func(i, j int) bool { return strings.ToLower(boxes[i].label) < strings.ToLower(boxes[j].label) })
	now := time.Now()
	var total, cost, stoppedRate float64
	var anyPriced, allOwn = false, true
	lines := []string{styleBold.Render(title) + styleMuted.Render(fmt.Sprintf("  %d", len(boxes))), ""}
	for _, mach := range boxes {
		p, id, _ := remote.ParseSandboxTarget(mach.target)
		agents, terms := 0, 0
		_, panes := m.shown(mach)
		for _, pane := range panes {
			if pane.Agent != nil || mach.agents[pane.ID] {
				agents++
			} else {
				terms++
			}
		}
		state := styleOK.Render("running")
		switch {
		case mach.busy != "":
			state = styleWork.Render(mach.busy)
		case mach.sandboxState != "":
			state = styleMuted.Render(string(mach.sandboxState))
		case mach.state == stateConnecting:
			state = styleWork.Render("connecting")
		case mach.state == stateAttention:
			state = styleWarn.Render("setup")
		case mach.state != stateOnline:
			state = styleErr.Render("offline")
		}
		right := styleMuted.Render(fmt.Sprintf("%d agents · %d terminals", agents, terms))
		if mach.state != stateOnline {
			right = ""
		}
		if spend := m.sandboxSpend(mach, now); spend != "" {
			right = joinRight(styleMuted.Render(spend), right)
			total += mach.runningFor(now).Hours()
			if c, told, own := m.sandboxCost(mach, now); told {
				cost, anyPriced = cost+c, true
				allOwn = allOwn && own
			}
			stoppedRate += m.sandboxStoppedRate(mach)
		}
		left := "  " + mach.label + "  " + styleMuted.Render(providerLabel(p)+" "+id)
		lines = append(lines, spread(left, joinRight(state, right), w))
	}
	if len(boxes) == 0 {
		lines = append(lines, styleMuted.Render("  none yet · M → New sandbox… makes one"))
	}
	if total > 0 || stoppedRate > 0 {
		var said []string
		if total > 0 {
			running := fmt.Sprintf("running for %s in all", shortDuration(time.Duration(total*float64(time.Hour))))
			if anyPriced {
				// "~" when any of it is conch's arithmetic rather than
				// what the provider has billed.
				running += " · " + map[bool]string{false: "~"}[allOwn] + money(cost)
			}
			said = append(said, running)
		}
		if stoppedRate > 0 {
			// Stopped is not free: the disk stays until the sandbox is
			// deleted, and so does the charge for it.
			said = append(said, "stopped ones keep "+rate(stoppedRate)+" of disk")
		}
		lines = append(lines, "", styleMuted.Render("  "+strings.Join(said, " · ")))
	}
	return append(lines, "", styleMuted.Render("enter opens one · m menu: open a port, snapshot, stop, delete · M new · one that runs, costs"))
}

func (m Model) machineLines(mach *machine, cols, rows int) []string {
	working, waiting := 0, 0
	projects, panes := m.shown(mach)
	for _, p := range panes {
		switch {
		case p.Agent.NeedsAttention():
			waiting++
		case p.Agent != nil && p.Agent.State == proto.AgentWorking:
			working++
		}
	}
	where := "this computer"
	if provider, id, ok := remote.ParseSandboxTarget(mach.target); ok {
		where = provider + " sandbox " + id
	} else if mach.target != "" {
		where = "ssh " + mach.target
	}
	lines := []string{styleBold.Render(mach.label) + styleMuted.Render("  "+where)}
	if mach.server.Hostname != "" {
		lines = append(lines, styleMuted.Render(fmt.Sprintf("%s · %s · conch build %s", mach.server.Hostname, mach.server.Platform, mach.server.Build)))
	}
	lines = append(lines, "")
	if mach.busy != "" {
		lines = append(lines, styleWork.Render(mach.busy+" the sandbox…"), "")
	}
	switch mach.state {
	case stateOnline:
		lines = append(lines, styleMuted.Render(fmt.Sprintf("%d projects · %d panes · %d working · %d waiting", len(projects), len(panes), working, waiting)))
		if line := m.usageLine(usageOf(panes), "usage"); line != "" {
			lines = append(lines, styleMuted.Render(line))
		}
		lines = append(lines, m.limitsLines(mach, cols)...)
		if len(mach.agentList) > 0 {
			var parts []string
			for _, a := range mach.agentList {
				label := firstNonEmpty(a.Label, agentLabel(a.Name))
				if a.Installed {
					parts = append(parts, styleOK.Render("✓ "+label+" "+a.Version))
				} else {
					parts = append(parts, styleMuted.Render("○ "+label))
				}
			}
			lines = append(lines, strings.Join(parts, styleMuted.Render("  ")), styleMuted.Render("c  start or install an agent"))
		}
		if mach.warning != "" {
			lines = append(lines, styleWarn.Render(mach.warning))
		}
		lines = append(lines, "",
			styleMuted.Render("a  add a project      t  new task in its home"),
			styleMuted.Render("c  start an agent     n  open a terminal"),
			styleMuted.Render("M  add a machine      m  machine menu   ?  all keys"),
		)
	case stateConnecting:
		lines = append(lines, styleWork.Render("connecting…"))
	case stateAttention:
		if mach.sandboxState != "" {
			provider, _, _ := mach.sandbox()
			lines = append(lines, styleMuted.Render(mach.err+": its files are kept, nothing runs there"), "",
				styleMuted.Render("m → Start sandbox   (or run: conch sandbox -provider "+provider+" start "+mach.id+")"))
			break
		}
		lines = append(lines, styleWarn.Render(mach.err), "", styleMuted.Render("m → Install / upgrade conch there   (or run: conch machine upgrade "+mach.id+")"))
	default:
		lines = append(lines, styleErr.Render("offline: "+mach.err))
		if len(panes) > 0 {
			lines = append(lines, styleMuted.Render(fmt.Sprintf("showing %d panes as last seen; they keep running if the machine is up", len(panes))))
		}
		hint := "R  reconnect now (retrying automatically)"
		if mach.id == localMachine {
			hint = "m → Start the server   R  reconnect"
		}
		lines = append(lines, "", styleMuted.Render(hint))
	}
	return centered(cols, rows, lines...)
}

// ---- status bar ----

// ---- layout helpers ----

// exactly pads or cuts lines to n entries.
func exactly(lines []string, n int) []string {
	out := make([]string, n)
	copy(out, lines)
	return out
}

// fit truncates or pads an ANSI string to exactly w cells and resets
// styling so it cannot bleed into what follows.
func fit(s string, w int) string {
	s = ansi.Truncate(s, w, "")
	return padRight(s+"\x1b[0m", w)
}

func padRight(s string, w int) string {
	if n := w - ansi.StringWidth(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}

// spread lays out left and right text across exactly w cells, truncating
// the left side when both don't fit.
func spread(left, right string, w int) string {
	// The left side (usually a name) keeps up to two thirds of the width;
	// the right detail gives way first, losing its start.
	lw, rw := ansi.StringWidth(left), ansi.StringWidth(right)
	if lw+1+rw > w && rw > 0 {
		room := max(w-min(lw, w*2/3)-1, 0)
		switch {
		case room < 3:
			right = ""
		case rw > room:
			right = ansi.TruncateLeft(right, rw-room+1, "…")
		}
		rw = ansi.StringWidth(right)
	}
	if rw > 0 {
		rw++ // keep a space before the detail
	}
	left = ansi.Truncate(left, max(w-rw, 0), "…")
	gap := max(w-ansi.StringWidth(left)-ansi.StringWidth(right), 0)
	return left + strings.Repeat(" ", gap) + right
}

// centered places content as a left-aligned block in the middle of a w×h area.
func centered(w, h int, content ...string) []string {
	lines := make([]string, h)
	top := max((h-len(content))/2, 0)
	widest := 0
	for _, c := range content {
		widest = max(widest, ansi.StringWidth(c))
	}
	pad := strings.Repeat(" ", max((w-widest)/2, 0))
	for i, c := range content {
		if top+i >= h {
			break
		}
		lines[top+i] = pad + c
	}
	return lines
}

// agentMarks are the marks in front of an agent's name in the tree, one per
// agent, so which agent a pane runs reads at a glance rather than from the
// name somebody gave the pane. The state glyph in front of them is left
// alone: it says whether the agent wants you, which is the thing worth
// seeing first.
//
// Two sets, because a terminal draws text and a logo is a picture. With
// `[ui] icons = "nerd"` these are Nerd Font glyphs, of which exactly one is
// the agent's own logo — Google's, for Gemini. Nerd Fonts carries Font
// Awesome, Devicons, Material, Octicons, Codicons, Powerline, Seti and
// Weather, and none of them has an Anthropic or an OpenAI mark, so the
// others are a plain thing that suits the agent rather than a pretend
// logo. The real marks would need a terminal's image protocol, which not
// every terminal has and ssh and tmux lose.
//
// Each is in the Private Use Area of the BMP, for the reason the file icons
// are (fileicons.go): a Nerd Fonts v2 font draws nothing in the Material
// range at U+F0000, and a glyph a font has not got is worse than none.
//
// Otherwise they are coloured dots: emoji, so two cells wide where a glyph
// is one, and the width of an emoji is the thing terminals disagree about
// most. Every line the tree draws is measured with ansi.StringWidth and cut
// to fit, so a terminal that draws one wider than it measures shortens the
// name rather than spilling. `icons = "off"` leaves them out.
var (
	agentDots = map[string]string{
		"claude":   "🟠", // terracotta, as Claude's own
		"codex":    "🟢",
		"gemini":   "🔵",
		"opencode": "🟣",
		"devin":    "🟡",
	}
	// agentColors are the agents' own, for the glyph in nerd mode. A dot
	// carries its colour in the emoji itself and ignores a foreground.
	agentColors = map[string]lipgloss.Color{
		"claude":   "#D97757", // Anthropic's terracotta
		"codex":    "#10A37F", // OpenAI's green
		"gemini":   "#4285F4", // Google's blue
		"opencode": "#C792EA",
		"devin":    "#E3B341",
	}
	agentGlyphs = map[string]string{
		"claude":   "\uf069", // an asterisk, which is the shape of Anthropic's own
		"codex":    "\uf121", // </>
		"gemini":   "\uf1a0", // Google's own, the one real logo here
		"opencode": "\uf120", // a terminal
		"devin":    "\uf135", // a rocket
	}
)

// agentMark is the mark for a pane's agent, with the space after it, and
// the colour to draw it in — the agent's own, so Claude's glyph is
// Anthropic's terracotta. It is "" for a pane that is no agent, an agent
// conch has no mark for, or icons off.
func (m Model) agentMark(p proto.PaneInfo) (string, lipgloss.Style) {
	if p.Agent == nil {
		return "", styleMuted
	}
	marks := agentDots
	switch iconMode(m.cfg.UI.Icons) {
	case iconsOff:
		return "", styleMuted
	case iconsNerd:
		marks = agentGlyphs
	}
	mark, ok := marks[p.Agent.Name]
	if !ok {
		return "", styleMuted
	}
	style := styleMuted
	if c, ok := agentColors[p.Agent.Name]; ok {
		style = lipgloss.NewStyle().Foreground(c)
	}
	return mark + " ", style
}

// subagentParts draws a subagent under the agent running it: what it was
// asked to do, and what kind of agent it is. One is listed only while it
// runs, so it is always at work.
func (m Model) subagentParts(r row) (string, lipgloss.Style, string, lipgloss.Style, string) {
	glyph, style := spinner[m.spin%len(spinner)], styleWork
	if mach := m.machine(r.machine); mach != nil && mach.state != stateOnline {
		style = styleMuted // last seen; not live
	}
	p := m.pane(r.machine, r.paneID)
	if p == nil || p.Agent == nil {
		return glyph, style, r.label, styleMuted, ""
	}
	for _, a := range p.Agent.Subagents {
		if a.ID != r.label {
			continue
		}
		switch {
		case a.Description != "" && a.Type != "":
			return glyph, style, a.Description, lipgloss.NewStyle(), styleMuted.Render(a.Type)
		case a.Description != "":
			return glyph, style, a.Description, lipgloss.NewStyle(), ""
		case a.Type != "":
			return glyph, style, a.Type, lipgloss.NewStyle(), ""
		}
		break
	}
	return glyph, style, "subagent", lipgloss.NewStyle(), ""
}
