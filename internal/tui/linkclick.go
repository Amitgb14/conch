package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
)

// Clicking a link an agent printed. ctrl+b u lists every link on the
// screen, which is the way to reach one that has scrolled or is wrapped
// over a dozen rows; this is the other way people reach for — put the
// pointer on it and click.
//
// A link opens only with alt or ctrl held — the modifiers a terminal
// passes through — in every pane. A plain click is owed elsewhere: to the
// program when it asked for the mouse (an agent's own interface, vim), and
// otherwise to placing a selection, which a plain click opening a browser
// got in the way of.
//
// Not cmd+click, which is the one people ask for on a Mac: the mouse
// protocols carry three modifier bits — shift, alt and ctrl (X10 and SGR
// alike, see EncodeMouseButton) — and cmd has nowhere to go in them. A
// terminal cannot report it whatever it wanted to, and iTerm2 and
// Terminal.app keep cmd+click for their own URL opening anyway. So the
// modifiers here are the ones that can arrive.
//
// What a link can do instead is say it is a link before the click: with
// [ui] hover on, the one under the pointer is underlined in the accent
// (linkHoverAt), and only when a click there would really open it.

// linkUnder is the link at column x of row y of a pane's screen, or "".
// It starts from the beginning of the link even when the click landed in
// the middle of it, and follows it onto the rows it wrapped over.
func linkUnder(lines []string, x, y int) string {
	if y < 0 || y >= len(lines) {
		return ""
	}
	rows := make([]row2, len(lines))
	width := 0
	for i, l := range lines {
		rows[i] = rowOf(ansi.Strip(l))
		width = max(width, rows[i].width)
	}
	text := rows[y].text
	col := x - rows[y].indent
	if col < 0 || col > len([]rune(text)) {
		return ""
	}
	// The start of the link this column is inside: the last place a link
	// begins at or before it, with no space in between.
	runes := []rune(text)
	for _, start := range linkStarts {
		for i := 0; i+len(start) <= len(runes); i++ {
			if string(runes[i:i+len(start)]) != start {
				continue
			}
			end := i
			for end < len(runes) && runes[end] != ' ' && runes[end] != '\t' {
				end++
			}
			if col < i || col > end {
				continue
			}
			if link := linkAt(rows, y, len(string(runes[:i])), width); link != "" {
				return link
			}
		}
	}
	return ""
}

// linkClick is a press on a link that may yet open it.
type linkClick struct {
	machine, pane string
	link          string
	x, y          int
	seq           int
}

// linkOpenMsg opens the link a click was on, once the double-click window
// has gone by without a second press.
type linkOpenMsg struct{ seq int }

// linkMouse decides whether a press and its release over a link are a
// click on it, and reports whether it took the event.
//
// alt and ctrl are also how text is selected over a program that took the
// mouse (paneMouse), so opening on the press opened a browser at the start
// of every drag meant to copy a link — what people reach for most. So the
// press only notes the link and goes on to start a selection as before;
// the link opens on a release where it was pressed, with nothing dragged,
// and only after the double-click window, since a double click there
// selects the link to copy it.
func (m *Model) linkMouse(machine, pane string, f *proto.Frame, msg tea.MouseMsg, x, y int) (tea.Cmd, bool) {
	if msg.Button != tea.MouseButtonLeft {
		return nil, false
	}
	switch msg.Action {
	case tea.MouseActionPress:
		now := time.Now()
		if now.Before(m.linkQuiet) || (m.linkClick != nil && m.linkClick.seq < 0) {
			// The second or third press of a multiple click: the word or
			// line is being selected, and nothing opens.
			m.linkClick, m.linkQuiet = nil, now.Add(doubleClickWindow)
			return nil, false
		}
		m.linkClick = nil
		// Only with alt (option) or ctrl held, in every pane: a plain
		// click is for placing a selection, or the program's when it took
		// the mouse. cmd would be the Mac's choice, but no mouse protocol
		// can carry it.
		if f == nil || (!msg.Alt && !msg.Ctrl) {
			return nil, false
		}
		// The frame is the pane under the pointer's, not the focused
		// pane's: a click in another split was reading the wrong screen's
		// lines, which opened the wrong link or none at all.
		if link := linkUnder(f.Lines, x, y); link != "" {
			m.linkSeq++
			m.linkClick = &linkClick{machine: machine, pane: pane, link: link, x: x, y: y, seq: m.linkSeq}
		}
		return nil, false
	case tea.MouseActionRelease:
		lc := m.linkClick
		if lc == nil || lc.seq < 0 {
			return nil, false
		}
		dragged := m.sel != nil && m.sel.hasContent
		if dragged || lc.machine != machine || lc.pane != pane || lc.x != x || lc.y != y {
			m.linkClick = nil // a selection: it is copied, and nothing opens
			return nil, false
		}
		// A click. The empty selection its press began goes, and so does
		// the click a program would have been sent: it was the link's.
		m.sel, m.click = nil, nil
		seq := lc.seq
		lc.seq = -seq // released: a press from here on is a double click
		m.linkQuiet = time.Now().Add(doubleClickWindow)
		return tea.Tick(doubleClickWindow, func(time.Time) tea.Msg { return linkOpenMsg{seq} }), true
	}
	return nil, false
}

