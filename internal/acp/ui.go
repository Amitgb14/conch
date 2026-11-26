package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/Amitgb14/conch/internal/pane"
)

// The pane's side: `conch acp` draws the conversation as lines in the
// terminal and takes typed prompts below it. It is deliberately a plain
// scrolling transcript rather than a full-screen interface — the pane
// keeps it as history, `conch read` reads it, and the one thing drawn in
// place is the prompt line or a permission menu at the bottom.
//
// The agent's state is said over OSC 7501 from the protocol itself:
// working from the moment a prompt is sent, blocked while a permission
// request waits, done when session/prompt ends. No screen rule guesses.
//
// A permission request is drawn as a numbered menu with a cursor (❯),
// the shape internal/phone reads off a screen, so the phone and the Mac
// app answer it with up, down and enter as they answer any agent.

// RunOptions are what `conch acp` was started with.
type RunOptions struct {
	// Agent is the agent's name in conch (its manifest), said in its
	// status reports; Label the name people know it by.
	Agent, Label string
	Argv         []string
	Env          []string
	Dir          string
	// Prompt is a first message; Load a saved session to reopen.
	Prompt, Load string
	In           io.Reader
	Out          io.Writer
	// Width is the terminal's width now, for redrawing the prompt line;
	// nil is 80.
	Width func() int
}

// state is the agent's, as reported.
type state int

const (
	stateStarting state = iota
	stateIdle
	stateWorking
	stateBlocked
)

// ask is a permission request waiting on the person.
type ask struct {
	p      PermissionParams
	cursor int
	answer chan PermissionOutcome
	ctx    context.Context
}

type ui struct {
	o   RunOptions
	app string

	mu      sync.Mutex
	client  *Client
	session string
	state   state
	midLine bool
	last    string // the kind of the last update drawn, to break lines between kinds
	tools   map[string]string
	input   []rune
	queued  []string
	shown   int // rows the bottom (prompt or menu) takes on screen, 0 when none
	asks    []*ask
	quit    chan struct{}
	closed  bool
}

