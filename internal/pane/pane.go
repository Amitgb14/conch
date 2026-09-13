// Package pane runs a program on a pseudo-terminal and keeps an emulated
// screen of its output, so the server can render it to any number of clients
// and keep it alive while none are attached.
package pane

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"

	"github.com/amitghadge/conch/internal/config"
	"github.com/amitghadge/conch/internal/detect"
	"github.com/amitghadge/conch/internal/proto"
)

// Options configures a new pane.
type Options struct {
	ID      string
	Name    string
	Command []string
	Cwd     string
	BaseEnv []string // environment to start from; nil means the server's
	Env     []string // extra KEY=VALUE entries appended to BaseEnv
	Cols    int
	Rows    int
}

// Pane is one program running on a PTY.
type Pane struct {
	id      string
	name    string
	command []string
	cwd     string
	created time.Time

	cmd  *exec.Cmd
	ptmx *os.File

	// emuMu guards emu. vt.SafeEmulator is not enough: its CellAt returns a
	// pointer into the screen that is read after its lock is released.
	// Emulator callbacks run under emuMu and take mu, so mu must never be
	// held while acquiring emuMu.
	emuMu sync.RWMutex
	emu   *vt.Emulator

	// done is closed once the process has exited and output is drained.
	done chan struct{}

	mu            sync.Mutex
	state         string
	exitCode      int
	title         string
	customName    string
	mouseModes    map[ansi.Mode]bool
	cursorVisible bool
	changed       chan struct{} // closed and replaced on every screen change
	version       uint64
}

const (
	defaultCols = 80
	defaultRows = 24
)

// Start launches the program and begins mirroring its output.
func Start(opts Options) (*Pane, error) {
	if len(opts.Command) == 0 {
		return nil, errors.New("pane: empty command")
	}
	if opts.Cols <= 0 {
		opts.Cols = defaultCols
	}
	if opts.Rows <= 0 {
		opts.Rows = defaultRows
	}
	if opts.Name == "" {
		opts.Name = baseName(opts.Command[0])
	}

	p := &Pane{
		id:            opts.ID,
		name:          opts.Name,
		command:       opts.Command,
		cwd:           opts.Cwd,
		created:       time.Now(),
		emu:           vt.NewEmulator(opts.Cols, opts.Rows),
		done:          make(chan struct{}),
		state:         proto.PaneRunning,
		cursorVisible: true,
		changed:       make(chan struct{}),
		mouseModes:    map[ansi.Mode]bool{},
	}
	p.emu.SetCallbacks(vt.Callbacks{
		// Titles come from titleScanner, not the emulator's Title callback.
		CursorVisibility: func(v bool) {
			p.mu.Lock()
			p.cursorVisible = v
			p.mu.Unlock()
		},
		EnableMode:  func(mode ansi.Mode) { p.setMode(mode, true) },
		DisableMode: func(mode ansi.Mode) { p.setMode(mode, false) },
	})

	cmd := exec.Command(opts.Command[0], opts.Command[1:]...)
	cmd.Dir = opts.Cwd
	base := opts.BaseEnv
	if base == nil {
		base = os.Environ()
	}
	cmd.Env = config.MergeEnv(base, append([]string{
		"TERM=xterm-256color",
		"COLORTERM=truecolor",
		"CONCH_PANE_ID=" + opts.ID,
	}, opts.Env...)...)

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(opts.Cols), Rows: uint16(opts.Rows)})
	if err != nil {
		return nil, err
	}
	p.cmd = cmd
	p.ptmx = ptmx

	readDone := make(chan struct{})
	go p.readLoop(readDone)
	// Replies the emulator generates (cursor position reports, device
	// attributes, encoded keys) come out of its input pipe and go to the PTY.
	// Reading that pipe needs no lock. The queue decouples the two so a
	// program that is slow to read its input (say, during a large paste)
	// cannot stall the emulator, and with it the program's own output.
	input := make(chan []byte, 4096)
	go func() {
		defer close(input)
		buf := make([]byte, 32*1024)
		for {
			n, err := p.emu.Read(buf)
			if n > 0 {
				input <- append([]byte(nil), buf[:n]...)
			}
			if err != nil {
				return
			}
		}
	}()
	go func() {
		for b := range input {
			if _, err := ptmx.Write(b); err != nil {
				for range input { // drain so the reader never blocks
				}
				return
			}
		}
	}()
	go p.wait(readDone)
	return p, nil
}

func (p *Pane) readLoop(done chan<- struct{}) {
	defer close(done)
	buf := make([]byte, 32*1024)
	var titles titleScanner
	for {
		n, err := p.ptmx.Read(buf)
		if n > 0 {
			if t, ok := titles.scan(buf[:n]); ok {
				p.mu.Lock()
				p.title = t
				p.mu.Unlock()
			}
			p.emuMu.Lock()
			_, _ = p.emu.Write(buf[:n])
			p.emuMu.Unlock()
			p.notify()
		}
		if err != nil {
			return
		}
	}
}

