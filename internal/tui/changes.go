package tui

import (
	"fmt"
	"reflect"
	"slices"
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
	// marked are the files space picked for the next commit, by path, and
	// hunks the single hunks picked inside a file's diff.
	marked map[string]bool
	hunks  map[string]*fileHunks
	// touched is when each file was last written, so the list can say which
	// ones an agent is working in rather than only what the totals came to.
	touched map[string]time.Time

	diffFile    string // "" when the file list is shown
	loadingDiff bool
	diffLive    bool // a background re-read of the open diff is on its way
	diff        []string
	diffErr     string
	diffScroll  int
	hunkSel     int // the hunk the diff's cursor is on
	// An open diff re-reads itself while an agent writes: fresh are the
	// lines the last read brought, marked until freshUntil, and followTo
	// is the first of them (1-based) to bring into view on the next render.
	fresh      map[int]bool
	freshUntil time.Time
	followTo   int
	noFollow   bool // the reader scrolled away, so new lines don't move the view
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
//
// A server that watches worktrees says so instead, within a debounce, so
// against one the poll is only a backstop for an event that went missing —
// and runs rarely enough to cost nothing.
const (
	changesPollEvery = 2 * time.Second
	changesBackstop  = 15 * time.Second
)

type changesPollMsg struct{}

// diffFreshFor is how long the lines a background re-read brought stay
// marked, so a glance at the diff says what the agent just wrote.
const diffFreshFor = 4 * time.Second

type diffMsg struct {
	projectID, branch, file string
	diff                    string
	err                     error
	live                    bool // a background re-read, not a reader's own
}

// changesCacheMax is how many branches' changes are kept, so stepping
// through a project's branches and back doesn't re-read git each time.
const changesCacheMax = 16

// changesFor is the view of a branch's changes, kept from last time when it
// was looked at before: known says so, and the caller refreshes it in the
// background instead of showing "reading the branch…" again.
func (m *Model) changesFor(machine, projectID, branch string) (cv *changesView, known bool) {
	key := machine + "|" + projectID + "|" + branch
	if m.changesCache == nil {
		m.changesCache = map[string]*changesView{}
	}
	cv, ok := m.changesCache[key]
	if !ok {
		cv = &changesView{machine: machine, projectID: projectID, branch: branch}
		m.changesCache[key] = cv
	}
	// A view that is still loading is kept as it is: a second look must not
	// throw away the read in flight and start another.
	known = cv.data != nil
	m.changesSeen = append(slices.DeleteFunc(m.changesSeen, func(k string) bool { return k == key }), key)
	for len(m.changesSeen) > changesCacheMax {
		if m.changesSeen[0] != key {
			delete(m.changesCache, m.changesSeen[0])
		}
		m.changesSeen = m.changesSeen[1:]
	}
	return cv, known
}

// forgetChanges drops a project's cached branches, e.g. when its git state
// changed underneath.
func (m *Model) forgetChanges(machine, projectID string) {
	prefix := machine + "|" + projectID + "|"
	for k := range m.changesCache {
		if strings.HasPrefix(k, prefix) {
			delete(m.changesCache, k)
		}
	}
	m.changesSeen = slices.DeleteFunc(m.changesSeen, func(k string) bool { return strings.HasPrefix(k, prefix) })
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
	cv.diffFile, cv.diff, cv.diffErr, cv.diffScroll, cv.hunkSel = file, nil, "", 0, 0
	cv.fresh, cv.freshUntil, cv.followTo, cv.noFollow = nil, time.Time{}, 0, false
	return cv.fetchDiff(m, file, false)
}

// refreshDiff re-reads the open diff, keeping it on screen (and scrolled)
// until the new one arrives.
func (cv *changesView) refreshDiff(m *Model) tea.Cmd {
	if cv.diffFile == "" {
		return nil
	}
	return cv.fetchDiff(m, cv.diffFile, false)
}

// liveDiff re-reads the open diff in the background, on every poll, so edits
// an agent is making show as they land. It cannot wait for the file list to
// change: rewriting a line in place leaves the branch's counts alone, and the
// diff on screen would quietly go stale.
func (cv *changesView) liveDiff(m *Model) tea.Cmd {
	if cv.diffFile == "" || cv.diffLive || cv.loadingDiff {
		return nil
	}
	return cv.fetchDiff(m, cv.diffFile, true)
}

func (cv *changesView) fetchDiff(m *Model, file string, live bool) tea.Cmd {
	if live {
		cv.diffLive = true
	} else {
		cv.loadingDiff = true
	}
	c, pid, branch := m.clientOf(cv.machine), cv.projectID, cv.branch
	return func() tea.Msg {
		var out proto.DiffResult
		if c == nil {
			return diffMsg{projectID: pid, branch: branch, file: file, err: errString("machine is offline"), live: live}
		}
		err := callCtx(c, proto.MethodProjectDiff, proto.DiffParams{ProjectID: pid, Branch: branch, File: file}, &out)
		return diffMsg{projectID: pid, branch: branch, file: file, diff: out.Diff, err: err, live: live}
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
		if cv.data != nil {
			// A file whose lines or state moved since the last read is being
			// written. The watcher's events say so sooner and more exactly;
			// this is what an older server leaves us.
			was := make(map[string]proto.FileChange, len(cv.data.Files))
			for _, f := range cv.data.Files {
				was[f.Path] = f
			}
			var moved []string
			for _, f := range msg.data.Files {
				if old, ok := was[f.Path]; !ok || old.Added != f.Added || old.Deleted != f.Deleted || old.Code != f.Code {
					moved = append(moved, f.Path)
				}
			}
			cv.touch(moved)
		}
		selected := ""
		if cv.data != nil && cv.sel < len(cv.data.Files) {
			selected = cv.data.Files[cv.sel].Path
		}
		changed = msg.poll && cv.data != nil
		cv.err = ""
		cv.data = &msg.data
		cv.sel = clamp(cv.sel, 0, max(len(msg.data.Files)-1, 0))
		present := map[string]bool{}
		for i, f := range msg.data.Files { // keep the selection on its file
			if f.Path == selected {
				cv.sel = i
			}
			present[f.Path] = true
		}
		for path := range cv.marked { // a committed or reverted file is no longer picked
			if !present[path] || msg.data.Worktree == "" {
				delete(cv.marked, path)
			}
		}
		return changed
	case diffMsg:
		if msg.projectID != cv.projectID || msg.branch != cv.branch || msg.file != cv.diffFile {
			return
		}
		if msg.live {
			cv.diffLive = false
		} else {
			cv.loadingDiff = false
		}
		if msg.err != nil {
			if msg.live { // a flaky background read keeps what is on screen
				return
			}
			cv.diffErr = msg.err.Error()
			return
		}
		prev := cv.diff
		cv.diffErr, cv.diff, cv.followTo = "", diffLines(msg.diff), 0
		if fresh := freshLines(prev, cv.diff); len(fresh) > 0 {
			cv.fresh, cv.freshUntil = fresh, time.Now().Add(diffFreshFor)
			first := len(cv.diff)
			for i := range fresh {
				first = min(first, i)
			}
			cv.followTo = first + 1
		}
		cv.pruneHunks(msg.file, cv.diff)
		_, hunks, _ := cv.hunksOf()
		cv.hunkSel = clamp(cv.hunkSel, 0, max(len(hunks)-1, 0))
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
			cv.diffScroll, cv.noFollow = cv.diffScroll-1, true
		case "down", "j":
			cv.diffScroll, cv.noFollow = cv.diffScroll+1, true
		case "pgup", "b":
			cv.diffScroll, cv.noFollow = cv.diffScroll-page, true
		case "pgdown", "f":
			cv.diffScroll, cv.noFollow = cv.diffScroll+page, true
		case "g", "home":
			cv.diffScroll, cv.noFollow = 0, false
		case "G", "end":
			cv.diffScroll, cv.noFollow = len(cv.diff), true
		case "F":
			cv.noFollow = !cv.noFollow
		case "R":
			return false, cv.refreshDiff(m)
		case "tab":
			return true, nil
		case "y":
			return false, copyText(strings.Join(cv.diff, "\n") + "\n")
		case " ", "x":
			cv.markHunk(m, h)
		case "n", "]":
			cv.noFollow = true
			cv.toHunk(m, cv.hunkSel+1, h)
		case "N", "[":
			cv.noFollow = true
			cv.toHunk(m, cv.hunkSel-1, h)
		case "c":
			return false, m.openCommit(cv.target(), cv.selection())
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
	case " ":
		if files > 0 && cv.data.Worktree != "" {
			path := cv.data.Files[cv.sel].Path
			if cv.marked == nil {
				cv.marked = map[string]bool{}
			}
			if cv.marked[path] {
				delete(cv.marked, path)
			} else {
				cv.marked[path] = true
			}
			cv.sel = clamp(cv.sel+1, 0, files-1)
		}
	case "c":
		return false, m.openCommit(cv.target(), cv.selection())
	case "P":
		return false, m.pushBranch(cv.target())
	case "p":
		return false, m.openPullRequest(cv.target())
	case "M":
		return false, m.openMerge(cv.target())
	case "D":
		return false, m.discardBranch(cv.target())
	case "A":
		return false, m.openCompare(cv.target())
	}
	return false, nil
}

// markHunk marks the hunk the cursor is on, for a file being committed by
// hunks rather than whole.
func (cv *changesView) markHunk(m *Model, h int) {
	_, hunks, _ := cv.hunksOf()
	switch {
	case cv.data == nil || cv.data.Worktree == "":
		m.setFlash("only a checked-out branch's own changes can be committed", true)
	case len(hunks) == 0:
		m.setFlash("this diff has no hunks to mark", true)
	default:
		cv.toggleHunk(cv.hunkSel)
		cv.toHunk(m, cv.hunkSel+1, h) // on to the next, as marking a file does
	}
}

// toHunk moves the diff's cursor to hunk i and scrolls it into view.
func (cv *changesView) toHunk(m *Model, i, h int) {
	_, hunks, at := cv.hunksOf()
	if len(hunks) == 0 {
		return
	}
	cv.hunkSel = clamp(i, 0, len(hunks)-1)
	start := at[cv.hunkSel]
	if start < cv.diffScroll || start >= cv.diffScroll+max(h-2, 1) {
		cv.diffScroll = start
	}
}

func (cv *changesView) target() harvestTarget {
	return harvestTarget{machine: cv.machine, projectID: cv.projectID, branch: cv.branch}
}

// markedPaths lists the marked files in the order shown, with a rename's
// old path too so the commit takes both sides of it.
func (cv *changesView) markedPaths() []string {
	if cv.data == nil {
		return nil
	}
	var paths []string
	for _, f := range cv.data.Files {
		if cv.marked[f.Path] {
			if f.OrigPath != "" {
				paths = append(paths, f.OrigPath)
			}
			paths = append(paths, f.Path)
		}
	}
	return paths
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
		return cv.renderDiff(m, w, h)
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
		return append(lines, styleWork.Render(spinner[m.spin%len(spinner)])+styleMuted.Render(" reading the branch…"))
	}

	title := "Uncommitted changes"
	if cv.data.Worktree == "" {
		title = "Files changed since " + cv.data.Base
	}
	// Tracked lines only, so the total matches the branch's row in the
	// tree; untracked files are counted as files instead, since their whole
	// contents would dwarf the edits.
	added, deleted, untracked := 0, 0, 0
	for _, f := range cv.data.Files {
		if f.Code == "?" {
			untracked++
			continue
		}
		added += f.Added
		deleted += f.Deleted
	}
	header := fmt.Sprintf("%s  %s", styleBold.Render(title), styleMuted.Render(fmt.Sprintf("%d files", len(cv.data.Files))))
	if len(cv.data.Files) > untracked {
		header += "  " + diffStat(added, deleted)
	}
	if untracked > 0 {
		header += styleMuted.Render(fmt.Sprintf("  · %d untracked", untracked))
	}
	if n := len(cv.marked); n > 0 {
		header += styleOK.Render(fmt.Sprintf("  · %d marked", n))
	}
	if n := cv.changingNow(); n > 0 {
		header += styleWarn.Render(fmt.Sprintf("  · %d changing", n))
	}
	lines = append(lines, header)

	// Room for the files' "… more" line and the commits below: a blank line,
	// their heading and up to seven lines (six commits and their own "… more").
	listH := max(h-cv.filesTop(m)-min(len(cv.data.Commits), 7)-3, 3)
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
		if cv.marked[f.Path] {
			stat = styleOK.Render("✓ ") + stat
		}
		name := f.Path
		if f.OrigPath != "" {
			name = f.OrigPath + " → " + f.Path
		}
		// The cursor keeps the first column and the "being written" mark the
		// second, beside the name: on a wide pane the right-hand numbers are
		// a screen away and a mark there goes unseen.
		live := cv.changing(f.Path)
		mark, cursor := " ", " "
		if live {
			mark = "▌"
		}
		if i == cv.sel {
			cursor = "▸"
		}
		plain := fmt.Sprintf("%s%s %s %s", cursor, mark, f.Code, name)
		switch {
		case i == cv.sel:
			sel := styleSelDim
			if m.focus == focusMain {
				sel = styleSel
			}
			lines = append(lines, spread(sel.Render(ansi.Truncate(plain, w-12, "…")), stat, w))
		case live:
			// The whole row is washed, so which file is being written is
			// plain from across the screen. Colours inside would reset the
			// background, so the row is rendered without them.
			lines = append(lines, styleLive.Render(spread(plain, ansi.Strip(stat), w)))
		default:
			left := fmt.Sprintf("%s%s %s %s", cursor, mark, codeStyle(f.Code).Render(padRight(f.Code, 1)), name)
			lines = append(lines, spread(left, stat, w))
		}
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

func (cv *changesView) renderDiff(m Model, w, h int) []string {
	right := "esc back · ↑↓ scroll"
	if cv.diff != nil && cv.loadingDiff {
		right = "refreshing… · " + right
	}
	working := cv.data != nil && cv.data.Worktree != "" // only a checked-out branch changes under us
	if working {
		if cv.noFollow {
			right = "F follow · " + right
		} else {
			right = "following · " + right
		}
	}
	_, hunks, at := cv.hunksOf()
	marks := map[int]int{} // diff line → hunk index
	for i, line := range at {
		marks[line] = i
	}
	title := cv.diffFile
	fresh := cv.fresh
	if !cv.freshUntil.After(time.Now()) {
		fresh = nil
	}
	if n := len(fresh); n > 0 {
		title += styleWarn.Render(fmt.Sprintf("  %d line%s just changed", n, plural(n)))
	}
	if working && len(hunks) > 0 {
		right = "space mark · n next hunk · c commit · " + right
		if fh := cv.hunks[cv.diffFile]; fh != nil {
			title += styleOK.Render(fmt.Sprintf("  %d of %d hunks marked", len(fh.marked), len(hunks)))
		}
	}
	lines := []string{spread(styleBold.Render(title), styleMuted.Render(right), w)}
	switch {
	case cv.diffErr != "":
		return append(lines, styleErr.Render(cv.diffErr))
	case cv.diff == nil:
		return append(lines, styleWork.Render(spinner[m.spin%len(spinner)])+styleMuted.Render(" reading the diff…"))
	case len(cv.diff) == 0:
		// A live re-read found the file back as the branch has it: committed
		// underneath, or the agent undid what it wrote.
		return append(lines, styleMuted.Render("  no changes in this file"))
	}
	// Follow what just arrived, unless the reader scrolled somewhere.
	if cv.followTo > 0 {
		if start := cv.followTo - 1; !cv.noFollow && (start < cv.diffScroll || start >= cv.diffScroll+max(h-1, 1)) {
			cv.diffScroll = clamp(start-1, 0, max(len(cv.diff)-(h-1), 0))
		}
		cv.followTo = 0
	}
	for i := cv.diffScroll; i < len(cv.diff) && len(lines) < h; i++ {
		l := strings.ReplaceAll(cv.diff[i], "\t", "    ")
		l = ansi.Strip(l) // diffs are data; never let them drive the terminal
		prefix := ""
		switch {
		case strings.HasPrefix(l, "+++"), strings.HasPrefix(l, "---"), strings.HasPrefix(l, "diff "), strings.HasPrefix(l, "index "):
			l = styleMuted.Render(l)
		case strings.HasPrefix(l, "@@"):
			if hunk, ok := marks[i]; ok && len(hunks) > 0 && cv.data != nil && cv.data.Worktree != "" {
				cursor, mark := " ", " "
				if hunk == cv.hunkSel {
					cursor = "▸"
				}
				if cv.markedHunk(hunk) {
					mark = styleOK.Render("✓")
				}
				prefix = cursor + mark
			}
			l = styleWork.Render(l)
		case strings.HasPrefix(l, "+"):
			l = styleOK.Render(l)
		case strings.HasPrefix(l, "-"):
			l = styleErr.Render(l)
		}
		gutter := " "
		if fresh[i] {
			gutter = styleWarn.Render("▌")
		}
		lines = append(lines, gutter+prefix+l)
	}
	return lines
}

// touch marks files as being written now. The paths are relative to the
// worktree, named the same way the file list names them.
func (cv *changesView) touch(paths []string) {
	if len(paths) == 0 {
		return
	}
	if cv.touched == nil {
		cv.touched = map[string]time.Time{}
	}
	now := time.Now()
	for _, p := range paths {
		cv.touched[p] = now
	}
	for p, at := range cv.touched { // keep only what is still worth showing
		if now.Sub(at) > diffFreshFor {
			delete(cv.touched, p)
		}
	}
}

// changing reports whether a file was written in the last few seconds.
func (cv *changesView) changing(path string) bool {
	at, ok := cv.touched[path]
	return ok && time.Since(at) < diffFreshFor
}

// changingNow is how many of the files shown are being written.
func (cv *changesView) changingNow() int {
	n := 0
	if cv.data == nil {
		return 0
	}
	for _, f := range cv.data.Files {
		if cv.changing(f.Path) {
			n++
		}
	}
	return n
}

// diffLines splits a diff into lines, with an empty diff no lines at all
// rather than one blank one.
func diffLines(diff string) []string {
	text := strings.TrimRight(diff, "\n")
	if text == "" {
		return []string{}
	}
	return strings.Split(text, "\n")
}

// freshLines reports which lines of next are content prev did not have, so a
// re-read can mark what an agent just wrote. Lines are matched by text and
// each one of prev is spent once, so moved lines are not mistaken for new
// ones but a line written twice counts twice. Only added and removed lines
// count: context and headers shift around without anything having changed.
func freshLines(prev, next []string) map[int]bool {
	if prev == nil {
		return nil // the first read of a file did not just change
	}
	left := make(map[string]int, len(prev))
	for _, l := range prev {
		left[l]++
	}
	var fresh map[int]bool
	for i, l := range next {
		if len(l) == 0 || (l[0] != '+' && l[0] != '-') {
			continue
		}
		if strings.HasPrefix(l, "+++") || strings.HasPrefix(l, "---") {
			continue
		}
		if left[l] > 0 {
			left[l]--
			continue
		}
		if fresh == nil {
			fresh = map[int]bool{}
		}
		fresh[i] = true
	}
	return fresh
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
			cv.diffScroll, cv.noFollow = clamp(cv.diffScroll+delta, 0, max(len(cv.diff)-(h-2), 0)), true
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
	return tea.Tick(m.changesPollEvery(), func(time.Time) tea.Msg { return changesPollMsg{} })
}

// changesPollEvery is how long to wait before re-reading the branches on
// screen. Every machine showing one has to be watching its worktrees for the
// slow backstop to be safe; one older server and they all keep polling.
func (m *Model) changesPollEvery() time.Duration {
	watched := false
	for _, l := range m.tab().root.leaves() {
		cv := l.changes
		if cv == nil {
			continue
		}
		c := m.clientOf(cv.machine)
		if c == nil || len(c.MissingCapabilities([]string{proto.CapWorktreeWatch})) > 0 {
			return changesPollEvery
		}
		// A server that watches may still not be watching *this* worktree:
		// one too big for its budget is left to polling, and backing off
		// for it would mean hearing about its edits a great deal later.
		if cv.data == nil || !cv.data.Watched {
			return changesPollEvery
		}
		watched = true
	}
	if !watched {
		return changesPollEvery
	}
	return changesBackstop
}
