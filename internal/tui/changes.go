package tui

import (
	"fmt"
	"reflect"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
)

// changesView shows what a branch changed: uncommitted files when it is
// checked out (else files since the base), commits ahead, and a file diff.
type changesView struct {
	machine   string
	projectID string
	branch    string

	data    *proto.Changes
	err     string
	loading bool
	sel     int // selected file
	scroll  int // file list scroll

	diffFile   string // "" when the file list is shown
	diff       []string
	diffErr    string
	diffScroll int
}

type changesMsg struct {
	projectID, branch string
	data              proto.Changes
	err               error
	poll              bool // a background refresh, not a user's reload
}

// changesPollEvery is how often a visible changes view re-reads the branch,
// so files an agent writes show up without waiting for the project's git
// refresh (every 5 to 30 seconds).
const changesPollEvery = 2 * time.Second

type changesPollMsg struct{}

type diffMsg struct {
	projectID, branch, file string
	diff                    string
	err                     error
}

func (cv *changesView) reload(m *Model) tea.Cmd {
	cv.loading = true
	c, pid, branch := m.clientOf(cv.machine), cv.projectID, cv.branch
	return func() tea.Msg {
		var out proto.Changes
		if c == nil {
			return changesMsg{projectID: pid, branch: branch, err: errString("machine is offline")}
		}
		err := callCtx(c, proto.MethodProjectChanges, proto.ChangesParams{ProjectID: pid, Branch: branch}, &out)
		return changesMsg{projectID: pid, branch: branch, data: out, err: err}
	}
}

// poll re-reads the branch in the background, keeping what is on screen
// until the answer arrives.
func (cv *changesView) poll(m *Model) tea.Cmd {
	if cv.loading {
		return nil
	}
	cmd := cv.reload(m)
	cv.loading = false // not a visible reload
	return func() tea.Msg {
		msg := cmd().(changesMsg)
		msg.poll = true
		return msg
	}
}

func (cv *changesView) loadDiff(m *Model, file string) tea.Cmd {
	cv.diffFile, cv.diff, cv.diffErr, cv.diffScroll = file, nil, "", 0
	return cv.fetchDiff(m, file)
}

// refreshDiff re-reads the open diff, keeping it on screen (and scrolled)
// until the new one arrives.
func (cv *changesView) refreshDiff(m *Model) tea.Cmd {
	if cv.diffFile == "" {
		return nil
	}
	return cv.fetchDiff(m, cv.diffFile)
}

func (cv *changesView) fetchDiff(m *Model, file string) tea.Cmd {
	c, pid, branch := m.clientOf(cv.machine), cv.projectID, cv.branch
	return func() tea.Msg {
		var out proto.DiffResult
		if c == nil {
			return diffMsg{projectID: pid, branch: branch, file: file, err: errString("machine is offline")}
		}
		err := callCtx(c, proto.MethodProjectDiff, proto.DiffParams{ProjectID: pid, Branch: branch, File: file}, &out)
		return diffMsg{projectID: pid, branch: branch, file: file, diff: out.Diff, err: err}
	}
}

// receive applies a result. changed reports that a background refresh found
// the branch different from what was shown.
func (cv *changesView) receive(msg tea.Msg) (changed bool) {
	switch msg := msg.(type) {
	case changesMsg:
		if msg.projectID != cv.projectID || msg.branch != cv.branch {
			return false
		}
		cv.loading = false
		if msg.err != nil {
			if !msg.poll {
				cv.err = msg.err.Error()
			}
			return false
		}
		if msg.poll && cv.data != nil && reflect.DeepEqual(*cv.data, msg.data) {
			return false
		}
		selected := ""
		if cv.data != nil && cv.sel < len(cv.data.Files) {
			selected = cv.data.Files[cv.sel].Path
		}
		changed = msg.poll && cv.data != nil
		cv.err = ""
		cv.data = &msg.data
		cv.sel = clamp(cv.sel, 0, max(len(msg.data.Files)-1, 0))
		for i, f := range msg.data.Files { // keep the selection on its file
			if f.Path == selected {
				cv.sel = i
			}
		}
		return changed
	case diffMsg:
		if msg.projectID != cv.projectID || msg.branch != cv.branch || msg.file != cv.diffFile {
			return
		}
		if msg.err != nil {
			cv.diffErr = msg.err.Error()
			return
		}
		cv.diff = strings.Split(strings.TrimRight(msg.diff, "\n"), "\n")
	}
	return false
}

