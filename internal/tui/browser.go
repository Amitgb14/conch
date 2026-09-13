package tui

import (
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
)

// browser is the Add project overlay: it browses folders on a machine
// (through its server, so remote machines work the same) and adds one as a
// project, or creates a new folder or project.
type browser struct {
	machine string
	list    *proto.FSList
	err     string
	req     int    // latest listing request; older replies are dropped
	want    string // name to select once the next listing arrives
	sel     int
	scroll  int
	hidden  bool
	filter  string

	mode  browserMode
	input textinput.Model

	lastClick    time.Time
	lastClickRow int
}

type browserMode int

const (
	browseMode browserMode = iota
	filterMode
	newFolderMode
	newProjectMode
	gotoMode
)

type dirListMsg struct {
	req  int
	list proto.FSList
	err  error
}

// Rows before the first entry inside the box: path, status line.
const browserRowsTop = 2

func newBrowser(m *Model, mid, start string) (*browser, tea.Cmd) {
	b := &browser{machine: mid, input: textinput.New()}
	b.input.Prompt = ""
	return b, b.load(m, start, "")
}

func (b *browser) client(m *Model) *client.Client { return m.clientOf(b.machine) }

// load lists dir, selecting the entry named want when it arrives.
func (b *browser) load(m *Model, dir, want string) tea.Cmd {
	b.req++
	req, c, hidden := b.req, b.client(m), b.hidden
	b.want = want
	return func() tea.Msg {
		if c == nil {
			return dirListMsg{req: req, err: errString("machine is offline")}
		}
		var list proto.FSList
		err := callCtx(c, proto.MethodFSList, proto.FSListParams{Path: dir, Hidden: hidden}, &list)
		return dirListMsg{req: req, list: list, err: err}
	}
}

// row is one line of the listing: ".." or an entry.
type browserRow struct {
	up    bool
	entry proto.FSEntry
}

func (b *browser) rows() []browserRow {
	if b.list == nil {
		return nil
	}
	var rows []browserRow
	if b.list.Parent != "" && b.filter == "" {
		rows = append(rows, browserRow{up: true})
	}
	f := strings.ToLower(b.filter)
	for _, e := range b.list.Entries {
		if f == "" || strings.Contains(strings.ToLower(e.Name), f) {
			rows = append(rows, browserRow{entry: e})
		}
	}
	return rows
}

func (b *browser) selectedPath() (string, bool) {
	rows := b.rows()
	if b.list == nil || b.sel < 0 || b.sel >= len(rows) {
		return "", false
	}
	if rows[b.sel].up {
		return b.list.Parent, true
	}
	return path.Join(b.list.Path, rows[b.sel].entry.Name), true
}

func (b *browser) update(m *Model, msg tea.Msg) (bool, tea.Cmd) {
	switch msg := msg.(type) {
	case dirListMsg:
		if msg.req != b.req {
			return false, nil
		}
		if msg.err != nil {
			b.err = msg.err.Error()
			return false, nil
		}
		b.err, b.list, b.filter, b.sel, b.scroll = "", &msg.list, "", 0, 0
		for i, r := range b.rows() {
			if !r.up && r.entry.Name == b.want {
				b.sel = i
			}
		}
		return false, nil
	case tea.KeyMsg:
		if b.mode != browseMode {
			return false, b.inputKey(m, msg)
		}
		return b.browseKey(m, msg)
	}
	if b.mode != browseMode {
		var cmd tea.Cmd
		b.input, cmd = b.input.Update(msg)
		return false, cmd
	}
	return false, nil
}

func (b *browser) startInput(mode browserMode, value, placeholder string) tea.Cmd {
	b.mode = mode
	b.input.SetValue(value)
	b.input.Placeholder = placeholder
	b.input.CursorEnd()
	return b.input.Focus()
}

