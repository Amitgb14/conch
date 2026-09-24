package tui

import (
	"fmt"
	"path"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
)

// The file explorer browses one checkout of a project — the main one, or a
// branch's worktree — as a lazily expanded tree beside a preview of the
// selected file. It is not an editor: it answers "what did the agent just
// write?" without leaving conch, and a to hand a file's path to the agent
// working in that checkout.
//
// Paths inside the view are slash-separated and relative to the checkout,
// as the server lists them; "" is the checkout itself.

const (
	filesTop = 3 // title, path, rule
	// filesWideAt is the width from which the preview sits beside the tree;
	// below it the preview takes the whole body when asked for.
	filesWideAt  = 100
	filesTreeMin = 28
	filesTreeMax = 45
)

// filesTooOld is said when a machine's server cannot list files: an older
// one answers with folders only, which would look like an empty repository.
const filesTooOld = "this machine's conch is too old to browse files — update it"

type filesListMsg struct {
	machine, root, rel string
	list               proto.FSList
	err                error
}

type filesReadMsg struct {
	machine, root, rel string
	res                proto.FSReadResult
	err                error
}

// filesDir is one listed folder.
type filesDir struct {
	entries   []proto.FSEntry
	truncated bool
	loading   bool
	err       string
}

// filesPreview is the file on the right.
type filesPreview struct {
	rel     string
	res     *proto.FSReadResult
	loading bool
	err     string
}

// filesLine is one line of the tree: an entry, or a note under a folder
// ("reading…", "empty") that the selection passes over.
type filesLine struct {
	rel   string
	e     proto.FSEntry
	depth int
	note  string
}

type filesCrumb struct {
	x0, x1 int
	rel    string
}

type filesView struct {
	machine, projectID string
	want               string // the branch asked for; "" is the main checkout
	branch             string // what is checked out at root
	root               string
	note               string // why root is not the checkout asked for
	err                string // why nothing can be listed

	dirs    map[string]*filesDir
	open    map[string]bool
	sel     int
	scroll  int
	selPath string
	hidden  bool // dotfiles
	ignored bool // what git ignores
	filter  string
	typing  bool

	reading    bool // the preview has the keys, and the body when narrow
	prev       filesPreview
	prevScroll int

	// From the last render, for the mouse and for reading ahead.
	wide   bool
	treeW  int
	crumbs []filesCrumb
	status string // the checkout's git status last seen, to notice a commit
}

// filesCheckout is the checkout to browse for a branch: its worktree, or the
// main checkout when it has none (and a note saying so).
func (m Model) filesCheckout(mid, pid, branch string) (root, checkedOut, note string) {
	proj := m.project(mid, pid)
	if proj == nil {
		return "", "", ""
	}
	if branch != "" {
		for _, wt := range proj.Worktrees {
			if wt.Branch == branch {
				return wt.Path, branch, ""
			}
		}
		note = branch + " has no worktree · showing the main checkout"
	}
	for _, wt := range proj.Worktrees {
		if wt.Main {
			return wt.Path, wt.Branch, note
		}
	}
	return proj.Path, "", note
}

func (m *Model) newFilesView(v viewRef) *filesView {
	fv := &filesView{machine: v.Machine, projectID: v.ProjectID, want: v.Branch}
	fv.root, fv.branch, fv.note = m.filesCheckout(v.Machine, v.ProjectID, v.Branch)
	fv.reset()
	return fv
}

func (fv *filesView) reset() {
	fv.dirs, fv.open = map[string]*filesDir{}, map[string]bool{}
	fv.sel, fv.scroll, fv.selPath = 0, 0, ""
	fv.prev, fv.prevScroll, fv.reading = filesPreview{}, 0, false
}

// load lists the checkout's top, or says why it can't.
func (fv *filesView) load(m *Model) tea.Cmd {
	fv.err = ""
	c := m.clientOf(fv.machine)
	switch {
	case fv.root == "":
		fv.err = "this project is gone"
		return nil
	case c == nil:
		fv.err = m.offlineText(fv.machine)
		return nil
	case len(c.MissingCapabilities([]string{proto.CapFSFiles})) > 0:
		fv.err = filesTooOld
		return nil
	}
	fv.status = m.filesStatusSig(fv)
	return fv.list(m, "")
}

// list reads one folder, keeping what it showed until the answer comes.
func (fv *filesView) list(m *Model, rel string) tea.Cmd {
	c := m.clientOf(fv.machine)
	if c == nil {
		return nil
	}
	d := fv.dirs[rel]
	if d == nil {
		d = &filesDir{}
		fv.dirs[rel] = d
	}
	d.loading = true
	mid, root := fv.machine, fv.root
	params := proto.FSListParams{Root: root, Path: rel, Files: true, Hidden: fv.hidden, Ignored: fv.ignored}
	return func() tea.Msg {
		var out proto.FSList
		err := callCtx(c, proto.MethodFSList, params, &out)
		return filesListMsg{machine: mid, root: root, rel: rel, list: out, err: err}
	}
}

