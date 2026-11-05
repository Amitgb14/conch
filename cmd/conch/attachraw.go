package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
)

// `conch attach -raw PANE` makes the terminal it runs in the pane's
// terminal, as far as showing it goes: it is given what the program wrote
// (pane.raw.v1) rather than frames of the screen conch keeps, so the
// terminal emulates the program itself — its own scrollback, selection,
// search, images, cursor shapes. This is how the Mac app's Ghostty shows a
// pane (macos/), and what Rex does with libghostty.
//
// Two things stay conch's. Its emulator still answers the program's
// questions, so the server takes them out of the stream (internal/pane
// raw.go); and the pane's size is still settled by the server between
// everyone showing it (size.go), so this terminal shows the stream at the
// size the pane is, whatever it asked for. Typing here takes the size, as
// in tmux; the terminal's own answers to what it was asked — its name,
// whether it draws images, focus coming and going — and the pointer
// merely moving go as replies (PaneSendTextParams.Reply), which take
// nothing.
//
// Against a server without pane.raw.v1 it is the frames attach, so the app
// works the same with an older or remote server, only repainted.

// attachRawControl is what a control paste may say to a raw view, beside
// attachControl's search, which a raw view leaves to its own terminal.
type attachRawControl struct {
	// Cols and Rows are the size the window has room for: asked of the
	// server in place of this terminal's own, which the app shrinks to
	// the pane's size while somebody else's size holds it (letterbox).
	Cols int `json:"cols,omitempty"`
	Rows int `json:"rows,omitempty"`
	// Claim takes the pane's size for this view, as typing would, without
	// typing anything: a click on the letterbox.
	Claim bool `json:"claim,omitempty"`
}

// rawSession is one pane attached raw.
type rawSession struct {
	c       *client.Client
	id      string
	out     io.Writer
	control string
	// want is the size the app said it has room for; zero means this
	// terminal's own.
	wantCols, wantRows int
	hold               bool
	over               string
	// view only looks (-view): it asks for no size and takes none, so
	// showing the pane somewhere small cannot shrink it.
	view bool
}

// attachRawLoop runs a raw attach until the pane ends, the input ends or
// the connection goes; it says false when the server cannot (no
// pane.raw.v1), for the caller to attach with frames instead.
func attachRawLoop(c *client.Client, id string, in io.Reader, out io.Writer, size func() (int, int, bool), winch, quit <-chan os.Signal, hold bool) (string, bool, error) {
	return attachRawLoopAs(c, id, in, out, size, winch, quit, hold, false)
}

// attachRawLoopAs is attachRawLoop, only looking when view is set.
func attachRawLoopAs(c *client.Client, id string, in io.Reader, out io.Writer, size func() (int, int, bool), winch, quit <-chan os.Signal, hold, view bool) (string, bool, error) {
	if !serverCan(c, proto.CapPaneRaw) {
		return "", false, nil
	}
	s := &rawSession{c: c, id: id, out: out, control: os.Getenv(attachControlEnv), hold: hold, view: view}
	s.resize(size)
	var start proto.PaneRawStart
	if err := call(c, proto.MethodPaneRaw, proto.PaneRef{ID: id}, &start); err != nil {
		return "", true, err
	}
	s.settle(start.Cols, start.Rows, size, winch)
	io.WriteString(out, rawClear) //nolint:errcheck
	out.Write(start.Replay)       //nolint:errcheck
	if why := s.ended(); why != "" {
		if !hold {
			return why, true, nil
		}
		s.over = why
		s.note()
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
				return "conch attach: the connection to the server closed", true, nil
			}
			if done := s.event(msg); done != "" {
				return done, true, nil
			}
		case b, ok := <-input:
			if !ok {
				return "", true, nil // the window went: the pane carries on
			}
			if s.over != "" {
				return s.over, true, nil
			}
			held = s.input(append(held, b...), false, size)
			flush.Stop()
			if len(held) > 0 {
				flush.Reset(attachFlush)
			}
		case <-flush.C:
			held = s.input(held, true, size)
		case <-winch:
			s.resize(size)
		case <-quit:
			return "", true, nil
		}
	}
}

// rawSettle is how long a raw view run by the app waits for its terminal
// to be the pane's size before writing the replay (settle).
var rawSettle = 2 * time.Second

// settle waits, run by the app, until this terminal is the pane's size.
// The view's terminal starts at whatever size it was made at and is sized
// to the pane a moment later; a replay written into a smaller one scrolls
// its top off into the terminal's history, and growing the terminal after
// does not bring it back — the pane showed blank rows and a cursor. Run in
// somebody's own terminal there is nothing to wait for: the pane was just
// sized to it.
func (s *rawSession) settle(cols, rows int, size func() (int, int, bool), winch <-chan os.Signal) {
	if s.control == "" || cols <= 0 || rows <= 0 {
		return
	}
	deadline := time.After(rawSettle)
	for {
		if w, h, ok := size(); ok && w == cols && h == rows {
			return
		}
		select {
		case <-winch:
		case <-time.After(20 * time.Millisecond):
		case <-deadline:
			return // drawn as it is, rather than not at all
		}
	}
}

