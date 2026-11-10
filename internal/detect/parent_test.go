package detect

import (
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
)

func TestStatParent(t *testing.T) {
	for stat, want := range map[string]int{
		"4242 (claude) S 17 4242 4242 0 -1":            17,
		"4242 (my (odd) name) R 99 1 1":                99,
		"4242 (with space) S 1 0":                      1,
		"4242 ()) S 5 1":                               5,
		"1 (init) S 0 1 1 0 -1 4194560 12 0 0 0 0 0 0": 0,
	} {
		if got, err := statParent([]byte(stat)); err != nil || got != want {
			t.Errorf("%q: %d %v, want %d", stat, got, err, want)
		}
	}
	for _, bad := range []string{"", "4242 claude S 17", "4242 (claude)", "4242 (claude) S", "4242 (claude) S x"} {
		if _, err := statParent([]byte(bad)); err == nil {
			t.Errorf("%q: no error", bad)
		}
	}
}

func TestParentPID(t *testing.T) {
	if got, err := ParentPID(os.Getpid()); err != nil || got != os.Getppid() {
		t.Fatalf("own parent: %d %v, want %d", got, err, os.Getppid())
	}
	cmd := exec.Command("/bin/sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	child := cmd.Process.Pid
	if got, err := ParentPID(child); err != nil || got != os.Getpid() {
		t.Fatalf("child's parent: %d %v", got, err)
	}
	cmd.Process.Kill()
	cmd.Wait()
	// A process that is gone has no parent to give.
	if got, err := ParentPID(child); err == nil {
		t.Fatalf("gone: %d", got)
	}
	if _, err := ParentPID(-1); err == nil {
		t.Fatal("pid -1")
	}
}

func TestStatTPGID(t *testing.T) {
	stat := "4242 (my (odd) name) S 17 4242 4242 34816 4300 4194560"
	if got, err := statTPGID([]byte(stat)); err != nil || got != 4300 {
		t.Fatalf("tpgid: %d %v, want 4300", got, err)
	}
	if got, err := statTPGID([]byte("4242 (bash) S 1 4242 4242 0 -1 0")); err != nil || got != -1 {
		t.Fatalf("no terminal: %d %v, want -1", got, err)
	}
	for _, bad := range []string{"", "4242 (bash) S 1 4242 4242 0", "4242 (bash) S 1 2 3 4 x"} {
		if _, err := statTPGID([]byte(bad)); err == nil {
			t.Errorf("%q: no error", bad)
		}
	}
}

// TestTerminalForeground: a program alone on a terminal is in front of it;
// one with no terminal has nothing in front.
func TestTerminalForeground(t *testing.T) {
	cmd := exec.Command("/bin/sleep", "30")
	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer ptmx.Close()
	defer cmd.Process.Kill()
	var fg Process
	for i := 0; i < 100; i++ { // until the exec has happened
		if fg, err = TerminalForeground(cmd.Process.Pid); err == nil && fg.Name == "sleep" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil || fg.Name != "sleep" || fg.PID != cmd.Process.Pid {
		t.Fatalf("foreground: %+v %v, want sleep %d", fg, err, cmd.Process.Pid)
	}

	bare := exec.Command("/bin/sleep", "30")
	bare.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := bare.Start(); err != nil {
		t.Fatal(err)
	}
	defer bare.Process.Kill()
	if fg, err := TerminalForeground(bare.Process.Pid); err == nil {
		t.Fatalf("no terminal: %+v, want an error", fg)
	}
	if _, err := TerminalForeground(1 << 22); err == nil {
		t.Fatal("no such process: want an error")
	}
}
