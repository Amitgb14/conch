package pane

import (
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
)

// A program on the alternate screen — an agent's own interface, vim, less —
// has no scrollback: the terminal keeps none for that screen, and neither
// does the emulator, because nothing scrolls off it. The program repaints
// instead. So what it showed a page ago is gone, and dragging over its pane
// can only ever reach a screenful.
//
// conch watches the screen between frames and notices when its content has
// moved up: when the rows of the new screen are the old ones shifted, the
// rows that fell off the top are kept here. That reconstructs the scrollback
// of anything whose output scrolls — an agent's transcript, a log being
// tailed — without keeping a copy of every repaint. A program that redraws
// its whole screen (a full-screen editor) leaves nothing behind, which is
// right: none of it scrolled away.
const (
	// altHistoryMax is how many lines are kept per pane. A screen is around
	// fifty, so this is a few hundred screens: enough to look back through a
	// long answer, far less than a session's whole output.
	altHistoryMax = 5000
	// altShiftMin is the least of the old screen that has to still be there,
	// and line up, for this to be scrolling rather than a repaint that
	// happens to share a line.
	altShiftMin = 2
)

// chunkByLines splits output so that no more than n newlines go into the
// emulator between two looks at the screen. A program printing a hundred
// lines in one write would otherwise scroll the screen away entirely
// between looks, and there would be nothing left to recognise as scrolling.
func chunkByLines(b []byte, n int) [][]byte {
	if n < 1 {
		n = 1
	}
	var out [][]byte
	start, seen := 0, 0
	for i, c := range b {
		if c != '\n' {
			continue
		}
		if seen++; seen >= n {
			out = append(out, b[start:i+1])
			start, seen = i+1, 0
		}
	}
	if start < len(b) {
		out = append(out, b[start:])
	}
	return out
}

// altScroll keeps what scrolled off the alternate screen.
type altScroll struct {
	lines []string // oldest first
	prev  []string // the screen as it was at the last look
	on    bool     // whether the alternate screen is in use
}

// note is given the screen as it is now, and keeps whatever has scrolled off
// it since the last look. Both are rendered lines, trailing blanks trimmed.
func (a *altScroll) note(now []string) {
	if len(now) == 0 {
		return
	}
	shift := scrolledBy(a.prev, now)
	for i := 0; i < shift; i++ {
		a.lines = append(a.lines, a.prev[i])
	}
	if n := len(a.lines) - altHistoryMax; n > 0 {
		a.lines = append(a.lines[:0], a.lines[n:]...)
	}
	a.prev = append(a.prev[:0], now...)
}

// reset forgets everything: the program left the alternate screen, or the
// pane is being resized, and what was kept no longer lines up.
func (a *altScroll) reset() {
	a.lines, a.prev = nil, nil
}

// scrolledBy reports how many lines the screen moved up between two looks:
// the smallest shift that lines the old screen's tail up with the new
// screen's head. Zero means it was repainted, or did not change.
func scrolledBy(prev, now []string) int {
	if len(prev) == 0 || len(prev) != len(now) {
		return 0
	}
	// Only what the screen had written counts: a program often leaves the
	// bottom blank and fills it as it goes, and those rows hold new text
	// after a scroll, not the old.
	last := lastNonBlank(prev)
	if last < 0 || strings.TrimSpace(now[0]) == "" {
		return 0 // nothing was there to scroll away, or nothing arrived
	}
	// Find the top of the new screen in the old one: that distance is how
	// far it moved. The lines below it have to follow in the same order, or
	// this is a repaint that happens to share a line.
	// The old screen's last line may have been caught half written: a read
	// from the pty can end mid-line, and the rest arrives with the output
	// that scrolls it. So that line only has to begin the new one.
	same := func(i, j int) bool {
		if i == last {
			return strings.HasPrefix(now[j], prev[i])
		}
		return prev[i] == now[j]
	}
	for shift := 1; shift <= last; shift++ {
		if !same(shift, 0) {
			continue
		}
		run := 0
		for i := 0; shift+i <= last && same(shift+i, i); i++ {
			run++
		}
		// Everything left of the old screen has to follow, and one line
		// on its own says nothing: a repaint can share a line by chance.
		if available := last - shift + 1; run == available && available >= altShiftMin {
			return shift
		}
	}
	return 0
}

// lastNonBlank is the index of the last line with anything on it, or -1.
func lastNonBlank(lines []string) int {
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			return i
		}
	}
	return -1
}

// plain renders a screen row as text without styling, for comparing screens.
func plainRow(line uv.Line) string {
	var b strings.Builder
	for _, c := range line {
		if c.Content == "" {
			b.WriteByte(' ')
			continue
		}
		b.WriteString(c.Content)
	}
	return strings.TrimRight(b.String(), " ")
}
