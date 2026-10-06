package remote

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/sandbox"
)

// commandProvider is a provider reached through a program of its own that
// wraps ssh (Devin's shape): neither a token nor a key is asked for.
type commandProvider struct {
	fakeProvider
	argv   []string
	cmdErr error
	asked  []string
}

func (c *commandProvider) Name() string { return "devin" }

func (c *commandProvider) SSHCommand(id string) ([]string, error) {
	c.asked = append(c.asked, id)
	return c.argv, c.cmdErr
}

// fakeDevinCLI is `devin ssh ID [ssh args…]`: it logs its argv, takes the
// script out of RemoteCommand as ssh on the other side would, and hands it
// to the fake ssh, which plays the machine.
const fakeDevinCLI = `#!/bin/sh
for a in "$@"; do printf '%s\n' "$a"; done >> "$A4_DEVIN_LOG"
printf '===\n' >> "$A4_DEVIN_LOG"
[ "$1" = ssh ] || { echo "devin: not ssh" >&2; exit 2; }
if [ -n "$A4_DEVIN_FAIL" ]; then printf 'debug1: Connecting to ssh.devin.ai\n%s\n' "$A4_DEVIN_FAIL" >&2; exit "${A4_DEVIN_EXIT:-255}"; fi
cmd=
for a in "$@"; do case "$a" in RemoteCommand=*) cmd=${a#RemoteCommand=} ;; esac; done
b64=$(printf '%s' "$cmd" | sed -n 's/.*echo \([A-Za-z0-9+/=]*\) | base64 -d.*/\1/p')
script=$(printf '%s' "$b64" | base64 -d)
exec "$CONCH_SSH" -- "devin-box" "$script"
`

