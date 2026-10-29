package main

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/doctor"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/remote"
)

// doctorEnv is a4Env with a terminal that draws, outside tmux, and a git
// that knows who is committing — the healthy case each test then breaks
// one piece of.
func doctorEnv(t *testing.T) string {
	t.Helper()
	dir := a4Env(t)
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("COLORTERM", "truecolor")
	t.Setenv("TMUX", "")
	t.Setenv("CONCH_SSH", "")
	gitconfig := filepath.Join(t.TempDir(), "gitconfig")
	os.WriteFile(gitconfig, []byte("[user]\n\tname = Test Person\n\temail = t@example.com\n"), 0o600)
	t.Setenv("GIT_CONFIG_GLOBAL", gitconfig)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, v := range []string{"GIT_AUTHOR_NAME", "GIT_COMMITTER_NAME", "GIT_AUTHOR_EMAIL", "GIT_COMMITTER_EMAIL", "EMAIL"} {
		t.Setenv(v, "")
	}
	// Never the developer's own tmux, whose server would be asked.
	bin := t.TempDir()
	doctorScript(t, bin, "tmux", "exit 1")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

// doctorFind runs every check and returns the one named.
func doctorFind(t *testing.T, name string) doctor.Check {
	t.Helper()
	for _, c := range doctorChecks() {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no %s check", name)
	return doctor.Check{}
}

func doctorScript(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestDoctorWithoutAServer: the first thing a new person might run. Nothing
// is started — the server not running is said, with how to start it — and
// the agents are not guessed at, since finding one is the server's job.
func TestDoctorWithoutAServer(t *testing.T) {
	doctorEnv(t)
	out, _ := a4Capture(t, "", func() {
		if err := runDoctor(nil); err != nil {
			t.Errorf("warnings alone are not a failure: %v", err)
		}
	})
	for _, want := range []string{
		"✓ conch     " + proto.Version,
		"✓ config    defaults (no config.toml yet",
		"! server    not running",
		"→ run conch, which starts it",
		"! agents    not checked",
		"✓ git       ", ", committing as Test Person",
		"! gh        not signed in", "→ gh auth login", // a4Env's gh fails everything
		"✓ terminal  xterm-256color, 24-bit colour",
		"✓ machines  this computer only",
		"conch works; 3 things worth a look.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor does not say %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(config.SocketPath()); err == nil {
		t.Error("the doctor started a server")
	}
}

// TestDoctorExitsOneOnAFailure: a script can ask whether conch is usable.
func TestDoctorExitsOneOnAFailure(t *testing.T) {
	doctorEnv(t)
	t.Setenv("TERM", "dumb")
	code, out, errOut := a4RunMain(t, "", "doctor")
	if code != 1 {
		t.Errorf("exit %d with a failure, want 1", code)
	}
	if !strings.Contains(out, `✗ terminal  TERM is "dumb"`) || !strings.Contains(out, "1 thing to fix, 3 more worth a look.") {
		t.Errorf("the failure is not said:\n%s", out)
	}
	if errOut != "" {
		t.Errorf("nothing more to say on stderr, got %q", errOut)
	}

	t.Setenv("TERM", "xterm")
	if code, out, _ := a4RunMain(t, "", "doctor"); code != 0 {
		t.Errorf("exit %d with warnings only:\n%s", code, out)
	}
}

func TestDoctorJSON(t *testing.T) {
	doctorEnv(t)
	t.Setenv("TERM", "")
	out, _ := a4Capture(t, "", func() {
		if err := runDoctor([]string{"-json"}); err != exitStatus(1) {
			t.Errorf("-json still exits 1 on a failure, got %v", err)
		}
	})
	var checks []doctor.Check
	if err := json.Unmarshal([]byte(out), &checks); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	byName := map[string]doctor.Check{}
	for _, c := range checks {
		byName[c.Name] = c
	}
	if c := byName["terminal"]; c.Status != doctor.Fail || c.Fix == "" {
		t.Errorf("terminal: %+v", c)
	}
	if c := byName["conch"]; c.Status != doctor.OK || c.Fix != "" {
		t.Errorf("conch: %+v", c)
	}
}

func TestDoctorRefuses(t *testing.T) {
	doctorEnv(t)
	if err := runDoctor([]string{"extra"}); err == nil || err.Error() != doctorUsage {
		t.Errorf("an argument: %v", err)
	}
	machineFlag = "busybox"
	err := runDoctor(nil)
	if err == nil || !strings.Contains(err.Error(), "checks this computer, not busybox") {
		t.Errorf("-m: %v", err)
	}
	machineFlag = "local" // the local machine by name is this computer
	if _, _ = a4Capture(t, "", func() { err = runDoctor(nil) }); err != nil {
		t.Errorf("-m local: %v", err)
	}
}

// TestDoctorServer: a server on another build, or one lacking what this
// conch needs, is the usual reason a new feature "does nothing".
func TestDoctorServer(t *testing.T) {
	doctorEnv(t)
	srv := startA4Server(t, config.SocketPath())
	srv.setHello(func(n int) proto.HelloResult {
		h := currentHello(n)
		h.Started = time.Now().Add(-3 * time.Hour)
		return h
	})
	if c := doctorFind(t, "server"); c.Status != doctor.OK || c.Detail != "pid 1000, up 3h" {
		t.Errorf("a current server: %+v", c)
	}

	srv.setHello(func(n int) proto.HelloResult {
		h := currentHello(n)
		h.Capabilities = h.Capabilities[:len(h.Capabilities)-2]
		return h
	})
	c := doctorFind(t, "server")
	if c.Status != doctor.Warn || !strings.Contains(c.Detail, "2 features missing") || !strings.HasPrefix(c.Fix, "conch server reload") {
		t.Errorf("an older server: %+v", c)
	}

	srv.setHello(func(n int) proto.HelloResult {
		h := currentHello(n)
		h.Version, h.BuildID = "0.0.1-dev", "someone-elses"
		return h
	})
	c = doctorFind(t, "server")
	exe, _ := os.Executable()
	if c.Status != doctor.Warn || !strings.Contains(c.Detail, "on another build (0.0.1-dev) than this conch") ||
		!strings.HasPrefix(c.Fix, "conch server reload -binary ") || !strings.Contains(c.Fix, exe) {
		t.Errorf("another build: %+v (a plain reload would exec the server's own file again)", c)
	}

	srv.setHello(func(n int) proto.HelloResult {
		h := currentHello(n)
		h.BuildID = "someone-elses"
		return h
	})
	if c = doctorFind(t, "server"); !strings.Contains(c.Detail, "on another build of "+proto.Version+" than this conch") {
		t.Errorf("another build of the same version is said once: %+v", c)
	}

	srv.setHello(func(n int) proto.HelloResult {
		h := currentHello(n)
		h.Version, h.BuildID = "99.0.0", "someone-elses"
		return h
	})
	c = doctorFind(t, "server")
	if c.Status != doctor.Warn || !strings.Contains(c.Detail, "on a newer conch (99.0.0)") || strings.Contains(c.Fix, "reload") {
		t.Errorf("a newer server is not reloaded backwards: %+v", c)
	}

	srv.setHello(func(n int) proto.HelloResult {
		h := currentHello(n)
		h.Capabilities = slices.DeleteFunc(slices.Clone(h.Capabilities), func(c string) bool { return c == "server.reload.v1" })
		return h
	})
	if c = doctorFind(t, "server"); !strings.HasPrefix(c.Fix, "conch server stop, then conch") {
		t.Errorf("a server that cannot reload: %+v", c)
	}
}

// TestDoctorServerNotAnswering: no server is "not running", but a socket
// something holds without answering is not — starting conch then would
// clear the socket and every pane with it.
func TestDoctorServerNotAnswering(t *testing.T) {
	doctorEnv(t)
	ln, err := net.Listen("unix", config.SocketPath())
	if err != nil {
		t.Fatal(err)
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	ln.Close() // the file stays, and nothing listens: refused
	if c := doctorFind(t, "server"); c.Status != doctor.Warn || c.Detail != "not running" {
		t.Errorf("a socket left behind: %+v", c)
	}
	os.Remove(config.SocketPath())

	if os.Getuid() == 0 {
		t.Skip("root connects whatever the socket's mode")
	}
	ln, err = net.Listen("unix", config.SocketPath())
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	os.Chmod(config.SocketPath(), 0) // somebody else's
	c := doctorFind(t, "server")
	if c.Status != doctor.Fail || !strings.Contains(c.Detail, "does not answer") || strings.Contains(c.Fix, "run conch") {
		t.Errorf("a socket that refuses: %+v", c)
	}
}

func TestDoctorAgents(t *testing.T) {
	doctorEnv(t)
	srv := startA4Server(t, config.SocketPath())
	agents := newA4Var([]proto.AgentAvailability{
		{Name: "claude", Installed: true, Version: "2.1.3", Tier: proto.TierSupported},
		{Name: "codex", Installed: true, Tier: proto.TierSupported}, // no version to show
		{Name: "gemini", Tier: proto.TierSupported},
		{Name: "pi", Tier: proto.TierRunsHere}, // somebody's own manifest, not a gap
	})
	fail := newA4Var(false)
	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		if msg.Method != proto.MethodAgentStatus {
			return nil, nil
		}
		if fail.Get() {
			return nil, &proto.Error{Code: proto.ErrInternal, Message: "no shell"}
		}
		return proto.AgentStatusResult{Agents: agents.Get()}, nil
	})
	c := doctorFind(t, "agents")
	if c.Status != doctor.OK || c.Detail != "claude 2.1.3 · codex  (not installed: gemini)" {
		t.Errorf("some installed: %+v", c)
	}

	agents.Set([]proto.AgentAvailability{{Name: "claude"}, {Name: "codex"}, {Name: "gemini"}})
	c = doctorFind(t, "agents")
	if c.Status != doctor.Fail || c.Fix != "conch agent install claude  (or codex, gemini)" {
		t.Errorf("none installed: %+v", c)
	}
	agents.Set(nil)
	if c = doctorFind(t, "agents"); c.Status != doctor.Fail || c.Fix != "conch agent install claude" {
		t.Errorf("an empty list, with no others to offer: %+v", c)
	}

	fail.Set(true)
	c = doctorFind(t, "agents")
	if c.Status != doctor.Warn || !strings.Contains(c.Detail, "no shell") {
		t.Errorf("the server failing: %+v", c)
	}

	// A server from before agent.status: no advice of its own to stop the
	// server, beside the server line's to reload it.
	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		return nil, &proto.Error{Code: proto.ErrUnknown, Message: "unknown method"}
	})
	c = doctorFind(t, "agents")
	if c.Status != doctor.Warn || !strings.Contains(c.Detail, "too old to say") || strings.Contains(c.Detail, "stop") {
		t.Errorf("an old server: %+v", c)
	}
}

