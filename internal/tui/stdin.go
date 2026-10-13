package tui

import (
	"bytes"
	"io"
	"os"
)

// seqHoldMax is the longest unfinished escape sequence held back for the
// next read. A mouse report is about a dozen bytes; anything longer that
// never finished is passed on rather than kept.
const seqHoldMax = 32

// WholeSequences wraps the terminal conch reads keys and mouse from so
// that no read ends partway through an escape sequence.
//
// Bubble Tea reads 256 bytes at a time and parses each read on its own. A
// fast wheel or a moving pointer fills that buffer with mouse reports, and
// when one is cut at the edge — `ESC [ < 6 5 ; 1 3` | `0 ; 4 3 M` — it
// reports the halves as keys: alt+[ and then the runes `<65;130;43M`.
// conch forwards keys to the focused pane, so the shell there printed
// `65;130;43M` at its prompt, a few at a time, while somebody scrolled.
// Holding the unfinished tail back for the next read gives Bubble Tea the
// whole report.
//
// It is still the terminal (Fd, Name, Write, Close are the file's), so
// Bubble Tea puts it in raw mode and waits on it as it would on stdin.
func WholeSequences(f *os.File) io.Reader {
	return &seqReader{File: f, src: f}
}

type seqReader struct {
	*os.File
	src  io.Reader // the file; a test reads from chunks instead
	held []byte
}

func (r *seqReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for {
		if len(r.held) >= len(p) {
			// A buffer too small to finish it in: hand it over as it is.
			n := copy(p, r.held)
			r.held = append(r.held[:0], r.held[n:]...)
			return n, nil
		}
		k := copy(p, r.held)
		r.held = r.held[:0]
		n, err := r.src.Read(p[k:])
		n += k
		if err != nil {
			return n, err
		}
		cut := unfinishedSeq(p[:n], n == len(p))
		r.held = append(r.held, p[cut:n]...)
		if cut > 0 {
			return cut, nil
		}
		// Nothing but the start of a sequence: its rest is already on its
		// way, so wait for it rather than hand over an empty read, which
		// would make Bubble Tea drop what it was holding itself.
	}
}

// unfinishedSeq returns where an escape sequence left unfinished at the
// end of b begins, or len(b) when b ends cleanly.
//
// A read that filled the buffer was cut by its size, so any unfinished
// escape at its end is held. A shorter read ended where the terminal's
// writes did, and a lone ESC or `ESC [` there is a key — Escape, alt+[ —
// that must not wait for the next keypress; only `ESC [ <`, the start of
// a mouse report nobody types, is held then.
func unfinishedSeq(b []byte, full bool) int {
	from := max(len(b)-seqHoldMax, 0)
	i := bytes.LastIndexByte(b[from:], 0x1b)
	if i < 0 {
		return len(b)
	}
	i += from
	tail := b[i+1:]
	switch {
	case len(tail) == 0, len(tail) == 1 && (tail[0] == '[' || tail[0] == 'O'):
		if full {
			return i
		}
		return len(b)
	case tail[0] != '[':
		return len(b)
	}
	// A CSI ends at its final byte (0x40–0x7e); parameters and
	// intermediates before it are 0x20–0x3f.
	for _, c := range tail[1:] {
		if c < 0x20 || c > 0x3f {
			return len(b)
		}
	}
	if full || tail[1] == '<' {
		return i
	}
	return len(b)
}