func (p *Pane) wait(readDone <-chan struct{}) {
	err := p.cmd.Wait()
	// Let trailing output land on the screen. A background child that still
	// holds the terminal open would keep the read blocked, so don't wait
	// forever.
	select {
	case <-readDone:
	case <-time.After(500 * time.Millisecond):
	}

	code := 0
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		code = exitErr.ExitCode()
	} else if err != nil {
		code = -1
	}
	p.mu.Lock()
	p.state = proto.PaneExited
	p.exitCode = code
	// Close under mu so Foreground never issues an ioctl on a closed (and
	// possibly reused) descriptor.
	_ = p.ptmx.Close()
	p.mu.Unlock()

	// End the reply-copy goroutine by closing the emulator's input pipe.
	// Emulator.Close would do the same but races with Read on its closed flag.
	if pw, ok := p.emu.InputPipe().(io.Closer); ok {
		_ = pw.Close()
	}
	p.notify()
	close(p.done)
}

func (p *Pane) notify() {
	p.mu.Lock()
	close(p.changed)
	p.changed = make(chan struct{})
	p.version++
	p.mu.Unlock()
}

// Version increases with every screen change.
func (p *Pane) Version() uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.version
}

// Foreground returns the terminal's foreground process.
func (p *Pane) Foreground() (detect.Process, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state != proto.PaneRunning {
		return detect.Process{}, errExited
	}
	return detect.ForegroundProcess(p.ptmx)
}

// ID returns the pane ID.
func (p *Pane) ID() string { return p.id }

// Done is closed when the process has exited.
func (p *Pane) Done() <-chan struct{} { return p.done }

// Changed returns a channel that is closed at the next screen change.
func (p *Pane) Changed() <-chan struct{} {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.changed
}

func (p *Pane) size() (cols, rows int) {
	p.emuMu.RLock()
	defer p.emuMu.RUnlock()
	return p.emu.Width(), p.emu.Height()
}

// Info returns the pane's metadata.
func (p *Pane) Info() proto.PaneInfo {
	cols, rows := p.size()
	p.mu.Lock()
	defer p.mu.Unlock()
	info := proto.PaneInfo{
		ID:       p.id,
		Name:     p.name,
		Command:  p.command,
		Cwd:      p.cwd,
		Cols:     cols,
		Rows:     rows,
		PID:      p.cmd.Process.Pid,
		State:    p.state,
		ExitCode: p.exitCode,
		Created:  p.created,
	}
	if p.customName != "" {
		info.Name, info.CustomName = p.customName, true
	}
	return info
}

// Rename sets the pane's name; "" restores the default.
func (p *Pane) Rename(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.customName = strings.TrimSpace(name)
}

// Title returns the last window title the program set.
func (p *Pane) Title() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.title
}

// SendMouse forwards a mouse event. It does nothing unless the program
// enabled mouse reporting.
func (p *Pane) SendMouse(m proto.PaneSendMouseParams) error {
	if !p.running() {
		return errExited
	}
	var ev uv.Mouse
	ev.X, ev.Y = m.X, m.Y
	switch m.Button {
	case "left":
		ev.Button = uv.MouseLeft
	case "middle":
		ev.Button = uv.MouseMiddle
	case "right":
		ev.Button = uv.MouseRight
	case "wheel_up":
		ev.Button = uv.MouseWheelUp
	case "wheel_down":
		ev.Button = uv.MouseWheelDown
	case "none", "":
		ev.Button = uv.MouseNone
	default:
		return fmt.Errorf("pane: unknown mouse button %q", m.Button)
	}
	if m.Shift {
		ev.Mod |= uv.ModShift
	}
	if m.Alt {
		ev.Mod |= uv.ModAlt
	}
	if m.Ctrl {
		ev.Mod |= uv.ModCtrl
	}
	var event uv.MouseEvent
	switch m.Action {
	case proto.MousePress:
		event = uv.MouseClickEvent(ev)
	case proto.MouseRelease:
		event = uv.MouseReleaseEvent(ev)
	case proto.MouseMotion:
		event = uv.MouseMotionEvent(ev)
	case proto.MouseWheel:
		event = uv.MouseWheelEvent(ev)
	default:
		return fmt.Errorf("pane: unknown mouse action %q", m.Action)
	}
	p.emuMu.Lock()
	defer p.emuMu.Unlock()
	p.emu.SendMouse(event)
	return nil
}

func (p *Pane) running() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state == proto.PaneRunning
}

// Resize changes the terminal size seen by the program.
func (p *Pane) Resize(cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return errors.New("pane: size must be positive")
	}
	p.emuMu.Lock()
	same := cols == p.emu.Width() && rows == p.emu.Height()
	if !same {
		p.emu.Resize(cols, rows)
	}
	p.emuMu.Unlock()
	if same {
		return nil
	}
	if p.running() {
		if err := pty.Setsize(p.ptmx, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)}); err != nil {
			return err
		}
	}
	p.notify()
	return nil
}

var errExited = errors.New("pane: process has exited")

