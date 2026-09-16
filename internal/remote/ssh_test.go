package remote

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

func TestA4SSHBinary(t *testing.T) {
	t.Setenv("CONCH_SSH", "")
	if got := sshBinary(); got != "ssh" {
		t.Fatalf("default ssh binary %q", got)
	}
	t.Setenv("CONCH_SSH", "/opt/fake/ssh")
	if got := sshBinary(); got != "/opt/fake/ssh" {
		t.Fatalf("override %q", got)
	}
}

func TestA4QuoteConfig(t *testing.T) {
	for in, want := range map[string]string{
		"/plain/path":       "/plain/path",
		"/with space/cfg":   `"/with space/cfg"`,
		"/tab\there":        "\"/tab\there\"",
		`/quote"d`:          `"/quote\"d"`,
		"":                  "",
		"/a b/\"c\"/config": `"/a b/\"c\"/config"`,
	} {
		if got := quoteConfig(in); got != want {
			t.Errorf("quoteConfig(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestA4SSHConfigContents(t *testing.T) {
	a4Env(t)
	home := os.Getenv("HOME")
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	userCfg := filepath.Join(home, ".ssh", "config")
	if err := os.WriteFile(userCfg, []byte("Host x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONCH_SSH_CONFIG", "/etc/conch extra/ssh.conf")

	path, err := sshConfig()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(os.Getenv("CONCH_HOME"), "ssh", "config"); path != want {
		t.Fatalf("config path %q, want %q", path, want)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	extra := strings.Index(s, `Include "/etc/conch extra/ssh.conf"`)
	user := strings.Index(s, "Include "+quoteConfig(userCfg))
	if extra < 0 || user < 0 || extra > user {
		t.Fatalf("includes missing or out of order (extra before user):\n%s", s)
	}
	for _, want := range []string{"ServerAliveInterval 15", "ControlMaster auto", "ControlPersist 60", "/%C"} {
		if !strings.Contains(s, want) {
			t.Fatalf("config lacks %q:\n%s", want, s)
		}
	}
	ctl := filepath.Join(os.TempDir(), "conch-ssh-")
	if !strings.Contains(s, "ControlPath "+ctl) {
		t.Fatalf("control path not under TMPDIR %s:\n%s", ctl, s)
	}
	if st, err := os.Stat(path); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("config mode: %v %v", st, err)
	}

	// Cached for the process: a changed environment is not re-read.
	t.Setenv("CONCH_SSH_CONFIG", "")
	if again, err := sshConfig(); err != nil || again != path {
		t.Fatalf("second call %q %v", again, err)
	}
	if b2, _ := os.ReadFile(path); string(b2) != s {
		t.Fatal("config rewritten on second call")
	}
}

func TestA4SSHConfigNoUserConfig(t *testing.T) {
	a4Env(t)
	path, err := sshConfig()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), ".ssh/config") {
		t.Fatalf("absent user config included:\n%s", b)
	}
}

func TestA4SSHConfigUnwritableDir(t *testing.T) {
	a4Env(t)
	file := filepath.Join(t.TempDir(), "file")
	os.WriteFile(file, nil, 0o600)
	t.Setenv("CONCH_HOME", filepath.Join(file, "sub")) // parent is a file
	if _, err := sshConfig(); err == nil {
		t.Fatal("config dir under a file accepted")
	}
	if _, err := sshCmd(context.Background(), "h", "true", false); err == nil {
		t.Fatal("sshCmd without a config")
	}
	if _, err := run(context.Background(), "h", "true", nil, false); err == nil {
		t.Fatal("run without a config")
	}
	if _, err := Bridge("h", "/bin/conch"); err == nil {
		t.Fatal("Bridge without a config")
	}
}

func TestA4SSHCmdArgs(t *testing.T) {
	a4Env(t)
	t.Setenv("CONCH_SSH", "/fake/ssh")
	cfg, _ := sshConfig()

	cmd, err := sshCmd(context.Background(), "dev@box", "echo hi", false)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/fake/ssh", "-F", cfg, "-T", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10", "--", "dev@box", "echo hi"}
	if strings.Join(cmd.Args, "|") != strings.Join(want, "|") {
		t.Fatalf("batch args\n got %q\nwant %q", cmd.Args, want)
	}
	if cmd.WaitDelay != 2*time.Second {
		t.Fatalf("WaitDelay %v", cmd.WaitDelay)
	}

	cmd, _ = sshCmd(context.Background(), "-oProxyCommand=evil", "x", true)
	want = []string{"/fake/ssh", "-F", cfg, "-T", "--", "-oProxyCommand=evil", "x"}
	if strings.Join(cmd.Args, "|") != strings.Join(want, "|") {
		t.Fatalf("interactive args\n got %q\nwant %q", cmd.Args, want)
	}
}

func TestA4RunThroughFakeSSH(t *testing.T) {
	a4Env(t)
	f := newFakeSSH(t)
	stdinFile := filepath.Join(f.dir, "stdin")
	t.Setenv("A4_STDIN_TO", stdinFile)
	t.Setenv("A4_STDOUT", "remote says hi")

	out, err := run(context.Background(), "dev@box", "do-thing", []byte("payload"), false)
	if err != nil || string(out) != "remote says hi" {
		t.Fatalf("run: %q %v", out, err)
	}
	if b, _ := os.ReadFile(stdinFile); string(b) != "payload" {
		t.Fatalf("stdin reached ssh as %q", b)
	}
	calls := f.calls(t)
	if len(calls) != 1 {
		t.Fatalf("calls %q", calls)
	}
	argv := calls[0]
	if argv[len(argv)-1] != "do-thing" || argv[len(argv)-2] != "dev@box" || argv[len(argv)-3] != "--" {
		t.Fatalf("argv %q", argv)
	}
	if !strings.Contains(strings.Join(argv, " "), "BatchMode=yes") {
		t.Fatalf("background run not in batch mode: %q", argv)
	}
}

func TestA4RunErrors(t *testing.T) {
	a4Env(t)
	newFakeSSH(t)

	t.Setenv("A4_STDOUT", "partial")
	t.Setenv("A4_STDERR", "line1\nline2\nline3\nline4\nfinal words\n")
	t.Setenv("A4_EXIT", "2")
	out, err := run(context.Background(), "box", "x", nil, false)
	if err == nil || string(out) != "partial" {
		t.Fatalf("failing run: %q %v", out, err)
	}
	if err.Error() != "line3\nline4\nfinal words" {
		t.Fatalf("error keeps the last three lines: %q", err)
	}

	t.Setenv("A4_STDERR", "")
	_, err = run(context.Background(), "box", "x", nil, false)
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 2 {
		t.Fatalf("no stderr keeps the exit error: %v", err)
	}

	t.Setenv("CONCH_SSH", filepath.Join(t.TempDir(), "no-such-ssh"))
	if _, err := run(context.Background(), "box", "x", nil, false); err == nil {
		t.Fatal("missing ssh binary")
	}
}

func TestA4SSHError(t *testing.T) {
	base := errors.New("exit status 255")
	if got := sshError(base, "  \n"); got != base {
		t.Fatalf("blank stderr: %v", got)
	}
	for _, s := range []string{"dev@box: Permission denied (publickey).", "Host key verification failed."} {
		got := sshError(base, s).Error()
		if !strings.HasPrefix(got, s) || !strings.Contains(got, "conch machine add") || !strings.Contains(got, "ssh-add") {
			t.Fatalf("hint missing for %q: %q", s, got)
		}
	}
	if got := sshError(base, "ssh: connect to host box port 22: Connection refused").Error(); strings.Contains(got, "ssh-add") {
		t.Fatalf("hint on unrelated error: %q", got)
	}
}

func TestA4ProbeMachine(t *testing.T) {
	a4Env(t)
	f := newFakeSSH(t)
	f.setProbe(t, currentProbe("linux/amd64"))
	p, err := ProbeMachine(context.Background(), "dev@box", false)
	if err != nil {
		t.Fatal(err)
	}
	if p.Platform != "linux/amd64" || p.Bin != "/home/dev/.local/bin/conch" || p.Info == nil || len(p.Missing()) != 0 {
		t.Fatalf("probe %+v", p)
	}
	// The probe script spans lines, so check the logged invocation as a whole.
	if argv := strings.Join(f.calls(t)[0], "\n"); !strings.Contains(argv, "uname -s") || !strings.Contains(argv, ".local/bin/conch") {
		t.Fatalf("probe invocation %q", argv)
	}

	f.setProbe(t, "Linux\n")
	if _, err := ProbeMachine(context.Background(), "dev@box", false); err == nil || !strings.Contains(err.Error(), "unexpected probe output") {
		t.Fatalf("short probe: %v", err)
	}

	t.Setenv("A4_PROBE_FAIL", "ssh: Could not resolve hostname box")
	if _, err := ProbeMachine(context.Background(), "dev@box", false); err == nil || !strings.Contains(err.Error(), "Could not resolve") {
		t.Fatalf("probe ssh failure: %v", err)
	}
}

func TestA4ParseProbeEdges(t *testing.T) {
	p, err := parseProbe("  linux  \n amd64 \nbin=/usr/bin/conch\n{not json\n")
	if err != nil || p.Platform != "linux/amd64" || p.Bin != "/usr/bin/conch" || p.Info != nil {
		t.Fatalf("garbled json: %+v %v", p, err)
	}
	if p, err := parseProbe("Darwin\narm64\n"); err != nil || p.Platform != "darwin/arm64" {
		t.Fatalf("darwin arm64: %+v %v", p, err)
	}
	if _, err := parseProbe(""); err == nil {
		t.Fatal("empty probe accepted")
	}
}

func TestA4ErrorsAndQuoting(t *testing.T) {
	ie := &InstallError{Platform: "linux/amd64", Reason: "conch is not installed on box (linux/amd64)"}
	if ie.Error() != ie.Reason {
		t.Fatalf("InstallError %q", ie.Error())
	}
	oe := &OutdatedServerError{Missing: []string{"a.v1", "b.v1"}}
	if got := oe.Error(); !strings.Contains(got, "lacks a.v1, b.v1") || !strings.Contains(got, "restart") {
		t.Fatalf("OutdatedServerError %q", got)
	}
	for in, want := range map[string]string{
		"/usr/bin/conch":  "'/usr/bin/conch'",
		"it's":            `'it'\''s'`,
		"":                "''",
		"$(rm -rf ~) `x`": "'$(rm -rf ~) `x`'",
	} {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %q, want %q", in, got, want)
		}
		// The quoting must survive a real shell.
		out, err := exec.Command("/bin/sh", "-c", "printf %s "+shellQuote(in)).Output()
		if err != nil || string(out) != in {
			t.Errorf("sh round trip of %q: %q %v", in, out, err)
		}
	}
}

