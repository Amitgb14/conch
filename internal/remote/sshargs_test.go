package remote

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeLoginArgs(t *testing.T) {
	for _, c := range []struct {
		in   []string
		want string
	}{
		{nil, ""},
		{[]string{}, ""},
		{[]string{"-o", "KexAlgorithms=+diffie-hellman-group1-sha1"}, "-o|KexAlgorithms=+diffie-hellman-group1-sha1"},
		{[]string{"-oHostKeyAlgorithms=+ssh-rsa"}, "-oHostKeyAlgorithms=+ssh-rsa"},
		// A bare Key=Value is written as in ssh_config.
		{[]string{"PubkeyAcceptedAlgorithms=+ssh-rsa"}, "-o|PubkeyAcceptedAlgorithms=+ssh-rsa"},
		{[]string{"-p", "2222", "-i", "~/.ssh/prod key", "-A"}, "-p|2222|-i|~/.ssh/prod key|-A"},
		{[]string{"-p2222", "-vvv"}, "-p2222|-vvv"},
		{[]string{"-J", "bastion", "-o", "ProxyCommand ssh -W %h:%p jump"}, "-J|bastion|-o|ProxyCommand ssh -W %h:%p jump"},
		// -C then -p with its value in the same word.
		{[]string{"-Cp22"}, "-Cp22"},
	} {
		got, err := NormalizeLoginArgs(c.in)
		if err != nil {
			t.Fatalf("%q: %v", c.in, err)
		}
		if strings.Join(got, "|") != c.want {
			t.Fatalf("%q: got %q, want %q", c.in, got, c.want)
		}
		// Normalizing twice changes nothing: saved options go through it again.
		again, err := NormalizeLoginArgs(got)
		if err != nil || strings.Join(again, "|") != c.want {
			t.Fatalf("%q again: %q %v", c.in, again, err)
		}
	}
	for _, bad := range [][]string{
		{"box"},            // would be taken for the host
		{"-p"},             // no value
		{"-o"},             // no value
		{"-F", "/tmp/cfg"}, // conch's config is the one used
		{"-G"}, {"-V"}, {"-Q", "kex"}, {"-O", "exit"}, {"-W", "h:22"},
		{"--"}, {"-"}, {"-Z"}, {"-vZ"},
		{"=x"}, {"Ke y=x"}, // not a keyword
		{"-o", "A=b\nHost *"}, // a newline
		{"-oA=\x00"},
		{"-p", "22", "extra"},
	} {
		if got, err := NormalizeLoginArgs(bad); err == nil {
			t.Errorf("%q accepted as %q", bad, got)
		}
	}
}

func TestLoginCommandWithArgs(t *testing.T) {
	a4Env(t)
	t.Setenv("CONCH_SSH", "/fake/ssh")
	cfg, err := sshConfig()
	if err != nil {
		t.Fatal(err)
	}
	got, err := LoginCommand("prod-db", "KexAlgorithms=+diffie-hellman-group14-sha1", "-p", "2200")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/fake/ssh", "-F", cfg, "-o", "ControlPath=none", "-o", "KexAlgorithms=+diffie-hellman-group14-sha1", "-p", "2200", "--", "prod-db"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	if _, err := LoginCommand("prod-db", "other-host"); err == nil {
		t.Fatal("a second host accepted as an option")
	}
}

func TestCopyKeyCommandPicksKey(t *testing.T) {
	a4Env(t)
	t.Setenv("CONCH_SSH", "/fake/ssh")
	home := os.Getenv("HOME")
	sshDir := filepath.Join(home, ".ssh")

	// No key at all: one is made at id_ed25519.
	cmd, key, err := CopyKeyCommand("box", nil)
	if err != nil {
		t.Fatal(err)
	}
	if key != filepath.Join(sshDir, "id_ed25519.pub") {
		t.Fatalf("key %q", key)
	}
	if cmd[0] != "/bin/sh" || cmd[4] != filepath.Join(sshDir, "id_ed25519") || cmd[5] != "/fake/ssh" || cmd[len(cmd)-1] != "box" {
		t.Fatalf("command %q", cmd)
	}

	// An existing rsa key is used before making a new one; ed25519 first
	// when both are there.
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(sshDir, "id_rsa.pub"), []byte("ssh-rsa AAA me\n"), 0o600)
	if _, key, _ := CopyKeyCommand("box", nil); key != filepath.Join(sshDir, "id_rsa.pub") {
		t.Fatalf("rsa: %q", key)
	}
	os.WriteFile(filepath.Join(sshDir, "id_ed25519.pub"), []byte("ssh-ed25519 AAA me\n"), 0o600)
	if _, key, _ := CopyKeyCommand("box", nil); key != filepath.Join(sshDir, "id_ed25519.pub") {
		t.Fatalf("ed25519: %q", key)
	}
	// A directory named like a key is not a key.
	os.Remove(filepath.Join(sshDir, "id_ed25519.pub"))
	os.Mkdir(filepath.Join(sshDir, "id_ecdsa.pub"), 0o700)
	if _, key, _ := CopyKeyCommand("box", nil); key != filepath.Join(sshDir, "id_rsa.pub") {
		t.Fatalf("dir taken for a key: %q", key)
	}

	if _, _, err := CopyKeyCommand("-oProxyCommand=x", nil); err == nil {
		t.Fatal("bad host accepted")
	}
	if _, _, err := CopyKeyCommand("box", []string{"-F", "x"}); err == nil {
		t.Fatal("bad option accepted")
	}
}

