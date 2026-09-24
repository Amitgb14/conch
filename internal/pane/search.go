package pane

import (
	"unicode"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
)

// Search looks for query in the pane's history and screen, from just after
// (or, backward, just before) line and col, going round once when it reaches
// either end. Lines count from the oldest line of history, so the screen
// starts at History; col is in cells. A query without capitals matches
// either case, as in less and vim's smartcase.
func (p *Pane) Search(query string, line, col int, backward bool) proto.PaneSearchResult {
	lines, history := p.textLines()
	res := proto.PaneSearchResult{History: history}
	q := []rune(query)
	if len(q) == 0 || len(lines) == 0 {
		return res
	}
	fold := !hasUpper(q)
	if fold {
		q = lowerRunes(q)
	}
	// A position before the first line or past the last starts at that end.
	if line < 0 {
		line, col = 0, -1
	} else if line >= len(lines) {
		line, col = len(lines)-1, int(^uint(0)>>1)
	}

	// matches lists where q starts on line i, in cells, left to right.
	matches := func(i int) []int {
		r := []rune(lines[i])
		hay := r
		if fold {
			hay = lowerRunes(r)
		}
		var cols []int
		for j := 0; j+len(q) <= len(hay); j++ {
			if runesEqual(hay[j:j+len(q)], q) {
				cols = append(cols, ansi.StringWidth(string(r[:j])))
			}
		}
		return cols
	}
	found := func(i, c int, wrapped bool) proto.PaneSearchResult {
		res.Found, res.Line, res.Col, res.Wrapped = true, i, c, wrapped
		res.Width = ansi.StringWidth(string(q))
		return res
	}

	n := len(lines)
	// Every line once, starting with the cursor's; the cursor's line comes
	// round again at the end for the matches on its other side.
	for step := 0; step <= n; step++ {
		i := line + step
		if backward {
			i = line - step
		}
		wrapped := i < 0 || i >= n
		i = ((i % n) + n) % n
		cols := matches(i)
		if backward {
			for k := len(cols) - 1; k >= 0; k-- {
				if step > 0 || cols[k] < col {
					return found(i, cols[k], wrapped)
				}
			}
		} else {
			for _, c := range cols {
				if step > 0 || c > col {
					return found(i, c, wrapped)
				}
			}
		}
	}
	return res
}

// textLines renders history and screen as plain text, oldest first, and
// says how many of them are history.
func (p *Pane) textLines() (lines []string, history int) {
	p.emuMu.RLock()
	defer p.emuMu.RUnlock()
	cols, rows := p.emu.Width(), p.emu.Height()
	if p.emu.IsAltScreen() {
		// The alternate screen's history is what conch kept (altscroll.go).
		history = len(p.alt.lines)
		lines = append(lines, p.alt.lines...)
	} else {
		history = p.emu.ScrollbackLen()
		for y := 0; y < history; y++ {
			lines = append(lines, cellText(cols, func(x int) *uv.Cell { return p.emu.ScrollbackCellAt(x, y) }))
		}
	}
	for y := 0; y < rows; y++ {
		lines = append(lines, cellText(cols, func(x int) *uv.Cell { return p.emu.CellAt(x, y) }))
	}
	return lines, history
}

// cellText is a row as plain text. A wide character's second cell is left
// out, so that the text's width is the row's.
func cellText(cols int, at func(x int) *uv.Cell) string {
	var b []rune
	for x := 0; x < cols; x++ {
		c := at(x)
		if c == nil || c.Content == "" {
			b = append(b, ' ')
			continue
		}
		b = append(b, []rune(c.Content)...)
		if c.Width > 1 {
			x += c.Width - 1
		}
	}
	for len(b) > 0 && b[len(b)-1] == ' ' {
		b = b[:len(b)-1]
	}
	return string(b)
}

func hasUpper(r []rune) bool {
	for _, c := range r {
		if unicode.IsUpper(c) {
			return true
		}
	}
	return false
}

func lowerRunes(r []rune) []rune {
	out := make([]rune, len(r))
	for i, c := range r {
		out[i] = unicode.ToLower(c)
	}
	return out
}

func runesEqual(a, b []rune) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