// refresh re-lists every folder on screen; folders read before but closed
// now are forgotten, to be read again when opened.
func (fv *filesView) refresh(m *Model) tea.Cmd {
	if fv.err != "" || m.clientOf(fv.machine) == nil {
		return fv.load(m)
	}
	var cmds []tea.Cmd
	for rel := range fv.dirs {
		if rel == "" || fv.shown(rel) {
			cmds = append(cmds, fv.list(m, rel))
		} else {
			delete(fv.dirs, rel)
		}
	}
	if fv.prev.rel != "" {
		cmds = append(cmds, fv.read(m, fv.prev.rel))
	}
	return tea.Batch(cmds...)
}

// shown reports whether a folder is open and so are all above it.
func (fv *filesView) shown(rel string) bool {
	for p := rel; p != "" && p != "."; p = path.Dir(p) {
		if !fv.open[p] {
			return false
		}
	}
	return true
}

func (fv *filesView) receiveList(msg filesListMsg) {
	d := fv.dirs[msg.rel]
	if d == nil {
		return // closed and forgotten meanwhile
	}
	d.loading = false
	if msg.err != nil {
		d.err = msg.err.Error()
		return
	}
	d.err, d.entries, d.truncated = "", msg.list.Entries, msg.list.Truncated
}

// read fetches a file for the preview. The same file read again keeps its
// content on screen until the answer comes, as the diff view does.
func (fv *filesView) read(m *Model, rel string) tea.Cmd {
	c := m.clientOf(fv.machine)
	if c == nil {
		fv.prev = filesPreview{rel: rel, err: m.offlineText(fv.machine)}
		return nil
	}
	if len(c.MissingCapabilities([]string{proto.CapFSRead})) > 0 {
		fv.prev = filesPreview{rel: rel, err: "this machine's conch is too old to preview files — update it"}
		return nil
	}
	if fv.prev.rel != rel {
		fv.prev, fv.prevScroll = filesPreview{rel: rel}, 0
	}
	fv.prev.loading = true
	mid, root := fv.machine, fv.root
	return func() tea.Msg {
		var out proto.FSReadResult
		err := callCtx(c, proto.MethodFSRead, proto.FSReadParams{Root: root, Path: rel}, &out)
		return filesReadMsg{machine: mid, root: root, rel: rel, res: out, err: err}
	}
}

func (fv *filesView) receiveRead(msg filesReadMsg) {
	if msg.rel != fv.prev.rel {
		return // the selection moved on
	}
	fv.prev.loading = false
	if msg.err != nil {
		fv.prev.err, fv.prev.res = msg.err.Error(), nil
		return
	}
	res := msg.res
	fv.prev.err, fv.prev.res = "", &res
}

// filesStatusSig is the checkout's git status as the project last reported
// it: when it moves — a commit, a checkout — the listed statuses are stale.
func (m Model) filesStatusSig(fv *filesView) string {
	if proj := m.project(fv.machine, fv.projectID); proj != nil {
		for _, wt := range proj.Worktrees {
			if wt.Path == fv.root && wt.Status != nil {
				return fmt.Sprintf("%s %+v", wt.Head, *wt.Status)
			} else if wt.Path == fv.root {
				return wt.Head
			}
		}
	}
	return ""
}

// changedCount is how many files are uncommitted in the checkout.
func (m Model) filesChanged(fv *filesView) int {
	if proj := m.project(fv.machine, fv.projectID); proj != nil {
		for _, wt := range proj.Worktrees {
			if wt.Path == fv.root && wt.Status != nil {
				return wt.Status.Files
			}
		}
	}
	return 0
}

// lines is the tree as shown: open folders expanded, or with a filter every
// loaded entry that matches and the folders leading to it.
func (fv *filesView) lines() []filesLine {
	var out []filesLine
	filtering := strings.TrimSpace(fv.filter) != ""
	var walk func(rel string, depth int)
	walk = func(rel string, depth int) {
		d := fv.dirs[rel]
		switch {
		case d == nil || (d.entries == nil && d.loading):
			out = append(out, filesLine{depth: depth, note: "reading…"})
			return
		case d.entries == nil && d.err != "":
			out = append(out, filesLine{depth: depth, note: d.err})
			return
		}
		n := 0
		for _, e := range d.entries {
			child := joinRel(rel, e.Name)
			if filtering && !fv.matchTree(child, e) {
				continue
			}
			n++
			out = append(out, filesLine{rel: child, e: e, depth: depth})
			if e.Dir && ((!filtering && fv.open[child]) || (filtering && fv.dirs[child] != nil)) {
				walk(child, depth+1)
			}
		}
		switch {
		case d.truncated && !filtering:
			out = append(out, filesLine{depth: depth, note: fmt.Sprintf("… only the first %d are shown", len(d.entries))})
		case n == 0 && !filtering:
			out = append(out, filesLine{depth: depth, note: "empty"})
		}
	}
	walk("", 0)
	if filtering && len(out) == 0 {
		out = append(out, filesLine{note: "nothing loaded matches " + fv.filter})
	}
	return out
}

