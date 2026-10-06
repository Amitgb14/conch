package pane

import (
	"errors"
	"os"
	"slices"
	"sort"
	"strings"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
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
	// AltHistory is what conch kept as it scrolled off the alternate screen
	// (altscroll.go): text read off the screen, which no replay can rebuild.
	// Empty on the main screen, and in a snapshot from an older server.
	AltHistory []string `json:"alt_history,omitempty"`
}

// replayChunk is how many lines of a replay go into the emulator at once.
const replayChunk = 500

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
	stopRead, readDone := p.readChans()
	close(stopRead)
	select {
	case <-readDone:
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
	if p.emu.IsAltScreen() {
		snap.AltHistory = slices.Clone(p.alt.lines)
	}
	p.emuMu.RUnlock()

	// The terminal is the caller's from here: this pane keeps a reference
	// so Resume can take it back, but must not close it behind the pane
	// that adopts it.
	p.mu.Lock()
	p.handedOver = true
	p.mu.Unlock()
	return snap, p.ptmx, nil
}

// readChans returns the current read loop's stop and done channels, which
// Resume replaces.
func (p *Pane) readChans() (stop, done chan struct{}) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stopRead, p.readDone
}

// Resume restarts reading after a Detach whose handover failed, as soon as
// the stopped reader has finished.
func (p *Pane) Resume() {
	stop, done := make(chan struct{}), make(chan struct{})
	p.mu.Lock()
	old := p.readDone
	p.stopRead, p.readDone = stop, done
	p.handedOver = false // the handover failed: the terminal is ours again
	ptmx := p.ptmx
	p.mu.Unlock()
	closeOnExec(ptmx) // KeepOnExec opened it to the exec that didn't happen
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
	for _, l := range p.hist {
		// Cut to the width, or a line kept while the pane was wider
		// would wrap in the new emulator and push the rest down.
		lines = append(lines, fitWidth(l, cols))
	}
	if alt && len(lines) > 0 {
		// The main screen's history goes in before the alternate screen
		// is drawn, or a pane on it — an agent's full-screen interface —
		// would lose it at every reload. Blank rows push all of it off the
		// main screen into history; the main screen's own rows are not
		// reachable while the alternate one is in use.
		b.WriteString(ansi.ResetStyle)
		b.WriteString(strings.Join(lines, "\r\n"+ansi.ResetStyle))
		b.WriteString(strings.Repeat("\r\n", rows))
		lines = nil
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
		done:          make(chan struct{}),
		state:         proto.PaneRunning,
		cursorVisible: true,
		changed:       make(chan struct{}),
		mouseModes:    map[ansi.Mode]bool{},
		modes:         map[ansi.Mode]bool{},
		proc:          proc,
		ptmx:          ptmx,
	}
	// It came through an exec open, and would otherwise be inherited by
	// every program started from now on, holding this terminal open after
	// its pane has closed it: a program there then never hears it hang up.
	closeOnExec(ptmx)
	p.newEmulator(snap.Cols, snap.Rows)
	// A piece at a time, so the history being replayed goes into text as
	// it scrolls rather than all of it into the emulator's cells first.
	for _, chunk := range chunkByLines([]byte(snap.Replay), replayChunk) {
		_, _ = p.emu.Write(chunk)
		p.takeScrollback()
	}
	if p.emu.IsAltScreen() {
		p.alt.on = true
		p.alt.lines = keepLast(slices.Clone(snap.AltHistory), altHistoryMax)
	}
	// Replies the replay provoked (none are expected) must not reach the
	// program: start copying only now.
	p.startIO()
	go p.wait()
	p.redraw()
	return p, nil
}

// redraw asks the program to repaint by sending its process group SIGWINCH
// without changing the size. Changing the size and back (the usual trick)
// breaks programs that repaint only what they believe changed, such as
// Claude Code: two resizes in a row leave stale text on screen.
func (p *Pane) redraw() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state != proto.PaneRunning {
		return
	}
	pgrp, err := unix.IoctlGetInt(int(p.ptmx.Fd()), unix.TIOCGPGRP)
	if err != nil || pgrp <= 0 {
		return
	}
	_ = unix.Kill(-pgrp, unix.SIGWINCH)
}

// KeepOnExec lets a file descriptor survive exec: Go opens files
// close-on-exec.
func KeepOnExec(f *os.File) error {
	_, err := unix.FcntlInt(f.Fd(), unix.F_SETFD, 0)
	return err
}

// closeOnExec undoes KeepOnExec.
func closeOnExec(f *os.File) {
	if f == nil {
		return
	}
	if rc, err := f.SyscallConn(); err == nil {
		_ = rc.Control(func(fd uintptr) { unix.CloseOnExec(int(fd)) })
	}
}
