package pane

// oscScanner extracts complete OSC sequences from raw terminal output —
// `ESC ] <num> ; <data>` ended by BEL or ST. conch reads them itself
// rather than taking them from the emulator: the emulator truncates a
// title at the first multi-byte character and drops one containing ';',
// and it knows nothing of the sequences conch cares about beyond that.
// State is kept across chunks, so a sequence split between two reads is
// still recognised.
type oscScanner struct {
	state int
	num   []byte
	data  []byte
	out   []oscSeq
}

// oscSeq is one complete sequence: its number and everything after the
// first ';'.
type oscSeq struct{ num, data string }

const (
	oscGround = iota
	oscEsc    // saw ESC
	oscNum    // inside "ESC ] <digits>"
	oscData   // inside the payload
	oscDataEsc
	// maxOSC is the longest payload kept. A title is a line; a program
	// status report is a few keys and two short base64 values; anything
	// longer is not something conch acts on, and a program that writes
	// megabytes into an OSC must not grow this buffer with it.
	maxOSC = 4096
)

// scan consumes output and returns every complete sequence in it, in
// order. The slice is reused: read it before the next call.
func (s *oscScanner) scan(b []byte) []oscSeq {
	s.out = s.out[:0]
	for _, c := range b {
		switch s.state {
		case oscGround:
			// 8-bit C1 OSC (0x9d) is not recognised: that byte also occurs
			// inside UTF-8 text.
			if c == 0x1b {
				s.state = oscEsc
			}
		case oscEsc:
			if c == ']' {
				s.state, s.num = oscNum, s.num[:0]
			} else if c != 0x1b {
				s.state = oscGround
			}
		case oscNum:
			switch {
			case c >= '0' && c <= '9' && len(s.num) < 5:
				s.num = append(s.num, c)
			case c == ';':
				s.state, s.data = oscData, s.data[:0]
			default:
				s.state = oscGround
			}
		case oscData:
			switch c {
			case 0x07:
				s.finish()
			case 0x1b:
				s.state = oscDataEsc
			default:
				if len(s.data) < maxOSC {
					s.data = append(s.data, c)
				}
			}
		case oscDataEsc:
			if c == '\\' {
				s.finish()
			} else {
				s.state = oscGround // aborted sequence
			}
		}
	}
	return s.out
}

func (s *oscScanner) finish() {
	s.state = oscGround
	s.out = append(s.out, oscSeq{num: string(s.num), data: string(s.data)})
}
