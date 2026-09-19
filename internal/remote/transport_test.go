package remote

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestA4ExecTransportCommand(t *testing.T) {
	a4Env(t)
	tr := Exec("sandbox fix-login", "container", "exec", "-i", "vm1", "sh", "-c")
	if tr.Describe() != "sandbox fix-login" {
		t.Fatalf("describe %q", tr.Describe())
	}
	cmd, err := tr.Command(context.Background(), "uname -s")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(cmd.Args, " "); got != "container exec -i vm1 sh -c uname -s" {
		t.Fatalf("argv %q", got)
	}
	// The script is one argument, however many words it holds.
	if cmd.Args[len(cmd.Args)-1] != "uname -s" {
		t.Fatalf("script split: %q", cmd.Args)
	}
	// Without a name, the command itself names the machine.
	if d := Exec("", "podman", "exec", "box").Describe(); d != "podman exec box" {
		t.Fatalf("fallback describe %q", d)
	}
	if _, err := Exec("nothing").Command(context.Background(), "x"); err == nil {
		t.Fatal("empty argv accepted")
	}
	// Nothing about it prompts, and the bridge runs it unchanged.
	if tr.interactive() {
		t.Fatal("exec transports don't prompt")
	}
	if tr.forBridge() != tr {
		t.Fatal("exec transport changed for the bridge")
	}
}

func TestA4ExecTransportRunScript(t *testing.T) {
	a4Env(t)
	tr := Exec("local", "/bin/sh", "-c")
	out, err := runScript(context.Background(), tr, "cat; echo ' seen'", []byte("payload"))
	if err != nil || strings.TrimSpace(string(out)) != "payload seen" {
		t.Fatalf("stdin and stdout: %q %v", out, err)
	}
	// Only the last three lines of a long complaint are kept.
	_, err = runScript(context.Background(), tr, "for i in 1 2 3 4 5; do echo line$i >&2; done; exit 2", nil)
	if err == nil || strings.Contains(err.Error(), "line2") || !strings.Contains(err.Error(), "line5") {
		t.Fatalf("stderr tail: %v", err)
	}
	// A failure with nothing to say keeps the exit status.
	if _, err := runScript(context.Background(), tr, "exit 7", nil); err == nil || !strings.Contains(err.Error(), "exit status 7") {
		t.Fatalf("silent failure: %v", err)
	}
}

func TestA4SSHTransport(t *testing.T) {
	a4Env(t)
	tr := SSH("dev@box", true)
	if tr.Describe() != "dev@box" || !tr.interactive() {
		t.Fatalf("interactive ssh: %q %v", tr.Describe(), tr.interactive())
	}
	cmd, err := tr.Command(context.Background(), "uname -s")
	if err != nil {
		t.Fatal(err)
	}
	argv := strings.Join(cmd.Args, " ")
	if !strings.HasSuffix(argv, "-- dev@box uname -s") {
		t.Fatalf("argv %q", argv)
	}
	if strings.Contains(argv, "BatchMode=yes") {
		t.Fatalf("interactive run refused prompts: %q", argv)
	}

	// The bridge's stdin and stdout are the protocol, so it never prompts.
	bridge := tr.forBridge()
	if bridge.interactive() {
		t.Fatal("the bridge transport prompts")
	}
	cmd, _ = bridge.Command(context.Background(), "conch bridge")
	if !strings.Contains(strings.Join(cmd.Args, " "), "BatchMode=yes") {
		t.Fatalf("bridge argv %q", cmd.Args)
	}
	// A non-interactive transport fails rather than waiting for a person.
	cmd, _ = SSH("box", false).Command(context.Background(), "x")
	if !strings.Contains(strings.Join(cmd.Args, " "), "BatchMode=yes") {
		t.Fatalf("background argv %q", cmd.Args)
	}
}

func TestA4SSHTransportWithPassword(t *testing.T) {
	a4Env(t)
	tr := SSH("dev@box", true).(*sshTransport)
	withPW, done, err := tr.withPassword("hunter2")
	if err != nil {
		t.Fatal(err)
	}
	// The original is untouched, and the copy answers prompts itself
	// instead of waiting for the terminal.
	if tr.opts.askpass != nil {
		t.Fatal("the original transport took the password")
	}
	if withPW.interactive() {
		t.Fatal("an askpass transport still waits for the terminal")
	}
	cmd, err := withPW.Command(context.Background(), "x")
	if err != nil {
		t.Fatal(err)
	}
	argv, env := strings.Join(cmd.Args, " "), strings.Join(cmd.Env, " ")
	if !strings.Contains(argv, "NumberOfPasswordPrompts=1") || !strings.Contains(env, "SSH_ASKPASS=") {
		t.Fatalf("askpass run: %q %q", argv, env)
	}
	// The helper, and the password in it, go away afterwards.
	dir := withPW.(*sshTransport).opts.askpass.dir
	if _, err := os.Stat(filepath.Join(dir, "password")); err != nil {
		t.Fatalf("no password file: %v", err)
	}
	done()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("askpass helper left behind: %v", err)
	}
	// Only ssh can take a password.
	if _, err := Connect(context.Background(), Exec("vm", "/bin/sh", "-c"), Options{Password: "x"}); err == nil ||
		!strings.Contains(err.Error(), "does not take a password") {
		t.Fatalf("password on an exec machine: %v", err)
	}
}