// key handles a key while the view has focus; back reports that focus
// should return to the tree.
func (cv *changesView) key(m *Model, k tea.KeyMsg) (back bool, cmd tea.Cmd) {
	_, h := m.paneArea()
	if cv.diffFile != "" {
		page := max(h-2, 1)
		switch k.String() {
		case "esc", "q", "left", "h":
			cv.diffFile = ""
		case "up", "k":
			cv.diffScroll--
		case "down", "j":
			cv.diffScroll++
		case "pgup", "b":
			cv.diffScroll -= page
		case "pgdown", " ", "f":
			cv.diffScroll += page
		case "g", "home":
			cv.diffScroll = 0
		case "G", "end":
			cv.diffScroll = len(cv.diff)
		case "tab":
			return true, nil
		case "y":
			return false, copyText(strings.Join(cv.diff, "\n") + "\n")
		}
		cv.diffScroll = clamp(cv.diffScroll, 0, max(len(cv.diff)-(h-2), 0))
		return false, nil
	}
	files := 0
	if cv.data != nil {
		files = len(cv.data.Files)
	}
	switch k.String() {
	case "esc", "q", "left", "h", "tab":
		return true, nil
	case "up", "k":
		cv.sel = clamp(cv.sel-1, 0, max(files-1, 0))
	case "down", "j":
		cv.sel = clamp(cv.sel+1, 0, max(files-1, 0))
	case "enter", "right", "l":
		if files > 0 {
			return false, cv.loadDiff(m, cv.data.Files[cv.sel].Path)
		}
	case "R":
		return false, cv.reload(m)
	case "o":
		if pr := m.branchPR(cv.machine, cv.projectID, cv.branch); pr != nil {
			return false, openURL(pr.URL)
		}
	case "y":
		if files > 0 {
			return false, copyText(cv.data.Files[cv.sel].Path)
		}
	}
	return false, nil
}

// filesTop is the view row of the first file: title, meta, an optional pull
// request line, a blank line and the section header come first.
func (cv *changesView) filesTop(m Model) int {
	if m.branchPR(cv.machine, cv.projectID, cv.branch) != nil {
		return 5
	}
	return 4
}

func (cv *changesView) render(m Model, w, h int) []string {
	if cv.diffFile != "" {
		return cv.renderDiff(w, h)
	}
	proj := m.project(cv.machine, cv.projectID)
	lines := []string{styleBold.Render(cv.branch)}
	meta := ""
	if proj != nil {
		meta = proj.Name
		for _, b := range proj.Branches {
			if b.Name == cv.branch && b.Name != proj.Base {
				meta += fmt.Sprintf(" · %d ahead, %d behind %s", b.BaseAhead, b.BaseBehind, proj.Base)
			}
		}
	}
	switch {
	case cv.data != nil && cv.data.Worktree != "":
		meta += " · " + m.tildify(cv.machine, cv.data.Worktree)
	case cv.data != nil:
		meta += " · not checked out"
	}
	lines = append(lines, styleMuted.Render(meta))
	if pr := m.branchPR(cv.machine, cv.projectID, cv.branch); pr != nil {
		lines[0] = spread(lines[0], styleMuted.Render("o open PR"), w)
		lines = append(lines, prBadge(pr)+" "+ansi.Truncate(pr.Title, max(w/2, 10), "…")+styleMuted.Render(" · ")+prSummary(pr))
	}
	lines = append(lines, "")

	switch {
	case cv.err != "":
		return append(lines, styleErr.Render(cv.err))
	case cv.data == nil:
		return append(lines, styleMuted.Render("loading…"))
	}

	title := "Uncommitted changes"
	if cv.data.Worktree == "" {
		title = "Files changed since " + cv.data.Base
	}
	added, deleted := 0, 0
	for _, f := range cv.data.Files {
		added += f.Added
		deleted += f.Deleted
	}
	header := fmt.Sprintf("%s  %s", styleBold.Render(title), styleMuted.Render(fmt.Sprintf("%d files", len(cv.data.Files))))
	if len(cv.data.Files) > 0 {
		header += "  " + diffStat(added, deleted)
	}
	lines = append(lines, header)

	listH := max(h-cv.filesTop(m)-min(len(cv.data.Commits), 6)-3, 3)
	if len(cv.data.Files) == 0 {
		lines = append(lines, styleMuted.Render("  nothing here"))
	}
	if cv.sel < cv.scroll {
		cv.scroll = cv.sel
	}
	if cv.sel >= cv.scroll+listH {
		cv.scroll = cv.sel - listH + 1
	}
	for i := cv.scroll; i < len(cv.data.Files) && i < cv.scroll+listH; i++ {
		f := cv.data.Files[i]
		stat := diffStat(f.Added, f.Deleted)
		if f.Binary {
			stat = styleMuted.Render("binary")
		}
		name := f.Path
		if f.OrigPath != "" {
			name = f.OrigPath + " → " + f.Path
		}
		left := fmt.Sprintf("  %s %s", codeStyle(f.Code).Render(padRight(f.Code, 1)), name)
		if i == cv.sel {
			sel := styleSelDim
			if m.focus == focusMain {
				sel = styleSel
			}
			left = sel.Render(ansi.Truncate(fmt.Sprintf("▸ %s %s", f.Code, name), w-12, "…"))
		}
		lines = append(lines, spread(left, stat, w))
	}
	if more := len(cv.data.Files) - (cv.scroll + listH); more > 0 {
		lines = append(lines, styleMuted.Render(fmt.Sprintf("  … %d more", more)))
	}

	if len(cv.data.Commits) > 0 {
		lines = append(lines, "", styleBold.Render(fmt.Sprintf("Commits ahead of %s", cv.data.Base))+styleMuted.Render(fmt.Sprintf("  %d", len(cv.data.Commits))))
		for i, c := range cv.data.Commits {
			if len(lines) >= h {
				break
			}
			if i >= 6 && len(cv.data.Commits) > 7 {
				lines = append(lines, styleMuted.Render(fmt.Sprintf("  … %d more", len(cv.data.Commits)-i)))
				break
			}
			left := fmt.Sprintf("  %s %s", styleWork.Render(c.Hash[:min(7, len(c.Hash))]), c.Subject)
			lines = append(lines, spread(left, styleMuted.Render(ago(c.Time)), w))
		}
	}
	return lines
}