// SendText types text into the program. With paste set, the text is wrapped
// in bracketed-paste markers if the program enabled that mode.
func (p *Pane) SendText(text string, paste bool) error {
	if !p.running() {
		return errExited
	}
	p.emuMu.Lock()
	defer p.emuMu.Unlock()
	if paste {
		p.emu.Paste(text)
	} else {
		p.emu.SendText(text)
	}
	return nil
}

// SendKeys sends named keys, encoded for the program's current terminal
// modes (e.g. application cursor keys).
func (p *Pane) SendKeys(keys []string) error {
	if !p.running() {
		return errExited
	}
	events := make([]uv.KeyPressEvent, len(keys))
	for i, name := range keys {
		k, err := ParseKey(name)
		if err != nil {
			return err
		}
		events[i] = k
	}
	p.emuMu.Lock()
	defer p.emuMu.Unlock()
	for _, k := range events {
		if seq := modifiedSequence(k); seq != "" {
			_, _ = io.WriteString(p.emu.InputPipe(), seq)
			continue
		}
		p.emu.SendKey(k)
	}
	return nil
}

// mouseModes are the DEC private modes by which a program asks for mouse
// reports.
var mouseModes = map[ansi.Mode]bool{
	ansi.ModeMouseX10: true, ansi.ModeMouseNormal: true, ansi.ModeMouseHighlight: true,
	ansi.ModeMouseButtonEvent: true, ansi.ModeMouseAnyEvent: true,
}

func (p *Pane) setMode(mode ansi.Mode, on bool) {
	if !mouseModes[mode] {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if on {
		p.mouseModes[mode] = true
	} else {
		delete(p.mouseModes, mode)
	}
}

// Frame renders the visible screen with the cursor drawn in.
func (p *Pane) Frame() proto.Frame { return p.FrameAt(0) }

// FrameAt renders the view scrolled offset lines back into history; 0 is
// the live screen. The offset is clamped to the history available.
func (p *Pane) FrameAt(offset int) proto.Frame {
	p.mu.Lock()
	title, cursorVisible, running := p.title, p.cursorVisible, p.state == proto.PaneRunning
	mouse := len(p.mouseModes) > 0
	p.mu.Unlock()

	p.emuMu.RLock()
	defer p.emuMu.RUnlock()
	cols, rows := p.emu.Width(), p.emu.Height()
	alt := p.emu.IsAltScreen()
	history := 0
	if !alt {
		history = p.emu.ScrollbackLen()
	}
	offset = max(0, min(offset, history))
	cur := p.emu.CursorPosition()

	lines := make([]string, rows)
	line := make(uv.Line, cols)
	start := history - offset // index into history followed by the screen
	for y := 0; y < rows; y++ {
		idx := start + y
		for x := 0; x < cols; x++ {
			var c *uv.Cell
			if idx < history {
				c = p.emu.ScrollbackCellAt(x, idx)
			} else {
				c = p.emu.CellAt(x, idx-history)
			}
			if c != nil {
				line[x] = *c
			} else {
				line[x] = uv.EmptyCell
			}
		}
		if offset == 0 && running && cursorVisible && y == cur.Y && cur.X >= 0 && cur.X < cols {
			c := &line[cur.X]
			if c.IsZero() || c.Content == "" {
				*c = uv.EmptyCell
			}
			c.Style.Attrs |= uv.AttrReverse
		}
		lines[y] = line.Render()
	}
	return proto.Frame{
		ID: p.id, Cols: cols, Rows: rows, Lines: lines, Title: title,
		Offset: offset, History: history, Mouse: mouse, AltScreen: alt,
	}
}

// History returns how many lines have scrolled off the main screen.
func (p *Pane) History() int {
	p.emuMu.RLock()
	defer p.emuMu.RUnlock()
	if p.emu.IsAltScreen() {
		return 0
	}
	return p.emu.ScrollbackLen()
}

// PlainLines returns the visible screen as unstyled text.
func (p *Pane) PlainLines() []string {
	p.emuMu.RLock()
	defer p.emuMu.RUnlock()
	cols, rows := p.emu.Width(), p.emu.Height()
	lines := make([]string, rows)
	var b strings.Builder
	for y := 0; y < rows; y++ {
		b.Reset()
		for x := 0; x < cols; x++ {
			c := p.emu.CellAt(x, y)
			switch {
			case c == nil:
				b.WriteByte(' ')
			case c.Content != "":
				b.WriteString(c.Content)
			case !c.IsZero(): // zero cells are wide-character continuations
				b.WriteByte(' ')
			}
		}
		lines[y] = strings.TrimRight(b.String(), " ")
	}
	return lines
}

// Close terminates the program (SIGHUP to its process group, then SIGKILL)
// and waits for it to exit.
func (p *Pane) Close() {
	if p.running() {
		pid := p.cmd.Process.Pid
		_ = syscall.Kill(-pid, syscall.SIGHUP)
		select {
		case <-p.done:
			return
		case <-time.After(2 * time.Second):
		}
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	}
	<-p.done
}

func baseName(path string) string {
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		return path[i+1:]
	}
	return path
}