func (b *browser) browseKey(m *Model, k tea.KeyMsg) (bool, tea.Cmd) {
	rows := b.rows()
	page := m.browserListHeight() - 1
	switch k.String() {
	case "esc", "q":
		if b.filter != "" {
			b.filter, b.sel = "", 0
			return false, nil
		}
		m.overlay = nil
		return true, nil
	case "up", "k":
		b.sel--
	case "down", "j":
		b.sel++
	case "pgup":
		b.sel -= page
	case "pgdown":
		b.sel += page
	case "home":
		b.sel = 0
	case "end":
		b.sel = len(rows) - 1
	case "enter", "right", "l":
		if p, ok := b.selectedPath(); ok {
			want := ""
			if b.sel < len(rows) && rows[b.sel].up {
				want = path.Base(b.list.Path) // land on the folder we came from
			}
			return false, b.load(m, p, want)
		}
	case "left", "h", "backspace":
		if b.list != nil && b.list.Parent != "" {
			return false, b.load(m, b.list.Parent, path.Base(b.list.Path))
		}
	case "a", " ":
		if p, ok := b.selectedPath(); ok {
			return true, b.add(m, p)
		}
	case ".":
		if b.list != nil {
			return true, b.add(m, b.list.Path)
		}
	case "~":
		return false, b.load(m, "~", "")
	case "H":
		b.hidden = !b.hidden
		if b.list != nil {
			return false, b.load(m, b.list.Path, "")
		}
	case "/":
		return false, b.startInput(filterMode, b.filter, "type to filter")
	case "n":
		return false, b.startInput(newFolderMode, "", "folder name")
	case "p":
		return false, b.startInput(newProjectMode, "", "project name (a new git repository)")
	case "g":
		cur := "~"
		if b.list != nil {
			cur = b.list.Path
		}
		return false, b.startInput(gotoMode, cur, "path")
	}
	b.sel = clamp(b.sel, 0, max(len(rows)-1, 0))
	return false, nil
}

func (b *browser) inputKey(m *Model, k tea.KeyMsg) tea.Cmd {
	switch k.String() {
	case "esc":
		if b.mode == filterMode {
			b.filter = ""
		}
		b.mode = browseMode
		b.input.Blur()
		return nil
	case "enter":
		mode, value := b.mode, strings.TrimSpace(b.input.Value())
		b.mode = browseMode
		b.input.Blur()
		if b.list == nil {
			return nil
		}
		switch mode {
		case newFolderMode:
			if !validName(value) {
				m.setFlash("a folder name can't be empty or contain /", true)
				return nil
			}
			return b.mkdir(m, path.Join(b.list.Path, value), value)
		case newProjectMode:
			if !validName(value) {
				m.setFlash("a project name can't be empty or contain /", true)
				return nil
			}
			return b.create(m, path.Join(b.list.Path, value))
		case gotoMode:
			return b.load(m, value, "")
		}
		return nil
	}
	var cmd tea.Cmd
	b.input, cmd = b.input.Update(k)
	if b.mode == filterMode {
		b.filter, b.sel, b.scroll = b.input.Value(), 0, 0
	}
	return cmd
}

func validName(s string) bool {
	return s != "" && s != "." && s != ".." && !strings.ContainsRune(s, '/')
}

func (b *browser) add(m *Model, dir string) tea.Cmd {
	m.overlay = nil
	var info proto.ProjectInfo
	return m.callOn(b.machine, proto.MethodProjectAdd, proto.ProjectAddParams{Path: dir}, &info,
		func() tea.Msg { return flashMsg("added project " + info.Name) })
}

func (b *browser) mkdir(m *Model, dir, name string) tea.Cmd {
	c := b.client(m)
	parent := path.Dir(dir)
	return tea.Sequence(
		func() tea.Msg {
			if c == nil {
				return errMsg{errString("machine is offline")}
			}
			if err := callCtx(c, proto.MethodFSMkdir, proto.FSMkdirParams{Path: dir}, nil); err != nil {
				return errMsg{err}
			}
			return flashMsg("created folder " + name)
		},
		b.load(m, parent, name),
	)
}

func (b *browser) create(m *Model, dir string) tea.Cmd {
	m.overlay = nil
	var info proto.ProjectInfo
	return m.callOn(b.machine, proto.MethodProjectCreate, proto.ProjectCreateParams{Path: dir, Git: true}, &info,
		func() tea.Msg { return flashMsg("created project " + info.Name + " (git repository on main)") })
}

func (m Model) browserListHeight() int { return clamp(m.height-12, 5, 20) }

