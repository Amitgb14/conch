package phone

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// How conch web starts again: the settings' address first, else as it
// was last started (no flags counts), and not at all when neither says.
func TestGatewayArgs(t *testing.T) {
	s := OpenStore(t.TempDir())
	if args, ok := GatewayArgs("", s); ok || args != nil {
		t.Fatalf("never ran: %q %v", args, ok)
	}
	if args, ok := GatewayArgs(" https://x.ts.net ", s); !ok || args != nil {
		t.Fatalf("settings: %q %v", args, ok)
	}
	if err := s.GatewayStarted(999999, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	if args, ok := GatewayArgs("", s); !ok || args == nil || len(args) != 0 {
		t.Fatalf("no flags: %q %v", args, ok)
	}
	s.GatewayStarted(999999, []string{"-url", "https://x"}, time.Now()) //nolint:errcheck
	if args, ok := GatewayArgs("", s); !ok || !slices.Equal(args, []string{"-url", "https://x"}) {
		t.Fatalf("with flags: %q %v", args, ok)
	}
}

// The gateway is started as `exe web ARGS`, in a session of its own, its
// output to web.log in conch's folder; a start that fails is said.
func TestStartGateway(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CONCH_HOME", home)
	var got *exec.Cmd
	if err := StartGateway("/x/conch", []string{"-port", "9"}, func(c *exec.Cmd) error { got = c; return nil }); err != nil {
		t.Fatal(err)
	}
	if got == nil || !slices.Equal(got.Args, []string{"/x/conch", "web", "-port", "9"}) || !got.SysProcAttr.Setsid {
		t.Fatalf("started %+v", got)
	}
	if _, err := os.Stat(filepath.Join(home, "web.log")); err != nil {
		t.Fatalf("no web.log: %v", err)
	}
	if err := StartGateway("/x/conch", nil, func(*exec.Cmd) error { return os.ErrPermission }); err == nil {
		t.Fatal("a failed start was not an error")
	}
}
