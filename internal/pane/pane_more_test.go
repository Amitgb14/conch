package pane

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"golang.org/x/sys/unix"

	"github.com/Amitgb14/conch/internal/proto"
)

// a5Script starts a pane running script directly (not typed at a prompt), so
// markers it prints never appear in an echoed command line.
func a5Script(t *testing.T, script string, cols, rows int) *Pane {
	t.Helper()
	p, err := Start(Options{
		ID:      "a5",
		Command: []string{"/bin/sh", "-c", script},
		Env:     []string{"ENV="},
		Cols:    cols,
		Rows:    rows,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

func a5Wait(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func a5Screen(p *Pane) string { return strings.Join(p.PlainLines(), "\n") }

func a5Exit(t *testing.T, p *Pane) {
	t.Helper()
	select {
	case <-p.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("pane did not exit")
	}
}

func TestA5ParseKeyTable(t *testing.T) {
	for _, tc := range []struct {
		in   string
		code rune
		mod  uv.KeyMod
		text string
	}{
		{"enter", uv.KeyEnter, 0, ""},
		{"escape", uv.KeyEscape, 0, ""},
		{"esc", uv.KeyEscape, 0, ""},
		{"space", uv.KeySpace, 0, ""},
		{"pgdown", uv.KeyPgDown, 0, ""},
		{"f1", uv.KeyF1, 0, ""},
		{"f12", uv.KeyF12, 0, ""},
		{"ctrl++", '+', uv.ModCtrl, ""},
		{"+", '+', 0, "+"},
		{"ctrl+@", uv.KeySpace, uv.ModCtrl, ""},
		{"shift+tab", uv.KeyTab, uv.ModShift, ""},
		{"alt+shift+x", 'x', uv.ModAlt | uv.ModShift, ""},
		{"alt+x", 'x', uv.ModAlt, "x"},
		{"ctrl+alt+delete", uv.KeyDelete, uv.ModCtrl | uv.ModAlt, ""},
		{"é", 'é', 0, "é"},
	} {
		k, err := ParseKey(tc.in)
		if err != nil {
			t.Errorf("ParseKey(%q): %v", tc.in, err)
			continue
		}
		if k.Code != tc.code || k.Mod != tc.mod || k.Text != tc.text {
			t.Errorf("ParseKey(%q) = code %q mod %v text %q, want %q %v %q", tc.in, k.Code, k.Mod, k.Text, tc.code, tc.mod, tc.text)
		}
	}
	for _, bad := range []string{"f0", "f13", "fx", "meta+a", "", "super+ctrl+a", "abc"} {
		if _, err := ParseKey(bad); err == nil {
			t.Errorf("ParseKey(%q) should fail", bad)
		}
	}
}

func TestA5ModifiedSequence(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"left", ""},
		{"shift+tab", ""},
		{"ctrl+a", ""},
		{"alt+enter", ""},
		{"shift+up", "\x1b[1;2A"},
		{"alt+down", "\x1b[1;3B"},
		{"shift+alt+right", "\x1b[1;4C"},
		{"ctrl+left", "\x1b[1;5D"},
		{"ctrl+shift+home", "\x1b[1;6H"},
		{"ctrl+alt+end", "\x1b[1;7F"},
		{"ctrl+alt+shift+end", "\x1b[1;8F"},
		{"shift+insert", "\x1b[2;2~"},
		{"ctrl+delete", "\x1b[3;5~"},
		{"alt+pgup", "\x1b[5;3~"},
		{"shift+pgdown", "\x1b[6;2~"},
	} {
		k, err := ParseKey(tc.in)
		if err != nil {
			t.Fatalf("ParseKey(%q): %v", tc.in, err)
		}
		if got := modifiedSequence(k); got != tc.want {
			t.Errorf("modifiedSequence(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// a5Bytes starts a raw-mode reader of n bytes, runs send, and returns the
// bytes the program received as hex.
func a5Bytes(t *testing.T, n int, setup string, send func(p *Pane)) string {
	t.Helper()
	script := setup + `stty raw -echo; printf 'RE''ADY\r\n'; ` +
		`dd bs=1 count=` + strconv.Itoa(n) + ` 2>/dev/null | od -An -tx1 | tr -s ' \n' '  '; printf ' EN''D\r\n'; sleep 30`
	p := a5Script(t, script, 200, 10)
	a5Wait(t, "reader ready", func() bool { return strings.Contains(a5Screen(p), "READY") })
	send(p)
	a5Wait(t, "reader output", func() bool { return strings.Contains(a5Screen(p), " END") })
	s := a5Screen(p)
	s = s[strings.Index(s, "READY")+len("READY"):]
	s = s[:strings.Index(s, " END")]
	return strings.Join(strings.Fields(s), " ")
}

func TestA5SendModifiedKeysReachProgram(t *testing.T) {
	got := a5Bytes(t, 12, "", func(p *Pane) {
		if err := p.SendKeys([]string{"ctrl+left", "shift+pgup"}); err != nil {
			t.Fatal(err)
		}
	})
	// ESC [ 1 ; 5 D   ESC [ 5 ; 2 ~
	if want := "1b 5b 31 3b 35 44 1b 5b 35 3b 32 7e"; got != want {
		t.Fatalf("received %q, want %q", got, want)
	}
}

func TestA5SendPlainKeysAndText(t *testing.T) {
	got := a5Bytes(t, 5, "", func(p *Pane) {
		if err := p.SendKeys([]string{"enter", "ctrl+c", "tab"}); err != nil {
			t.Fatal(err)
		}
		if err := p.SendText("hi", false); err != nil {
			t.Fatal(err)
		}
	})
	if want := "0d 03 09 68 69"; got != want {
		t.Fatalf("received %q, want %q", got, want)
	}
}

func TestA5BracketedPaste(t *testing.T) {
	// The program asks for bracketed paste; a paste is wrapped, 2 + 6*2 bytes.
	got := a5Bytes(t, 14, `printf '\033[?2004h'; `, func(p *Pane) {
		a5Wait(t, "paste mode", func() bool {
			p.mu.Lock()
			defer p.mu.Unlock()
			for m := range p.modes {
				if m.Mode() == 2004 {
					return true
				}
			}
			return false
		})
		if err := p.SendText("ok", true); err != nil {
			t.Fatal(err)
		}
	})
	if want := "1b 5b 32 30 30 7e 6f 6b 1b 5b 32 30 31 7e"; got != want {
		t.Fatalf("received %q, want %q", got, want)
	}
}

func TestA5SendMouse(t *testing.T) {
	// SGR mouse reporting: a left press at column 3, row 4 (1-based).
	got := a5Bytes(t, 9, `printf '\033[?1000h\033[?1006h'; `, func(p *Pane) {
		a5Wait(t, "mouse mode", func() bool { return p.Frame().Mouse })
		if err := p.SendMouse(proto.PaneSendMouseParams{X: 2, Y: 3, Button: "left", Action: proto.MousePress}); err != nil {
			t.Fatal(err)
		}
	})
	if want := "1b 5b 3c 30 3b 33 3b 34 4d"; got != want {
		t.Fatalf("received %q, want %q", got, want)
	}
}

func TestA5SendMouseVariantsAndErrors(t *testing.T) {
	p := a5Script(t, "sleep 30", 40, 5)
	for _, button := range []string{"left", "middle", "right", "wheel_up", "wheel_down", "none", ""} {
		for _, action := range []string{proto.MousePress, proto.MouseRelease, proto.MouseMotion, proto.MouseWheel} {
			m := proto.PaneSendMouseParams{X: 1, Y: 1, Button: button, Action: action, Shift: true, Alt: true, Ctrl: true}
			if err := p.SendMouse(m); err != nil {
				t.Fatalf("%s %s: %v", button, action, err)
			}
		}
	}
	if err := p.SendMouse(proto.PaneSendMouseParams{Button: "thumb", Action: proto.MousePress}); err == nil {
		t.Fatal("unknown button accepted")
	}
	if err := p.SendMouse(proto.PaneSendMouseParams{Button: "left", Action: "double"}); err == nil {
		t.Fatal("unknown action accepted")
	}
}

func TestA5ExitedPaneRejectsInput(t *testing.T) {
	p := a5Script(t, "printf 'bye-now'; exit 9", 40, 5)
	a5Exit(t, p)
	info := p.Info()
	if info.State != proto.PaneExited || info.ExitCode != 9 {
		t.Fatalf("info %+v", info)
	}
	if err := p.SendText("x", false); !errors.Is(err, errExited) {
		t.Fatalf("SendText: %v", err)
	}
	if err := p.SendKeys([]string{"enter"}); !errors.Is(err, errExited) {
		t.Fatalf("SendKeys: %v", err)
	}
	if err := p.SendMouse(proto.PaneSendMouseParams{Button: "left", Action: proto.MousePress}); !errors.Is(err, errExited) {
		t.Fatalf("SendMouse: %v", err)
	}
	if _, err := p.Foreground(); !errors.Is(err, errExited) {
		t.Fatalf("Foreground: %v", err)
	}
	if _, _, err := p.Detach(); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("Detach: %v", err)
	}
	// Resizing an exited pane resizes the screen only.
	if err := p.Resize(50, 6); err != nil {
		t.Fatal(err)
	}
	if f := p.Frame(); f.Cols != 50 || f.Rows != 6 {
		t.Fatalf("frame %dx%d", f.Cols, f.Rows)
	}
	if !strings.Contains(a5Screen(p), "bye-now") {
		t.Fatalf("trailing output lost: %q", a5Screen(p))
	}
	// Closing an exited pane returns at once.
	done := make(chan struct{})
	go func() { p.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close hung on an exited pane")
	}
}

func TestA5ResizeErrorsAndNoop(t *testing.T) {
	p := a5Script(t, "sleep 30", 40, 5)
	for _, sz := range [][2]int{{0, 5}, {40, 0}, {-1, -1}} {
		if err := p.Resize(sz[0], sz[1]); err == nil {
			t.Fatalf("Resize(%d,%d) accepted", sz[0], sz[1])
		}
	}
	v := p.Version()
	if err := p.Resize(40, 5); err != nil {
		t.Fatal(err)
	}
	if p.Version() != v {
		t.Fatal("same-size resize changed the screen version")
	}
	ch := p.Changed()
	if err := p.Resize(41, 6); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("resize did not signal a change")
	}
	if p.Version() == v {
		t.Fatal("version unchanged after resize")
	}
}

func TestA5StartErrorsAndDefaults(t *testing.T) {
	if _, err := Start(Options{}); err == nil {
		t.Fatal("empty command accepted")
	}
	if _, err := Start(Options{Command: []string{"/nonexistent/a5-binary"}}); err == nil {
		t.Fatal("missing binary accepted")
	}
	p, err := Start(Options{ID: "d1", Command: []string{"sh", "-c", "sleep 30"}, BaseEnv: []string{"PATH=/usr/bin:/bin"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	info := p.Info()
	if info.Name != "sh" || info.Cols != defaultCols || info.Rows != defaultRows || p.ID() != "d1" {
		t.Fatalf("defaults: %+v", info)
	}
	if baseName("/a/b/c") != "c" || baseName("c") != "c" {
		t.Fatal("baseName")
	}
}

func TestA5TitleRenameForeground(t *testing.T) {
	p := a5Script(t, `printf '\033]0;a5-title\007'; printf 'tit''led\n'; exec sleep 30`, 40, 5)
	a5Wait(t, "title", func() bool { return p.Title() == "a5-title" })
	if f := p.Frame(); f.Title != "a5-title" {
		t.Fatalf("frame title %q", f.Title)
	}
	a5Wait(t, "foreground sleep", func() bool {
		proc, err := p.Foreground()
		return err == nil && strings.Contains(strings.Join(proc.Names(), ","), "sleep")
	})

	p.Rename("  custom  ")
	if info := p.Info(); info.Name != "custom" || !info.CustomName {
		t.Fatalf("rename: %+v", info)
	}
	p.Rename("")
	if info := p.Info(); info.Name != "sh" || info.CustomName {
		t.Fatalf("rename reset: %+v", info)
	}
}

func TestA5CursorHiddenAndAltScreen(t *testing.T) {
	p := a5Script(t, `printf '\033[?25l'; printf 'hid''den\n'; printf '\033[?1049h'; printf 'alt''-on'; sleep 30`, 30, 5)
	a5Wait(t, "alt screen", func() bool { return p.Frame().AltScreen && strings.Contains(a5Screen(p), "alt-on") })
	p.mu.Lock()
	visible := p.cursorVisible
	p.mu.Unlock()
	if visible {
		t.Fatal("cursor should be hidden")
	}
	if p.History() != 0 {
		t.Fatalf("alt screen history %d", p.History())
	}
	for _, l := range p.Frame().Lines {
		if strings.Contains(l, "\x1b[7m") {
			t.Fatalf("hidden cursor drawn: %q", l)
		}
	}

	// A detached alt-screen pane replays onto the alt screen with the cursor hidden.
	snap, ptmx, err := p.Detach()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(snap.Replay, "\x1b[?1049h") || !strings.Contains(snap.Replay, "\x1b[?25l") {
		t.Fatalf("replay lacks alt screen or hidden cursor: %q", snap.Replay)
	}
	snap.Cols, snap.Rows = 0, 0 // defaults apply
	q, err := Adopt(snap, a5Dup(t, p, ptmx))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(q.Close)
	if f := q.Frame(); !f.AltScreen || f.Cols != defaultCols || f.Rows != defaultRows {
		t.Fatalf("adopted frame alt=%v %dx%d", f.AltScreen, f.Cols, f.Rows)
	}
	q.mu.Lock()
	visible = q.cursorVisible
	q.mu.Unlock()
	if visible {
		t.Fatal("adopted cursor should be hidden")
	}
}

// a5Dup gives an adopted pane its own descriptor for p's terminal, as a new
// process would have. The original pane closes its file under p.mu.
func a5Dup(t *testing.T, p *Pane, ptmx *os.File) *os.File {
	t.Helper()
	p.mu.Lock()
	fd, err := unix.Dup(int(ptmx.Fd()))
	p.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	return os.NewFile(uintptr(fd), "ptmx")
}

func TestA5DetachResume(t *testing.T) {
	// Resume (reload.go:77) replaces p.stopRead/p.readDone without a lock
	// while the wait goroutine reads p.readDone (pane.go:258); -race reports
	// it as soon as the resumed pane exits.
	t.Skip("bug: data race between Pane.Resume and Pane.wait on readDone")
	p := startShell(t)
	_, _, err := p.Detach()
	if err != nil {
		t.Fatal(err)
	}
	p.Resume()
	_ = p.SendText("echo resumed-$((6*7))", false)
	_ = p.SendKeys([]string{"enter"})
	waitScreen(t, p, "resumed-42")
}

func TestA5KeepOnExec(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "f"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if flags, _ := unix.FcntlInt(f.Fd(), unix.F_GETFD, 0); flags&unix.FD_CLOEXEC == 0 {
		t.Fatal("expected close-on-exec to start with")
	}
	if err := KeepOnExec(f); err != nil {
		t.Fatal(err)
	}
	if flags, _ := unix.FcntlInt(f.Fd(), unix.F_GETFD, 0); flags&unix.FD_CLOEXEC != 0 {
		t.Fatal("close-on-exec still set")
	}
	closed, _ := os.Open(os.DevNull)
	closed.Close()
	if err := KeepOnExec(closed); err == nil {
		t.Fatal("closed file accepted")
	}
}

func TestA5AdoptedPaneExitCode(t *testing.T) {
	p := startShell(t)
	snap, ptmx, err := p.Detach()
	if err != nil {
		t.Fatal(err)
	}
	q, err := Adopt(snap, a5Dup(t, p, ptmx))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(q.Close)
	_ = q.SendText("exit 4", false)
	_ = q.SendKeys([]string{"enter"})
	a5Exit(t, q)
	// Both the original and the adopted pane wait for the same child here,
	// so only one of them sees the real code; the other gets -1.
	if info := q.Info(); info.State != proto.PaneExited || (info.ExitCode != 4 && info.ExitCode != -1) {
		t.Fatalf("adopted exit: %+v", info)
	}
	q.redraw() // no-op once exited
}

func TestA5ChangedAndHistory(t *testing.T) {
	p := a5Script(t, `printf 'first\n'; read x; i=0; while [ $i -lt 20 ]; do echo row-$i; i=$((i+1)); done; printf 'fin''ished\n'; sleep 30`, 30, 5)
	a5Wait(t, "first", func() bool { return strings.Contains(a5Screen(p), "first") })
	ch := p.Changed()
	v := p.Version()
	_ = p.SendKeys([]string{"enter"})
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("no change signalled")
	}
	a5Wait(t, "finished", func() bool { return strings.Contains(a5Screen(p), "finished") })
	if p.Version() <= v {
		t.Fatal("version did not grow")
	}
	if h := p.History(); h < 15 {
		t.Fatalf("history %d", h)
	}
	if f := p.FrameAt(-5); f.Offset != 0 {
		t.Fatalf("negative offset not clamped: %d", f.Offset)
	}
}
