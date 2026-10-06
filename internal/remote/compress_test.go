package remote

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The connection that carries a machine's panes is compressed, and only
// that one. A frame is a screenful of styled text that deflates to a tenth
// or less; the tens of microseconds it costs buy milliseconds back on any
// network. Nothing local goes near it, and the connections that copy a
// binary or ask a question gain nothing and pay for nothing.

func TestOnlyTheBridgeIsCompressed(t *testing.T) {
	isolateCompress(t)
	plain, ok := SSH("busybox", false).(*sshTransport)
	if !ok {
		t.Fatal("ssh transport")
	}
	if args := sshArgsOf(t, plain); contains(args, "-C") {
		t.Errorf("an ordinary ssh command is compressed: %v", args)
	}
	bridge, ok := plain.forBridge().(*sshTransport)
	if !ok {
		t.Fatal("bridge transport")
	}
	if args := sshArgsOf(t, bridge); !contains(args, "-C") {
		t.Errorf("the bridge is not compressed: %v", args)
	}
	// An interactive login is a person's terminal, not the protocol.
	login, err := LoginCommand("busybox")
	if err != nil {
		t.Fatal(err)
	}
	if contains(login, "-C") {
		t.Errorf("a login is compressed: %v", login)
	}
}

// TestCompressionCanBeTurnedOff: on a link as fast as the processor the
// sums come out the other way, so config.toml can say no.
func TestCompressionCanBeTurnedOff(t *testing.T) {
	dir := isolateCompress(t)
	if !Compress() {
		t.Fatal("compression is on unless turned off")
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"),
		[]byte("[remote]\nno_compression = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if Compress() {
		t.Fatal("no_compression was ignored")
	}
	bridge := SSH("busybox", false).(*sshTransport).forBridge().(*sshTransport)
	if args := sshArgsOf(t, bridge); contains(args, "-C") {
		t.Errorf("it compressed anyway: %v", args)
	}
	// A config that cannot be read leaves it on: the setting is the
	// exception, not the rule.
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("this is not toml"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !Compress() {
		t.Error("an unreadable config turned compression off")
	}
}

func isolateCompress(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("CONCH_HOME", dir)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CONCH_SSH", "")
	t.Setenv("CONCH_SSH_CONFIG", "")
	return dir
}

func sshArgsOf(t *testing.T, tr *sshTransport) []string {
	t.Helper()
	cmd, err := tr.Command(context.Background(), "echo hi")
	if err != nil {
		t.Fatal(err)
	}
	return cmd.Args
}

func contains(args []string, want string) bool {
	for _, a := range args {
		if a == want || strings.HasPrefix(a, want+" ") {
			return true
		}
	}
	return false
}