// Run is `conch acp`: the agent started, a session opened, and the
// conversation drawn until the person leaves or the agent does.
func Run(ctx context.Context, o RunOptions) error {
	if o.Width == nil {
		o.Width = func() int { return 80 }
	}
	label := o.Label
	if label == "" {
		label = o.Agent
	}
	u := &ui{o: o, app: appName(o.Agent), tools: map[string]string{}, quit: make(chan struct{})}
	u.printf("\x1b[2m%s · over ACP · %s\x1b[0m\n", sanitize(label), sanitize(strings.Join(o.Argv, " ")))

	c, err := Start(ctx, Options{Argv: o.Argv, Env: o.Env, Dir: o.Dir, Events: u})
	if err != nil {
		return u.fail(err)
	}
	defer c.Close()
	u.mu.Lock()
	u.client = c
	u.mu.Unlock()
	if c.Init.AgentInfo != nil && c.Init.AgentInfo.Name != "" {
		u.printf("\x1b[2m%s %s\x1b[0m\n", sanitize(c.Init.AgentInfo.Name), sanitize(c.Init.AgentInfo.Version))
	}

	if o.Load != "" {
		// The agent replays the conversation as updates before it
		// answers, and they are drawn as they come: the history, with
		// the state left alone — nothing is being worked on.
		u.mu.Lock()
		u.session = o.Load
		u.mu.Unlock()
		err = c.LoadSession(ctx, o.Load)
		if err != nil {
			// Starting afresh and calling that a resume is the one thing
			// this must not do: say so, send nothing.
			return u.fail(fmt.Errorf("session %s was not reopened: %w", o.Load, err))
		}
		u.printf("\n\x1b[2m— session %s reopened —\x1b[0m\n", sanitize(o.Load))
	} else {
		id, err := c.NewSession(ctx)
		if err != nil {
			if len(c.Init.AuthMethods) > 0 {
				err = fmt.Errorf("%w — the agent may need signing in first: %s", err, authHint(c.Init.AuthMethods))
			}
			return u.fail(err)
		}
		u.mu.Lock()
		u.session = id
		u.mu.Unlock()
	}

	u.mu.Lock()
	u.state = stateIdle
	if strings.TrimSpace(o.Prompt) != "" {
		u.submitLocked(o.Prompt)
	} else if len(u.queued) > 0 {
		// Typed and entered while the agent was starting.
		next := u.queued[0]
		u.queued = u.queued[1:]
		u.submitLocked(next)
	} else {
		u.statusLocked(pane.StatusIdle, "", "")
		u.drawBottomLocked()
	}
	u.mu.Unlock()

	keys := make(chan []byte)
	go func() {
		defer close(keys)
		if o.In == nil {
			return
		}
		buf := make([]byte, 4096)
		for {
			n, err := o.In.Read(buf)
			if n > 0 {
				keys <- append([]byte(nil), buf[:n]...)
			}
			if err != nil {
				return
			}
		}
	}()
	for {
		select {
		case b, ok := <-keys:
			if !ok {
				keys = nil // input ended: carry on until the agent does
				continue
			}
			u.keys(b)
		case <-u.quit:
			u.mu.Lock()
			u.eraseBottomLocked()
			u.mu.Unlock()
			u.status(pane.StatusIdle, "", "")
			return nil
		case <-c.Done():
			u.mu.Lock()
			u.eraseBottomLocked()
			u.mu.Unlock()
			why := "the agent exited"
			if s := c.Stderr(); s != "" {
				why += ": " + lastLines(s, 6)
			}
			return u.fail(errors.New(why))
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func authHint(ms []AuthMethod) string {
	var names []string
	for _, m := range ms {
		names = append(names, sanitize(m.Name))
	}
	return strings.Join(names, ", ")
}

// fail says what went wrong, in the transcript and as the agent's state.
func (u *ui) fail(err error) error {
	u.mu.Lock()
	u.breakLineLocked()
	u.mu.Unlock()
	u.printf("\x1b[31m%s\x1b[0m\n", sanitize(err.Error()))
	u.status(pane.StatusError, "", firstLine(err.Error()))
	return err
}

// appName is the agent's name as OSC 7501 allows it.
func appName(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < utf8.RuneSelf && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_.+-", r)) {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 || b.Len() > 32 {
		return "acp"
	}
	return b.String()
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	return clip(s, 200)
}

// ---- output ----

// write puts text on the terminal, lines ended for a terminal in raw
// mode. Callers hold mu, or own the terminal outright.
func (u *ui) write(s string) {
	if s == "" {
		return
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	u.midLine = !strings.HasSuffix(s, "\n")
	_, _ = io.WriteString(u.o.Out, strings.ReplaceAll(s, "\n", "\r\n"))
}

func (u *ui) printf(format string, args ...any) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.eraseBottomLocked()
	u.write(fmt.Sprintf(format, args...))
	u.drawBottomLocked()
}

func (u *ui) breakLineLocked() {
	if u.midLine {
		u.write("\n")
	}
}

// sanitize keeps what an agent says from being a terminal escape of its
// own — a forged status report, a cleared screen — by dropping control
// characters other than newline and tab.
func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			return r
		case r < 0x20 || r == 0x7f || r >= 0x80 && r < 0xa0:
			return -1
		}
		return r
	}, strings.ToValidUTF8(s, "�"))
}

func (u *ui) status(st, kind, msg string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.statusLocked(st, kind, msg)
}

func (u *ui) statusLocked(st, kind, msg string) {
	_, _ = io.WriteString(u.o.Out, pane.StatusSequence(pane.ProgramStatus{
		State: st, Kind: kind, App: u.app, Msg: clip(sanitize(strings.ReplaceAll(msg, "\n", " ")), 500), Progress: -1}))
}

// ---- the bottom: a prompt line, or a permission menu ----

