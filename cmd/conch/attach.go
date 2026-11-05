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

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/tui"
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

const attachUsage = "usage: conch attach [-hold] [-raw] [-view] PANE"

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

// attachControlEnv carries a token from whatever runs this — the Mac app —
// that it can put at the start of a paste to say something to this view
// rather than to the pane: a search to show (attachControl). Only this
// command can scroll its own view, since the server keeps a scroll per
// connection, so the app's find bar asks here. The token is what tells
// such a paste from somebody pasting text that happens to look like one;
// without it every paste goes to the pane.
const attachControlEnv = "CONCH_ATTACH_CONTROL"

// attachControlMark starts a control paste, before the token: an invisible
// separator, so it is nothing anybody types.
const attachControlMark = "\u2063conch-attach:"

// attachControl is what a control paste says, as JSON after the token.
type attachControl struct {
	// Find is the query whose matches are marked; the match the app's
	// search found is at Line, of History lines of history then
	// (pane.search's own counting), brought into view if it is not.
	Find    string `json:"find,omitempty"`
	Line    int    `json:"line,omitempty"`
	History int    `json:"history,omitempty"`
	// Clear ends the search: nothing marked, the view left where it is.
	Clear bool `json:"clear,omitempty"`
}

// attachMatch is how a search's matches are drawn: dark on yellow, as the
// TUI's warn colour marks them.
const attachMatch = "\x1b[0;30;43m"

