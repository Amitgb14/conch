package pane

import (
	"slices"
	"strings"
	"sync/atomic"

	"github.com/charmbracelet/x/ansi"
)

// A pane's raw output is for a client with a terminal of its own — the Mac
// app's Ghostty — which would rather be the pane's terminal than draw a
// copy of conch's screen. It gets a replay that rebuilds the history and
// the screen, then every piece of output after it, in order. Both are
// taken with emuMu held, the same lock the read loop writes the emulator
// under, so nothing falls between the replay and the first piece, and
// nothing is in both.
//
// conch's emulator still sees everything first and still answers the
// program's questions (where is the cursor, what are you, what colour is
// the background), so those are taken out of what a raw client is given
// (queryFilter): a terminal fed them would answer as well, and the
// program would read two replies to one question.

// rawBacklog is how many pieces of output a raw client may fall behind by
// before it is given a fresh replay instead (RawSub.Lost).
const rawBacklog = 512

// RawSub is one client's raw output. C carries each piece; once Lost
// says so, pieces stopped being sent and the client is to start again.
type RawSub struct {
	C     chan []byte
	lost  atomic.Bool
	lostC chan struct{} // closed when lost
}

// Lost reports whether the subscriber fell behind and missed output.
func (s *RawSub) Lost() bool { return s.lost.Load() }

// LostC is closed once the subscriber has fallen behind, so whoever reads
// C, which will carry nothing more, is woken to start again.
func (s *RawSub) LostC() <-chan struct{} { return s.lostC }

func newRawSub(n int) *RawSub {
	return &RawSub{C: make(chan []byte, n), lostC: make(chan struct{})}
}

// SubscribeRaw starts raw output: the replay to begin with, the size it is
// for, and the subscription carrying what follows.
func (p *Pane) SubscribeRaw() (replay string, cols, rows int, sub *RawSub) {
	sub = newRawSub(rawBacklog)
	p.emuMu.Lock()
	defer p.emuMu.Unlock()
	replay, cols, rows = p.rawReplayLocked()
	if p.raws == nil {
		p.raws = map[*RawSub]struct{}{}
	}
	if len(p.raws) == 0 {
		p.rawq = queryFilter{} // nobody was reading: what it held is stale
	}
	p.raws[sub] = struct{}{}
	return replay, cols, rows, sub
}

// UnsubscribeRaw ends a subscription. Its channel is not closed: the
// read loop may hold a piece for it, and the subscriber stops reading.
func (p *Pane) UnsubscribeRaw(sub *RawSub) {
	p.emuMu.Lock()
	delete(p.raws, sub)
	p.emuMu.Unlock()
}

// lose marks a subscription behind: it is given nothing more, and its
// server sends a fresh replay in its place. Called with emuMu held.
func (p *Pane) lose(sub *RawSub) {
	sub.lost.Store(true)
	close(sub.lostC)
	delete(p.raws, sub)
}

// resetRawLocked starts every raw subscriber again from a replay at the
// pane's new size. A raw client's terminal reflows what it shows when its
// width changes — Ghostty does — and conch's emulator does not. Height
// parts them too: a shorter Ghostty pushes rows off the top into its
// history to keep the cursor where it was, while conch's emulator cuts
// them off the bottom, so the cursor sits on different rows. A program
// that repaints by counting rows up from the cursor (Claude Code does)
// then erases the wrong ones there, leaving a screen of half-erased
// leftovers that conch's own copy of the pane never had. A replay drawn
// from conch's copy puts the two back in step. What conch's copy does not
// keep is lost from the client's screen at a resize: an inline image
// (kitty graphics), which only the client drew, and its scroll position.
// Called with emuMu held,
// after the emulator took the size: what is queued is in the replay.
func (p *Pane) resetRawLocked() {
	for sub := range p.raws {
		p.lose(sub)
	}
}

