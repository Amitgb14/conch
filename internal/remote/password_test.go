package remote

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/proto"
)

func TestAskpassHelper(t *testing.T) {
	a4Env(t)
	a, err := newAskpass("pa ss'\"$word")
	if err != nil {
		t.Fatal(err)
	}
	for name, mode := range map[string]os.FileMode{"password": 0o600, "askpass": 0o700} {
		st, err := os.Stat(filepath.Join(a.dir, name))
		if err != nil || st.Mode().Perm() != mode {
			t.Fatalf("%s: %v %v", name, st.Mode(), err)
		}
	}
	ask := func(prompt string) string {
		out, err := exec.Command(a.script(), prompt).Output()
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}
	if got := ask("dev@box's password: "); got != "pa ss'\"$word" {
		t.Fatalf("password prompt: %q", got)
	}
	for _, p := range []string{"Are you sure you want to continue connecting (yes/no/[fingerprint])? ", "Please type 'yes', 'no' or the fingerprint: "} {
		if got := strings.TrimSpace(ask(p)); got != "no" {
			t.Fatalf("%q answered %q", p, got)
		}
	}

	t.Setenv("SSH_ASKPASS", "/usr/bin/other")
	t.Setenv("DISPLAY", "")
	env := a.env()
	if !slices.Contains(env, "SSH_ASKPASS="+a.script()) || !slices.Contains(env, "SSH_ASKPASS_REQUIRE=force") ||
		slices.Contains(env, "SSH_ASKPASS=/usr/bin/other") || !slices.Contains(env, "DISPLAY=conch") {
		t.Fatalf("env: %v", env)
	}
	t.Setenv("DISPLAY", ":1")
	if slices.Contains(a.env(), "DISPLAY=conch") {
		t.Fatal("replaced a real DISPLAY")
	}

	a.close()
	if _, err := os.Stat(a.dir); !os.IsNotExist(err) {
		t.Fatal("password left behind")
	}
}

func TestSSHCmdAuthOptions(t *testing.T) {
	a4Env(t)
	ap, _ := newAskpass("x")
	defer ap.close()
	args := func(o sshOpts) string {
		cmd, err := sshCmdWith(context.Background(), "dev@box", "true", o)
		if err != nil {
			t.Fatal(err)
		}
		return strings.Join(cmd.Args[1:], " ")
	}
	if got := args(sshOpts{askpass: ap}); !strings.Contains(got, "BatchMode=no") || !strings.Contains(got, "NumberOfPasswordPrompts=1") ||
		!strings.Contains(got, "StrictHostKeyChecking=accept-new") || strings.Contains(got, "BatchMode=yes") {
		t.Fatalf("askpass: %s", got)
	}
	if got := args(sshOpts{keyOnly: true}); !strings.Contains(got, "ControlPath=none") || !strings.Contains(got, "PasswordAuthentication=no") ||
		!strings.Contains(got, "BatchMode=yes") {
		t.Fatalf("key only: %s", got)
	}
	if got := args(sshOpts{interactive: true}); strings.Contains(got, "BatchMode") {
		t.Fatalf("interactive: %s", got)
	}
	if got := args(sshOpts{}); !strings.Contains(got, "BatchMode=yes") || strings.Contains(got, "accept-new") {
		t.Fatalf("background: %s", got)
	}
	cmd, _ := sshCmdWith(context.Background(), "dev@box", "true", sshOpts{})
	if cmd.Env != nil {
		t.Fatal("background runs set an environment")
	}
}