func (u *ui) eraseBottomLocked() {
	if u.shown == 0 {
		return
	}
	s := "\r"
	if u.shown > 1 {
		s += fmt.Sprintf("\x1b[%dA", u.shown-1)
	}
	_, _ = io.WriteString(u.o.Out, s+"\x1b[J")
	u.shown = 0
	u.midLine = false
}

func (u *ui) drawBottomLocked() {
	if u.closed {
		return
	}
	var lines []string
	switch {
	case len(u.asks) > 0:
		a := u.asks[0]
		lines = append(lines, "\x1b[1m"+askTitle(a.p)+"\x1b[0m")
		for i, o := range a.p.Options {
			cur := "  "
			if i == a.cursor {
				cur = "❯ "
			}
			lines = append(lines, fmt.Sprintf("%s%d. %s", cur, i+1, sanitize(oneLine(o.Name))))
		}
	case u.state == stateIdle:
		in := strings.ReplaceAll(string(u.input), "\n", "↵")
		lines = []string{"\x1b[1m›\x1b[0m " + sanitize(in)}
	default:
		return
	}
	u.breakLineLocked()
	width := max(u.o.Width(), 10)
	rows := 0
	for i, l := range lines {
		rows += max(1, (visibleLen(l)+width-1)/width)
		if i > 0 {
			_, _ = io.WriteString(u.o.Out, "\r\n")
		}
		_, _ = io.WriteString(u.o.Out, l)
	}
	u.shown = rows
	// A line exactly as wide as the terminal leaves the cursor waiting on
	// its last column; counting it as a row of its own would erase one
	// too many.
	u.midLine = false
}

func visibleLen(s string) int {
	n, esc := 0, false
	for _, r := range s {
		switch {
		case esc:
			esc = r != 'm'
		case r == 0x1b:
			esc = true
		default:
			n++
		}
	}
	return n
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func askTitle(p PermissionParams) string {
	t := ""
	if p.ToolCall.Title != nil {
		t = *p.ToolCall.Title
	}
	if t == "" {
		t = p.ToolCall.Kind
	}
	if t == "" {
		t = "a tool call"
	}
	return "Allow " + sanitize(oneLine(t)) + "?"
}

// ---- events from the agent ----

// Update implements Events.
func (u *ui) Update(sessionID string, up Update) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if sessionID != "" && u.session != "" && sessionID != u.session {
		return
	}
	text := ""
	kind := up.SessionUpdate
	switch kind {
	case "agent_message_chunk", "agent_thought_chunk", "user_message_chunk":
		var b ContentBlock
		if json.Unmarshal(up.Content, &b) != nil {
			return
		}
		text = blockText(b)
		if text == "" {
			return
		}
		switch kind {
		case "agent_thought_chunk":
			text = "\x1b[2;3m" + sanitize(text) + "\x1b[0m"
		case "user_message_chunk":
			text = sanitize(text)
			if u.last != kind {
				text = "\x1b[1m›\x1b[0m " + text
			}
		default:
			text = sanitize(text)
		}
		if u.last != kind && u.last != "" {
			u.eraseBottomLocked()
			u.breakLineLocked()
			u.write("\n")
		}
	case "tool_call":
		title := ""
		if up.Title != nil {
			title = *up.Title
		}
		u.tools[up.ToolCallID] = title
		text = "\x1b[36m⏺\x1b[0m " + sanitize(oneLine(title))
		if up.Kind != "" && up.Kind != "other" {
			text += " \x1b[2m(" + sanitize(up.Kind) + ")\x1b[0m"
		}
		text += "\n" + toolContent(up.Content)
		u.eraseBottomLocked()
		u.breakLineLocked()
	case "tool_call_update":
		if up.Title != nil {
			u.tools[up.ToolCallID] = *up.Title
		}
		content := toolContent(up.Content)
		mark := ""
		switch up.Status {
		case "completed":
			mark = "\x1b[32m  ✓\x1b[0m "
		case "failed":
			mark = "\x1b[31m  ✗\x1b[0m "
		}
		if mark == "" && content == "" {
			return
		}
		if mark != "" {
			text = mark + sanitize(oneLine(u.tools[up.ToolCallID])) + "\n"
		}
		text += content
		u.eraseBottomLocked()
		u.breakLineLocked()
	case "plan":
		var b strings.Builder
		b.WriteString("\x1b[1mPlan\x1b[0m\n")
		for _, e := range up.Entries {
			box := "☐"
			switch e.Status {
			case "in_progress":
				box = "◐"
			case "completed":
				box = "☑"
			}
			b.WriteString("  " + box + " " + sanitize(oneLine(e.Content)) + "\n")
		}
		text = b.String()
		u.eraseBottomLocked()
		u.breakLineLocked()
	default:
		return // modes, commands, usage: nothing to draw
	}
	u.last = kind
	u.eraseBottomLocked()
	u.write(text)
	u.drawBottomLocked()
}