func TestDoctorConfig(t *testing.T) {
	doctorEnv(t)
	os.WriteFile(config.File(), []byte("[keys]\nprefix = \n"), 0o600)
	c := doctorFind(t, "config")
	if c.Status != doctor.Fail || !strings.Contains(c.Detail, "does not parse, so none of it is in force") {
		t.Errorf("a broken file: %+v", c)
	}

	os.WriteFile(config.File(), []byte("[[actions]]\nname = \"Deploy\"\n"), 0o600)
	c = doctorFind(t, "config")
	if c.Status != doctor.Warn || !strings.Contains(c.Detail, "Deploy has no command to run") {
		t.Errorf("an action with no command: %+v", c)
	}

	os.WriteFile(config.File(), []byte("[keys]\nprefix = \"ctrl+a\"\n"), 0o600)
	if c = doctorFind(t, "config"); c.Status != doctor.OK || c.Detail != config.File() {
		t.Errorf("a good file: %+v", c)
	}
}

// TestDoctorSocket: a CONCH_HOME deep enough that no server can listen.
func TestDoctorSocket(t *testing.T) {
	doctorEnv(t)
	at := "/tmp/" + strings.Repeat("d", socketLimit()-len("/tmp/")-len("/s"))
	t.Setenv("CONCH_SOCKET", at+"/s") // exactly the limit
	if c := doctorSocket(); c.Status != doctor.OK {
		t.Errorf("at the limit: %+v", c)
	}
	t.Setenv("CONCH_SOCKET", at+"/ss")
	c := doctorSocket()
	if c.Status != doctor.Fail || !strings.Contains(c.Detail, "allows") || !strings.Contains(c.Fix, "CONCH_HOME") {
		t.Errorf("one byte over: %+v", c)
	}
}