// passwordSSH wraps the fake ssh: it asks $SSH_ASKPASS for a password, as
// OpenSSH does, and fails like a server rejecting it unless it matches.
func passwordSSH(t *testing.T, f *fakeSSH, want string) string {
	t.Helper()
	wrapper := filepath.Join(f.dir, "ssh-password")
	script := `#!/bin/sh
case "$*" in *ControlPath=none*)
  if [ -n "$A4_KEY_FAIL" ]; then echo "dev@box: Permission denied (publickey)." >&2; exit 255; fi
  A4_STDIN_TO= exec "` + f.path + `" "$@" ;;
esac
if [ -z "$SSH_ASKPASS" ] || [ "$SSH_ASKPASS_REQUIRE" != force ]; then echo "no askpass" >&2; exit 255; fi
got=$("$SSH_ASKPASS" "dev@box's password: ")
if [ "$got" != "$A4_WANT_PW" ]; then echo "dev@box: Permission denied (password)." >&2; exit 255; fi
printf '%s\n' "$SSH_ASKPASS" >> "$A4_ASKPASS_LOG"
exec "` + f.path + `" "$@"
`
	if err := os.WriteFile(wrapper, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONCH_SSH", wrapper)
	t.Setenv("A4_WANT_PW", want)
	t.Setenv("A4_KEY_FAIL", "")
	t.Setenv("A4_ASKPASS_LOG", filepath.Join(f.dir, "askpass.log"))
	return wrapper
}

func TestConnectWithPassword(t *testing.T) {
	a4Env(t)
	f := newFakeSSH(t)
	passwordSSH(t, f, "hunter2 ok")
	hello := proto.HelloResult{Version: proto.Version, Protocol: proto.ProtocolVersion, Capabilities: proto.Capabilities, PID: 4242, Platform: "linux/amd64"}
	srv := startFakeServer(t, hello)
	t.Setenv("A4_BRIDGE_SOCK", srv.sock)
	f.setProbe(t, currentProbe("linux/amd64"))

	if _, err := Connect(context.Background(), "dev@box", Options{Password: "wrong"}); err == nil || !strings.Contains(err.Error(), "Permission denied") {
		t.Fatalf("wrong password: %v", err)
	}
	c, err := Connect(context.Background(), "dev@box", Options{Password: "hunter2 ok"})
	if err != nil {
		t.Fatal(err)
	}
	c.Close()

	// The password never appears in an argument, and its files are gone.
	log, _ := os.ReadFile(f.log)
	if strings.Contains(string(log), "hunter2") {
		t.Fatalf("password in ssh arguments:\n%s", log)
	}
	helpers, _ := os.ReadFile(filepath.Join(f.dir, "askpass.log"))
	lines := strings.Fields(string(helpers))
	if len(lines) < 2 { // the probe and the bridge
		t.Fatalf("askpass used %d times", len(lines))
	}
	for _, h := range lines {
		if _, err := os.Stat(filepath.Dir(h)); !os.IsNotExist(err) {
			t.Fatalf("askpass files left in %s", filepath.Dir(h))
		}
	}
	// Without a password nothing asks: the fake refuses, like BatchMode.
	if _, err := Connect(context.Background(), "dev@box", Options{}); err == nil || !strings.Contains(err.Error(), "no askpass") {
		t.Fatalf("no password: %v", err)
	}
}

func TestSetUpKeyLogin(t *testing.T) {
	a4Env(t)
	home := os.Getenv("HOME")
	t.Setenv("CONCH_SSH_KEYGEN", "/bin/false") // the user's key must be used, not a new one
	f := newFakeSSH(t)
	passwordSSH(t, f, "pw")
	stdin := filepath.Join(f.dir, "stdin.txt")
	t.Setenv("A4_STDIN_TO", stdin)
	os.MkdirAll(filepath.Join(home, ".ssh"), 0o700)
	os.WriteFile(filepath.Join(home, ".ssh", "id_rsa.pub"), []byte("not a key\n"), 0o644) // skipped
	os.WriteFile(filepath.Join(home, ".ssh", "id_ecdsa.pub"), []byte("ecdsa-sha2-nistp256 AAAAE2 me@laptop\n"), 0o644)

	path, err := SetUpKeyLogin(context.Background(), "dev@box", "pw")
	if err != nil || path != filepath.Join(home, ".ssh", "id_ecdsa.pub") {
		t.Fatalf("set up: %q %v", path, err)
	}
	if b, _ := os.ReadFile(stdin); string(b) != "ecdsa-sha2-nistp256 AAAAE2 me@laptop\n" {
		t.Fatalf("key sent: %q", b)
	}
	calls := f.calls(t)
	if len(calls) != 2 || !strings.Contains(calls[0][len(calls[0])-1], "authorized_keys") ||
		!slices.Contains(calls[1], "ControlPath=none") || calls[1][len(calls[1])-1] != "true" {
		t.Fatalf("calls: %q", calls)
	}

	// Key login still refused afterwards: said so, with the key's path.
	t.Setenv("A4_KEY_FAIL", "1")
	path, err = SetUpKeyLogin(context.Background(), "dev@box", "pw")
	if !errors.Is(err, ErrKeyLoginUnverified) || !strings.Contains(err.Error(), "publickey") || path == "" {
		t.Fatalf("unverified: %q %v", path, err)
	}
	// A wrong password: the key isn't added.
	t.Setenv("A4_KEY_FAIL", "")
	if _, err := SetUpKeyLogin(context.Background(), "dev@box", "nope"); err == nil || !strings.Contains(err.Error(), "add the key") {
		t.Fatalf("wrong password: %v", err)
	}
}

func TestSetUpKeyLoginCreatesConchKey(t *testing.T) {
	a4Env(t)
	f := newFakeSSH(t)
	passwordSSH(t, f, "pw")
	keygen := filepath.Join(t.TempDir(), "ssh-keygen")
	os.WriteFile(keygen, []byte(`#!/bin/sh
for a in "$@"; do last=$a; done
printf 'private' > "$last"
printf 'ssh-ed25519 AAAAC3 conch@test\n' > "$last.pub"
`), 0o755)
	t.Setenv("CONCH_SSH_KEYGEN", keygen)

	cfg, _ := sshConfig() // generated before the key exists
	if b, _ := os.ReadFile(cfg); strings.Contains(string(b), "IdentityFile") {
		t.Fatal("config names a key that doesn't exist")
	}
	path, err := SetUpKeyLogin(context.Background(), "dev@box", "pw")
	if err != nil || path != conchKey()+".pub" {
		t.Fatalf("conch key: %q %v", path, err)
	}
	cfg, _ = sshConfig()
	if b, _ := os.ReadFile(cfg); !strings.Contains(string(b), "IdentityFile "+conchKey()) {
		t.Fatalf("config doesn't use the new key:\n%s", b)
	}
	// A second machine reuses the key.
	os.Remove(keygen)
	if _, err := SetUpKeyLogin(context.Background(), "dev@box", "pw"); err != nil {
		t.Fatalf("reuse: %v", err)
	}

	// ssh-keygen failing is reported.
	a4Env(t)
	passwordSSH(t, f, "pw")
	t.Setenv("CONCH_SSH_KEYGEN", "/bin/false")
	if _, err := SetUpKeyLogin(context.Background(), "dev@box", "pw"); err == nil || !strings.Contains(err.Error(), "create an ssh key") {
		t.Fatalf("keygen failure: %v", err)
	}
}

func TestValidKeyLine(t *testing.T) {
	for s, want := range map[string]bool{
		"ssh-ed25519 AAAA me":          true,
		"ecdsa-sha2-nistp256 AAAA":     true,
		"sk-ssh-ed25519@openssh.com A": true,
		"ssh-rsa":                      false,
		"":                             false,
		"-----BEGIN OPENSSH":           false,
	} {
		if validKeyLine(s) != want {
			t.Errorf("%q: %v", s, !want)
		}
	}
}
