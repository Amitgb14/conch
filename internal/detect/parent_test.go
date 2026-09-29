package detect

import (
	"os"
	"os/exec"
	"testing"
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
