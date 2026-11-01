package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/x/term"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
)

// `conch attach PANE` shows one of the server's panes in the terminal it
// runs in, and is typed into as that pane: the screen conch keeps for it,
// repainted frame by frame, with keys, the mouse and the window's size
// going back. It is how the Mac app (macos/) puts a pane in a view of its
// own — each view is a terminal running this — and it works the same in
// any terminal.
//
// The pane's program never sees this terminal. conch's server is its
// terminal and answers its questions; what arrives here is the screen
// already drawn (proto.Frame). So nothing here emulates anything, and
// what this terminal knows about modes is wrong in a way that matters for
// keys: it never saw the program ask for application cursor keys or the
// mouse. Arrows and their kin therefore go as names (pane.send_keys) for
// the server to encode as the program asked, and the mouse goes as events
// (pane.send_mouse) the server sends only if the program wants them.
// Everything else typed is passed through as it came.

const attachUsage = "usage: conch attach PANE"

// Escapes this terminal is put into, and taken out of: the alternate
// screen so whatever was there comes back, no cursor of its own (the
// frame draws the program's), SGR mouse reports for button presses, drags
// and the wheel, and bracketed paste, so a paste arrives whole.
const (
	attachEnter = "\x1b[?1049h\x1b[?25l\x1b[?1002h\x1b[?1006h\x1b[?2004h"
	attachLeave = "\x1b[?2004l\x1b[?1006l\x1b[?1002l\x1b[?25h\x1b[?1049l"
)

// attachFlush is how long a lone escape, or the start of a sequence
// without its end, is held back for the rest of it: an esc typed on its
// own has to arrive too.
const attachFlush = 25 * time.Millisecond

// attachWheel is the lines one notch of the wheel scrolls the history.
const attachWheel = 3

func runAttach(args []string) error {
	if len(args) != 1 || strings.HasPrefix(args[0], "-") {
		return errors.New(attachUsage)
	}
	if !term.IsTerminal(os.Stdin.Fd()) || !term.IsTerminal(os.Stdout.Fd()) {
		return errors.New("conch attach needs a terminal: it shows the pane in the one it runs in")
	}
	c, err := connect(false)
	if err != nil {
		return err
	}
	defer c.Close()
	id, err := resolvePane(c, args[0])
	if err != nil {
		return err
	}
	state, err := term.MakeRaw(os.Stdin.Fd())
	if err != nil {
		return err
	}
	defer term.Restore(os.Stdin.Fd(), state) //nolint:errcheck
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	defer signal.Stop(winch)
	size := func() (int, int, bool) {
		w, h, err := term.GetSize(os.Stdout.Fd())
		return w, h, err == nil && w > 0 && h > 0
	}
	msg, err := attachLoop(c, id, os.Stdin, os.Stdout, size, winch)
	if msg != "" {
		fmt.Fprintln(os.Stderr, msg)
	}
	return err
}

// attachSession is one attached pane: what is on screen, and how far back
// into its history this view is.
type attachSession struct {
	c     *client.Client
	id    string
	out   io.Writer
	frame *proto.Frame
	// offset is the history scroll this view asked for, the frame's own
	// Offset being what the server has caught up to.
	offset int
}