func TestDoctorGit(t *testing.T) {
	doctorEnv(t)
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	c := doctorGit()
	if c.Status != doctor.Fail || !strings.Contains(c.Detail, "not found") || c.Fix == "" {
		t.Errorf("no git: %+v", c)
	}

	old := doctorScript(t, bin, "git", `case "$1" in
--version) echo "git version 2.30.1 (Apple Git-130)" ;;
config) [ "$3" = user.name ] && echo "Old Hand" || echo old@example.com ;;
esac`)
	if c = doctorGit(); c.Status != doctor.Warn || !strings.Contains(c.Detail, "2.30.1; telling which branches would conflict needs 2.38") {
		t.Errorf("an old git: %+v", c)
	}

	os.WriteFile(old, []byte("#!/bin/sh\n[ \"$1\" = --version ] && echo 'git version 2.45.0'\nexit 1\n"), 0o755)
	c = doctorGit()
	if c.Status != doctor.Warn || !strings.Contains(c.Detail, "no name or email") || !strings.Contains(c.Fix, "user.email") {
		t.Errorf("no identity: %+v", c)
	}

	// The environment names somebody as well as config does.
	t.Setenv("GIT_AUTHOR_NAME", "Env Person")
	t.Setenv("EMAIL", "env@example.com")
	if c = doctorGit(); c.Status != doctor.OK || c.Detail != "2.45.0, committing as Env Person" {
		t.Errorf("an identity from the environment: %+v", c)
	}
	t.Setenv("GIT_AUTHOR_NAME", "")
	t.Setenv("EMAIL", "")

	// Old and nameless: both said at once.
	os.WriteFile(old, []byte("#!/bin/sh\n[ \"$1\" = --version ] && echo 'git version 2.30.1'\nexit 1\n"), 0o755)
	c = doctorGit()
	if !strings.Contains(c.Detail, "no name or email") || !strings.Contains(c.Detail, "needs 2.38") ||
		!strings.Contains(c.Fix, "user.email") || !strings.Contains(c.Fix, "upgrade git") {
		t.Errorf("two problems: %+v", c)
	}

	os.WriteFile(old, []byte("#!/bin/sh\n[ \"$1\" = config ] && echo someone\nexit 0\n"), 0o755)
	if c = doctorGit(); c.Status != doctor.OK || c.Detail != "unknown version, committing as someone" {
		t.Errorf("a version it cannot read: %+v", c)
	}
}