func blockText(b ContentBlock) string {
	switch b.Type {
	case "text":
		return b.Text
	case "resource_link":
		return "[" + b.Name + " " + b.URI + "]"
	case "":
		return ""
	}
	return "[" + b.Type + "]"
}

// diffLines bounds how much of one diff is drawn: the transcript is for
// following along, and the branch's diff view is for reading.
const diffLines = 40

// toolContent is a tool call's content as lines: text, diffs, terminals.
func toolContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var items []ToolCallContent
	if json.Unmarshal(raw, &items) != nil {
		return ""
	}
	var b strings.Builder
	for _, it := range items {
		switch it.Type {
		case "content":
			if it.Content != nil {
				for _, l := range strings.Split(strings.TrimRight(sanitize(blockText(*it.Content)), "\n"), "\n") {
					if l != "" {
						b.WriteString("  \x1b[2m" + clip(l, 300) + "\x1b[0m\n")
					}
				}
			}
		case "diff":
			old := ""
			if it.OldText != nil {
				old = *it.OldText
			}
			b.WriteString(diffText(it.Path, old, it.NewText, it.OldText == nil))
		case "terminal":
			b.WriteString("  \x1b[2m[terminal " + sanitize(it.TerminalID) + "]\x1b[0m\n")
		}
	}
	return b.String()
}