// attachLoop runs an attached pane until the pane ends, the input ends
// (the window was closed), or the connection goes. What it returns as a
// message is said after the terminal is given back.
func attachLoop(c *client.Client, id string, in io.Reader, out io.Writer, size func() (int, int, bool), winch <-chan os.Signal) (string, error) {
	s := &attachSession{c: c, id: id, out: out}
	io.WriteString(out, attachEnter) //nolint:errcheck
	defer io.WriteString(out, attachLeave)
	s.resize(size)
	if err := call(c, proto.MethodPaneSubscribe, proto.PaneRef{ID: id}, nil); err != nil {
		return "", err
	}

	input := make(chan []byte)
	go func() {
		defer close(input)
		buf := make([]byte, 4096)
		for {
			n, err := in.Read(buf)
			if n > 0 {
				input <- bytes.Clone(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()

	var held []byte
	flush := time.NewTimer(time.Hour)
	flush.Stop()
	for {
		select {
		case msg, ok := <-c.Events:
			if !ok {
				return "conch attach: the connection to the server closed", nil
			}
			if done := s.event(msg); done != "" {
				return done, nil
			}
		case b, ok := <-input:
			if !ok {
				return "", nil // the window went: the pane carries on
			}
			held = s.input(append(held, b...), false)
			flush.Stop()
			if len(held) > 0 {
				flush.Reset(attachFlush)
			}
		case <-flush.C:
			held = s.input(held, true)
		case <-winch:
			s.resize(size)
		}
	}
}

// event handles one push from the server, and says why to stop if the
// pane is gone.
func (s *attachSession) event(msg proto.Message) string {
	switch msg.Event {
	case proto.EventPaneFrame:
		var f proto.Frame
		if json.Unmarshal(msg.Data, &f) != nil || f.ID != s.id {
			return ""
		}
		s.frame = &f
		io.WriteString(s.out, renderAttachFrame(&f)) //nolint:errcheck
	case proto.EventPaneExited, proto.EventPaneClosed:
		var info proto.PaneInfo
		if json.Unmarshal(msg.Data, &info) != nil || info.ID != s.id {
			return ""
		}
		if msg.Event == proto.EventPaneExited {
			return fmt.Sprintf("pane %s ended (exit %d)", s.id, info.ExitCode)
		}
		return fmt.Sprintf("pane %s was closed", s.id)
	}
	return ""
}

func (s *attachSession) resize(size func() (int, int, bool)) {
	if w, h, ok := size(); ok {
		call(s.c, proto.MethodPaneResize, proto.PaneResizeParams{ID: s.id, Cols: w, Rows: h}, nil) //nolint:errcheck
	}
}

// renderAttachFrame is a frame as escapes for this terminal: drawn from
// the top left inside one synchronized update, each line cleared to its
// end so a shorter line leaves nothing of the last, and whatever is below
// the frame's rows cleared too.
func renderAttachFrame(f *proto.Frame) string {
	var b strings.Builder
	b.WriteString("\x1b[?2026h\x1b[H")
	for i, l := range f.Lines {
		if i > 0 {
			b.WriteString("\r\n")
		}
		b.WriteString(l)
		b.WriteString("\x1b[0m\x1b[K")
	}
	b.WriteString("\x1b[0m\x1b[J\x1b[?2026l")
	return b.String()
}

// attachKeys are the sequences sent by name, so the server encodes them
// in the mode the program asked for — which this terminal never heard.
var attachKeys = map[string]string{
	"A": "up", "B": "down", "C": "right", "D": "left", "H": "home", "F": "end",
}

// attachTilde are the CSI n ~ keys sent by name for the same reason.
var attachTilde = map[string]string{
	"1": "home", "2": "insert", "3": "delete", "4": "end", "5": "pgup", "6": "pgdown", "7": "home", "8": "end",
}

// attachMods is xterm's modifier parameter as conch's key names.
func attachMods(p string) string {
	n, err := strconv.Atoi(p)
	if err != nil || n < 2 || n > 8 {
		return ""
	}
	n--
	var mods string
	if n&4 != 0 {
		mods += "ctrl+"
	}
	if n&2 != 0 {
		mods += "alt+"
	}
	if n&1 != 0 {
		mods += "shift+"
	}
	return mods
}

// input handles what was typed, and answers what it is still waiting on
// the rest of. final hands over whatever is held, as typed: a lone esc
// after the wait is the esc key.
func (s *attachSession) input(b []byte, final bool) []byte {
	var text []byte
	send := func() {
		if len(text) > 0 {
			s.typed()
			call(s.c, proto.MethodPaneSendText, proto.PaneSendTextParams{ID: s.id, Text: string(text)}, nil) //nolint:errcheck
			text = text[:0]
		}
	}
	for len(b) > 0 {
		if b[0] != 0x1b {
			i := bytes.IndexByte(b, 0x1b)
			if i < 0 {
				i = len(b)
			}
			text = append(text, b[:i]...)
			b = b[i:]
			continue
		}
		n, kind := attachSequence(b)
		switch {
		case n == 0 && !final:
			send()
			return b // the rest of it has not arrived yet
		case n == 0:
			text = append(text, b...)
			b = nil
			continue
		}
		seq := b[:n]
		b = b[n:]
		switch kind {
		case attachMouse:
			send()
			s.mouse(seq)
		case attachPaste:
			send()
			body := seq[len("\x1b[200~") : len(seq)-len("\x1b[201~")]
			s.typed()
			call(s.c, proto.MethodPaneSendText, proto.PaneSendTextParams{ID: s.id, Text: string(body), Paste: true}, nil) //nolint:errcheck
		case attachNamed:
			send()
			s.typed()
			call(s.c, proto.MethodPaneSendKeys, proto.PaneSendKeysParams{ID: s.id, Keys: []string{attachKeyName(seq)}}, nil) //nolint:errcheck
		default:
			text = append(text, seq...)
		}
	}
	send()
	return nil
}

const (
	attachText = iota
	attachMouse
	attachPaste
	attachNamed
)

// attachSequence is how long the escape sequence at the start of b is and
// what kind, or 0 when it has not all arrived. b starts with ESC.
func attachSequence(b []byte) (int, int) {
	if len(b) < 2 {
		return 0, attachText
	}
	switch b[1] {
	case '[':
	case 'O': // SS3: an arrow or F1–F4 in application mode
		if len(b) < 3 {
			return 0, attachText
		}
		if _, ok := attachKeys[string(b[2])]; ok {
			return 3, attachNamed
		}
		return 3, attachText
	default:
		return 2, attachText // alt and a key
	}
	if bytes.HasPrefix(b, []byte("\x1b[200~")) {
		end := bytes.Index(b, []byte("\x1b[201~"))
		if end < 0 {
			return 0, attachText
		}
		return end + len("\x1b[201~"), attachPaste
	}
	if len("\x1b[200~") > len(b) && bytes.HasPrefix([]byte("\x1b[200~"), b) {
		return 0, attachText
	}
	// CSI: parameters and intermediates, then a final byte in @–~.
	for i := 2; i < len(b); i++ {
		c := b[i]
		if c >= 0x40 && c <= 0x7e {
			seq := b[:i+1]
			switch {
			case len(seq) > 3 && seq[2] == '<' && (c == 'M' || c == 'm'):
				return i + 1, attachMouse
			case c == '~':
				if _, ok := attachTilde[attachParam(seq, 0)]; ok {
					return i + 1, attachNamed
				}
			default:
				if _, ok := attachKeys[string(c)]; ok {
					return i + 1, attachNamed
				}
			}
			return i + 1, attachText
		}
		if c < 0x20 || c > 0x7e {
			return i, attachText // not a CSI after all: pass on what there is
		}
	}
	return 0, attachText
}

// attachParam is the nth ;-separated parameter of a CSI sequence.
func attachParam(seq []byte, n int) string {
	body := string(seq[2 : len(seq)-1])
	parts := strings.Split(body, ";")
	if n < len(parts) {
		return parts[n]
	}
	return ""
}

// attachKeyName is the key a named sequence stands for: "\x1b[1;5A" is
// ctrl+up, "\x1b[5~" pgup, "\x1bOA" up.
func attachKeyName(seq []byte) string {
	final := seq[len(seq)-1]
	if seq[1] == 'O' {
		return attachKeys[string(final)]
	}
	if final == '~' {
		return attachMods(attachParam(seq, 1)) + attachTilde[attachParam(seq, 0)]
	}
	return attachMods(attachParam(seq, 1)) + attachKeys[string(final)]
}

// typed brings the view back to the live screen, as typing does in any
// terminal: what is typed is answered down there.
func (s *attachSession) typed() {
	if s.offset != 0 {
		s.offset = 0
		call(s.c, proto.MethodPaneScroll, proto.PaneScrollParams{ID: s.id}, nil) //nolint:errcheck
	}
}

// mouse handles one SGR mouse report from this terminal. The wheel is
// the rule the TUI keeps (AGENTS.md): on the main screen it scrolls
// conch's history, on the alternate screen it goes to a program that asked
// for the mouse, and otherwise moves it by arrows, as terminals do for a
// full-screen program that did not. Presses, releases and drags go to the
// program only when it asked; the server checks that too.
func (s *attachSession) mouse(seq []byte) {
	// ESC [ < Cb ; Cx ; Cy M (press) or m (release), 1-based.
	params := strings.Split(string(seq[3:len(seq)-1]), ";")
	if len(params) != 3 {
		return
	}
	cb, err1 := strconv.Atoi(params[0])
	x, err2 := strconv.Atoi(params[1])
	y, err3 := strconv.Atoi(params[2])
	if err1 != nil || err2 != nil || err3 != nil || x < 1 || y < 1 {
		return
	}
	release := seq[len(seq)-1] == 'm'
	p := proto.PaneSendMouseParams{ID: s.id, X: x - 1, Y: y - 1,
		Shift: cb&4 != 0, Alt: cb&8 != 0, Ctrl: cb&16 != 0}
	f := s.frame
	if cb&64 != 0 { // the wheel
		up := cb&3 == 0
		switch {
		case f != nil && f.AltScreen && f.Mouse:
			p.Button, p.Action = "wheel_down", proto.MouseWheel
			if up {
				p.Button = "wheel_up"
			}
			call(s.c, proto.MethodPaneSendMouse, p, nil) //nolint:errcheck
		case f != nil && f.AltScreen:
			key := "down"
			if up {
				key = "up"
			}
			keys := []string{key, key, key}
			call(s.c, proto.MethodPaneSendKeys, proto.PaneSendKeysParams{ID: s.id, Keys: keys}, nil) //nolint:errcheck
		default:
			limit := 0
			if f != nil {
				limit = f.History
			}
			next := s.offset - attachWheel
			if up {
				next = s.offset + attachWheel
			}
			next = min(max(next, 0), limit)
			if next != s.offset {
				s.offset = next
				call(s.c, proto.MethodPaneScroll, proto.PaneScrollParams{ID: s.id, Offset: next}, nil) //nolint:errcheck
			}
		}
		return
	}
	if f == nil || !f.Mouse {
		return
	}
	switch {
	case cb&32 != 0:
		p.Action = proto.MouseMotion
	case release:
		p.Action = proto.MouseRelease
	default:
		p.Action = proto.MousePress
	}
	p.Button = [...]string{"left", "middle", "right", "none"}[cb&3]
	call(s.c, proto.MethodPaneSendMouse, p, nil) //nolint:errcheck
}