func joinRel(dir, name string) string {
	if dir == "" {
		return name
	}
	return dir + "/" + name
}

// matchTree reports whether an entry, or anything loaded under it, answers
// to the filter.
func (fv *filesView) matchTree(rel string, e proto.FSEntry) bool {
	if fuzzy(e.Name, fv.filter) {
		return true
	}
	if d := fv.dirs[rel]; e.Dir && d != nil {
		for _, c := range d.entries {
			if fv.matchTree(joinRel(rel, c.Name), c) {
				return true
			}
		}
	}
	return false
}

// fuzzy reports whether the letters of pattern appear in name in order,
// ignoring case and spaces.
func fuzzy(name, pattern string) bool {
	name = strings.ToLower(name)
	for _, r := range strings.ToLower(pattern) {
		if r == ' ' {
			continue
		}
		i := strings.IndexRune(name, r)
		if i < 0 {
			return false
		}
		name = name[i+len(string(r)):]
	}
	return true
}

// place keeps the selection on the path it was on, and off notes.
func (fv *filesView) place(list []filesLine) {
	if len(list) == 0 {
		fv.sel = 0
		return
	}
	if fv.selPath != "" {
		for i, l := range list {
			if l.note == "" && l.rel == fv.selPath {
				fv.sel = i
				return
			}
		}
	}
	fv.sel = clamp(fv.sel, 0, len(list)-1)
	if list[fv.sel].note != "" {
		if i := nextEntry(list, fv.sel, 1); i >= 0 {
			fv.sel = i
		} else if i := nextEntry(list, fv.sel, -1); i >= 0 {
			fv.sel = i
		}
	}
	if list[fv.sel].note == "" {
		fv.selPath = list[fv.sel].rel
	}
}

func nextEntry(list []filesLine, i, d int) int {
	for ; i >= 0 && i < len(list); i += d {
		if list[i].note == "" {
			return i
		}
	}
	return -1
}

func (fv *filesView) selected(list []filesLine) (filesLine, bool) {
	if fv.sel >= 0 && fv.sel < len(list) && list[fv.sel].note == "" {
		return list[fv.sel], true
	}
	return filesLine{}, false
}

// ---- rendering ----

func (fv *filesView) render(m Model, w, h int) []string {
	if w < 1 || h < 1 {
		return nil
	}
	fv.wide = w >= filesWideAt
	fv.treeW = w
	if fv.wide {
		fv.treeW = clamp(w*2/5, filesTreeMin, filesTreeMax)
	}
	title := styleBold.Render("Files") + "  " + m.filesTitle(fv)
	hint := "enter open · a → agent · y copy · / find · esc tree"
	if fv.typing || fv.filter != "" {
		cursor := ""
		if fv.typing {
			cursor = "█"
			hint = "enter keep · esc clear"
		}
		title += styleMuted.Render("  / ") + fv.filter + cursor
	} else if fv.reading {
		hint = "↑↓ scroll · e edit · a → agent · esc back"
	}
	lines := []string{spread(title, styleMuted.Render(hint), w), fv.crumbLine(m, w)}
	if fv.wide {
		lines = append(lines, styleMuted.Render(strings.Repeat("─", fv.treeW)+"┬"+strings.Repeat("─", max(w-fv.treeW-1, 0))))
	} else {
		lines = append(lines, styleMuted.Render(strings.Repeat("─", w)))
	}
	bodyH := h - filesTop
	if bodyH <= 0 {
		return exactly(lines, h)
	}
	if fv.err != "" {
		return append(lines, styleErr.Render(fit("  "+fv.err, w)))
	}
	list := fv.lines()
	fv.place(list)
	if !fv.wide && fv.reading {
		return append(lines, fv.previewLines(m, w, bodyH)...)
	}
	if fv.sel < fv.scroll {
		fv.scroll = fv.sel
	}
	if fv.sel >= fv.scroll+bodyH {
		fv.scroll = fv.sel - bodyH + 1
	}
	fv.scroll = clamp(fv.scroll, 0, max(len(list)-bodyH, 0))
	var right []string
	if fv.wide {
		right = fv.previewLines(m, w-fv.treeW-1, bodyH)
	}
	focused := m.focus == focusMain && !fv.reading
	for i := 0; i < bodyH; i++ {
		left := strings.Repeat(" ", fv.treeW)
		if j := fv.scroll + i; j < len(list) {
			left = fv.treeLine(m, list[j], fv.treeW, j == fv.sel, focused)
		}
		if !fv.wide {
			lines = append(lines, left)
			continue
		}
		r := ""
		if i < len(right) {
			r = right[i]
		}
		lines = append(lines, left+styleMuted.Render("│")+fit(r, w-fv.treeW-1))
	}
	return lines
}

