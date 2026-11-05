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
			sub.lost.Store(true)
			close(sub.lostC)
			delete(p.raws, sub)
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
		b.WriteString(ansi.SetWindowTitle(title))
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
// Everything else passes, including the questions only the client's
// terminal can answer — whether it draws images (kitty graphics), its
// name (XTVERSION), its key protocol — since conch's emulator stays
// silent on those. A sequence split between two reads is held until it is
// whole; a held sequence that turns out not to be a query goes out
// unchanged.
type queryFilter struct {
	held []byte // a sequence begun and not yet ended
}

// maxHeld is the longest sequence held back to see what it is. A query is
// a few bytes; anything longer than this is not one, and goes out.
const maxHeld = 64

// filter returns b without the queries in it. State carries across calls.
func (f *queryFilter) filter(b []byte) []byte {
	if len(f.held) == 0 && !slices.Contains(b, 0x1b) {
		return b
	}
	in := b
	if len(f.held) > 0 {
		in = append(f.held, b...)
		f.held = nil
	}
	out := make([]byte, 0, len(in))
	for i := 0; i < len(in); {
		c := in[i]
		if c != 0x1b {
			out = append(out, c)
			i++
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
