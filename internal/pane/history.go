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

// scrollRegion is the rows the emulator's current screen scrolls, top and
// bottom from 1, and whether that is less than the whole screen — a
// program that keeps its prompt still below lines that scroll sets one
// (DECSTBM). A replay has to set it again, or the terminal it is drawn in
// scrolls the prompt away with everything else (#282). The emulator says
// which screen is current only to itself, so it is found by reflection; if
// the emulator changes shape this says there is none, and
// TestRawReplayCarriesScrollRegion says so.
func scrollRegion(e *vt.Emulator) (top, bottom int, ok bool) {
	scr := currentScreen(e)
	if scr == nil {
		return 0, 0, false
	}
	r := scr.ScrollRegion()
	if r.Min.Y <= 0 && r.Max.Y >= e.Height() || r.Min.Y >= r.Max.Y {
		return 0, 0, false
	}
	return r.Min.Y + 1, r.Max.Y, true
}

// currentScreen is the emulator's screen in use, main or alternate, found
// by reflection as scrollRegion finds it; nil if the emulator changed shape.
func currentScreen(e *vt.Emulator) *vt.Screen {
	f := reflect.ValueOf(e).Elem().FieldByName("scr")
	if !f.IsValid() || f.Type() != reflect.TypeFor[*vt.Screen]() {
		return nil
	}
	return *(**vt.Screen)(unsafe.Pointer(f.UnsafeAddr()))
}

// pendingWrap says whether the cursor sits past the last column waiting
// to wrap: a line exactly as wide as the screen leaves it so, and the
// next character goes on the next row. A replay that only places the
// cursor puts it in the last column instead, one row short of where the
// program thinks it is, and a program that repaints by counting rows up
// from the cursor (Claude Code does) then writes over the wrong ones on a
// raw client. The emulator keeps it to itself (atPhantom); false if it
// changed shape, which TestRawClientKeepsAPendingWrap says.
func pendingWrap(e *vt.Emulator) bool {
	f := reflect.ValueOf(e).Elem().FieldByName("atPhantom")
	if !f.IsValid() || f.Kind() != reflect.Bool {
		return false
	}
	return *(*bool)(unsafe.Pointer(f.UnsafeAddr()))
}

// savedCursor is where the current screen's ESC 7 left the cursor, for a
// replay to save it there again: ESC 8 after a client attaches goes back
// to it, not to the top left. ok is false if the emulator changed shape,
// which TestRawClientKeepsASavedCursor says.
func savedCursor(e *vt.Emulator) (x, y int, ok bool) {
	scr := currentScreen(e)
	if scr == nil {
		return 0, 0, false
	}
	f := reflect.ValueOf(scr).Elem().FieldByName("saved")
	if !f.IsValid() || f.Type() != reflect.TypeFor[vt.Cursor]() {
		return 0, 0, false
	}
	c := *(*vt.Cursor)(unsafe.Pointer(f.UnsafeAddr()))
	return c.X, c.Y, true
}

// parserData is how much of one escape sequence's payload — an OSC's
// text, a DCS's, an APC's — the emulator keeps; the rest of a longer one
// is dropped, as the emulator always did past its own limit. Nothing conch
// reads from a payload comes close: a title, a link, a working directory,
// an OSC 7501 record. The long ones are an image, which conch's emulator
// does not draw, and OSC 52, which raw.go takes out before any client.
// It is the ansi package's own default.
const parserData = 64 * 1024

// shrinkParserData gives the emulator's parser a payload buffer of
// parserData. vt.NewEmulator gives it 4 MB, allocated whole for every
// terminal whether anything long ever arrives or not: 184 of a real
// server's 300 MB with 46 panes (a heap profile, 2026-11-16). The
// emulator offers no way to the parser, so it is found by reflection; if
// the emulator changes shape this does nothing, and
// TestParserDataIsSmall says so.
func shrinkParserData(e *vt.Emulator) {
	f := reflect.ValueOf(e).Elem().FieldByName("parser")
	if !f.IsValid() || f.Type() != reflect.TypeFor[*ansi.Parser]() {
		return
	}
	if parser := *(**ansi.Parser)(unsafe.Pointer(f.UnsafeAddr())); parser != nil {
		parser.SetDataSize(parserData)
	}
}

// newEmulator makes the emulator for a pane.
func (p *Pane) newEmulator(cols, rows int) {
	p.emu = vt.NewEmulator(cols, rows)
	dropAltScrollback(p.emu)
	shrinkParserData(p.emu)
	p.setCallbacks()
}