// filesTitle is the project and the checkout, and why it is not the one
// asked for.
func (m Model) filesTitle(fv *filesView) string {
	name := "?"
	if proj := m.project(fv.machine, fv.projectID); proj != nil {
		name = proj.Name
	}
	if fv.machine != localMachine {
		if mach := m.machine(fv.machine); mach != nil {
			name = mach.label + " · " + name
		}
	}
	if fv.branch != "" {
		name += " · " + fv.branch
	}
	if fv.note != "" {
		name += styleWarn.Render("  " + fv.note)
	}
	return name
}

// crumbLine is the checkout's path and then the selection's folders, each a
// place to click back to.
func (fv *filesView) crumbLine(m Model, w int) string {
	fv.crumbs = fv.crumbs[:0]
	root := m.tildify(fv.machine, fv.root)
	s := " " + root
	fv.crumbs = append(fv.crumbs, filesCrumb{x0: 1, x1: 1 + ansi.StringWidth(root), rel: ""})
	if dir := path.Dir(fv.selPath); fv.selPath != "" && dir != "." {
		acc := ""
		for _, seg := range strings.Split(dir, "/") {
			acc = joinRel(acc, seg)
			s += styleMuted.Render(" › ")
			x0 := ansi.StringWidth(s)
			s += seg
			fv.crumbs = append(fv.crumbs, filesCrumb{x0: x0, x1: x0 + ansi.StringWidth(seg), rel: acc})
		}
	}
	if n := m.filesChanged(fv); n > 0 {
		s += styleMuted.Render(fmt.Sprintf(" · %d changed", n))
	}
	return fit(s, w)
}

// treeLine draws one line of the tree, from its parts: the name gives way
// first, and a selected line is built plain rather than sliced once drawn.
func (fv *filesView) treeLine(m Model, l filesLine, w int, selected, focused bool) string {
	indent := strings.Repeat("  ", min(l.depth, max((w-8)/2, 0)))
	if l.note != "" {
		return styleMuted.Render(fit(" "+indent+"  "+l.note, w))
	}
	e := l.e
	marker := " "
	if e.Dir {
		marker = "▸"
		if fv.open[l.rel] || (fv.filter != "" && fv.dirs[l.rel] != nil) {
			marker = "▾"
		}
	}
	mode := iconMode(m.cfg.UI.Icons)
	icon := renderIcon(iconFor(e.Name, e.Dir, marker == "▾", e.Broken), mode, selected)
	if icon != "" {
		icon += " "
	}
	name := e.Name
	if e.Dir {
		name += "/"
	}
	if e.Symlink {
		name += " →"
	}
	status := e.Status
	if status == "" {
		status = " "
	}
	prefix := " " + indent + marker + " "
	room := max(w-ansi.StringWidth(prefix)-ansi.StringWidth(icon)-3, 1)
	name = ansi.Truncate(name, room, "…")
	pad := strings.Repeat(" ", max(w-ansi.StringWidth(prefix)-ansi.StringWidth(icon)-ansi.StringWidth(name)-2, 0))
	if selected {
		sel := styleSel
		if !focused {
			sel = styleSelDim
		}
		return sel.Render(fit(prefix+ansi.Strip(icon)+name+pad+status+" ", w))
	}
	nameStyle := lipgloss.NewStyle()
	switch {
	case e.Ignored || e.Broken:
		nameStyle = styleMuted
	case e.Status != "":
		nameStyle = codeStyle(e.Status).UnsetBold()
	}
	return fit(styleMuted.Render(prefix)+icon+nameStyle.Render(name)+pad+codeStyle(e.Status).Render(status)+" ", w)
}