func TestA4Tail(t *testing.T) {
	tl := &tail{max: 5}
	if n, _ := tl.Write([]byte("abc")); n != 3 || tl.String() != "abc" {
		t.Fatalf("tail %q", tl.String())
	}
	if n, _ := tl.Write([]byte("defgh")); n != 5 || tl.String() != "defgh" {
		t.Fatalf("tail keeps last bytes: %q", tl.String())
	}
	tl.Write([]byte("0123456789"))
	if tl.String() != "56789" {
		t.Fatalf("oversized write: %q", tl.String())
	}
}

// otherPlatform is a supported platform that is not this computer's.
func otherPlatform() string {
	if runtime.GOOS+"/"+runtime.GOARCH == "linux/amd64" {
		return "darwin/arm64"
	}
	return "linux/amd64"
}

func TestA4InstallWithRemoteBinary(t *testing.T) {
	a4Env(t)
	f := newFakeSSH(t)
	bin := filepath.Join(t.TempDir(), "conch-linux")
	os.WriteFile(bin, []byte("ELF-ish fake binary"), 0o755)
	t.Setenv("CONCH_REMOTE_BINARY", bin)

	var steps []string
	path, err := Install(context.Background(), "dev@box", otherPlatform(), false, func(s string) { steps = append(steps, s) })
	if err != nil {
		t.Fatal(err)
	}
	if path != "/home/dev/.local/bin/conch" {
		t.Fatalf("installed path %q", path)
	}
	if b, _ := os.ReadFile(filepath.Join(f.dir, "installed.bin")); string(b) != "ELF-ish fake binary" {
		t.Fatalf("remote received %q", b)
	}
	if len(steps) != 1 || !strings.Contains(steps[0], "copying conch to dev@box") {
		t.Fatalf("steps %q", steps)
	}
	script := f.calls(t)[0]
	if s := script[len(script)-1]; !strings.Contains(s, "mv -f") || !strings.Contains(s, "chmod 755") {
		t.Fatalf("install script %q", s)
	}

	// A nil progress func is fine.
	if _, err := Install(context.Background(), "dev@box", otherPlatform(), false, nil); err != nil {
		t.Fatal(err)
	}

	t.Setenv("A4_INSTALL_FAIL", "mkdir: cannot create directory: Read-only file system")
	if _, err := Install(context.Background(), "dev@box", otherPlatform(), false, nil); err == nil || !strings.HasPrefix(err.Error(), "install conch: ") || !strings.Contains(err.Error(), "Read-only") {
		t.Fatalf("remote failure: %v", err)
	}

	t.Setenv("CONCH_REMOTE_BINARY", filepath.Join(t.TempDir(), "missing"))
	if _, err := Install(context.Background(), "dev@box", otherPlatform(), false, nil); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing binary: %v", err)
	}
}

