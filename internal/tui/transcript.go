package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
)

// An agent draws on the alternate screen, which keeps no scrollback: what it
// said a page ago is not on screen and conch has nothing to scroll back to,
// so dragging over its pane can only ever reach a screenful. The agents keep
// their conversations on disk, though, and conch can read them — this shows
// one, to be scrolled through and copied from, whole or in part.
type transcriptView struct {
	machine, title string
	lines          []string // the conversation, wrapped to width
	width          int      // what lines were wrapped to
	doc            string   // as it came, unwrapped
	off            int      // the first line shown
	rows           int      // how many fit, from the last render
	err            string
	loading        bool
	atEnd          bool       // open at the newest part, once it is laid out
	sel            *selection // over lines, by their place in the whole
}

type transcriptMsg struct {
	doc string
	err error
}

// openTranscript reads a saved conversation and shows it.
func (m *Model) openTranscript(mid string, s proto.SessionInfo) tea.Cmd {
	c := m.clientOf(mid)
	if c == nil {
		m.setFlash(m.offlineText(mid), true)
		return nil
	}
	if len(c.MissingCapabilities([]string{proto.CapSessionHandoff})) > 0 {
		m.setFlash("this machine's conch is too old to read a conversation; reload it", true)
		return nil
	}
	if s.ID == "" {
		m.setFlash("that run saved no conversation to read", true)
		return nil
	}
	title := s.Title
	if title == "" {
		title = s.Agent + " " + s.ID
	}
	v := &transcriptView{machine: mid, title: title, loading: true}
	m.overlay = v
	ref := proto.SessionRef{Agent: s.Agent, ID: s.ID, Dir: s.Dir}
	return func() tea.Msg {
		var out proto.SessionExport
		if err := callCtx(c, proto.MethodSessionExport, ref, &out); err != nil {
			return transcriptMsg{err: err}
		}
		return transcriptMsg{doc: out.Doc}
	}
}

func (v *transcriptView) receive(msg transcriptMsg) {
	v.loading = false
	if msg.err != nil {
		v.err = msg.err.Error()
		return
	}
	v.doc, v.width, v.atEnd = msg.doc, 0, true // wrapped at the next render
	if strings.TrimSpace(v.doc) == "" {
		v.err = "nothing was said in this conversation"
	}
}

// layout wraps the conversation for w columns, keeping the lines it was
// written with — indentation and blank lines included, since a conversation
// has code in it.
func (v *transcriptView) layout(w int) {
	if w <= 0 || (v.width == w && v.lines != nil) {
		return
	}
	v.width, v.lines, v.sel = w, nil, nil
	text := plainText(v.doc)
	if strings.TrimSpace(text) == "" {
		return // nothing said, or nothing read yet: no line to show
	}
	for _, line := range strings.Split(strings.ReplaceAll(text, "\t", "    "), "\n") {
		line = strings.TrimRight(line, " ")
		for ansi.StringWidth(line) > w {
			cut := ansi.Truncate(line, w, "")
			v.lines = append(v.lines, cut)
			line = strings.TrimPrefix(line, cut)
		}
		v.lines = append(v.lines, line)
	}
}

// plainText takes the escapes and control characters out of a conversation
// before any of it is drawn. A saved conversation is whatever was said, and
// people paste terminal output into agents — escape sequences included. Drawn
// as they are, the terminal would obey them: the screen jumps, rows repeat,
// and what is copied could carry them into the next terminal it is pasted
// into.
func plainText(s string) string {
	s = ansi.Strip(s)
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case r == '\r': // a carriage return would draw over the line
		case r < 0x20 || r == 0x7f: // the rest of the C0 controls
		case r >= 0x80 && r <= 0x9f: // and the C1 ones, which start sequences
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (v *transcriptView) maxOff() int { return max(len(v.lines)-v.rows, 0) }

func (v *transcriptView) scroll(by int) {
	v.off = clamp(v.off+by, 0, v.maxOff())
}

func (v *transcriptView) update(m *Model, msg tea.Msg) (bool, tea.Cmd) {
	switch msg := msg.(type) {
	case transcriptMsg:
		v.receive(msg)
		return false, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "q":
			m.overlay = nil
			return true, nil
		case "up", "k":
			v.scroll(-1)
		case "down", "j":
			v.scroll(1)
		case "pgup":
			v.scroll(-max(v.rows-1, 1))
		case "pgdown":
			v.scroll(max(v.rows-1, 1))
		case "g", "home":
			v.off = 0
		case "G", "end":
			v.off = v.maxOff()
		case "a":
			m.overlay = nil
			return true, copyText(strings.Join(v.lines, "\n"))
		case "y", "enter":
			if v.sel == nil || !v.sel.hasContent {
				m.setFlash("drag over the text to choose what to copy, or a for all of it", true)
				return false, nil
			}
			m.overlay = nil
			return true, copyText(v.sel.text(v.lines, v.width))
		}
	}
	return false, nil
}

