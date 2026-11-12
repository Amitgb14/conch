package tui

import (
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/gitx"
	"github.com/Amitgb14/conch/internal/proto"
)

// printablePane is p with what a program or an agent named itself made
// safe to draw: a newer server cleans these itself, an older one sends
// them as they were set, and a line break in a title then broke the
// layout of every row below it. Escapes are safeView's to stop.
func printablePane(p proto.PaneInfo) proto.PaneInfo {
	p.Name = gitx.Printable(p.Name)
	p.Title = gitx.Printable(p.Title)
	p.SSH = gitx.Printable(p.SSH)
	if p.Agent != nil {
		a := *p.Agent
		a.Message, a.Reason = gitx.Printable(a.Message), gitx.Printable(a.Reason)
		p.Agent = &a
	}
	return p
}

// maxShownTabName is the most of an agent's name for a tab that is kept:
// a tab bar holds several.
const maxShownTabName = 40

// tabName is name cut to maxShownTabName cells.
func tabName(name string) string {
	return ansi.Truncate(name, maxShownTabName, "…")
}

// safeView is the screen as it may reach the terminal conch runs in:
// colours (SGR) and links (OSC 8), which is all conch draws with, and
// nothing else a terminal would act on.
//
// The screen is made of text from elsewhere — a pane's title and name, an
// agent's message, a pull request's title, a view.show's reason, a
// branch, whatever was pasted into the palette — and any of it drawn raw
// is an instruction to the person's own terminal: OSC 52 writes their
// clipboard, OSC 2 retitles the window, a clear or a cursor move tears the
// layout. The server cleans what it knows of, and so do the places that
// draw such text; this is the last word for whatever either missed, or
// for an older server that cleans nothing. Another escape is dropped
// whole, a control character but the line break dropped (a tab becomes a
// space), a C1 control dropped, and a byte that is not UTF-8 shown as
// U+FFFD — a terminal may take a raw 0x9b for the start of a sequence.
func safeView(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == 0x1b:
			n, keep := viewEscape(s[i:])
			if keep {
				b.WriteString(s[i : i+n])
			}
			i += n
		case c == '\n':
			b.WriteByte(c)
			i++
		case c == '\t':
			b.WriteByte(' ')
			i++
		case c < 0x20 || c == 0x7f:
			i++
		case c < 0x80:
			b.WriteByte(c)
			i++
		default:
			r, size := utf8.DecodeRuneInString(s[i:])
			switch {
			case r == utf8.RuneError && size == 1:
				b.WriteRune(utf8.RuneError)
			case r >= 0x80 && r <= 0x9f:
			default:
				b.WriteString(s[i : i+size])
			}
			i += size
		}
	}
	return b.String()
}

// viewEscape is how long the escape at the start of s is, and whether it
// is one conch draws with.
func viewEscape(s string) (int, bool) {
	if len(s) < 2 {
		return len(s), false
	}
	switch s[1] {
	case '[':
		// CSI: parameters, intermediates, a final byte. Kept only as SGR —
		// digits, ; and : (an underline colour's 58:2::r:g:b), then m.
		sgr := true
		for i := 2; i < len(s); i++ {
			c := s[i]
			switch {
			case c >= 0x40 && c <= 0x7e:
				return i + 1, sgr && c == 'm'
			case c >= '0' && c <= '9', c == ';', c == ':':
			case c >= 0x20 && c <= 0x3f:
				sgr = false
			default:
				return i, false // not a CSI after all: what follows is looked at alone
			}
		}
		return len(s), false
	case ']', 'P', 'X', '^', '_':
		// A string, through BEL (OSC only) or ESC \. Kept only as a link
		// (OSC 8) with nothing in it a terminal would act on.
		for i := 2; i < len(s); i++ {
			switch {
			case s[i] == 0x07 && s[1] == ']':
				return i + 1, osc8(s[2:i])
			case s[i] == 0x1b && i+1 < len(s) && s[i+1] == '\\':
				return i + 2, s[1] == ']' && osc8(s[2:i])
			case s[i] == 0x1b:
				return i, false // ends the string; the ESC starts what follows
			}
		}
		return len(s), false
	}
	return 2, false // ESC and one more: a reset, a cursor save, a charset
}

// osc8 says whether an OSC body is a link with nothing else in it.
func osc8(body string) bool {
	if !strings.HasPrefix(body, "8;") {
		return false
	}
	for i := 0; i < len(body); i++ {
		if c := body[i]; c < 0x20 || c == 0x7f {
			return false
		}
	}
	return true
}