// previewLines draws the selected file, or says what the selection is.
func (fv *filesView) previewLines(m Model, w, h int) []string {
	if w < 1 || h < 1 {
		return nil
	}
	list := fv.lines()
	l, ok := fv.selected(list)
	if !ok {
		return nil
	}
	if l.e.Dir {
		out := []string{styleBold.Render(fit(" "+l.e.Name+"/", w))}
		if d := fv.dirs[l.rel]; d != nil && d.entries != nil {
			out = append(out, styleMuted.Render(fit(" "+count(len(d.entries), "entry"), w)))
		} else {
			out = append(out, styleMuted.Render(fit(" enter opens it", w)))
		}
		return out
	}
	p := fv.prev
	if p.rel != l.rel {
		hint := " enter shows it"
		if fv.wide {
			hint = " reading…"
		}
		return []string{styleBold.Render(fit(" "+l.e.Name, w)), styleMuted.Render(fit(hint, w))}
	}
	head := " " + path.Base(p.rel)
	var body []string
	switch {
	case p.err != "":
		body = []string{styleErr.Render(fit(" "+p.err, w))}
	case p.res == nil:
		body = []string{styleMuted.Render(fit(" reading…", w))}
	case p.res.Binary:
		kind := "binary"
		if p.res.MIME != "" {
			kind += " · " + p.res.MIME
		}
		body = []string{"", styleMuted.Render(fit(fmt.Sprintf("  %s · %s", kind, sizeText(p.res.Size)), w)),
			styleMuted.Render(fit("  e opens it in $EDITOR", w))}
	default:
		text := strings.TrimSuffix(p.res.Data, "\n")
		src := strings.Split(text, "\n")
		if p.res.Data == "" {
			src = nil
		}
		more := ""
		if p.res.Truncated {
			more = "+"
		}
		head += styleMuted.Render(fmt.Sprintf(" · %d%s line%s", len(src), more, plural(len(src))))
		numW := len(fmt.Sprint(len(src)))
		fv.prevScroll = clamp(fv.prevScroll, 0, max(len(src)-(h-1), 0))
		for i := fv.prevScroll; i < len(src) && len(body) < h-1; i++ {
			num := styleMuted.Render(fmt.Sprintf(" %*d ", numW, i+1))
			body = append(body, fit(num+" "+previewText(src[i], max(w-numW-3, 1)), w))
		}
		if len(src) == 0 {
			body = append(body, styleMuted.Render(fit("  (empty)", w)))
		}
		if p.res.Truncated && len(body) < h-1 {
			body = append(body, styleMuted.Render(fit(fmt.Sprintf("  … the first %s of %s · e opens it in $EDITOR",
				sizeText(int64(len(p.res.Data))), sizeText(p.res.Size)), w)))
		}
	}
	if l.e.Status != "" {
		head += styleMuted.Render(" · ") + codeStyle(l.e.Status).Render(statusWord(l.e.Status))
	}
	if p.loading && p.res != nil {
		head += styleMuted.Render(" · reading…")
	}
	return append([]string{styleBold.Render(fit(head, w))}, body...)
}

// previewText makes a line of a file safe to draw in w cells: tabs become
// spaces and control characters — an escape, a C1 byte that ends a title —
// become dots, so a file cannot move the cursor or retitle the terminal.
func previewText(s string, w int) string {
	var b strings.Builder
	col := 0
	for _, r := range s {
		switch {
		case r == '\t':
			n := 4 - col%4
			b.WriteString(strings.Repeat(" ", n))
			col += n
			continue
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0):
			r = '·'
		}
		b.WriteRune(r)
		col++
		if col > w+4 {
			break // the rest would be cut anyway
		}
	}
	return ansi.Truncate(b.String(), w, "…")
}

func statusWord(code string) string {
	switch code {
	case "M", "T":
		return "modified"
	case "A":
		return "added"
	case "D":
		return "deleted"
	case "R":
		return "renamed"
	case "C":
		return "copied"
	case "U":
		return "conflict"
	case "?":
		return "untracked"
	}
	return code
}

