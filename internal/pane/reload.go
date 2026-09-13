package pane

import (
	"errors"
	"os"
	"sort"
	"strings"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
	"golang.org/x/sys/unix"

	"github.com/Amitgb14/conch/internal/proto"
)

// Snapshot is what a new server process needs to adopt a running pane: its
// metadata and a replay of its screen. The terminal itself is handed over
// as an open file descriptor.
type Snapshot struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	CustomName string    `json:"custom_name,omitempty"`
	Command    []string  `json:"command"`
	Cwd        string    `json:"cwd"`
	Created    time.Time `json:"created"`
	PID        int       `json:"pid"`
	Cols       int       `json:"cols"`
	Rows       int       `json:"rows"`
	Title      string    `json:"title,omitempty"`
	// Replay is terminal output that rebuilds the history, the screen, the
	// cursor and the modes in a fresh emulator.
	Replay string `json:"replay"`
}

// ErrNotRunning means the pane's program has exited, so there is nothing to
// hand over.
var ErrNotRunning = errors.New("pane: not running")

// Detach stops reading the program's output and returns a snapshot and the
// terminal. Output written meanwhile waits in the kernel for whoever reads
// next. Resume undoes it if the handover fails.
func (p *Pane) Detach() (Snapshot, *os.File, error) {
	if !p.running() {
		return Snapshot{}, nil, ErrNotRunning
	}
	close(p.stopRead)
	select {
	case <-p.readDone:
	case <-time.After(2 * time.Second):
		p.Resume()
		return Snapshot{}, nil, errors.New("pane: output reader did not stop")
	}
	p.mu.Lock()
	snap := Snapshot{ID: p.id, Name: p.name, CustomName: p.customName, Command: p.command, Cwd: p.cwd,
		Created: p.created, PID: p.proc.Pid, Title: p.title}
	modes := make([]ansi.Mode, 0, len(p.modes))
	for m := range p.modes {
		modes = append(modes, m)
	}
	cursorVisible := p.cursorVisible
	p.mu.Unlock()

	p.emuMu.RLock()
	snap.Cols, snap.Rows = p.emu.Width(), p.emu.Height()
	snap.Replay = p.replayLocked(modes, cursorVisible)
	p.emuMu.RUnlock()
	return snap, p.ptmx, nil
}

// Resume restarts reading after a Detach whose handover failed, as soon as
// the stopped reader has finished.
func (p *Pane) Resume() {
	old := p.readDone
	stop, done := make(chan struct{}), make(chan struct{})
	p.stopRead, p.readDone = stop, done
	go func() {
		<-old
		p.readLoop(stop, done)
	}()
}

// replayLocked renders history and screen as terminal output. The caller
// holds emuMu.
func (p *Pane) replayLocked(modes []ansi.Mode, cursorVisible bool) string {
	var b strings.Builder
	cols, rows := p.emu.Width(), p.emu.Height()
	line := func(get func(x int) *uv.Cell) string {
		l := make(uv.Line, cols)
		for x := 0; x < cols; x++ {
			if c := get(x); c != nil {
				l[x] = *c
			} else {
				l[x] = uv.EmptyCell
			}
		}
		return strings.TrimRight(l.Render(), " ")
	}
	alt := p.emu.IsAltScreen()
	var lines []string
	if !alt {
		for y := 0; y < p.emu.ScrollbackLen(); y++ {
			y := y
			lines = append(lines, line(func(x int) *uv.Cell { return p.emu.ScrollbackCellAt(x, y) }))
		}
	}
	for y := 0; y < rows; y++ {
		y := y
		lines = append(lines, line(func(x int) *uv.Cell { return p.emu.CellAt(x, y) }))
	}
	if alt {
		b.WriteString(ansi.SetMode(ansi.ModeAltScreenSaveCursor))
	}
	b.WriteString(ansi.ResetStyle)
	b.WriteString(strings.Join(lines, "\r\n"+ansi.ResetStyle))
	b.WriteString(ansi.ResetStyle)
	cur := p.emu.CursorPosition()
	b.WriteString(ansi.CursorPosition(cur.X+1, cur.Y+1))

	sort.Slice(modes, func(i, j int) bool { return modes[i].Mode() < modes[j].Mode() })
	for _, m := range modes {
		if m.Mode() == ansi.ModeAltScreenSaveCursor.Mode() || m.Mode() == ansi.ModeAltScreen.Mode() {
			continue // set above, before drawing
		}
		b.WriteString(ansi.SetMode(m))
	}
	if !cursorVisible {
		b.WriteString(ansi.HideCursor)
	}
	return b.String()
}

// Adopt takes over a pane another server process detached: the program is
// still running (and still this process's child after exec) on ptmx.
func Adopt(snap Snapshot, ptmx *os.File) (*Pane, error) {
	proc, err := os.FindProcess(snap.PID)
	if err != nil {
		return nil, err
	}
	if snap.Cols <= 0 || snap.Rows <= 0 {
		snap.Cols, snap.Rows = defaultCols, defaultRows
	}
	p := &Pane{
		id: snap.ID, name: snap.Name, customName: snap.CustomName, command: snap.Command, cwd: snap.Cwd,
		created: snap.Created, title: snap.Title,
		emu:           vt.NewEmulator(snap.Cols, snap.Rows),
		done:          make(chan struct{}),
		state:         proto.PaneRunning,
		cursorVisible: true,
		changed:       make(chan struct{}),
		mouseModes:    map[ansi.Mode]bool{},
		modes:         map[ansi.Mode]bool{},
		proc:          proc,
		ptmx:          ptmx,
	}
	p.setCallbacks()
	_, _ = p.emu.Write([]byte(snap.Replay))
	// Replies the replay provoked (none are expected) must not reach the
	// program: start copying only now.
	p.startIO()
	go p.wait()
	p.redraw()
	return p, nil
}

// redraw nudges the program to repaint, by changing the terminal size and
// back: full-screen programs redraw on SIGWINCH, repairing anything the
// replay missed.
func (p *Pane) redraw() {
	cols, rows := p.size()
	if rows < 2 {
		return
	}
	_ = pty.Setsize(p.ptmx, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows - 1)})
	time.Sleep(20 * time.Millisecond)
	_ = pty.Setsize(p.ptmx, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
}

// KeepOnExec lets a file descriptor survive exec: Go opens files
// close-on-exec.
func KeepOnExec(f *os.File) error {
	_, err := unix.FcntlInt(f.Fd(), unix.F_SETFD, 0)
	return err
}