func TestA4BinaryForSamePlatform(t *testing.T) {
	a4Env(t)
	t.Setenv("CONCH_REMOTE_BINARY", "/should/not/be/used")
	exe, _ := os.Executable()
	got, err := binaryFor(context.Background(), runtime.GOOS+"/"+runtime.GOARCH, func(string) {})
	if err != nil || got != exe {
		t.Fatalf("same platform: %q %v, want %q", got, err, exe)
	}
	got, err = binaryFor(context.Background(), otherPlatform(), func(string) {})
	if err != nil || got != "/should/not/be/used" {
		t.Fatalf("CONCH_REMOTE_BINARY: %q %v", got, err)
	}
}

func TestA4ConnectNeedsInstall(t *testing.T) {
	a4Env(t)
	f := newFakeSSH(t)

	f.setProbe(t, "Linux\nx86_64\n")
	var steps []string
	_, err := Connect(context.Background(), "dev@box", Options{Progress: func(s string) { steps = append(steps, s) }})
	var ie *InstallError
	if !errors.As(err, &ie) || ie.Platform != "linux/amd64" || !strings.Contains(ie.Reason, "not installed on dev@box") {
		t.Fatalf("not installed: %v", err)
	}
	if len(steps) != 1 || steps[0] != "probing dev@box" {
		t.Fatalf("steps %q", steps)
	}

	f.setProbe(t, "Linux\nx86_64\nbin=/usr/local/bin/conch\n"+`{"version":"0.0.1","build":"old","capabilities":["pane.v1"]}`+"\n")
	_, err = Connect(context.Background(), "dev@box", Options{})
	if !errors.As(err, &ie) || !strings.Contains(ie.Reason, "older build") || !strings.Contains(ie.Reason, "pane.frame.v1") {
		t.Fatalf("outdated binary: %v", err)
	}

	t.Setenv("A4_PROBE_FAIL", "ssh: connect to host box port 22: Operation timed out")
	if _, err := Connect(context.Background(), "dev@box", Options{}); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("probe failure: %v", err)
	}
}