func sizeText(n int64) string {
	switch {
	case n < 1<<10:
		return fmt.Sprintf("%d B", n)
	case n < 1<<20:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	case n < 1<<30:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
}

// ---- keys ----

func (fv *filesView) filterKey(k tea.KeyMsg) {
	switch k.String() {
	case "esc":
		fv.filter, fv.typing = "", false
	case "enter":
		fv.typing = false
	case "backspace":
		if r := []rune(fv.filter); len(r) > 0 {
			fv.filter = string(r[:len(r)-1])
		}
	default:
		if k.Type == tea.KeyRunes {
			fv.filter += string(k.Runes)
		} else if k.Type == tea.KeySpace {
			fv.filter += " "
		}
	}
}

func (fv *filesView) key(m *Model, k tea.KeyMsg) (back bool, cmd tea.Cmd) {
	if fv.typing {
		fv.filterKey(k)
		return false, nil
	}
	if fv.err != "" {
		switch k.String() {
		case "r":
			return false, fv.load(m)
		case "esc", "q", "left", "h", "tab":
			return true, nil
		}
		return false, nil
	}
	list := fv.lines()
	fv.place(list)
	l, ok := fv.selected(list)

	// What the selected file is for, whether the tree or the preview has
	// the keys.
	switch k.String() {
	case "y":
		if ok {
			return false, copyText(path.Join(fv.root, l.rel))
		}
		return false, nil
	case "a":
		if ok {
			return false, m.filesToAgent(fv, l)
		}
		return false, nil
	case "e":
		if ok && !l.e.Dir {
			return false, m.filesEdit(fv, l.rel)
		}
		return false, nil
	case "d":
		if ok {
			return false, m.filesDiff(fv, l)
		}
		return false, nil
	case "r":
		m.setFlash("reading the checkout again", false)
		return false, fv.refresh(m)
	case "w":
		return false, m.filesNextCheckout(fv)
	case ".":
		fv.hidden = !fv.hidden
		return false, fv.relist(m)
	case "i":
		fv.ignored = !fv.ignored
		return false, fv.relist(m)
	}

	if fv.reading {
		page := max(m.height-8, 1)
		switch k.String() {
		case "esc", "q", "left", "h", "enter", "tab":
			fv.reading = false
		case "up", "k":
			fv.prevScroll--
		case "down", "j":
			fv.prevScroll++
		case "pgup":
			fv.prevScroll -= page
		case "pgdown", " ":
			fv.prevScroll += page
		case "home", "g":
			fv.prevScroll = 0
		case "end", "G":
			fv.prevScroll = 1 << 30 // clamped when drawn
		}
		fv.prevScroll = max(fv.prevScroll, 0)
		return false, nil
	}

	move := func(d, n int) {
		for ; n > 0; n-- {
			i := nextEntry(list, fv.sel+d, d)
			if i < 0 {
				return
			}
			fv.sel = i
		}
	}
	switch k.String() {
	case "esc":
		if fv.filter != "" {
			fv.filter = ""
			return false, nil
		}
		return true, nil
	case "q", "tab":
		return true, nil
	case "/":
		fv.typing = true
		return false, nil
	case "up", "k":
		move(-1, 1)
	case "down", "j":
		move(1, 1)
	case "pgup":
		move(-1, 10)
	case "pgdown":
		move(1, 10)
	case "home", "g":
		if i := nextEntry(list, 0, 1); i >= 0 {
			fv.sel = i
		}
	case "end", "G":
		if i := nextEntry(list, len(list)-1, -1); i >= 0 {
			fv.sel = i
		}
	case "enter", "right", "l":
		if ok {
			return false, fv.activate(m, l)
		}
	case "left", "h":
		switch {
		case ok && l.e.Dir && fv.open[l.rel]:
			delete(fv.open, l.rel)
		case ok && strings.Contains(l.rel, "/"):
			fv.selPath = path.Dir(l.rel)
			fv.sel = -1
			list = fv.lines()
			fv.place(list)
		default:
			return true, nil // at the top with nothing to fold: back to the tree
		}
	}
	if l, ok := fv.selected(list); ok {
		fv.selPath = l.rel
	}
	return false, fv.follow(m)
}

// relist reads the open folders again after a change to what is listed.
func (fv *filesView) relist(m *Model) tea.Cmd {
	what := map[bool]string{true: "shown", false: "hidden"}
	m.setFlash(fmt.Sprintf("dotfiles %s · ignored files %s", what[fv.hidden], what[fv.ignored]), false)
	return fv.refresh(m)
}

// activate opens a folder, or a file's preview.
func (fv *filesView) activate(m *Model, l filesLine) tea.Cmd {
	if l.e.Dir {
		if fv.open[l.rel] {
			delete(fv.open, l.rel)
			return nil
		}
		fv.open[l.rel] = true
		if d := fv.dirs[l.rel]; d == nil || (!d.loading && d.entries == nil) {
			return fv.list(m, l.rel)
		}
		return nil
	}
	fv.reading = true
	if fv.prev.rel != l.rel || fv.prev.err != "" {
		return fv.read(m, l.rel)
	}
	return nil
}

// follow reads the selected file ahead when the preview is beside the tree.
func (fv *filesView) follow(m *Model) tea.Cmd {
	if !fv.wide {
		return nil
	}
	list := fv.lines()
	l, ok := fv.selected(list)
	if !ok || l.e.Dir || l.rel == fv.prev.rel {
		return nil
	}
	return fv.read(m, l.rel)
}

// ---- mouse ----

func (fv *filesView) mouse(m *Model, msg tea.MouseMsg, x, y int) tea.Cmd {
	if fv.err != "" {
		return nil
	}
	list := fv.lines()
	fv.place(list)
	inPreview := fv.reading && !fv.wide || fv.wide && x > fv.treeW
	switch {
	case msg.Button == tea.MouseButtonWheelUp && inPreview:
		fv.prevScroll = max(fv.prevScroll-3, 0)
		return nil
	case msg.Button == tea.MouseButtonWheelDown && inPreview:
		fv.prevScroll += 3
		return nil
	case msg.Button == tea.MouseButtonWheelUp:
		if i := nextEntry(list, fv.sel-1, -1); i >= 0 {
			fv.sel = i
		}
	case msg.Button == tea.MouseButtonWheelDown:
		if i := nextEntry(list, fv.sel+1, 1); i >= 0 {
			fv.sel = i
		}
	case msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft:
		if y == 1 {
			return fv.clickCrumb(m, x)
		}
		if inPreview {
			fv.reading = true
			return nil
		}
		i := fv.scroll + y - filesTop
		if y < filesTop || i < 0 || i >= len(list) || list[i].note != "" {
			return nil
		}
		if i == fv.sel {
			return fv.activate(m, list[i])
		}
		fv.sel, fv.reading = i, false
	default:
		return nil
	}
	if l, ok := fv.selected(list); ok {
		fv.selPath = l.rel
	}
	return fv.follow(m)
}

// clickCrumb goes back up to a folder in the path: it is selected and
// folded, or for the checkout itself everything is.
func (fv *filesView) clickCrumb(m *Model, x int) tea.Cmd {
	for _, c := range fv.crumbs {
		if x < c.x0 || x >= c.x1 {
			continue
		}
		fv.reading = false
		if c.rel == "" {
			fv.open = map[string]bool{}
			fv.selPath, fv.sel, fv.scroll = "", 0, 0
			fv.place(fv.lines())
			return fv.follow(m)
		}
		delete(fv.open, c.rel)
		fv.selPath, fv.sel = c.rel, -1
		fv.place(fv.lines())
		return nil
	}
	return nil
}

// ---- actions ----

// filesToAgent types a file's path into the agent working in the checkout,
// on the checkout's machine: the path is that machine's, as a dropped file's
// is (drop.go). An agent shown beside the explorer comes first, then the one
// most recently busy.
func (m *Model) filesToAgent(fv *filesView, l filesLine) tea.Cmd {
	mach := m.machine(fv.machine)
	if mach == nil || mach.c == nil {
		m.setFlash(m.offlineText(fv.machine), true)
		return nil
	}
	shown := map[string]bool{}
	for _, lf := range m.tab().root.leaves() {
		if lf.view.Kind == kindPane && lf.view.Machine == fv.machine {
			shown[lf.view.PaneID] = true
		}
	}
	var best *proto.PaneInfo
	for i := range mach.panes {
		p := &mach.panes[i]
		if p.Agent == nil || p.State != proto.PaneRunning || !withinDir(p.Cwd, fv.root) {
			continue
		}
		switch {
		case best == nil:
			best = p
		case shown[p.ID] != shown[best.ID]:
			if shown[p.ID] {
				best = p
			}
		case p.Agent.Since.After(best.Agent.Since):
			best = p
		}
	}
	if best == nil {
		m.setFlash("no agent is working in this checkout — c starts one", true)
		return nil
	}
	target := path.Join(fv.root, l.rel)
	if rel, ok := strings.CutPrefix(target, strings.TrimSuffix(best.Cwd, "/")+"/"); ok {
		target = rel // the agent reads paths from where it runs
	}
	if strings.ContainsAny(target, " \t'\"") {
		target = shellQuote(target, '"')
	}
	mach.c.Notify(proto.MethodPaneSendText, proto.PaneSendTextParams{ID: best.ID, Text: target + " ", Paste: true})
	m.setFlash("sent "+target+" to "+agentLabel(best.Agent.Name)+" · "+best.DisplayName(), false)
	return nil
}

// withinDir reports whether dir is root or under it, slash-separated as a
// server's paths are.
func withinDir(dir, root string) bool {
	root = strings.TrimSuffix(root, "/")
	return root != "" && (dir == root || strings.HasPrefix(dir, root+"/"))
}

// filesEdit opens a file in the machine's own $EDITOR, in a terminal of its
// own in the checkout.
func (m *Model) filesEdit(fv *filesView, rel string) tea.Cmd {
	c := m.clientOf(fv.machine)
	if c == nil {
		m.setFlash(m.offlineText(fv.machine), true)
		return nil
	}
	cols, rows := m.paneArea()
	mid := fv.machine
	params := proto.PaneCreateParams{Name: "edit · " + path.Base(rel), Cwd: fv.root, Cols: cols, Rows: rows,
		Command: []string{"/bin/sh", "-lc", `exec ${VISUAL:-${EDITOR:-vi}} "$1"`, "sh", path.Join(fv.root, rel)}}
	return func() tea.Msg {
		var info proto.PaneInfo
		if err := callCtx(c, proto.MethodPaneCreate, params, &info); err != nil {
			return errMsg{err}
		}
		return createdMsg{machine: mid, info: info}
	}
}

// filesDiff shows a changed file's diff in its branch's changes view.
func (m *Model) filesDiff(fv *filesView, l filesLine) tea.Cmd {
	switch {
	case l.e.Dir:
		m.setFlash("d shows a file's diff; open the folder to pick one", true)
		return nil
	case l.e.Status == "":
		m.setFlash(l.e.Name+" has no uncommitted changes", true)
		return nil
	case fv.branch == "":
		m.setFlash("this checkout has no branch to show changes for", true)
		return nil
	}
	cmd := m.openBranch(fv.machine, fv.projectID, fv.branch)
	if cv := m.changes; cv != nil && cv.machine == fv.machine && cv.projectID == fv.projectID && cv.branch == fv.branch {
		m.focus = focusMain
		return tea.Batch(cmd, cv.loadDiff(m, l.rel))
	}
	return cmd
}

// filesNextCheckout moves to the project's next checkout: the main one,
// then each branch's worktree.
func (m *Model) filesNextCheckout(fv *filesView) tea.Cmd {
	proj := m.project(fv.machine, fv.projectID)
	if proj == nil {
		return nil
	}
	wants := []string{""}
	for _, wt := range proj.Worktrees {
		if !wt.Main && wt.Branch != "" {
			wants = append(wants, wt.Branch)
		}
	}
	if len(wants) == 1 {
		m.setFlash(proj.Name+" has no other checkouts", false)
		return nil
	}
	i := slices.Index(wants, fv.want)
	if fv.note != "" {
		i = 0 // showing the main checkout in place of a branch with none
	}
	next := wants[(i+1)%len(wants)]
	fv.want = next
	fv.root, fv.branch, fv.note = m.filesCheckout(fv.machine, fv.projectID, next)
	fv.reset()
	if l := m.tab().focused(); l.files == fv {
		l.view.Branch = next
	}
	return tea.Batch(fv.load(m), m.saveState())
}

// filesRow is the tree's Files row for a project.
func filesRow(mid, pid, branch string) row {
	return row{id: sectionID(mid, pid, "files"), kind: kindFiles, machine: mid, projectID: pid, branch: branch}
}

// openFiles opens the explorer for what the tree has selected: a branch's
// worktree from a branch or its panes, else the project's main checkout.
func (m *Model) openFiles() tea.Cmd {
	pl := m.contextPlace()
	if pl.projectID == "" || m.project(pl.machine, pl.projectID) == nil {
		m.setFlash("select a project or branch to browse its files", true)
		return nil
	}
	r := filesRow(pl.machine, pl.projectID, pl.branch)
	// The cursor goes to the Files row, as openBranch's does: a browsing
	// split follows the cursor, and left on a branch it would show that.
	var cmds []tea.Cmd
	if indexOfRow(m.rows, r.id) < 0 {
		m.expanded[projectNodeID(pl.machine, pl.projectID)] = true
		cmds = append(cmds, m.rebuild(), m.saveState())
	}
	if indexOfRow(m.rows, r.id) >= 0 {
		m.cursor = r.id
		m.keepCursorVisible()
	}
	m.focus = focusMain
	cmd := tea.Batch(append(cmds, m.show(r))...)
	if l := m.tab().focused(); l.view.Row == r.id && l.view.Branch != r.branch {
		l.view.Branch = r.branch // the same row, another checkout
		cmd = tea.Batch(cmd, m.syncView())
	}
	return cmd
}

// filesEvent passes a worktree's edits to the explorers showing it: the
// folders on screen are listed again, and the previewed file read again when
// it is one of those that changed.
func (m *Model) filesEvent(mid string, wc proto.WorktreeChanged) tea.Cmd {
	var cmds []tea.Cmd
	for _, t := range m.openTabs() {
		for _, l := range t.root.leaves() {
			fv := l.files
			if fv == nil || fv.machine != mid || fv.root != wc.Worktree || fv.err != "" {
				continue
			}
			for rel := range fv.dirs {
				if rel == "" || fv.shown(rel) {
					cmds = append(cmds, fv.list(m, rel))
				} else {
					delete(fv.dirs, rel)
				}
			}
			if fv.prev.rel != "" && (wc.More || slices.Contains(wc.Paths, fv.prev.rel)) {
				cmds = append(cmds, fv.read(m, fv.prev.rel))
			}
		}
	}
	return tea.Batch(cmds...)
}

// filesProjectUpdated re-lists an explorer whose checkout's git status
// moved: a commit changes every status without writing a file.
func (m *Model) filesProjectUpdated(mid, pid string) tea.Cmd {
	var cmds []tea.Cmd
	for _, t := range m.openTabs() {
		for _, l := range t.root.leaves() {
			fv := l.files
			if fv == nil || fv.machine != mid || fv.projectID != pid || fv.err != "" {
				continue
			}
			if sig := m.filesStatusSig(fv); sig != fv.status {
				fv.status = sig
				cmds = append(cmds, fv.refresh(m))
			}
		}
	}
	return tea.Batch(cmds...)
}

// filesReceive hands an answer to every explorer on that checkout.
func (m *Model) filesReceive(msg tea.Msg) tea.Cmd {
	var cmds []tea.Cmd
	for _, t := range m.openTabs() {
		for _, l := range t.root.leaves() {
			fv := l.files
			if fv == nil {
				continue
			}
			switch msg := msg.(type) {
			case filesListMsg:
				if fv.machine == msg.machine && fv.root == msg.root {
					fv.receiveList(msg)
					cmds = append(cmds, fv.follow(m))
				}
			case filesReadMsg:
				if fv.machine == msg.machine && fv.root == msg.root {
					fv.receiveRead(msg)
				}
			}
		}
	}
	return tea.Batch(cmds...)
}