func newFakeDevinCLI(t *testing.T) (path, log string) {
	t.Helper()
	dir := t.TempDir()
	path, log = filepath.Join(dir, "devin"), filepath.Join(dir, "devin.log")
	if err := os.WriteFile(path, []byte(fakeDevinCLI), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("A4_DEVIN_LOG", log)
	t.Setenv("A4_DEVIN_FAIL", "")
	t.Setenv("A4_DEVIN_EXIT", "")
	return path, log
}

// A sandbox reached through its provider's own CLI is connected to, set
// up and bridged through it, and conch's own ssh is never run directly.
func TestConnectToASandboxThroughItsCLI(t *testing.T) {
	a4Env(t)
	f := newFakeSSH(t)
	f.setProbe(t, currentProbe("linux/amd64"))
	srv := startFakeServer(t, proto.HelloResult{Version: proto.Version, Protocol: proto.ProtocolVersion, Capabilities: proto.Capabilities, PID: 4242, Platform: "linux/amd64"})
	t.Setenv("A4_BRIDGE_SOCK", srv.sock)
	cli, log := newFakeDevinCLI(t)
	p := &commandProvider{fakeProvider: fakeProvider{state: sandbox.StateStarted}, argv: []string{cli, "ssh", "devin-abc"}}
	useNamedProvider(t, "devin", p, nil)

	tr, err := TransportFor(context.Background(), "web-fix", "devin:devin-abc", false)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Describe() != "web-fix" || p.accesses != 0 || strings.Join(p.asked, ",") != "devin-abc" {
		t.Fatalf("transport %q, %d accesses, asked %v", tr.Describe(), p.accesses, p.asked)
	}
	c, err := Connect(context.Background(), tr, Options{})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	c.Close()
	b, _ := os.ReadFile(log)
	runs := strings.Split(strings.TrimSuffix(string(b), "===\n"), "===\n")
	if len(runs) < 2 {
		t.Fatalf("the CLI ran %d times:\n%s", len(runs), b)
	}
	for _, run := range runs { // probe and bridge alike
		args := strings.Split(strings.TrimSpace(run), "\n")
		if len(args) < 7 || strings.Join(args[:5], " ") != "ssh devin-abc -T -o ConnectTimeout=15" ||
			args[5] != "-o" || !strings.HasPrefix(args[6], "RemoteCommand=exec sh -c ") {
			t.Fatalf("argv: %q", args)
		}
	}
}

// The script reaches the other side whole: quotes, $, %, newlines and the
// stdin it reads, which a pipe into sh would have taken.
func TestWrapRemoteRunsTheScriptAsItIs(t *testing.T) {
	script := "printf '%s|' \"it's\" '$HOME' \"100%\"\nread line\necho \"got:$line\"\n"
	cmd := exec.Command("/bin/sh", "-c", wrapRemote(script))
	cmd.Stdin = strings.NewReader("hello\n")
	out, err := cmd.CombinedOutput()
	if err != nil || string(out) != "it's|$HOME|100%|got:hello\n" {
		t.Fatalf("ran %q %v", out, err)
	}
	if w := wrapRemote(script); strings.ContainsAny(w, "%\n'") {
		t.Fatalf("the option carries what ssh would read: %q", w)
	}
	if out, err := exec.Command("/bin/sh", "-c", wrapRemote("")).CombinedOutput(); err != nil || len(out) != 0 {
		t.Fatalf("empty: %q %v", out, err)
	}
}

func TestCommandTransportFailures(t *testing.T) {
	a4Env(t)
	cli, _ := newFakeDevinCLI(t)
	tr := &commandSSHTransport{argv: []string{cli, "ssh", "devin-abc"}, name: "web-fix"}

	// ssh's 255 is a connection that failed, worth trying again; the
	// wrapper's debug lines are not what anybody needs to read.
	t.Setenv("A4_DEVIN_FAIL", "Connection closed by 10.0.0.1 port 22")
	_, err := runScript(context.Background(), tr, "true", nil)
	var conn *ConnectionError
	if !errors.As(err, &conn) || strings.Contains(err.Error(), "debug1") || !strings.Contains(err.Error(), "Connection closed") {
		t.Fatalf("255: %v", err)
	}
	// Anything else is the wrapper's own complaint, naming the machine.
	t.Setenv("A4_DEVIN_FAIL", "Error: not logged in — run `devin auth login` first")
	t.Setenv("A4_DEVIN_EXIT", "1")
	_, err = runScript(context.Background(), tr, "true", nil)
	if errors.As(err, &conn) || err == nil || err.Error() != "web-fix: Error: not logged in — run `devin auth login` first" {
		t.Fatalf("not logged in: %v", err)
	}
	// Silence keeps the exit status, and only the last three lines are kept.
	if got := tr.failed(errors.New("exit status 1"), "debug1: a\n\n"); got.Error() != "exit status 1" {
		t.Fatalf("silent: %v", got)
	}
	if got := tr.failed(errors.New("x"), "1\n2\n3\n4\ndebug1: 5\n"); got.Error() != "web-fix: 2\n3\n4" {
		t.Fatalf("long: %q", got)
	}
	if _, err := (&commandSSHTransport{name: "x"}).Command(context.Background(), "true"); err == nil {
		t.Fatal("no command, no error")
	}
	if tr.interactive() || tr.forBridge() != tr {
		t.Fatal("a wrapper prompts nobody, and bridges as it is")
	}

	// A provider whose CLI can't be found says so, before anything runs.
	useNamedProvider(t, "devin", &commandProvider{fakeProvider: fakeProvider{state: sandbox.StateStarted},
		cmdErr: errors.New("the devin CLI is needed")}, nil)
	if _, err := TransportFor(context.Background(), "web-fix", "devin:devin-abc", false); err == nil || !strings.Contains(err.Error(), "devin CLI") {
		t.Fatalf("no CLI: %v", err)
	}
	// A sleeping session is stopped, and the CLI is never asked.
	p := &commandProvider{fakeProvider: fakeProvider{state: sandbox.StateStopped}}
	useNamedProvider(t, "devin", p, nil)
	var stopped *SandboxStoppedError
	if _, err := TransportFor(context.Background(), "web-fix", "devin:devin-abc", false); !errors.As(err, &stopped) || len(p.asked) != 0 {
		t.Fatalf("asleep: %v %v", err, p.asked)
	}
}

func TestSandboxShellThroughItsCLI(t *testing.T) {
	a4Env(t)
	useNamedProvider(t, "devin", &commandProvider{fakeProvider: fakeProvider{state: sandbox.StateStarted},
		argv: []string{"/fake/devin", "ssh", "devin-abc"}}, nil)
	ctx := context.Background()

	cmd, err := SandboxShell(ctx, "web-fix", "devin:devin-abc", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(cmd.Args, " "); got != "/fake/devin ssh devin-abc" {
		t.Fatalf("shell: %s", got)
	}
	if cmd.Cancel != nil {
		t.Fatal("a shell must outlive the context used to ask for access")
	}
	cmd, err = SandboxShell(ctx, "web-fix", "devin:devin-abc", "htop", true)
	if err != nil {
		t.Fatal(err)
	}
	if args := cmd.Args; len(args) != 6 || strings.Join(args[:5], " ") != "/fake/devin ssh devin-abc -t -o" ||
		args[5] != "RemoteCommand="+wrapRemote("htop") {
		t.Fatalf("command: %q", args)
	}
	// A command run with no terminal asks for none.
	cmd, _ = SandboxShell(ctx, "web-fix", "devin:devin-abc", "uptime", false)
	if got := strings.Join(cmd.Args, " "); strings.Contains(got, " -t") || !strings.Contains(got, "RemoteCommand=") {
		t.Fatalf("no tty: %s", got)
	}
}