func TestA4ConnectInstallsAndBridges(t *testing.T) {
	a4Env(t)
	f := newFakeSSH(t)
	hello := proto.HelloResult{Version: proto.Version, Protocol: proto.ProtocolVersion, Capabilities: proto.Capabilities, PID: 4242, Hostname: "box", Platform: "linux/amd64"}
	srv := startFakeServer(t, hello)
	t.Setenv("A4_BRIDGE_SOCK", srv.sock)
	bin := filepath.Join(t.TempDir(), "conch-other")
	os.WriteFile(bin, []byte("binary"), 0o755)
	t.Setenv("CONCH_REMOTE_BINARY", bin)
	f.setProbe(t, "Linux\nx86_64\n")
	if otherPlatform() != "linux/amd64" {
		f.setProbe(t, "Darwin\narm64\n")
	}

	var steps []string
	c, err := Connect(context.Background(), "dev@box", Options{Install: true, Progress: func(s string) { steps = append(steps, s) }})
	if err != nil {
		t.Fatal(err)
	}
	if c.Server.PID != 4242 || c.Server.Hostname != "box" {
		c.Close()
		t.Fatalf("server %+v", c.Server)
	}
	if err := c.Call(context.Background(), proto.MethodPing, nil, nil); err != nil {
		t.Fatalf("ping through bridge: %v", err)
	}
	c.Close()

	joined := strings.Join(steps, "\n")
	for _, want := range []string{"probing dev@box", "not installed", "installing", "copying conch", "connecting"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("steps lack %q: %q", want, steps)
		}
	}
	calls := f.calls(t)
	last := calls[len(calls)-1]
	if got := last[len(last)-1]; got != "'/home/dev/.local/bin/conch' bridge" {
		t.Fatalf("bridge command %q", got)
	}
}

func TestA4ConnectInstallFails(t *testing.T) {
	a4Env(t)
	f := newFakeSSH(t)
	f.setProbe(t, "Linux\nx86_64\n")
	if otherPlatform() != "linux/amd64" {
		f.setProbe(t, "Darwin\narm64\n")
	}
	t.Setenv("CONCH_REMOTE_BINARY", filepath.Join(t.TempDir(), "missing"))
	if _, err := Connect(context.Background(), "dev@box", Options{Install: true}); err == nil {
		t.Fatal("install failure ignored")
	}
}