func (v *transcriptView) render(m Model) box {
	// The border takes two columns and two rows, and a screen can be smaller
	// than a dialog's usual size: keep inside it either way.
	w := min(m.dialogWidth(), max(m.width-2, 8))
	inner := max(w-2, 6)
	v.layout(inner)
	v.rows = clamp(m.height-8, 1, 30)
	v.rows = min(v.rows, max(m.height-4, 1))
	if v.atEnd && len(v.lines) > 0 {
		v.off, v.atEnd = v.maxOff(), false // the latest is what you came for
	}
	v.off = clamp(v.off, 0, v.maxOff())

	var content []string
	switch {
	case v.loading:
		content = []string{"", styleMuted.Render(" reading the conversation…"), ""}
	case v.err != "":
		content = []string{"", styleErr.Render(" " + ansi.Truncate(v.err, inner-1, "…")), ""}
	default:
		for i := v.off; i < len(v.lines) && i < v.off+v.rows; i++ {
			content = append(content, v.line(i, inner))
		}
		for len(content) < v.rows {
			content = append(content, "")
		}
		where := styleMuted.Render(spread(" drag to select · y copy · a all · g top · G bottom · esc close",
			v.position()+" ", inner))
		content = append(content, "", where)
	}
	lines := frameLines(" "+ansi.Truncate(v.title, max(inner-10, 8), "…")+" ", content, w, colorAccent)
	return box{lines: lines, x: max((m.width-w)/2, 0), y: max((m.height-len(lines))/2, 0)}
}

// line renders one line of the conversation, with any selected part of it
// picked out.
func (v *transcriptView) line(i, w int) string {
	text := fit(v.lines[i], w)
	if v.sel == nil {
		return text
	}
	from, to, ok := v.sel.span(i, w)
	if !ok {
		return text
	}
	plain := ansi.Strip(text)
	return ansi.Cut(plain, 0, from) + styleSelection.Render(ansi.Cut(plain, from, to)) + ansi.Cut(plain, to, w)
}

func (v *transcriptView) position() string {
	if len(v.lines) == 0 {
		return ""
	}
	last := min(v.off+v.rows, len(v.lines))
	return counted(len(v.lines), "line") + " · " + itoa(v.off+1) + "–" + itoa(last)
}

// mouse scrolls with the wheel and selects by dragging; dragging above or
// below the text scrolls it, so a selection can be taken past the screen —
// which is the whole point of reading the conversation here.
func (v *transcriptView) mouse(m *Model, msg tea.MouseMsg, b box) tea.Cmd {
	if len(v.lines) == 0 {
		return nil
	}
	top := b.y + 1 // the border
	x := msg.X - b.x - 1
	row := msg.Y - top
	switch {
	case msg.Button == tea.MouseButtonWheelUp:
		v.scroll(-3)
		return nil
	case msg.Button == tea.MouseButtonWheelDown:
		v.scroll(3)
		return nil
	case msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft:
		if row < 0 || row >= v.rows {
			return nil
		}
		at := clamp(v.off+row, 0, len(v.lines)-1)
		v.sel = &selection{ax: clamp(x, 0, v.width), ay: at, bx: clamp(x, 0, v.width), by: at, dragging: true}
	case msg.Action == tea.MouseActionMotion && v.sel != nil && v.sel.dragging:
		switch {
		case row < 0:
			v.scroll(-1)
			row = 0
		case row >= v.rows:
			v.scroll(1)
			row = v.rows - 1
		}
		at := clamp(v.off+row, 0, len(v.lines)-1)
		v.sel.bx, v.sel.by = clamp(x, 0, v.width), at
		v.sel.hasContent = v.sel.hasContent || at != v.sel.ay || v.sel.bx != v.sel.ax
	case msg.Action == tea.MouseActionRelease && v.sel != nil && v.sel.dragging:
		v.sel.dragging = false
		if !v.sel.hasContent {
			v.sel = nil
			return nil
		}
		return copyText(v.sel.text(v.lines, v.width))
	}
	return nil
}
