package pane

import (
	"bytes"
	"slices"
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

// splitAfter splits b after every occurrence of sep; every byte comes out
// once, in order.
func splitAfter(b []byte, sep string) [][]byte {
	var out [][]byte
	for len(b) > 0 {
		i := bytes.Index(b, []byte(sep))
		if i < 0 {
			break
		}
		out = append(out, b[:i+len(sep)])
		b = b[i+len(sep):]
	}
	if len(b) > 0 {
		out = append(out, b)
	}
	return out
}

// altScroll keeps what scrolled off the alternate screen.
type altScroll struct {
	lines []string // oldest first
	prev  []string // the screen as it was at the last look
	base  []string // the screen as it was when the last whole frame was in
	back  int      // lines the program's own view has moved back, and not yet forward
	on    bool     // whether the alternate screen is in use
}

// note is given the screen as it is now, and keeps whatever has scrolled off
// it since the last look. Both are rendered lines, trailing blanks trimmed.
func (a *altScroll) note(now []string) {
	if len(now) == 0 {
		return
	}
	shift := scrolledBy(a.prev, now)
	a.keep(a.prev[:shift])
	a.prev = append(a.prev[:0], now...)
	if shift > 0 {
		a.base = append(a.base[:0], now...) // the next frame starts from here
	}
}

// keep adds lines that scrolled away, dropping the oldest past the cap.
func (a *altScroll) keep(lines []string) {
	a.lines = keepLast(append(a.lines, lines...), altHistoryMax)
}

// noteFrame is given the screen once a program has finished drawing a
// frame, and keeps what scrolled off the top of the part of it that moved.
// An agent's interface scrolls its conversation above a prompt and a
// status line that stay where they are — and the status line changes as
// it works — so the screen as a whole never lines up shifted, and note
// sees nothing. It compares whole frames: halfway through a repaint the
// screen is part new and part old, which would look like a scroll of the
// wrong lines.
func (a *altScroll) noteFrame(now []string) {
	if len(now) == 0 {
		return
	}
	if top, shift := scrolledRegion(a.base, now); shift > 0 {
		// Lines the program showed again by scrolling its own view back
		// are kept already; only what comes after them is new.
		old := min(a.back, shift)
		a.back -= old
		a.keep(a.base[top+old : top+shift])
	} else if _, down := scrolledRegion(now, a.base); down > 0 {
		a.back += down // its view moved back: the wheel over an agent
	} else if !slices.Equal(a.base, now) && len(a.base) == len(now) && changedAbove(a.base, now) {
		a.back = 0 // redrawn outright, as a jump to the bottom does
	}
	a.base = append(a.base[:0], now...)
}

// changedAbove reports whether anything changed in the upper two thirds of
// the screen: more than a prompt or a status line.
func changedAbove(prev, now []string) bool {
	for i := 0; i < len(prev)*2/3; i++ {
		if prev[i] != now[i] {
			return true
		}
	}
	return false
}

// reset forgets everything: the program left the alternate screen, or the
// pane is being resized, and what was kept no longer lines up.
func (a *altScroll) reset() {
	a.lines, a.prev, a.base, a.back = nil, nil, nil, 0
}

// scrolledRegion finds a scroll in part of the screen: rows at the top
// that stayed the same (a header), then a long run of the old screen's
// rows shifted up by shift, with what is left below the run taken for a
// footer that stayed put or was redrawn. It reports where the part that
// moved begins and how far it moved; shift is 0 when nothing did.
//
// Only a long run counts, measured in rows with text on them: a repaint
// that shares a few lines with the screen before is not a scroll. And the
// header and footer are held to a third of the screen each, so a prompt
// being typed into or a status line ticking over — the bottom of the
// screen changing under a conversation that stayed still — is not taken
// for one.
func scrolledRegion(prev, now []string) (top, shift int) {
	n := len(prev)
	if n == 0 || n != len(now) {
		return 0, 0
	}
	for top < n && prev[top] == now[top] {
		top++
	}
	if top > n/3 {
		return 0, 0 // unchanged, or changed too little of it to tell
	}
	need := max(altShiftMin+1, n/5)
	for s := 1; top+s < n; s++ {
		run, text := 0, 0
		for top+s+run < n && prev[top+s+run] == now[top+run] {
			if strings.TrimSpace(now[top+run]) != "" {
				text++
			}
			run++
		}
		if text >= need && n-(top+s+run) <= n/3 {
			return top, s
		}
	}
	return 0, 0
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
