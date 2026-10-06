package remote

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// conch's connections to a machine deflate what they carry, because a
// frame is the whole screen as styled text: 4.3 MB of real frame traffic
// crossed a LAN as 54 KB, and faster in wall clock even there.
//
// It is in the ssh config rather than on the bridge's command line, and
// that is the whole point of these tests. Compression belongs to a
// connection; conch shares connections (ControlMaster auto); so `ssh -C`
// on a session riding a master an earlier probe opened is ignored —
// silently, which is how it shipped once.

func TestCompressionIsOnTheConnection(t *testing.T) {
	isolateCompress(t)
	cfg, err := sshConfig()
	if err != nil {
		t.Fatal(err)
	}
	text := readFile(t, cfg)
	if !strings.Contains(text, "Compression yes") {
		t.Fatalf("the shared config does not compress:\n%s", text)
	}
	// It belongs with the sharing it has to survive.
	if !strings.Contains(text, "ControlMaster auto") {
		t.Fatalf("the shared config no longer shares; this test is about the two together:\n%s", text)
	}
	// And not on the command line, where sharing would swallow it.
	tr, ok := SSH("busybox", false).(*sshTransport)
	if !ok {
		t.Fatal("ssh transport")
	}
	cmd, err := tr.forBridge().(*sshTransport).Command(t.Context(), "echo hi")
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range cmd.Args {
		if a == "-C" {
			t.Errorf("the bridge asks for -C on its command line, which a shared master ignores: %v", cmd.Args)
		}
	}
}

// TestCompressionCanBeTurnedOff: on a link as fast as the processor the
// sums come out the other way, so config.toml can say no. It is read when
// the config is written, so it applies to connections made after that.
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
	resetConfig()
	cfg, err := sshConfig()
	if err != nil {
		t.Fatal(err)
	}
	if text := readFile(t, cfg); strings.Contains(text, "Compression yes") {
		t.Fatalf("it compressed anyway:\n%s", text)
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

// TestLoginIsNotCompressed: an interactive login is a person at a terminal,
// makes its own connection, and gains nothing from deflating keystrokes.
func TestLoginIsNotCompressed(t *testing.T) {
	isolateCompress(t)
	cfg, err := loginSSHConfig()
	if err != nil {
		t.Fatal(err)
	}
	if text := readFile(t, cfg); strings.Contains(text, "Compression yes") {
		t.Errorf("a login config compresses:\n%s", text)
	}
}

func isolateCompress(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("CONCH_HOME", dir)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CONCH_SSH", "")
	t.Setenv("CONCH_SSH_CONFIG", "")
	resetConfig()
	t.Cleanup(resetConfig)
	return dir
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
