package tui

import (
	"strings"
	"unicode"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// selection is a text selection over the visible pane, in view cells, or
// over the page a leaf shows (a diff, a file, the sessions list).
type selection struct {
	paneID     string
	leaf       int // the leaf whose page is selected; 0 for the viewed pane
	ax, ay     int // anchor: where the drag started
	bx, by     int // head: where it is now
	dragging   bool
	hasContent bool // moved past the anchor, so there is something to copy
	keyboard   bool // made in scroll mode; it follows the history as it scrolls

	// rows is the text of every pane row seen on screen while the
	// selection lasted, by view row, moved along as the history scrolls.
	// A selection taller than the screen copies whole from it: the rows
	// scrolled away are no longer in the frame.
	rows map[int]string
}

var styleSelection = lipgloss.NewStyle().Reverse(true)

// ordered returns the selection's start and end, start first, end
// inclusive.
func (s selection) ordered() (x1, y1, x2, y2 int) {
	if s.ay < s.by || (s.ay == s.by && s.ax <= s.bx) {
		return s.ax, s.ay, s.bx, s.by
	}
	return s.bx, s.by, s.ax, s.ay
}

// span is the [from, to) cell range selected on row y of a w-wide view.
func (s selection) span(y, w int) (from, to int, ok bool) {
	x1, y1, x2, y2 := s.ordered()
	if y < y1 || y > y2 {
		return 0, 0, false
	}
	from, to = 0, w
	if y == y1 {
		from = x1
	}
	if y == y2 {
		to = x2 + 1
	}
	return clamp(from, 0, w), clamp(to, 0, w), from < to
}

// text extracts the selected text from rendered lines, and from the rows
// remembered for it where it reaches past them. Trailing spaces are trimmed
// from each line, as terminals do.
func (s selection) text(lines []string, w int) string {
	_, y1, _, y2 := s.ordered()
	var out []string
	for y := y1; y <= y2; y++ {
		var line string
		switch {
		case y >= 0 && y < len(lines):
			line = ansi.Strip(lines[y])
		case s.rows != nil:
			l, ok := s.rows[y]
			if !ok {
				continue
			}
			line = l
		default:
			continue
		}
		from, to, ok := s.span(y, w)
		if !ok {
			continue
		}
		out = append(out, strings.TrimRight(ansi.Cut(line, from, to), " "))
	}
	return strings.Join(out, "\n")
}

// remember keeps the text of the rows on screen now.
func (s *selection) remember(lines []string) {
	if s.rows == nil {
		s.rows = map[int]string{}
	}
	for y, l := range lines {
		s.rows[y] = ansi.Strip(l)
	}
}

// shiftRows moves the remembered rows n lines down the screen, as
// scrolling n lines back moves the text they hold.
func (s *selection) shiftRows(n int) {
	if n == 0 || len(s.rows) == 0 {
		return
	}
	moved := make(map[int]string, len(s.rows))
	for y, l := range s.rows {
		moved[y+n] = l
	}
	s.rows = moved
}

// highlight draws the selection over rendered lines.
func (s selection) highlight(lines []string, w int) []string {
	out := make([]string, len(lines))
	for y, l := range lines {
		from, to, ok := s.span(y, w)
		if !ok {
			out[y] = l
			continue
		}
		plain := padRight(ansi.Strip(l), w)
		out[y] = ansi.Cut(l, 0, from) + "\x1b[0m" + styleSelection.Render(ansi.Cut(plain, from, to)) + "\x1b[0m" + ansi.Cut(l, to, w)
	}
	return out
}

// lineAt is the cell range of the text on a rendered line, without the
// spaces it ends with: a triple click takes the line, and a line of
// trailing blanks copied with it would be the terminal's padding, not the
// program's output. An empty line selects nothing.
func lineAt(line string) (from, to int) {
	plain := strings.TrimRight(ansi.Strip(line), " \t")
	if w := ansi.StringWidth(plain); w > 0 {
		return 0, w - 1
	}
	return 0, -1
}

// wordAt returns the cell range of the word under x on a rendered line.
func wordAt(line string, x int) (from, to int) {
	plain := ansi.Strip(line)
	w := ansi.StringWidth(plain)
	isWord := func(i int) bool {
		c := ansi.Cut(plain, i, i+1)
		r := []rune(c)
		return len(r) > 0 && !unicode.IsSpace(r[0]) && !strings.ContainsRune("│|\"'`()[]{}<>,;", r[0])
	}
	if x < 0 || x >= w || !isWord(x) {
		return x, x
	}
	from, to = x, x
	for from > 0 && isWord(from-1) {
		from--
	}
	for to+1 < w && isWord(to+1) {
		to++
	}
	return from, to
}