// TestDoctorGH: signed in or not is asked offline, and the token gh hands
// back is never shown.
func TestDoctorGH(t *testing.T) {
	doctorEnv(t)
	t.Setenv("CONCH_GH", filepath.Join(t.TempDir(), "absent"))
	if c := doctorGH(); c.Status != doctor.Warn || !strings.Contains(c.Fix, "cli.github.com") {
		t.Errorf("no gh: %+v", c)
	}

	gh := doctorScript(t, t.TempDir(), "gh", `case "$1" in
--version) echo "gh version 2.50.0 (2024-06-01)"; echo https://github.com/cli/cli/releases ;;
auth) [ "$2" = token ] && echo gho_SECRETTOKEN ;;
*) exit 1 ;;
esac`)
	t.Setenv("CONCH_GH", gh)
	out, _ := a4Capture(t, "", func() { _ = runDoctor(nil) })
	if !strings.Contains(out, "✓ gh        2.50.0 signed in") {
		t.Errorf("signed in:\n%s", out)
	}
	if strings.Contains(out, "SECRET") {
		t.Errorf("the token was shown:\n%s", out)
	}

	// A gh from before `auth token` is asked the old way.
	os.WriteFile(gh, []byte(`#!/bin/sh
case "$1 $2" in
"--version ") echo "gh version 2.4.0 (2021-11-21)" ;;
"auth status") exit 0 ;;
*) echo "unknown command" >&2; exit 1 ;;
esac
`), 0o755)
	if c := doctorGH(); c.Status != doctor.OK || c.Detail != "2.4.0 signed in" {
		t.Errorf("an old gh, signed in: %+v", c)
	}
}

