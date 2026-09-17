package pane

// stringFilter keeps UTF-8 text inside escape strings (OSC, DCS, SOS, PM,
// APC) from ending them early. The emulator's parser treats byte 0x9C as
// the C1 string terminator anywhere in such a string, but in UTF-8 it is
// also a continuation byte: "✳" is E2 9C B3. Claude Code sets titles like
// "✳ Terminals SSH support", so the title ended at the ✳ and the rest was
// printed where the cursor was — inside the agent's input box.
//
// Inside a string, each UTF-8 character is replaced by '?' for the emulator.
// Titles are read from the raw output by titleScanner, so they keep their
// text; a lone 0x9C that is not part of a character still ends the string.
// State carries across writes, since a sequence can arrive in pieces.
type stringFilter struct {
	state   int // filterGround, filterEscape, filterString, filterStringEscape
	pending int // continuation bytes still owed to the character being dropped
}

const (
	filterGround = iota
	filterEscape
	filterString
	filterStringEscape
)

// filter returns b as the emulator should see it. It returns b itself when
// nothing needs changing, which is nearly always.
func (f *stringFilter) filter(b []byte) []byte {
	var out []byte // nil until a byte is replaced
	for i, c := range b {
		keep, replace := f.step(c)
		switch {
		case out == nil && keep && !replace:
			continue
		case out == nil:
			out = append(make([]byte, 0, len(b)), b[:i]...)
		}
		switch {
		case replace:
			out = append(out, '?')
		case keep:
			out = append(out, c)
		}
	}
	if out == nil {
		return b
	}
	return out
}

// step advances the state for one byte and says whether to pass it on, or
// to put '?' in its place.
func (f *stringFilter) step(c byte) (keep, replace bool) {
	switch f.state {
	case filterGround:
		if c == 0x1b {
			f.state = filterEscape
		}
		return true, false
	case filterEscape:
		switch c {
		case ']', 'P', 'X', '^', '_': // OSC, DCS, SOS, PM, APC
			f.state, f.pending = filterString, 0
		case 0x1b:
		default:
			f.state = filterGround
		}
		return true, false
	case filterStringEscape:
		if c == '\\' { // ST
			f.state = filterGround
			return true, false
		}
		f.state = filterString
		return f.inString(c)
	}
	return f.inString(c)
}

func (f *stringFilter) inString(c byte) (keep, replace bool) {
	switch {
	case f.pending > 0 && c >= 0x80 && c <= 0xbf:
		f.pending-- // the rest of a character already replaced
		return false, false
	case c >= 0xc0 && c <= 0xf7: // the start of a character
		f.pending = utf8Continuations(c)
		return false, true
	}
	f.pending = 0
	switch c {
	case 0x07, 0x9c: // BEL, or a genuine C1 ST
		f.state = filterGround
	case 0x1b:
		f.state = filterStringEscape
	case 0x18, 0x1a: // CAN and SUB abandon the string
		f.state = filterGround
	}
	return true, false
}

func utf8Continuations(lead byte) int {
	switch {
	case lead >= 0xf0:
		return 3
	case lead >= 0xe0:
		return 2
	}
	return 1
}