// openClickedLink opens the link of a click no second press followed.
func (m *Model) openClickedLink(msg linkOpenMsg) tea.Cmd {
	lc := m.linkClick
	if lc == nil || lc.seq != -msg.seq {
		return nil // a double click, or another click since
	}
	m.linkClick = nil
	m.setFlash("opening "+ansi.Truncate(lc.link, 60, "…")+" · also copied", false)
	return tea.Batch(copyText(lc.link), openURL(lc.link))
}

// linkSpanUnder is the columns a link under x,y covers on its own row, so
// the one the pointer is on can be shown as the clickable thing it is. It
// is the same reckoning clicking does — the link starts where the click
// would find it — and it covers only this row of a link that wrapped,
// since that is what the pointer is over.
func linkSpanUnder(lines []string, x, y int) (from, to int, ok bool) {
	if y < 0 || y >= len(lines) {
		return 0, 0, false
	}
	r := rowOf(ansi.Strip(lines[y]))
	runes := []rune(r.text)
	col := x - r.indent
	if col < 0 || col > len(runes) {
		return 0, 0, false
	}
	for _, start := range linkStarts {
		for i := 0; i+len(start) <= len(runes); i++ {
			if string(runes[i:i+len(start)]) != start {
				continue
			}
			end := i
			for end < len(runes) && runes[end] != ' ' && runes[end] != '\t' {
				end++
			}
			if col < i || col > end {
				continue
			}
			// In cells, not runes: a line may hold anything.
			return r.indent + ansi.StringWidth(string(runes[:i])),
				r.indent + ansi.StringWidth(string(runes[:end])), true
		}
	}
	return 0, 0, false
}

// hoverLink is the link the pointer is on, remembered so the pane it is in
// can draw it as clickable.
type hoverLink struct {
	machine, pane string
	y, from, to   int
}

// linkHoverAt works out whether the pointer is over a link in a pane, and
// says whether what it found differs from what was there before — which is
// when the screen needs drawing again. A program that took the mouse is
// owed its clicks, so a link in one is only clickable with alt or ctrl
// held, and only then is it lit.
func (m *Model) linkHoverAt(msg tea.MouseMsg) bool {
	found := hoverLink{}
	if l, x, y, ok := m.paneCellAt(msg.X, msg.Y); ok {
		// Lit only when a click would open it: with alt or ctrl held.
		if f := m.frames[paneKey(l.view.Machine, l.view.PaneID)]; f != nil && (msg.Alt || msg.Ctrl) {
			if from, to, got := linkSpanUnder(f.Lines, x, y); got {
				found = hoverLink{machine: l.view.Machine, pane: l.view.PaneID, y: y, from: from, to: to}
			}
		}
	}
	// The pointer says it too, where the terminal takes OSC 22: a hand
	// over a link, an arrow everywhere else (pointer.go).
	if found.pane != "" {
		m.pointAt(pointerHand)
	} else {
		m.pointAt(pointerArrow)
	}
	if found == m.hoverLink {
		return false
	}
	m.hoverLink = found
	return true
}

// linkLines draws the hovered link in a pane as the clickable thing it is.
func (m Model) linkLines(machine, pane string, lines []string, w int) []string {
	h := m.hoverLink
	if h.pane == "" || h.machine != machine || h.pane != pane || h.y < 0 || h.y >= len(lines) {
		return lines
	}
	out := append([]string(nil), lines...)
	out[h.y] = highlightLink(out[h.y], h.from, h.to, w)
	return out
}

// paneCellAt is the leaf under x,y and the cell of its screen that is,
// without changing what is focused: hovering looks, it does not choose.
func (m Model) paneCellAt(x, y int) (*leaf, int, int, bool) {
	rects, _ := m.leafRects()
	id := m.leafAt(rects, x, y)
	if id == 0 {
		return nil, 0, 0, false
	}
	l := m.tab().leaf(id)
	if l == nil || l.view.Kind != kindPane {
		return nil, 0, 0, false
	}
	in := m.inner(rects[id])
	cx, cy := x-in.x, y-in.y
	if cx < 0 || cy < 0 || cx >= in.w || cy >= in.h {
		return nil, 0, 0, false
	}
	return l, cx, cy, true
}

// highlightLink draws the columns from..to of a line as the clickable link
// they are: the program's own colours stay, with an underline over them.
func highlightLink(line string, from, to, w int) string {
	if from >= to {
		return line
	}
	plain := padRight(ansi.Strip(line), w)
	from, to = clamp(from, 0, w), clamp(to, 0, w)
	if from >= to {
		return line
	}
	return ansi.Cut(line, 0, from) + "\x1b[0m" + styleLink.Render(ansi.Cut(plain, from, to)) + "\x1b[0m" +
		ansi.Cut(line, to, w)
}
