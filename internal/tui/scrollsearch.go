package tui

import (
	"fmt"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
)

// Search in scroll mode, as in tmux's copy mode: / looks back through the
// history, ? towards the live screen, n repeats the last search and N goes
// the other way. The server searches the whole history (pane.search); the
// view then scrolls to the match and puts the cursor on it, so v and y
// select and copy from there.
type scrollSearch struct {
	typing bool   // the query is being typed on the pane's bottom line
	input  string // what has been typed
	query  string // the last search made; kept for the next scroll mode
	back   bool   // its direction: towards older lines
}

type searchResultMsg struct {
	machine, pane string
	query         string
	back          bool
	res           proto.PaneSearchResult
	err           error
}

// styleSearchMatch marks what a search matched; applyTheme sets it again.
var styleSearchMatch = lipgloss.NewStyle().Background(colorWarn).Foreground(lipgloss.Color("0"))

// startSearch begins typing a query; back says which way it will look.
func (m *Model) startSearch(back bool) {
	m.search.typing, m.search.input, m.search.back = true, "", back
}

// searchTypingKey handles a key while the query is typed.
func (m *Model) searchTypingKey(k tea.KeyMsg) tea.Cmd {
	switch k.String() {
	case "enter":
		m.search.typing = false
		if m.search.input != "" {
			m.search.query = m.search.input
		}
		return m.runSearch(m.search.back) // empty: the last query again, as in less
	case "esc", "ctrl+c":
		m.search.typing = false
	case "backspace":
		if m.search.input == "" {
			m.search.typing = false // nothing left to delete: stop, as vim does
			return nil
		}
		r := []rune(m.search.input)
		m.search.input = string(r[:len(r)-1])
	case "ctrl+u":
		m.search.input = ""
	default:
		switch k.Type {
		case tea.KeySpace:
			m.search.input += " "
		case tea.KeyRunes:
			m.search.input += string(k.Runes)
		}
	}
	return nil
}

// runSearch asks the server for the next match from the cursor.
func (m *Model) runSearch(back bool) tea.Cmd {
	if m.search.query == "" || m.frame == nil || m.viewing == "" {
		return nil
	}
	mach := m.machine(m.viewMachine)
	if mach == nil || mach.c == nil {
		return nil
	}
	if len(mach.c.MissingCapabilities([]string{proto.CapPaneSearch})) > 0 {
		m.setFlash("conch on "+mach.label+" is too old to search a pane; update it", true)
		return nil
	}
	c, mid, id := mach.c, m.viewMachine, m.viewing
	params := proto.PaneSearchParams{
		ID: id, Query: m.search.query, Backward: back,
		Line: m.frame.History - m.offset + m.curY, Col: m.curX,
	}
	return func() tea.Msg {
		var res proto.PaneSearchResult
		err := callCtx(c, proto.MethodPaneSearch, params, &res)
		return searchResultMsg{machine: mid, pane: id, query: params.Query, back: back, res: res, err: err}
	}
}

// showSearchResult scrolls to a match and puts the cursor on it.
func (m *Model) showSearchResult(msg searchResultMsg) {
	if msg.err != nil {
		m.setFlash("search: "+msg.err.Error(), true)
		return
	}
	// Scroll mode may have ended, or the view moved on, while it looked.
	if !m.scrollMode || m.viewMachine != msg.machine || m.viewing != msg.pane || m.frame == nil {
		return
	}
	res := msg.res
	if !res.Found {
		m.setFlash(fmt.Sprintf("%q not found", msg.query), true)
		return
	}
	cols, rows := m.paneArea()
	top := res.History - m.offset // the first line on screen, in the result's lines
	if res.Line < top || res.Line >= top+rows {
		// Off screen: bring it to the middle.
		m.frame.History = max(m.frame.History, res.History)
		want := clamp(res.History-(res.Line-rows/2), 0, res.History)
		m.scrollPane(want - m.offset)
		top = res.History - m.offset
	}
	m.curY = clamp(res.Line-top, 0, rows-1)
	m.curX = clamp(res.Col, 0, max(cols-1, 0))
	if m.sel != nil && m.sel.keyboard {
		m.sel.bx, m.sel.by = m.curX, m.curY
	}
	switch {
	case res.Wrapped && msg.back:
		m.setFlash("search went past the oldest line and on from the bottom", false)
	case res.Wrapped:
		m.setFlash("search went past the bottom and on from the oldest line", false)
	}
}

// searchMatches draws the last query's matches over a pane's lines.
func (m Model) searchMatches(lines []string, w int) []string {
	q := m.search.query
	if q == "" || m.search.typing {
		return lines
	}
	out := make([]string, len(lines))
	for y, l := range lines {
		out[y] = highlightSpans(l, matchSpans(ansi.Strip(l), q), w)
	}
	return out
}

// searchPrompt draws the query being typed over a pane's bottom line.
func (m Model) searchPrompt(lines []string, w int) []string {
	if m.search.typing && len(lines) > 0 {
		prompt := "/"
		if !m.search.back {
			prompt = "?"
		}
		// The end of a long query stays in sight.
		text := ansi.TruncateLeft(prompt+m.search.input, max(ansi.StringWidth(prompt+m.search.input)-(w-1), 0), "")
		lines[len(lines)-1] = padRight(text+"█", w)
	}
	return lines
}

// matchSpans lists the [from, to) cells where q occurs in plain text, with
// the same smart case as the server's search: a query without capitals
// matches either case.
func matchSpans(plain, q string) [][2]int {
	query := []rune(q)
	if len(query) == 0 {
		return nil
	}
	fold := true
	for _, c := range query {
		if unicode.IsUpper(c) {
			fold = false
			break
		}
	}
	line := []rune(plain)
	lower := func(r rune) rune {
		if fold {
			return unicode.ToLower(r)
		}
		return r
	}
	var spans [][2]int
	for i := 0; i+len(query) <= len(line); i++ {
		same := true
		for j := range query {
			if lower(line[i+j]) != lower(query[j]) {
				same = false
				break
			}
		}
		if same {
			from := ansi.StringWidth(string(line[:i]))
			spans = append(spans, [2]int{from, from + ansi.StringWidth(string(query))})
			i += len(query) - 1 // matches don't overlap
		}
	}
	return spans
}

// highlightSpans styles the given cell ranges of a rendered line, which may
// carry its own styling.
func highlightSpans(line string, spans [][2]int, w int) string {
	if len(spans) == 0 {
		return line
	}
	plain := padRight(ansi.Strip(line), w)
	// Right to left, so the cells still to be styled keep their places.
	for i := len(spans) - 1; i >= 0; i-- {
		from, to := clamp(spans[i][0], 0, w), clamp(spans[i][1], 0, w)
		if from >= to {
			continue
		}
		line = ansi.Cut(line, 0, from) + "\x1b[0m" + styleSearchMatch.Render(ansi.Cut(plain, from, to)) + "\x1b[0m" + ansi.Cut(line, to, w)
	}
	return line
}
