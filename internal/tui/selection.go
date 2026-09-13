package tui

import (
	"strings"
	"unicode"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// selection is a text selection over the visible pane, in view cells.
type selection struct {
	paneID     string
	ax, ay     int // anchor: where the drag started
	bx, by     int // head: where it is now
	dragging   bool
	hasContent bool // moved past the anchor, so there is something to copy
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

// text extracts the selected text from rendered lines. Trailing spaces are
// trimmed from each line, as terminals do.
func (s selection) text(lines []string, w int) string {
	var out []string
	for y := range lines {
		from, to, ok := s.span(y, w)
		if !ok {
			continue
		}
		out = append(out, strings.TrimRight(ansi.Cut(ansi.Strip(lines[y]), from, to), " "))
	}
	return strings.Join(out, "\n")
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