func runAttach(args []string) error {
	// -hold keeps an ended pane's last screen until a key is pressed: the
	// Mac app's, for a kept action's output and an error in a pane's
	// first seconds, which the view would otherwise take with it.
	// -raw gives the terminal the program's own output (attachraw.go)
	// where the server can, frames where it cannot.
	// -view (with -raw) only looks: the pane is never resized or claimed
	// from here, for the Mac app's preview of a pane in ⌘K.
	hold, raw, view := false, false, false
	for len(args) > 0 {
		switch args[0] {
		case "-hold", "--hold":
			hold = true
		case "-raw", "--raw":
			raw = true
		case "-view", "--view":
			view = true
		default:
			goto named
		}
		args = args[1:]
	}
named:
	if len(args) != 1 || strings.HasPrefix(args[0], "-") {
		return errors.New(attachUsage)
	}
	if view && !raw {
		return errors.New("conch attach: -view goes with -raw")
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
	if err := attachRefusal(c, id); err != nil {
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
	// Killed, or its terminal hung up: the loop ends so the terminal is
	// given back — raw mode, the alternate screen, the mouse and bracketed
	// paste were all left on otherwise.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(quit)
	size := func() (int, int, bool) {
		w, h, err := term.GetSize(os.Stdout.Fd())
		return w, h, err == nil && w > 0 && h > 0
	}
	if raw {
		msg, ok, err := attachRawLoopAs(c, id, os.Stdin, os.Stdout, size, winch, quit, hold, view)
		if ok {
			if msg != "" {
				fmt.Fprintln(os.Stderr, msg)
			}
			return err
		}
	}
	msg, err := attachLoopHold(c, id, os.Stdin, os.Stdout, size, winch, quit, hold)
	if msg != "" {
		fmt.Fprintln(os.Stderr, msg)
	}
	return err
}

// attachRefusal says why a pane is not to be attached here: it is the
// pane this command runs in. Its frames would be output to itself, making
// a frame of their own, and every key typed would be sent to the pane,
// arrive on this command's own input and be sent again — the lock-up a
// conch inside its own pane once was (R63). Which pane that is comes from
// the server (pane.caller), as for the TUI, never from CONCH_PANE_ID. A
// server that cannot say is not a refusal.
func attachRefusal(c *client.Client, id string) error {
	var who proto.CallerInfo
	if call(c, proto.MethodPaneCaller, nil, &who) != nil || who.Pane == "" || who.Pane != id {
		return nil
	}
	return fmt.Errorf("conch attach %s: that is the pane this runs in, which cannot show itself; run it from another terminal", id)
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
	// control is the token a control paste starts with (attachControlEnv),
	// and query what the last one asked to have marked.
	control string
	query   string
	// hold keeps an ended pane's screen; over is why it ended, once it has.
	hold bool
	over string
}

// attachLoop runs an attached pane until the pane ends, the input ends
// (the window was closed), or the connection goes. What it returns as a
// message is said after the terminal is given back.
func attachLoop(c *client.Client, id string, in io.Reader, out io.Writer, size func() (int, int, bool), winch, quit <-chan os.Signal) (string, error) {
	return attachLoopHold(c, id, in, out, size, winch, quit, false)
}

// attachLoopHold is attachLoop, holding an ended pane's last screen when
// hold is set: a note on its bottom line, and the next key ends it.
func attachLoopHold(c *client.Client, id string, in io.Reader, out io.Writer, size func() (int, int, bool), winch, quit <-chan os.Signal, hold bool) (string, error) {
	s := &attachSession{c: c, id: id, out: out, control: os.Getenv(attachControlEnv), hold: hold}
	io.WriteString(out, attachEnter) //nolint:errcheck
	defer io.WriteString(out, attachLeave)
	s.resize(size)
	if err := call(c, proto.MethodPaneSubscribe, proto.PaneRef{ID: id}, nil); err != nil {
		return "", err
	}
	// A pane that has already ended sends its last frame and no exit, so
	// say so now rather than showing that screen for ever, typing into
	// nothing — also one that ended between being named and subscribed.
	if why := s.ended(); why != "" {
		if !hold {
			return why, nil
		}
		s.over = why // its last frame comes, and is held
		s.draw()
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
			// Held over an ended pane: a key ends it; the wheel still
			// scrolls what it printed.
			if s.over != "" && !bytes.HasPrefix(b, []byte("\x1b[<")) {
				return s.over, nil
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
		case <-quit:
			return "", nil // the terminal is given back on the way out
		}
	}
}

// ended says why the pane is over, if it is: exited, or no longer listed.
// A list that cannot be read says nothing.
func (s *attachSession) ended() string {
	var list proto.PaneList
	if call(s.c, proto.MethodPaneList, nil, &list) != nil {
		return ""
	}
	for _, p := range list.Panes {
		if p.ID == s.id {
			if p.State != proto.PaneRunning {
				return fmt.Sprintf("pane %s ended (exit %d)", s.id, p.ExitCode)
			}
			return ""
		}
	}
	return fmt.Sprintf("pane %s was closed", s.id)
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
		s.draw()
	case proto.EventPaneExited, proto.EventPaneClosed:
		var info proto.PaneInfo
		if json.Unmarshal(msg.Data, &info) != nil || info.ID != s.id {
			return ""
		}
		if msg.Event == proto.EventPaneExited {
			why := fmt.Sprintf("pane %s ended (exit %d)", s.id, info.ExitCode)
			if s.hold {
				s.over = why
				s.draw()
				return ""
			}
			return why
		}
		return fmt.Sprintf("pane %s was closed", s.id)
	}
	return ""
}

// draw paints the last frame, with the search's matches marked.
func (s *attachSession) draw() {
	if s.frame == nil {
		return
	}
	f := *s.frame
	if s.query != "" {
		f.Lines = make([]string, len(s.frame.Lines))
		for i, l := range s.frame.Lines {
			f.Lines[i] = markMatches(l, tui.MatchSpans(ansi.Strip(l), s.query), f.Cols)
		}
	}
	if s.over != "" && len(f.Lines) > 0 {
		// Why it ended, over its bottom line, and what ends the view.
		f.Lines = append([]string(nil), f.Lines...)
		note := " " + s.over + " · any key closes "
		if w := ansi.StringWidth(note); f.Cols > 0 && w > f.Cols {
			note = ansi.Truncate(note, f.Cols, "")
		} else if f.Cols > w {
			note += strings.Repeat(" ", f.Cols-w)
		}
		f.Lines[len(f.Lines)-1] = "\x1b[0;7m" + note + "\x1b[0m"
	}
	io.WriteString(s.out, renderAttachFrame(&f)) //nolint:errcheck
}

// markMatches draws the given cell ranges of a rendered line as matches,
// over whatever styling the line has.
func markMatches(line string, spans [][2]int, w int) string {
	if len(spans) == 0 || w <= 0 {
		return line
	}
	plain := ansi.Strip(line)
	if n := ansi.StringWidth(plain); n < w {
		plain += strings.Repeat(" ", w-n)
	}
	// Right to left, so the cells still to be marked keep their places.
	for i := len(spans) - 1; i >= 0; i-- {
		from, to := min(max(spans[i][0], 0), w), min(max(spans[i][1], 0), w)
		if from >= to {
			continue
		}
		line = ansi.Cut(line, 0, from) + attachMatch + ansi.Cut(plain, from, to) + "\x1b[0m" + ansi.Cut(line, to, w)
	}
	return line
}

// controlled handles a paste that is a control from the app, and says
// whether it was one: anything else is the person's paste, for the pane.
func (s *attachSession) controlled(body []byte) bool {
	if s.control == "" {
		return false
	}
	rest, ok := bytes.CutPrefix(body, []byte(attachControlMark+s.control+" "))
	if !ok {
		return false
	}
	var ctl attachControl
	if json.Unmarshal(rest, &ctl) != nil {
		return true // ours, and garbled: not for the pane either
	}
	switch {
	case ctl.Clear:
		s.query = ""
	case ctl.Find != "":
		s.query = ctl.Find
		if f := s.frame; f != nil && !f.AltScreen && ctl.Line < ctl.History {
			// In history: in view already, or brought to the middle, as
			// the TUI's search does.
			if top := ctl.History - s.offset; ctl.Line < top || ctl.Line >= top+f.Rows {
				s.scrollTo(ctl.History - (ctl.Line - f.Rows/2))
			}
		} else if ctl.Line >= ctl.History {
			s.scrollTo(0) // on the live screen
		}
	}
	s.draw()
	return true
}

// scrollTo moves this view next lines back into history, kept to what
// there is.
func (s *attachSession) scrollTo(next int) {
	limit := 0
	if s.frame != nil {
		limit = s.frame.History
	}
	next = min(max(next, 0), limit)
	if next != s.offset {
		s.offset = next
		call(s.c, proto.MethodPaneScroll, proto.PaneScrollParams{ID: s.id, Offset: next}, nil) //nolint:errcheck
	}
}

// scrollKey scrolls the history for the keys terminals keep for it —
// shift with page up and down, home and end — and says whether it did. A
// full-screen program has no history here and gets them, as it would in
// any terminal.
func (s *attachSession) scrollKey(name string) bool {
	f := s.frame
	if f == nil || f.AltScreen {
		return false
	}
	page := max(f.Rows-1, 1)
	switch name {
	case "shift+pgup":
		s.scrollTo(s.offset + page)
	case "shift+pgdown":
		s.scrollTo(s.offset - page)
	case "shift+home":
		s.scrollTo(f.History)
	case "shift+end":
		s.scrollTo(0)
	default:
		return false
	}
	return true
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
			if s.controlled(body) {
				continue
			}
			s.typed()
			call(s.c, proto.MethodPaneSendText, proto.PaneSendTextParams{ID: s.id, Text: string(body), Paste: true}, nil) //nolint:errcheck
		case attachNamed:
			send()
			name := attachKeyName(seq)
			if s.scrollKey(name) {
				continue
			}
			s.typed()
			call(s.c, proto.MethodPaneSendKeys, proto.PaneSendKeysParams{ID: s.id, Keys: []string{name}}, nil) //nolint:errcheck
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
		case up:
			s.scrollTo(s.offset + attachWheel)
		default:
			s.scrollTo(s.offset - attachWheel)
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