func (cv *changesView) renderDiff(w, h int) []string {
	lines := []string{spread(styleBold.Render(cv.diffFile), styleMuted.Render("esc back · ↑↓ scroll"), w)}
	switch {
	case cv.diffErr != "":
		return append(lines, styleErr.Render(cv.diffErr))
	case cv.diff == nil:
		return append(lines, styleMuted.Render("loading…"))
	}
	for i := cv.diffScroll; i < len(cv.diff) && len(lines) < h; i++ {
		l := strings.ReplaceAll(cv.diff[i], "\t", "    ")
		l = ansi.Strip(l) // diffs are data; never let them drive the terminal
		switch {
		case strings.HasPrefix(l, "+++"), strings.HasPrefix(l, "---"), strings.HasPrefix(l, "diff "), strings.HasPrefix(l, "index "):
			l = styleMuted.Render(l)
		case strings.HasPrefix(l, "@@"):
			l = styleWork.Render(l)
		case strings.HasPrefix(l, "+"):
			l = styleOK.Render(l)
		case strings.HasPrefix(l, "-"):
			l = styleErr.Render(l)
		}
		lines = append(lines, l)
	}
	return lines
}

// mouse handles a mouse event at x, y relative to the main area.
func (cv *changesView) mouse(m *Model, msg tea.MouseMsg, x, y int) tea.Cmd {
	_, h := m.paneArea()
	switch msg.Button {
	case tea.MouseButtonWheelUp, tea.MouseButtonWheelDown:
		delta := 3
		if msg.Button == tea.MouseButtonWheelUp {
			delta = -3
		}
		if cv.diffFile != "" {
			cv.diffScroll = clamp(cv.diffScroll+delta, 0, max(len(cv.diff)-(h-2), 0))
		} else if cv.data != nil {
			cv.sel = clamp(cv.sel+delta/3, 0, max(len(cv.data.Files)-1, 0))
		}
		return nil
	}
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft || cv.diffFile != "" || cv.data == nil {
		return nil
	}
	top := cv.filesTop(*m)
	if i := cv.scroll + y - top; y >= top && i >= 0 && i < len(cv.data.Files) {
		cv.sel = i
		return cv.loadDiff(m, cv.data.Files[i].Path) // one click opens the diff
	}
	return nil
}

func diffStat(added, deleted int) string {
	parts := []string{}
	if added > 0 {
		parts = append(parts, styleOK.Render(fmt.Sprintf("+%d", added)))
	}
	if deleted > 0 {
		parts = append(parts, styleErr.Render(fmt.Sprintf("−%d", deleted)))
	}
	return strings.Join(parts, " ")
}

func codeStyle(code string) lipgloss.Style {
	switch code {
	case "A", "?":
		return styleOK
	case "D", "U":
		return styleErr
	case "R", "C":
		return styleWork
	}
	return styleWarn
}

func ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
	return t.Format("Jan 2006")
}

// pollChanges schedules the next refresh of the changes views on screen,
// while any is visible. At most one is scheduled at a time.
func (m *Model) pollChanges() tea.Cmd {
	if m.changesPolling {
		return nil
	}
	visible := false
	for _, l := range m.tab().root.leaves() {
		if l.changes != nil {
			visible = true
		}
	}
	if !visible {
		return nil
	}
	m.changesPolling = true
	return tea.Tick(changesPollEvery, func(time.Time) tea.Msg { return changesPollMsg{} })
}
