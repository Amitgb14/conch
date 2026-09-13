package pane

import (
	"strings"
	"unicode/utf8"
)

// titleScanner extracts window titles (OSC 0 and OSC 2) from raw terminal
// output. The emulator's own OSC parsing truncates titles at the first
// multi-byte UTF-8 character and drops titles containing ';', and agents
// like Claude Code put symbols in their titles. It keeps state across
// chunks, so a sequence split between reads is still recognised.
type titleScanner struct {
	state int
	num   []byte
	data  []byte
}

const (
	tsGround = iota
	tsEsc    // saw ESC
	tsNum    // inside "ESC ] <digits>"
	tsData   // inside the OSC payload
	tsDataEsc
	maxTitle = 1024
)

// scan consumes output and returns the last complete title found in it.
func (s *titleScanner) scan(b []byte) (title string, ok bool) {
	for _, c := range b {
		switch s.state {
		case tsGround:
			// 8-bit C1 OSC (0x9d) is not recognised: that byte also occurs
			// inside UTF-8 text.
			if c == 0x1b {
				s.state = tsEsc
			}
		case tsEsc:
			if c == ']' {
				s.state, s.num = tsNum, s.num[:0]
			} else if c != 0x1b {
				s.state = tsGround
			}
		case tsNum:
			switch {
			case c >= '0' && c <= '9' && len(s.num) < 4:
				s.num = append(s.num, c)
			case c == ';':
				s.state, s.data = tsData, s.data[:0]
			default:
				s.state = tsGround
			}
		case tsData:
			switch c {
			case 0x07:
				title, ok = s.finish(title, ok)
			case 0x1b:
				s.state = tsDataEsc
			default:
				if len(s.data) < maxTitle {
					s.data = append(s.data, c)
				}
			}
		case tsDataEsc:
			if c == '\\' {
				title, ok = s.finish(title, ok)
			} else {
				s.state = tsGround // aborted sequence
			}
		}
	}
	return title, ok
}

func (s *titleScanner) finish(prevTitle string, prevOK bool) (string, bool) {
	s.state = tsGround
	if n := string(s.num); n != "0" && n != "2" {
		return prevTitle, prevOK
	}
	return strings.ToValidUTF8(string(s.data), string(utf8.RuneError)), true
}