func TestDoctorTerminal(t *testing.T) {
	doctorEnv(t)
	t.Setenv("TERM_PROGRAM", "Apple_Terminal")
	t.Setenv("COLORTERM", "")
	if c := doctorTerminal(config.Default()); c.Status != doctor.OK || c.Detail != "Apple_Terminal, xterm-256color, 256 colours" {
		t.Errorf("256 colours: %+v", c)
	}

	// A tmux that will not say is taken to be on its default.
	t.Setenv("TMUX", "/tmp/tmux-501/default,1,0")
	c := doctorTerminal(config.Default())
	if c.Status != doctor.Warn || !strings.Contains(c.Detail, "inside tmux, whose usual prefix ctrl+b is conch's too") || !strings.Contains(c.Fix, `prefix = "ctrl+a"`) {
		t.Errorf("tmux with conch's prefix: %+v", c)
	}

	// One that says C-a: conch on ctrl+b is fine, conch on ctrl+a clashes,
	// and the key offered instead is free in both.
	bin := t.TempDir()
	doctorScript(t, bin, "tmux", `[ "$3" = prefix ] && echo C-a || echo None`)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if c = doctorTerminal(config.Default()); c.Status != doctor.OK || !strings.HasSuffix(c.Detail, "inside tmux") {
		t.Errorf("tmux on C-a, conch on ctrl+b: %+v", c)
	}
	cfg := config.Default()
	cfg.Keys.Prefix = "ctrl+a"
	c = doctorTerminal(cfg)
	if c.Status != doctor.Warn || !strings.Contains(c.Detail, "whose prefix ctrl+a is conch's too") || !strings.Contains(c.Fix, `prefix = "ctrl+g"`) {
		t.Errorf("tmux on C-a, conch on ctrl+a: %+v", c)
	}
	doctorScript(t, bin, "tmux", `[ "$3" = prefix2 ] && echo C-g || echo C-a`)
	if c = doctorTerminal(cfg); !strings.Contains(c.Fix, `prefix = "ctrl+x"`) {
		t.Errorf("both of tmux's prefixes avoided: %+v", c)
	}

	t.Setenv("TERM", "")
	if c = doctorTerminal(cfg); c.Status != doctor.Fail {
		t.Errorf("no TERM: %+v", c)
	}
}

// TestDoctorMachines: counted, never named, and not dialled.
func TestDoctorMachines(t *testing.T) {
	doctorEnv(t)
	// One enabled, one switched off: only the first is counted.
	os.WriteFile(remote.CatalogPath(), []byte(`{"machines": [{"id":"secret-box","label":"secret-box","target":"me@secret-box","enabled":true},
{"id":"off","label":"off","target":"me@off","enabled":false}]}`), 0o600)
	t.Setenv("CONCH_SSH", "/bin/sh") // there; never run
	c := doctorMachines()
	if c.Status != doctor.OK || !strings.HasPrefix(c.Detail, "1 machine saved") || strings.Contains(c.Detail, "secret") {
		t.Errorf("one enabled machine: %+v", c)
	}
	t.Setenv("CONCH_SSH", filepath.Join(t.TempDir(), "no-ssh"))
	if c = doctorMachines(); c.Status != doctor.Fail || !strings.Contains(c.Detail, "no ssh to reach them") {
		t.Errorf("no ssh: %+v", c)
	}

	// Cut short by a crash: not "this computer only".
	os.WriteFile(remote.CatalogPath(), []byte(`{"machines": [{"id":`), 0o600)
	if c = doctorMachines(); c.Status != doctor.Fail || !strings.Contains(c.Detail, "cannot be read") {
		t.Errorf("an unreadable machines.json: %+v", c)
	}
}

func TestDoctorSince(t *testing.T) {
	for d, want := range map[time.Duration]string{
		0: "under a minute", 59 * time.Second: "under a minute", time.Minute: "1m",
		59 * time.Minute: "59m", time.Hour: "1h", 47 * time.Hour: "47h", 48 * time.Hour: "2d",
	} {
		if got := doctorSince(d); got != want {
			t.Errorf("%v: %q, want %q", d, got, want)
		}
	}
}
