package pane

import (
	"strings"
	"unicode/utf8"
)

// maxTitle is the longest window title kept.
const maxTitle = 1024

// titleOf is the window title a sequence carries, if it is one (OSC 0 or
// OSC 2). Agents put symbols in their titles, so what the emulator makes
// of them is not enough: this keeps the text as it came, minus anything
// that is not valid UTF-8.
func titleOf(seq oscSeq) (string, bool) {
	if seq.num != "0" && seq.num != "2" {
		return "", false
	}
	data := seq.data
	if len(data) > maxTitle {
		data = data[:maxTitle]
	}
	return strings.ToValidUTF8(data, string(utf8.RuneError)), true
}

// titleScanner reads titles out of raw output on its own, for a caller
// that wants nothing else from the stream.
type titleScanner struct{ osc oscScanner }

// scan consumes output and returns the last complete title found in it.
func (s *titleScanner) scan(b []byte) (title string, ok bool) {
	for _, seq := range s.osc.scan(b) {
		if t, got := titleOf(seq); got {
			title, ok = t, true
		}
	}
	return title, ok
}