// diffText is a short diff: the lines between what the two texts share
// at the start and the end.
func diffText(path, old, new string, created bool) string {
	a, b := splitLines(old), splitLines(new)
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	gone, added := a[pre:len(a)-suf], b[pre:len(b)-suf]
	var s strings.Builder
	what := "edit"
	if created {
		what = "new file"
	}
	fmt.Fprintf(&s, "  \x1b[1m%s\x1b[0m %s \x1b[31m-%d\x1b[0m \x1b[32m+%d\x1b[0m\n", what, sanitize(path), len(gone), len(added))
	n := 0
	for _, l := range gone {
		if n++; n > diffLines {
			break
		}
		s.WriteString("  \x1b[31m- " + clip(sanitize(l), 300) + "\x1b[0m\n")
	}
	for _, l := range added {
		if n++; n > diffLines {
			break
		}
		s.WriteString("  \x1b[32m+ " + clip(sanitize(l), 300) + "\x1b[0m\n")
	}
	if more := len(gone) + len(added) - diffLines; more > 0 {
		fmt.Fprintf(&s, "  \x1b[2m… %d more lines\x1b[0m\n", more)
	}
	return s.String()
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// Problem implements Events.
func (u *ui) Problem(msg string) {
	u.printf("\x1b[33m%s\x1b[0m\n", sanitize(msg))
}

// Permission implements Events: the request drawn as a menu, the agent
// blocked until somebody picks.
func (u *ui) Permission(ctx context.Context, p PermissionParams) PermissionOutcome {
	if len(p.Options) == 0 {
		return cancelledOutcome
	}
	a := &ask{p: p, answer: make(chan PermissionOutcome, 1), ctx: ctx}
	u.mu.Lock()
	u.eraseBottomLocked()
	u.breakLineLocked()
	u.asks = append(u.asks, a)
	if len(u.asks) == 1 {
		u.blockedLocked()
	}
	u.drawBottomLocked()
	u.mu.Unlock()

	var out PermissionOutcome
	select {
	case out = <-a.answer:
	case <-ctx.Done():
		out = cancelledOutcome
	case <-u.quit:
		out = cancelledOutcome
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.eraseBottomLocked()
	for i, x := range u.asks {
		if x == a {
			u.asks = append(u.asks[:i], u.asks[i+1:]...)
			break
		}
	}
	answer := "cancelled"
	for _, o := range p.Options {
		if out.Outcome == "selected" && o.OptionID == out.OptionID {
			answer = sanitize(oneLine(o.Name))
		}
	}
	u.write("\x1b[2m" + askTitle(p) + " → " + answer + "\x1b[0m\n")
	switch {
	case len(u.asks) > 0:
		u.blockedLocked()
	case u.state == stateBlocked:
		u.state = stateWorking
		u.statusLocked(pane.StatusWorking, "", "")
	}
	u.drawBottomLocked()
	return out
}

// blockedLocked says the agent waits on the first request.
func (u *ui) blockedLocked() {
	if u.state == stateWorking || u.state == stateBlocked {
		u.state = stateBlocked
	}
	u.statusLocked(pane.StatusBlocked, pane.BlockedPermission, askTitle(u.asks[0].p))
}

// ---- turns ----

func (u *ui) submitLocked(text string) {
	u.eraseBottomLocked()
	u.breakLineLocked()
	u.write("\x1b[1m›\x1b[0m " + sanitize(text) + "\n")
	u.last = "user_message_chunk"
	u.state = stateWorking
	u.statusLocked(pane.StatusWorking, "", "")
	c, sid := u.client, u.session
	go func() {
		stop, err := c.Prompt(context.Background(), sid, text)
		u.turnEnded(stop, err)
	}()
}

func (u *ui) turnEnded(stop string, err error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.closed {
		return
	}
	u.eraseBottomLocked()
	u.breakLineLocked()
	u.last = ""
	u.state = stateIdle
	switch {
	case err != nil && errors.Is(err, ErrClosed) || err != nil && strings.Contains(err.Error(), "the agent exited"):
		return // Run says why, once
	case err != nil:
		u.write("\x1b[31m" + sanitize(err.Error()) + "\x1b[0m\n")
		u.statusLocked(pane.StatusError, "", firstLine(err.Error()))
	case stop == StopCancelled:
		u.write("\x1b[2m(stopped)\x1b[0m\n")
		u.statusLocked(pane.StatusIdle, "", "")
	case stop == StopEndTurn || stop == "":
		u.statusLocked(pane.StatusDone, "", "")
	default:
		u.write("\x1b[33m(ended: " + sanitize(stop) + ")\x1b[0m\n")
		u.statusLocked(pane.StatusDone, "", stop)
	}
	if len(u.queued) > 0 {
		next := u.queued[0]
		u.queued = u.queued[1:]
		u.submitLocked(next)
		return
	}
	u.drawBottomLocked()
}

func (u *ui) cancelLocked() {
	if u.client == nil || u.session == "" || (u.state != stateWorking && u.state != stateBlocked) {
		return
	}
	u.queued = nil
	c, sid := u.client, u.session
	go func() { _ = c.Cancel(sid) }()
}

// ---- keys ----

// keys takes what the terminal sent: typing, Enter, the arrows that move
// a menu's cursor, escape or ctrl+c to stop a turn.
func (u *ui) keys(b []byte) {
	u.mu.Lock()
	defer u.mu.Unlock()
	for len(b) > 0 {
		k, n := nextKey(b)
		b = b[n:]
		u.keyLocked(k)
		if u.closed {
			return
		}
	}
}

type key struct {
	name string // up, down, enter, escape, backspace, ctrl+c, ctrl+d, ctrl+u, paste; "" for text
	text string
}

const (
	pasteStart = "\x1b[200~"
	pasteEnd   = "\x1b[201~"
)

func nextKey(b []byte) (key, int) {
	s := string(b)
	switch {
	case strings.HasPrefix(s, pasteStart):
		rest := s[len(pasteStart):]
		end := strings.Index(rest, pasteEnd)
		if end < 0 {
			return key{name: "paste", text: rest}, len(s)
		}
		return key{name: "paste", text: rest[:end]}, len(pasteStart) + end + len(pasteEnd)
	case strings.HasPrefix(s, "\x1b[A"), strings.HasPrefix(s, "\x1bOA"):
		return key{name: "up"}, 3
	case strings.HasPrefix(s, "\x1b[B"), strings.HasPrefix(s, "\x1bOB"):
		return key{name: "down"}, 3
	case strings.HasPrefix(s, "\x1b[") || strings.HasPrefix(s, "\x1bO"):
		// Another sequence: skip it whole, up to its final byte.
		for i := 2; i < len(b); i++ {
			if b[i] >= 0x40 && b[i] <= 0x7e {
				return key{}, i + 1
			}
		}
		return key{}, len(b)
	case b[0] == 0x1b:
		return key{name: "escape"}, 1
	case b[0] == '\r' || b[0] == '\n':
		return key{name: "enter"}, 1
	case b[0] == 0x7f || b[0] == 0x08:
		return key{name: "backspace"}, 1
	case b[0] == 0x03:
		return key{name: "ctrl+c"}, 1
	case b[0] == 0x04:
		return key{name: "ctrl+d"}, 1
	case b[0] == 0x15:
		return key{name: "ctrl+u"}, 1
	case b[0] < 0x20:
		return key{}, 1
	}
	r, n := utf8.DecodeRune(b)
	return key{text: string(r)}, n
}

func (u *ui) keyLocked(k key) {
	if len(u.asks) > 0 {
		a := u.asks[0]
		switch {
		case k.name == "up":
			a.cursor = (a.cursor + len(a.p.Options) - 1) % len(a.p.Options)
		case k.name == "down":
			a.cursor = (a.cursor + 1) % len(a.p.Options)
		case k.name == "enter":
			u.choose(a, a.cursor)
			return
		case k.name == "escape" || k.name == "ctrl+c":
			u.cancelLocked()
			return
		case k.name == "" && len(k.text) == 1 && k.text[0] >= '1' && k.text[0] <= '9':
			if i, _ := strconv.Atoi(k.text); i <= len(a.p.Options) {
				u.choose(a, i-1)
			}
			return
		default:
			return
		}
		u.eraseBottomLocked()
		u.drawBottomLocked()
		return
	}
	busy := u.state == stateWorking || u.state == stateBlocked || u.state == stateStarting
	switch k.name {
	case "escape", "ctrl+c":
		if busy {
			u.cancelLocked()
			return
		}
		u.input = nil
	case "ctrl+d":
		if !busy && len(u.input) == 0 {
			u.eraseBottomLocked()
			u.closed = true
			close(u.quit)
			return
		}
	case "ctrl+u":
		u.input = nil
	case "backspace":
		if len(u.input) > 0 {
			u.input = u.input[:len(u.input)-1]
		}
	case "enter":
		text := strings.TrimSpace(string(u.input))
		u.input = nil
		if text == "" {
			break
		}
		if busy {
			u.queued = append(u.queued, text)
			return
		}
		u.submitLocked(text)
		return
	case "paste", "":
		u.input = append(u.input, []rune(strings.ReplaceAll(k.text, "\r", "\n"))...)
	default:
		return
	}
	if !busy {
		u.eraseBottomLocked()
		u.drawBottomLocked()
	}
}

func (u *ui) choose(a *ask, i int) {
	if i < 0 || i >= len(a.p.Options) {
		return
	}
	select {
	case a.answer <- PermissionOutcome{Outcome: "selected", OptionID: a.p.Options[i].OptionID}:
	default:
	}
}
