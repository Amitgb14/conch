package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// Clicking a link an agent printed. ctrl+b u lists every link on the
// screen, which is the way to reach one that has scrolled or is wrapped
// over a dozen rows; this is the other way people reach for — put the
// pointer on it and click.
//
// Who gets the click matters. A pane running a program that asked for the
// mouse (an agent's own interface, vim) is owed every click, so there a
// link needs alt or ctrl held — the modifiers a terminal passes through.
// A pane whose program takes no mouse was doing nothing with the click
// anyway, so a plain one opens the link.

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

// clickedLink opens the link under a click, and says which it opened. It
// reports whether it took the click.
func (m *Model) clickedLink(msg tea.MouseMsg, x, y int, programTakesMouse bool) (tea.Cmd, bool) {
	if m.frame == nil || msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return nil, false
	}
	if programTakesMouse && !msg.Alt && !msg.Ctrl {
		return nil, false // the program is owed this click
	}
	link := linkUnder(m.frame.Lines, x, y)
	if link == "" {
		return nil, false
	}
	m.setFlash("opening "+ansi.Truncate(link, 60, "…")+" · also copied", false)
	return tea.Batch(copyText(link), openURL(link)), true
}

// linkHint is the help line's wording, kept here beside the rule it
// describes so the two cannot drift apart.
func linkHint(prefix string) string {
	return strings.Join([]string{
		"click a link an agent printed to open it (alt+click in an agent's own interface,",
		"which is owed its clicks); " + prefix + " u lists every link on the screen",
	}, " ")
}
