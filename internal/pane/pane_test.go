package pane

import (
	"strings"
	"testing"
	"time"

	"github.com/amitghadge/conch/internal/proto"
)

func startShell(t *testing.T) *Pane {
	t.Helper()
	p, err := Start(Options{
		ID:      "t1",
		Command: []string{"/bin/sh"},
		Env:     []string{"PS1=$ ", "ENV="},
		Cols:    60,
		Rows:    10,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

func waitScreen(t *testing.T, p *Pane, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(strings.Join(p.PlainLines(), "\n"), want) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("screen never showed %q; got:\n%s", want, strings.Join(p.PlainLines(), "\n"))
}

func TestEchoAndCtrlC(t *testing.T) {
	p := startShell(t)
	if err := p.SendText("echo hi-$((40+2))", false); err != nil {
		t.Fatal(err)
	}
	if err := p.SendKeys([]string{"enter"}); err != nil {
		t.Fatal(err)
	}
	waitScreen(t, p, "hi-42")

	_ = p.SendText("sleep 30; echo not-interrupted", false)
	_ = p.SendKeys([]string{"enter"})
	time.Sleep(200 * time.Millisecond)
	_ = p.SendKeys([]string{"ctrl+c"})
	_ = p.SendText("echo after-int", false)
	_ = p.SendKeys([]string{"enter"})
	waitScreen(t, p, "after-int")
}

func TestExitCodeAndResize(t *testing.T) {
	p := startShell(t)
	if err := p.Resize(100, 20); err != nil {
		t.Fatal(err)
	}
	_ = p.SendText("stty size; exit 3", false)
	_ = p.SendKeys([]string{"enter"})
	select {
	case <-p.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("pane did not exit")
	}
	info := p.Info()
	if info.State != proto.PaneExited || info.ExitCode != 3 {
		t.Fatalf("got state %s code %d, want exited 3", info.State, info.ExitCode)
	}
	if !strings.Contains(strings.Join(p.PlainLines(), "\n"), "20 100") {
		t.Fatalf("stty size did not report 20 100:\n%s", strings.Join(p.PlainLines(), "\n"))
	}
	if f := p.Frame(); f.Cols != 100 || f.Rows != 20 || len(f.Lines) != 20 {
		t.Fatalf("frame size %dx%d with %d lines", f.Cols, f.Rows, len(f.Lines))
	}
}

func TestParseKey(t *testing.T) {
	for _, tc := range []struct {
		in   string
		text string
		ok   bool
	}{
		{"enter", "", true},
		{"ctrl+c", "", true},
		{"alt+x", "x", true},
		{"ctrl+shift+left", "", true},
		{"f5", "", true},
		{"a", "a", true},
		{"hyper+a", "", false},
		{"nosuchkey", "", false},
	} {
		k, err := ParseKey(tc.in)
		if (err == nil) != tc.ok {
			t.Errorf("ParseKey(%q) err = %v, want ok=%v", tc.in, err, tc.ok)
			continue
		}
		if k.Text != tc.text {
			t.Errorf("ParseKey(%q).Text = %q, want %q", tc.in, k.Text, tc.text)
		}
	}
}

func TestScrollbackAndModes(t *testing.T) {
	p := startShell(t)
	_ = p.SendText(`i=1; while [ $i -le 60 ]; do echo line-$i; i=$((i+1)); done; printf '\033[?1000h'`, false)
	_ = p.SendKeys([]string{"enter"})
	waitScreen(t, p, "line-60")

	live := p.FrameAt(0)
	if live.Offset != 0 || live.History < 45 || !live.Mouse {
		t.Fatalf("live frame: offset %d history %d mouse %v", live.Offset, live.History, live.Mouse)
	}
	// Scrolled back far enough, line-1 comes into view; beyond history clamps.
	back := p.FrameAt(1 << 20)
	if back.Offset != back.History {
		t.Fatalf("offset %d not clamped to history %d", back.Offset, back.History)
	}
	if !strings.Contains(strings.Join(back.Lines, "\n"), "line-1") {
		t.Fatalf("oldest history not shown:\n%s", strings.Join(back.Lines, "\n"))
	}
	mid := p.FrameAt(10)
	if strings.Join(mid.Lines, "\n") == strings.Join(live.Lines, "\n") {
		t.Fatal("scrolled frame equals live frame")
	}

	_ = p.SendText(`printf '\033[?1000l'`, false)
	_ = p.SendKeys([]string{"enter"})
	deadline := time.Now().Add(3 * time.Second)
	for p.Frame().Mouse && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if p.Frame().Mouse {
		t.Fatal("mouse mode not cleared")
	}
}