// fakeSSHRunsRemote is an ssh that runs the remote command itself, in a
// "remote" home, so the copy can be checked end to end.
func fakeSSHRunsRemote(t *testing.T, remoteHome string, status int) string {
	t.Helper()
	dir := t.TempDir()
	script := `#!/bin/sh
while [ "$1" != "--" ]; do shift; done
shift; host=$1; shift
echo "fake ssh to $host" >&2
` + "[ " + `"` + "$FAKE_STATUS" + `"` + ` = 0 ] || exit 255
HOME="$REMOTE_HOME" exec /bin/sh -c "$1"
`
	p := filepath.Join(dir, "ssh")
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REMOTE_HOME", remoteHome)
	t.Setenv("FAKE_STATUS", map[bool]string{true: "0", false: "1"}[status == 0])
	return p
}

func TestCopyKeyScriptInstallsOnce(t *testing.T) {
	a4Env(t)
	remoteHome := t.TempDir()
	t.Setenv("CONCH_SSH", fakeSSHRunsRemote(t, remoteHome, 0))
	sshDir := filepath.Join(os.Getenv("HOME"), ".ssh")
	os.MkdirAll(sshDir, 0o700)
	pub := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGs me@laptop"
	os.WriteFile(filepath.Join(sshDir, "id_ed25519.pub"), []byte(pub+"\n"), 0o600)
	// Someone else's key is already there and stays.
	os.MkdirAll(filepath.Join(remoteHome, ".ssh"), 0o700)
	auth := filepath.Join(remoteHome, ".ssh", "authorized_keys")
	os.WriteFile(auth, []byte("ssh-rsa BBB other\n"), 0o600)

	run := func() string {
		cmd, _, err := CopyKeyCommand("box", []string{"-p", "22"})
		if err != nil {
			t.Fatal(err)
		}
		c := exec.Command(cmd[0], cmd[1:]...)
		c.Stdin = strings.NewReader("\n") // "press enter to close"
		out, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		return string(out)
	}
	if out := run(); !strings.Contains(out, "the key is installed") {
		t.Fatalf("output:\n%s", out)
	}
	run() // twice: the key is not added again
	b, _ := os.ReadFile(auth)
	if string(b) != "ssh-rsa BBB other\n"+pub+"\n" {
		t.Fatalf("authorized_keys:\n%s", b)
	}
	if st, _ := os.Stat(auth); st.Mode().Perm()&0o077 != 0 {
		t.Fatalf("authorized_keys mode %v", st.Mode())
	}
}

func TestCopyKeyScriptKeyWithoutNewline(t *testing.T) {
	a4Env(t)
	remoteHome := t.TempDir()
	t.Setenv("CONCH_SSH", fakeSSHRunsRemote(t, remoteHome, 0))
	sshDir := filepath.Join(os.Getenv("HOME"), ".ssh")
	os.MkdirAll(sshDir, 0o700)
	os.WriteFile(filepath.Join(sshDir, "id_ecdsa.pub"), []byte("ecdsa-sha2-nistp256 AAAE me"), 0o600) // no newline
	cmd, _, err := CopyKeyCommand("box", nil)
	if err != nil {
		t.Fatal(err)
	}
	c := exec.Command(cmd[0], cmd[1:]...)
	c.Stdin = strings.NewReader("\n")
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if b, _ := os.ReadFile(filepath.Join(remoteHome, ".ssh", "authorized_keys")); string(b) != "ecdsa-sha2-nistp256 AAAE me\n" {
		t.Fatalf("authorized_keys %q", b)
	}
}

func TestCopyKeyScriptFailure(t *testing.T) {
	a4Env(t)
	remoteHome := t.TempDir()
	t.Setenv("CONCH_SSH", fakeSSHRunsRemote(t, remoteHome, 255))
	sshDir := filepath.Join(os.Getenv("HOME"), ".ssh")
	os.MkdirAll(sshDir, 0o700)
	os.WriteFile(filepath.Join(sshDir, "id_rsa.pub"), []byte("ssh-rsa AAA me\n"), 0o600)
	cmd, _, err := CopyKeyCommand("box", nil)
	if err != nil {
		t.Fatal(err)
	}
	c := exec.Command(cmd[0], cmd[1:]...)
	c.Stdin = strings.NewReader("\n")
	out, err := c.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "copying the key failed") {
		t.Fatalf("err %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(remoteHome, ".ssh")); err == nil {
		t.Fatal("remote touched although ssh failed")
	}
}

func TestCopyKeyScriptMakesKey(t *testing.T) {
	a4Env(t)
	remoteHome := t.TempDir()
	t.Setenv("CONCH_SSH", fakeSSHRunsRemote(t, remoteHome, 0))
	// A fake ssh-keygen on PATH writes the pair it is asked for.
	bin := t.TempDir()
	keygen := `#!/bin/sh
while [ "$1" != "-f" ]; do shift; done
echo private > "$2"; echo "ssh-ed25519 NEWKEY made" > "$2.pub"
`
	os.WriteFile(filepath.Join(bin, "ssh-keygen"), []byte(keygen), 0o755)
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))

	cmd, key, err := CopyKeyCommand("box", nil)
	if err != nil {
		t.Fatal(err)
	}
	c := exec.Command(cmd[0], cmd[1:]...)
	c.Stdin = strings.NewReader("\n")
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if b, _ := os.ReadFile(key); string(b) != "ssh-ed25519 NEWKEY made\n" {
		t.Fatalf("key %q", b)
	}
	if st, err := os.Stat(filepath.Dir(key)); err != nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("~/.ssh: %v %v", st, err)
	}
	if b, _ := os.ReadFile(filepath.Join(remoteHome, ".ssh", "authorized_keys")); string(b) != "ssh-ed25519 NEWKEY made\n" {
		t.Fatalf("authorized_keys %q", b)
	}
}
