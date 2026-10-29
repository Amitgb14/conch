package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/remote"
)

func TestServerDial(t *testing.T) {
	for _, err := range []error{
		&net.OpError{Op: "dial", Err: os.NewSyscallError("connect", syscall.ENOENT)},
		&net.OpError{Op: "dial", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)},
	} {
		if got := serverDial(err); !errors.Is(got, errServerDown) {
			t.Errorf("%v: %v", err, got)
		}
	}
	// Anything else is not "not running": a socket that is somebody
	// else's, a server stuck mid-reload. Saying run conch would clear it.
	other := &net.OpError{Op: "dial", Err: os.NewSyscallError("connect", syscall.EACCES)}
	if got := serverDial(other); got != other {
		t.Errorf("EACCES became %v", got)
	}
	if got := downTo("stop", errServerDown); got.Error() != "the conch server is not running, so there is nothing to stop" {
		t.Errorf("downTo: %v", got)
	}
	if got := downTo("stop", other); got != other {
		t.Errorf("downTo another error: %v", got)
	}
}

func TestUnknownSubcommand(t *testing.T) {
	for _, tc := range []struct{ cmd, word, want string }{
		{"branch", "comit", `unknown branch subcommand "comit" — did you mean conch branch commit?; conch branch -h lists them`},
		{"project", "lst", `— did you mean conch project ls?`},
		{"project", "remove", `unknown project subcommand "remove"; conch project -h lists them`},
		// sandbox's own are under -provider; a typo of one is still found.
		{"sandbox", "strat", `did you mean conch sandbox start?`},
		{"server", "restart", `unknown server subcommand "restart"; conch server -h lists them`},
		// A command with none listed still says where to look.
		{"nosuch", "x", `unknown nosuch subcommand "x"; conch nosuch -h lists them`},
	} {
		if got := unknownSubcommand(tc.cmd, tc.word).Error(); !strings.Contains(got, tc.want) {
			t.Errorf("%s %s: %q, want %q", tc.cmd, tc.word, got, tc.want)
		}
	}
}

func TestNotFound(t *testing.T) {
	base := errors.New(`no pane "x"`)
	err := notFound(base, "conch status")
	if err.Error() != `no pane "x"; conch status lists them` || !errors.Is(err, base) {
		t.Errorf("%v", err)
	}
}

func TestErrorText(t *testing.T) {
	scope := proto.Errorf(proto.ErrOutOfScope, "the claude agent in p1 may not close p4")
	for _, tc := range []struct {
		cmd  string
		err  error
		want string
	}{
		{"send", errors.New("usage: conch send [-keys] ID TEXT..."), "usage: conch send [-keys] ID TEXT...\n  conch send -h explains it"},
		{"ls", errors.New("usage: conch status"), "usage: conch status\n  conch status -h explains it"}, // by its listed name
		{"bridge", errors.New("usage: conch bridge"), "usage: conch bridge"},                            // no help to point at
		{"branch", proto.Errorf(proto.ErrBadRequest, "nothing to commit"), "nothing to commit"},
		{"branch", proto.Errorf(proto.ErrInternal, "disk full"), "disk full"},
		// Codes somebody reads stay: an agent is taught out_of_scope.
		{"close", scope, scope.Error()},
		{"read", proto.Errorf(proto.ErrNotFound, "no pane"), "not_found: no pane"},
		// Wrapped, the code is part of a sentence that says more.
		{"task", fmt.Errorf("p5 started, but naming it failed: %w", proto.Errorf(proto.ErrBadRequest, "x")), "p5 started, but naming it failed: bad_request: x"},
		{"status", errors.New("plain"), "plain"},
	} {
		if got := errorText(tc.cmd, tc.err); got != tc.want {
			t.Errorf("%s %v:\n%q\nwant\n%q", tc.cmd, tc.err, got, tc.want)
		}
	}
}

// TestErrorsSayWhatNext: what somebody sees, through main.
func TestErrorsSayWhatNext(t *testing.T) {
	a4Env(t)
	for _, tc := range []struct {
		args []string
		want string
	}{
		// Not "dial unix …/conch.sock: connect: no such file or directory".
		{[]string{"read", "p1"}, "conch: the conch server is not running; run conch to start it"},
		{[]string{"close", "p1"}, "conch: the conch server is not running; run conch to start it"},
		{[]string{"wait", "p1"}, "conch: the conch server is not running; run conch to start it"},
		{[]string{"project", "frob"}, `unknown project subcommand "frob"; conch project -h lists them`},
		{[]string{"branch", "comit"}, "did you mean conch branch commit?"},
		{[]string{"send", "p1"}, "conch: usage: conch send [-keys] ID TEXT...\n  conch send -h explains it\n"},
	} {
		code, _, errOut := a4RunMain(t, "", tc.args...)
		if code != 1 || !strings.Contains(errOut, tc.want) || strings.Contains(errOut, "dial unix") {
			t.Errorf("%v: %d %q\nwant %q", tc.args, code, errOut, tc.want)
		}
	}
	if _, err := os.Stat(config.SocketPath()); err == nil {
		t.Fatal("an error about the server started one")
	}
}

// TestUnknownMachineSaysSo: a name conch has no machine by is tried as an
// ssh host, and ssh's own failure did not say that was what happened.
func TestUnknownMachineSaysSo(t *testing.T) {
	a4Env(t)
	t.Setenv("CONCH_SSH", filepath.Join(t.TempDir(), "no-such-ssh")) // every attempt fails
	_, err := connectMachine("nosuch")
	if err == nil || !strings.Contains(err.Error(), "nosuch is not a machine conch has (conch machine ls lists them)") {
		t.Fatalf("unknown: %v", err)
	}
	// A machine conch has is not said to be unknown when it cannot be
	// reached.
	if _, err := remote.SaveMachine(remote.Machine{Label: "box", Target: "dev@box"}); err != nil {
		t.Fatal(err)
	}
	if _, err := connectMachine("box"); err == nil || strings.Contains(err.Error(), "not a machine conch has") {
		t.Fatalf("saved: %v", err)
	}
}
