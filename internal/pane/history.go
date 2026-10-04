package pane

import (
	"reflect"
	"strings"
	"unsafe"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
)

// What scrolls off the main screen is kept as rendered text — the line's
// characters with its colours and links as escape codes — not as the
// emulator's cells. A cell is over a hundred bytes, so ten thousand lines
// of 120 columns took around 145 MB a pane; as text they take well under a
// tenth of that. Nothing needs the cells once a line has scrolled away:
// frames, search and the reload replay all render a line to text anyway.
//
// So after every piece of output the emulator takes, the lines it pushed
// into its own scrollback are rendered, added to the pane's history and
// cleared from the emulator, which then never holds more than a piece's
// worth.

// historyMax is how many lines of the main screen's history a pane keeps:
// as many as the emulator kept by itself.
const historyMax = vt.DefaultScrollbackSize

// takeScrollback moves the lines the emulator has pushed into its
// scrollback into the pane's history. Called with emuMu held, after every
// write to the emulator.
func (p *Pane) takeScrollback() {
	sb := p.emu.Scrollback()
	if sb.Len() == 0 {
		return
	}
	for _, l := range sb.Lines() {
		p.hist = append(p.hist, l.Render())
	}
	sb.Clear()
	p.hist = keepLast(p.hist, historyMax)
}

// keepLast drops lines from the front past n. It reslices rather than
// moving the rest down, so a full history costs nothing per line it takes;
// append copies what is left when it next grows the array.
func keepLast(lines []string, n int) []string {
	extra := len(lines) - n
	if extra <= 0 {
		return lines
	}
	clear(lines[:extra]) // let the dropped lines go before the array does
	return lines[extra:]
}

// fitWidth cuts a rendered line to cols columns. History is not rewrapped
// when the pane is resized, so a line kept while it was wider can be too
// long for it now.
func fitWidth(line string, cols int) string {
	if ansi.StringWidth(line) <= cols {
		return line
	}
	return ansi.Truncate(line, cols, "")
}

// historyText is a history line as plain text, as cellText makes a screen
// row: no styling, cut to the screen's width, trailing blanks trimmed.
func historyText(line string, cols int) string {
	return strings.TrimRight(ansi.Strip(fitWidth(line, cols)), " ")
}

// clearHistory forgets the main screen's history when the program erases
// the scrollback (ESC [3J), as the emulator does its own. It runs inside
// the emulator's Write, with emuMu held, and leaves the sequence to the
// emulator's own handler.
func (p *Pane) clearHistory(params ansi.Params) bool {
	if n, _, _ := params.Param(0, 0); n == 3 && !p.emu.IsAltScreen() {
		p.hist = nil
	}
	return false
}

// dropAltScrollback stops the emulator keeping scrollback for the
// alternate screen. It keeps one, as cells, for every line scrolled or
// erased there — a whole screen at every ESC [2J — and nothing can read it:
// its Scrollback is the main screen's. An agent with a full-screen
// interface filled it to ten thousand lines, around 140 MB, that were never
// looked at. What scrolls off the alternate screen is kept by altscroll.go
// instead. The emulator offers no way to reach that screen, so it is found
// by reflection; if the emulator changes shape this does nothing, and
// TestAltScreenKeepsNoCells says so.
func dropAltScrollback(e *vt.Emulator) {
	f := reflect.ValueOf(e).Elem().FieldByName("scrs")
	if f.Kind() != reflect.Array || f.Len() != 2 || f.Type().Elem() != reflect.TypeFor[vt.Screen]() {
		return
	}
	alt := (*vt.Screen)(unsafe.Pointer(f.Index(1).UnsafeAddr()))
	alt.SetScrollback(nil)
}

// newEmulator makes the emulator for a pane.
func (p *Pane) newEmulator(cols, rows int) {
	p.emu = vt.NewEmulator(cols, rows)
	dropAltScrollback(p.emu)
	p.setCallbacks()
}