func (b *browser) render(m Model) box {
	w := clamp(76, 36, max(m.width-4, 36))
	listH := m.browserListHeight()
	rows := b.rows()
	if b.sel < b.scroll {
		b.scroll = b.sel
	}
	if b.sel >= b.scroll+listH {
		b.scroll = b.sel - listH + 1
	}

	where := ""
	if mach := m.machine(b.machine); mach != nil && b.machine != localMachine {
		where = " on " + mach.label
	}
	cur := "…"
	if b.list != nil {
		cur = b.list.Path
		if h := b.list.Home; h != "" && (cur == h || strings.HasPrefix(cur, h+"/")) {
			cur = "~" + cur[len(h):]
		}
	}
	lines := []string{styleBold.Render(ansi.TruncateLeft(cur, max(ansi.StringWidth(cur)-(w-2), 0), "…"))}

	status := styleMuted.Render(fmt.Sprintf("%d folders", len(rows)))
	switch {
	case b.mode == filterMode:
		status = styleAccent.Render("filter ") + b.input.View()
	case b.mode == newFolderMode:
		status = styleAccent.Render("new folder ") + b.input.View()
	case b.mode == newProjectMode:
		status = styleAccent.Render("new project ") + b.input.View()
	case b.mode == gotoMode:
		status = styleAccent.Render("go to ") + b.input.View()
	case b.err != "":
		status = styleErr.Render(b.err)
	case b.filter != "":
		status = styleAccent.Render("/"+b.filter) + styleMuted.Render(fmt.Sprintf("  %d matching · esc clears", len(rows)))
	case b.list != nil && b.list.Truncated:
		status += styleWarn.Render(" (first 2000 shown; / filters)")
	}
	lines = append(lines, status)

	for i := b.scroll; i < b.scroll+listH; i++ {
		if i >= len(rows) {
			lines = append(lines, "")
			continue
		}
		r := rows[i]
		glyph, name, glyphStyle, right := "›", r.entry.Name, styleMuted, ""
		switch {
		case r.up:
			glyph, name = "↰", ".."
		case r.entry.Project:
			glyph, glyphStyle, right = "◆", styleOK, styleOK.Render("project")
		case r.entry.Git:
			glyph, glyphStyle, right = "◆", styleAccent, styleMuted.Render("git")
		}
		if i == b.sel {
			lines = append(lines, styleSel.Render(spread(" "+glyph+" "+name, ansi.Strip(right), w)))
		} else {
			lines = append(lines, spread(" "+glyphStyle.Render(glyph)+" "+name, right, w))
		}
	}
	lines = append(lines, "",
		styleMuted.Render(" enter open · ← up · a add selected · . add this folder · / filter"),
		styleMuted.Render(" n new folder · p new project · g go to path · ~ home · H hidden · esc close"),
	)
	bx := box{lines: frameLines(" Add project"+where+" ", lines, w, colorAccent)}
	bx.x = max((m.width-bx.width())/2, 0)
	bx.y = max((m.height-len(bx.lines))/3, 0)
	return bx
}

func (b *browser) mouse(m *Model, msg tea.MouseMsg, bx box) tea.Cmd {
	if !bx.contains(msg.X, msg.Y) {
		if msg.Action == tea.MouseActionPress {
			m.overlay = nil
		}
		return nil
	}
	rows := b.rows()
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		b.sel = clamp(b.sel-3, 0, max(len(rows)-1, 0))
		return nil
	case tea.MouseButtonWheelDown:
		b.sel = clamp(b.sel+3, 0, max(len(rows)-1, 0))
		return nil
	}
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft || b.mode != browseMode {
		return nil
	}
	i := b.scroll + msg.Y - bx.y - 1 - browserRowsTop
	if i < b.scroll || i >= len(rows) || i >= b.scroll+m.browserListHeight() {
		return nil
	}
	now := time.Now()
	double := i == b.lastClickRow && now.Sub(b.lastClick) < doubleClickWindow
	b.lastClick, b.lastClickRow, b.sel = now, i, i
	if double {
		_, cmd := b.browseKey(m, tea.KeyMsg{Type: tea.KeyEnter})
		return cmd
	}
	return nil
}