// sendRaw hands a piece of output to every raw subscriber. Called with
// emuMu held, right after the emulator took the same output. One that
// is behind is marked lost and given nothing more, rather than holding
// up the read loop.
func (p *Pane) sendRaw(b []byte) {
	if len(b) == 0 || len(p.raws) == 0 {
		return
	}
	piece := slices.Clone(b)
	for sub := range p.raws {
		select {
		case sub.C <- piece:
		default:
			p.lose(sub)
		}
	}
}

// rawReplayLocked is the replay a raw client starts from: the reload's
// replay (history, screen, cursor and modes), with what conch kept as it
// scrolled off the alternate screen put into the main history ahead of
// it, so a terminal that keeps scrollback has it once the program leaves
// the alternate screen. Called with emuMu held.
func (p *Pane) rawReplayLocked() (string, int, int) {
	p.mu.Lock()
	modes := make([]ansi.Mode, 0, len(p.modes))
	for m := range p.modes {
		modes = append(modes, m)
	}
	cursorVisible := p.cursorVisible
	title := p.title
	p.mu.Unlock()
	cols, rows := p.emu.Width(), p.emu.Height()
	var b strings.Builder
	if p.emu.IsAltScreen() && len(p.alt.lines) > 0 {
		// Into the main history first: printed and pushed off by blank
		// rows, before the replay draws everything else.
		lines := make([]string, 0, len(p.alt.lines))
		for _, l := range p.alt.lines {
			lines = append(lines, fitWidth(l, cols))
		}
		b.WriteString(ansi.ResetStyle)
		b.WriteString(strings.Join(lines, "\r\n"+ansi.ResetStyle))
		b.WriteString(strings.Repeat("\r\n", rows))
	}
	b.WriteString(p.replayLocked(modes, cursorVisible))
	if title != "" {
		// The program's own title, written back inside a sequence of
		// conch's: an escape in it would end that one and start its own.
		b.WriteString(ansi.SetWindowTitle(printableTitle(title)))
	}
	return b.String(), cols, rows
}

// queryFilter takes out of raw output the questions conch's emulator
// answers, so a raw client's terminal does not answer them too:
//
//   - device attributes (CSI c, CSI > c, CSI = c)
//   - device status and cursor position reports (CSI n, CSI ? n)
//   - mode reports (DECRQM, CSI Ps $ p and CSI ? Ps $ p)
//   - the foreground, background and cursor colours (OSC 10, 11, 12 with ?)
//   - whether the terminal speaks the Program Status Protocol (OSC 7501 ; ?)
//
// It also takes out OSC 52, the clipboard, both ways: a program setting it
// would write the person's own clipboard from a pane — any remote host,
// any file a pane cats — and one asking for it would be answered with the
// clipboard's contents, typed into the pane. conch's own screen never acts
// on a pane's OSC 52, nor does tmux by default; a raw client is no
// different.
//
// Everything else passes, including the questions only the client's
// terminal can answer — whether it draws images (kitty graphics), its
// name (XTVERSION), its key protocol — since conch's emulator stays
// silent on those. A sequence split between two reads is held until it is
// whole; a held sequence that turns out not to be a query goes out
// unchanged.
type queryFilter struct {
	held []byte // a sequence begun and not yet ended
	// dropping is inside an OSC 52 being taken out, which can be longer
	// than any read: everything up to its end goes.
	dropping bool
}

// maxHeld is the longest sequence held back to see what it is. A query is
// a few bytes; anything longer than this is not one, and goes out.
const maxHeld = 64