func TestA4ConnectOutdatedServer(t *testing.T) {
	a4Env(t)
	f := newFakeSSH(t)
	f.setProbe(t, currentProbe("linux/amd64"))
	srv := startFakeServer(t, proto.HelloResult{Version: "0.0.1", Capabilities: []string{"pane.v1"}, PID: 7})
	t.Setenv("A4_BRIDGE_SOCK", srv.sock)

	c, err := Connect(context.Background(), "dev@box", Options{})
	var oe *OutdatedServerError
	if !errors.As(err, &oe) || c == nil {
		t.Fatalf("outdated server: %v %v", c, err)
	}
	defer c.Close()
	if len(oe.Missing) != len(proto.Capabilities)-1 || c.Server.PID != 7 {
		t.Fatalf("missing %v pid %d", oe.Missing, c.Server.PID)
	}
	// The probed binary was current, so nothing was installed.
	for _, argv := range f.calls(t) {
		if strings.Contains(argv[len(argv)-1], "conch.new") {
			t.Fatal("installed despite a current binary")
		}
	}
}

func TestA4BridgeFailureReportsStderr(t *testing.T) {
	a4Env(t)
	newFakeSSH(t)
	t.Setenv("A4_BRIDGE_MODE", "fail")
	_, err := Bridge("dev@box", "/home/dev/.local/bin/conch")
	if err == nil || !strings.Contains(err.Error(), "Permission denied") || !strings.Contains(err.Error(), "ssh-add") {
		t.Fatalf("bridge failure: %v", err)
	}
}

func TestA4BridgeMissingSSH(t *testing.T) {
	a4Env(t)
	t.Setenv("CONCH_SSH", filepath.Join(t.TempDir(), "nope"))
	if _, err := Bridge("dev@box", "conch"); err == nil {
		t.Fatal("bridge with a missing ssh binary")
	}
}

func TestA4BridgeConnCloseKillsStuckSSH(t *testing.T) {
	if testing.Short() {
		t.Skip("waits for the 2s kill timeout")
	}
	a4Env(t)
	exe, _ := os.Executable()
	cmd := exec.Command(exe)
	cmd.Env = append(os.Environ(), "A4_HELPER_MODE=hang")
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	conn := &bridgeConn{cmd: cmd, r: stdout, w: stdin, done: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		close(conn.done)
	}()
	start := time.Now()
	conn.Close()
	conn.Close() // idempotent
	select {
	case <-conn.done:
	case <-time.After(10 * time.Second):
		t.Fatal("stuck ssh was not killed")
	}
	if d := time.Since(start); d < 1500*time.Millisecond {
		t.Fatalf("closed after %v, before the grace period", d)
	}
	if _, err := conn.Write([]byte("x")); err == nil {
		t.Fatal("write after close")
	}
}

func TestLoginCommand(t *testing.T) {
	a4Env(t)
	t.Setenv("CONCH_SSH", "/fake/ssh")
	cfg, err := sshConfig()
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"box", "dev@box", "ssh://dev@box:2222", "dev@[::1]"} {
		got, err := LoginCommand(target)
		if err != nil {
			t.Fatalf("%q: %v", target, err)
		}
		want := []string{"/fake/ssh", "-F", cfg, "--", target}
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Fatalf("%q:\n got %q\nwant %q", target, got, want)
		}
		if !IsLoginCommand(got) {
			t.Fatalf("%q not recognised as a login", target)
		}
	}
	for _, target := range []string{"", "-oProxyCommand=evil", "-", "dev box", "box\t", "box\n", "a\x00b", "a\x7fb", " box"} {
		if cmd, err := LoginCommand(target); err == nil {
			t.Fatalf("%q accepted: %q", target, cmd)
		}
	}
}

func TestLoginCommandUnwritableConfig(t *testing.T) {
	a4Env(t)
	if os.Getuid() == 0 {
		t.Skip("root writes anywhere")
	}
	blocker := filepath.Join(os.Getenv("CONCH_HOME"), "ssh")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil { // a file where the dir goes
		t.Fatal(err)
	}
	if _, err := LoginCommand("box"); err == nil {
		t.Fatal("no error when the ssh config can't be written")
	}
}

func TestIsLoginCommand(t *testing.T) {
	for _, c := range []struct {
		cmd  []string
		want bool
	}{
		{nil, false},
		{[]string{}, false},
		{[]string{"ssh"}, true},
		{[]string{"/usr/bin/ssh", "-F", "cfg", "--", "box"}, true},
		{[]string{"zsh", "-l"}, false},
		{[]string{"sshd"}, false},
		{[]string{"/opt/ssh/bin/mosh"}, false},
	} {
		if got := IsLoginCommand(c.cmd); got != c.want {
			t.Errorf("IsLoginCommand(%q) = %v, want %v", c.cmd, got, c.want)
		}
	}
}