// rawClear readies this terminal for a replay: the screen and the
// scrollback cleared, the cursor home, styles and modes back to their
// defaults (a soft reset), so what the replay sets is all that is set.
const rawClear = "\x1b[!p\x1b[H\x1b[2J\x1b[3J"

// serverCan reports whether the server said it has a capability.
func serverCan(c *client.Client, cap string) bool {
	for _, x := range c.Server.Capabilities {
		if x == cap {
			return true
		}
	}
	return false
}

func (s *rawSession) ended() string {
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

// note says, under what the pane printed last, why it ended and what
// closes the view.
func (s *rawSession) note() {
	fmt.Fprintf(s.out, "\x1b[0m\r\n\x1b[7m %s · any key closes \x1b[0m", s.over)
}

func (s *rawSession) event(msg proto.Message) string {
	switch msg.Event {
	case proto.EventPaneOutput:
		var o proto.PaneOutput
		if json.Unmarshal(msg.Data, &o) != nil || o.ID != s.id || s.over != "" {
			return ""
		}
		if o.Reset {
			io.WriteString(s.out, rawClear) //nolint:errcheck
		}
		s.out.Write(o.Data) //nolint:errcheck
	case proto.EventPaneExited, proto.EventPaneClosed:
		var info proto.PaneInfo
		if json.Unmarshal(msg.Data, &info) != nil || info.ID != s.id {
			return ""
		}
		why := fmt.Sprintf("pane %s was closed", s.id)
		if msg.Event == proto.EventPaneExited {
			why = fmt.Sprintf("pane %s ended (exit %d)", s.id, info.ExitCode)
		}
		if s.hold && s.over == "" {
			s.over = why
			s.note()
			return ""
		}
		return why
	}
	return ""
}

// resize asks for the size the app said it has room for, or else this
// terminal's own. Run by the app (a control token), it waits to be told:
// the terminal it runs in is the letterbox's size while another window
// holds the pane, and before the app has said anything it is whatever
// size the view was made at — asking for that shrank the pane under
// everyone else for a moment at every attach.
func (s *rawSession) resize(size func() (int, int, bool)) {
	if s.view {
		return
	}
	w, h := s.wantCols, s.wantRows
	if (w <= 0 || h <= 0) && s.control != "" {
		return
	}
	if w <= 0 || h <= 0 {
		var ok bool
		if w, h, ok = size(); !ok {
			return
		}
	}
	call(s.c, proto.MethodPaneResize, proto.PaneResizeParams{ID: s.id, Cols: w, Rows: h}, nil) //nolint:errcheck
}

// input passes what the terminal sent on to the pane: typing as typing,
// its answers and the pointer's moving as replies, and a control paste
// from the app to this view. What is not whole yet is returned to be
// tried again with more, or, when final, sent as it is.
func (s *rawSession) input(b []byte, final bool, size func() (int, int, bool)) []byte {
	var typed, reply []byte
	send := func() {
		if len(typed) > 0 {
			call(s.c, proto.MethodPaneSendText, proto.PaneSendTextParams{ID: s.id, Text: string(typed)}, nil) //nolint:errcheck
			typed = typed[:0]
		}
		if len(reply) > 0 {
			call(s.c, proto.MethodPaneSendText, proto.PaneSendTextParams{ID: s.id, Text: string(reply), Reply: true}, nil) //nolint:errcheck
			reply = reply[:0]
		}
	}
	for len(b) > 0 {
		if s.control != "" {
			if n, ctl, whole := s.controlAt(b); n > 0 || !whole {
				if !whole {
					if !final {
						send()
						return b
					}
				} else {
					send()
					s.apply(ctl, size)
					b = b[n:]
					continue
				}
			}
		}
		if b[0] != 0x1b {
			i := bytes.IndexByte(b, 0x1b)
			if s.control != "" {
				if j := bytes.Index(b, []byte(attachControlMark)); j >= 0 && (i < 0 || j < i) {
					i = j
				}
				if j := bytes.Index(b, []byte("\x1b[200~"+attachControlMark)); j >= 0 && (i < 0 || j < i) {
					i = j
				}
			}
			if i < 0 {
				i = len(b)
			}
			if i == 0 { // a control mark that is not ours after all
				i = len(attachControlMark)
			}
			if len(reply) > 0 {
				send()
			}
			typed = append(typed, b[:i]...)
			b = b[i:]
			continue
		}
		n, isReply := rawInputSequence(b, final)
		if n == 0 {
			send()
			return b // more to come
		}
		if isReply {
			if len(typed) > 0 {
				send()
			}
			reply = append(reply, b[:n]...)
		} else {
			if len(reply) > 0 {
				send()
			}
			typed = append(typed, b[:n]...)
		}
		b = b[n:]
	}
	send()
	return nil
}

// controlAt reads a control paste at the start of b — bracketed or not,
// since a raw view's terminal brackets a paste only when the program
// asked — and says how long it is. whole is false while what is there
// could still become one.
func (s *rawSession) controlAt(b []byte) (n int, ctl attachRawControl, whole bool) {
	start := []byte(attachControlMark + s.control + " ")
	open := []byte("\x1b[200~")
	bracketed := bytes.HasPrefix(b, open)
	body := b
	if bracketed {
		body = b[len(open):]
	} else if len(b) < len(open) && bytes.HasPrefix(open, b) {
		return 0, ctl, false // perhaps the start of a bracketed one
	}
	if !bytes.HasPrefix(body, start) {
		if len(body) < len(start) && bytes.HasPrefix(start, body) {
			return 0, ctl, false
		}
		return 0, ctl, true
	}
	dec := json.NewDecoder(bytes.NewReader(body[len(start):]))
	var raw json.RawMessage
	if err := dec.Decode(&raw); err != nil {
		if err == io.ErrUnexpectedEOF || err == io.EOF {
			return 0, ctl, false
		}
		// Ours and garbled: up to the end of the paste, or of what is here.
		end := len(body)
		if bracketed {
			if i := bytes.Index(body, []byte("\x1b[201~")); i >= 0 {
				end = i + len("\x1b[201~")
			}
		}
		return len(b) - len(body) + end, ctl, true
	}
	_ = json.Unmarshal(raw, &ctl)
	used := len(start) + int(dec.InputOffset())
	if bracketed {
		rest := body[used:]
		if !bytes.HasPrefix(rest, []byte("\x1b[201~")) {
			if len(rest) < len("\x1b[201~") && bytes.HasPrefix([]byte("\x1b[201~"), rest) {
				return 0, ctl, false
			}
		} else {
			used += len("\x1b[201~")
		}
		used += len(open)
	}
	return used, ctl, true
}

// apply does what a control paste asked.
func (s *rawSession) apply(ctl attachRawControl, size func() (int, int, bool)) {
	if ctl.Cols > 0 && ctl.Rows > 0 && (ctl.Cols != s.wantCols || ctl.Rows != s.wantRows) {
		s.wantCols, s.wantRows = ctl.Cols, ctl.Rows
		s.resize(size)
	}
	if ctl.Claim && !s.view {
		// Nothing typed, but typed through this view: the server gives
		// it the pane, at the size it asked for.
		call(s.c, proto.MethodPaneSendText, proto.PaneSendTextParams{ID: s.id, Text: ""}, nil) //nolint:errcheck
	}
}

// rawInputSequence is how long the escape sequence at the start of b is,
// and whether it is the terminal answering rather than a key: focus in and
// out, the replies to device attributes, modes, its key protocol and its
// size, any string (OSC, DCS, APC — no key makes one), and the pointer
// moving with no button down. 0 means it has not all arrived; with final
// set, what there is is a key (esc on its own, alt and a key).
func rawInputSequence(b []byte, final bool) (n int, reply bool) {
	if len(b) < 2 {
		if final {
			return len(b), false
		}
		return 0, false
	}
	switch b[1] {
	case '[':
		for i := 2; i < len(b); i++ {
			c := b[i]
			if c >= 0x40 && c <= 0x7e {
				return i + 1, csiReply(b[2:i], c)
			}
			if c < 0x20 || c > 0x3f {
				return i, false
			}
		}
		if final {
			return len(b), false
		}
		return 0, false
	case ']', 'P', '_':
		// A string ends at BEL or ESC \. A key typed with alt is the
		// same two bytes and nothing after, so a string not ended in
		// what arrived is alt and the key.
		for i := 2; i < len(b); i++ {
			if b[i] == 0x07 {
				return i + 1, true
			}
			if b[i] == 0x1b && i+1 < len(b) && b[i+1] == '\\' {
				return i + 2, true
			}
		}
		return 2, false
	}
	return 2, false // alt and a key, or SS3 and the rest as text
}

// csiReply says whether a CSI the terminal sent is an answer, not a key.
func csiReply(params []byte, final byte) bool {
	p := string(params)
	switch final {
	case 'I', 'O':
		return p == "" // focus in, out
	case 'c':
		return strings.HasPrefix(p, "?") || strings.HasPrefix(p, ">") // DA replies
	case 'y':
		return strings.HasSuffix(p, "$") // DECRPM
	case 'u':
		return strings.HasPrefix(p, "?") // kitty key protocol flags
	case 't':
		return true // window size reports, in-band resize
	case 'M', 'm':
		// SGR mouse: motion (32) with no button (3) is the pointer moving.
		if !strings.HasPrefix(p, "<") {
			return false
		}
		f := strings.SplitN(p[1:], ";", 2)
		btn, err := strconv.Atoi(f[0])
		return err == nil && btn&32 != 0 && btn&3 == 3
	}
	return false
}