// filter returns b without the queries in it. State carries across calls.
func (f *queryFilter) filter(b []byte) []byte {
	if len(f.held) == 0 && !f.dropping && !slices.Contains(b, 0x1b) {
		return b
	}
	in := b
	if len(f.held) > 0 {
		in = append(f.held, b...)
		f.held = nil
	}
	out := make([]byte, 0, len(in))
	for i := 0; i < len(in); {
		if f.dropping {
			n, done := oscEnd(in[i:])
			i += n
			f.dropping = !done
			if !done && i < len(in) { // an ESC last: ESC \ may be split
				f.held = slices.Clone(in[i:])
				return out
			}
			continue
		}
		c := in[i]
		if c != 0x1b {
			out = append(out, c)
			i++
			continue
		}
		if clipboardOSC(in[i:]) {
			f.dropping = true
			i += 5 // ESC ] 5 2 ;
			continue
		}
		n, query, whole := sequence(in[i:])
		if !whole {
			if len(in)-i > maxHeld {
				out = append(out, c) // too long to be a question
				i++
				continue
			}
			f.held = slices.Clone(in[i:])
			break
		}
		if !query {
			out = append(out, in[i:i+n]...)
		}
		i += n
	}
	return out
}

// sequence looks at an escape sequence at the start of b: how long it is,
// whether it is a query to take out, and whether it is whole yet.
func sequence(b []byte) (n int, query, whole bool) {
	if len(b) < 2 {
		return 0, false, false
	}
	switch b[1] {
	case '[':
		// CSI: parameters and intermediates, then a final byte 0x40–0x7e.
		for i := 2; i < len(b); i++ {
			c := b[i]
			if c >= 0x40 && c <= 0x7e {
				return i + 1, csiQuery(b[2:i], c), true
			}
			if c < 0x20 || c > 0x3f {
				return i, false, true // not a CSI after all: let it through
			}
		}
		return 0, false, false
	case ']':
		// OSC: up to BEL or ESC \.
		for i := 2; i < len(b); i++ {
			switch {
			case b[i] == 0x07:
				return i + 1, oscQuery(b[2:i]), true
			case b[i] == 0x1b && i+1 < len(b) && b[i+1] == '\\':
				return i + 2, oscQuery(b[2:i]), true
			case b[i] == 0x1b && i+1 == len(b):
				return 0, false, false
			case i-2 > maxHeld:
				return i, false, true // a long string: not a question
			}
		}
		return 0, false, false
	}
	return 1, false, true // any other escape: left alone, byte by byte
}

// csiQuery says whether a CSI with these parameters and this final byte is
// a question conch's emulator answers.
func csiQuery(params []byte, final byte) bool {
	switch final {
	case 'c':
		return true // DA1, DA2, DA3
	case 'n':
		return true // DSR, CPR
	case 'p':
		return len(params) > 0 && params[len(params)-1] == '$' // DECRQM
	}
	return false
}

// clipboardOSC says whether b starts an OSC 52, the clipboard. A start
// cut short by the end of a read ("ESC ] 5") is held as any unfinished
// sequence is, so this sees the whole of "ESC ] 5 2 ;" when the next read
// brings it.
func clipboardOSC(b []byte) bool {
	return len(b) >= 5 && string(b[:5]) == "\x1b]52;"
}

// oscEnd is how much of b an OSC being dropped takes, through its BEL or
// ESC \, and whether that end was in b.
func oscEnd(b []byte) (int, bool) {
	for i := 0; i < len(b); i++ {
		switch {
		case b[i] == 0x07:
			return i + 1, true
		case b[i] == 0x1b && i+1 == len(b):
			return i, false // ESC \ may be split across reads: held
		case b[i] == 0x1b && b[i+1] == '\\':
			return i + 2, true
		case b[i] == 0x1b:
			return i, true // an ESC that is not ST ends the string, and starts what follows
		}
	}
	return len(b), false
}

// printableTitle is a window title without control characters.
func printableTitle(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return -1
		}
		return r
	}, s)
}

// oscQuery says whether an OSC body is a question conch answers.
func oscQuery(body []byte) bool {
	s := string(body)
	for _, q := range []string{"10;?", "11;?", "12;?", "7501;?"} {
		if s == q {
			return true
		}
	}
	return false
}
