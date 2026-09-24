package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/buildinfo"
	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/server"
)

func TestA4MainHelpAndVersion(t *testing.T) {
	a4Env(t)
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	for _, arg := range []string{"help", "--help", "-h"} {
		os.Args = []string{"conch", arg}
		out, _ := a4Capture(t, "", main)
		if out != usage {
			t.Fatalf("%s printed %q", arg, out)
		}
	}
	for _, arg := range []string{"version", "--version", "-v"} {
		os.Args = []string{"conch", arg}
		out, _ := a4Capture(t, "", main)
		if !strings.HasPrefix(out, "conch "+proto.Version+" (build ") || !strings.Contains(out, buildinfo.Platform()) {
			t.Fatalf("%s printed %q", arg, out)
		}
	}
	os.Args = []string{"conch", "version", "--json"}
	out, _ := a4Capture(t, "", main)
	var info buildinfo.Info
	if err := json.Unmarshal([]byte(out), &info); err != nil || info.Version != proto.Version || len(info.Capabilities) != len(proto.Capabilities) {
		t.Fatalf("version --json: %q %v", out, err)
	}

	// -m is consumed before the command, repeatedly; the last one wins.
	os.Args = []string{"conch", "-m", "one", "--machine", "two", "help"}
	a4Capture(t, "", main)
	if machineFlag != "two" {
		t.Fatalf("machine flag %q", machineFlag)
	}
	machineFlag = ""

	// report outside a pane does nothing and never fails.
	os.Args = []string{"conch", "report", "claude-hook"}
	if out, errOut := a4Capture(t, `{"hook_event_name":"Stop"}`, main); out != "" || errOut != "" {
		t.Fatalf("report printed %q %q", out, errOut)
	}
}

func TestA4MainExitCodes(t *testing.T) {
	a4Env(t)
	code, out, errOut := a4RunMain(t, "", "frobnicate")
	if code != 2 || out != "" || !strings.Contains(errOut, `conch: unknown command "frobnicate"`) || !strings.Contains(errOut, "Usage:") {
		t.Fatalf("unknown command: %d %q %q", code, out, errOut)
	}
	code, _, errOut = a4RunMain(t, "", "send", "only-id")
	if code != 1 || !strings.Contains(errOut, "conch: usage: conch send") {
		t.Fatalf("usage error: %d %q", code, errOut)
	}
	code, out, _ = a4RunMain(t, "", "status")
	if code != 0 || out != "server: not running\n" {
		t.Fatalf("status without server: %d %q", code, out)
	}
	code, _, errOut = a4RunMain(t, "", "server", "bogus")
	if code != 1 || !strings.Contains(errOut, `unknown server subcommand "bogus"`) {
		t.Fatalf("server bogus: %d %q", code, errOut)
	}
}

func TestA4UsageErrors(t *testing.T) {
	a4Env(t)
	cases := []struct {
		name string
		run  func() error
		want string
	}{
		{"send none", func() error { return runSend(nil) }, "usage: conch send"},
		{"send bad flag", func() error { return runSend([]string{"-nope"}) }, "flag provided but not defined"},
		{"read none", func() error { return runRead(nil) }, "usage: conch read ID"},
		{"read two", func() error { return runRead([]string{"a", "b"}) }, "usage: conch read ID"},
		{"close none", func() error { return runClose(nil) }, "usage: conch close ID"},
		{"new bad flag", func() error { return runNew([]string{"-bogus"}) }, "flag provided but not defined"},
		{"server reload bad flag", func() error { return runServer([]string{"reload", "-x"}) }, "flag provided but not defined"},
		{"server unknown", func() error { return runServer([]string{"restart"}) }, `unknown server subcommand "restart"`},
		{"agent none", func() error { return runAgent(nil) }, "usage: conch agent explain ID"},
		{"agent explain none", func() error { return runAgent([]string{"explain"}) }, "usage: conch agent"},
		{"agent status extra", func() error { return runAgent([]string{"status", "x"}) }, "usage: conch agent"},
		{"agent setup bad flag", func() error { return runAgent([]string{"setup", "-bad"}) }, "flag provided but not defined"},
		{"task no prompt", func() error { return runTask(nil) }, "usage: conch task"},
		{"task bad flag", func() error { return runTask([]string{"-zzz"}) }, "flag provided but not defined"},
		{"ask no request", func() error { return runAsk([]string{"-y", "  "}) }, "usage: conch ask"},
		{"ask bad flag", func() error { return runAsk([]string{"-q"}) }, "flag provided but not defined"},
		{"machine unknown", func() error { return runMachine([]string{"reboot"}) }, `unknown machine subcommand "reboot"`},
		{"machine rm none", func() error { return runMachine([]string{"rm"}) }, "usage: conch machine rm ID"},
		{"machine upgrade none", func() error { return runMachine([]string{"upgrade"}) }, "usage: conch machine upgrade ID"},
		{"machine add none", func() error { return runMachine([]string{"add"}) }, "usage: conch machine add"},
		{"machine add two", func() error { return runMachine([]string{"add", "a", "b"}) }, "usage: conch machine add"},
		{"machine add bad flag", func() error { return runMachine([]string{"add", "-x", "a"}) }, "flag provided but not defined"},
	}
	for _, c := range cases {
		var err error
		a4Capture(t, "", func() { err = c.run() })
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", c.name, err, c.want)
		}
	}
}

func TestA4NoServer(t *testing.T) {
	a4Env(t)
	// Commands that don't start a server fail to dial.
	for name, run := range map[string]func() error{
		"send":          func() error { return runSend([]string{"p1", "hi"}) },
		"read":          func() error { return runRead([]string{"p1"}) },
		"close":         func() error { return runClose([]string{"p1"}) },
		"agent explain": func() error { return runAgent([]string{"explain", "p1"}) },
	} {
		if err := run(); err == nil {
			t.Errorf("%s without a server succeeded", name)
		}
	}
	if err := runServer([]string{"stop"}); err == nil || err.Error() != "server is not running" {
		t.Fatalf("stop: %v", err)
	}
	if err := runServer([]string{"reload"}); err == nil || err.Error() != "server is not running" {
		t.Fatalf("reload: %v", err)
	}
	// Commands that start one run this binary as `conch server`, which the
	// test binary refuses: the failure must surface, not hang.
	var err error
	a4Capture(t, "", func() { err = runProject([]string{"ls"}) })
	if err == nil || !strings.Contains(err.Error(), "server exited during startup") {
		t.Fatalf("project ls without a server: %v", err)
	}
}

func TestA4Status(t *testing.T) {
	a4Env(t)
	srv := startA4Server(t, config.SocketPath())
	long := strings.Repeat("x", 70)
	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		return proto.PaneList{Panes: []proto.PaneInfo{
			{ID: "p1", Name: "shell", State: proto.PaneRunning, Cols: 80, Rows: 24, Cwd: "/src", Command: []string{"/bin/sh", "-c", "echo a\n  echo   b"}},
			{ID: "p2", Name: "agent", State: proto.PaneExited, ExitCode: 7, Cols: 120, Rows: 40, Cwd: "/w", Command: []string{long},
				Agent: &proto.AgentStatus{Name: "claude", State: "idle"}},
		}}, nil
	})
	var err error
	out, _ := a4Capture(t, "", func() { err = runStatus() })
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 4 || !strings.HasPrefix(lines[0], "server: running  version "+proto.Version+"  pid 1000  up ") {
		t.Fatalf("status:\n%s", out)
	}
	if f := strings.Fields(lines[1]); strings.Join(f, " ") != "ID NAME STATE AGENT SIZE CWD COMMAND" {
		t.Fatalf("header %q", lines[1])
	}
	if !strings.Contains(lines[2], "p1") || !strings.Contains(lines[2], "running") || !strings.Contains(lines[2], "80x24") ||
		!strings.HasSuffix(lines[2], "/bin/sh -c echo a echo b") || !strings.Contains(lines[2], " - ") {
		t.Fatalf("pane row %q", lines[2])
	}
	if !strings.Contains(lines[3], "exited(7)") || !strings.Contains(lines[3], "claude:idle") ||
		!strings.HasSuffix(lines[3], strings.Repeat("x", 59)+"…") {
		t.Fatalf("exited row %q", lines[3])
	}

	srv.setHandle(func(proto.Message, *proto.Conn) (any, *proto.Error) { return proto.PaneList{}, nil })
	out, _ = a4Capture(t, "", func() { err = runStatus() })
	if err != nil || !strings.HasSuffix(out, "no panes\n") {
		t.Fatalf("empty: %q %v", out, err)
	}

	srv.setHandle(func(proto.Message, *proto.Conn) (any, *proto.Error) {
		return nil, proto.Errorf(proto.ErrUnknown, "unknown method")
	})
	a4Capture(t, "", func() { err = runStatus() })
	if err == nil || !strings.Contains(err.Error(), "older build without pane.list") || !strings.Contains(err.Error(), "pid 1000") {
		t.Fatalf("old server: %v", err)
	}
}

func TestA4PaneCommandsAgainstFake(t *testing.T) {
	a4Env(t)
	srv := startA4Server(t, config.SocketPath())
	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		switch msg.Method {
		case proto.MethodPaneCreate:
			return proto.PaneInfo{ID: "p9"}, nil
		case proto.MethodPaneRead:
			return proto.PaneReadResult{Lines: []string{"$ ls", "", "a b", "", "", ""}}, nil
		case proto.MethodPaneClose:
			return nil, proto.Errorf(proto.ErrNotFound, "no pane")
		case proto.MethodAgentExplain:
			return map[string]any{"state": "idle", "why": []string{"hook"}}, nil
		}
		return nil, nil
	})

	var err error
	cwd := t.TempDir()
	out, _ := a4Capture(t, "", func() {
		err = runNew([]string{"-cwd", cwd, "-name", "build", "--", "make", "test"})
	})
	if err != nil || out != "p9\n" {
		t.Fatalf("new: %q %v", out, err)
	}
	var create proto.PaneCreateParams
	create = proto.PaneCreateParams{}
	srv.params(t, proto.MethodPaneCreate, &create)
	if create.Name != "build" || create.Cwd != cwd || strings.Join(create.Command, " ") != "make test" || create.Agent != "" || create.Cols != 120 || create.Rows != 40 {
		t.Fatalf("create params %+v", create)
	}

	// No command: a login shell in the current directory (made absolute).
	t.Chdir(cwd)
	a4Capture(t, "", func() { err = runNew([]string{"-cwd", "."}) })
	create = proto.PaneCreateParams{}
	srv.params(t, proto.MethodPaneCreate, &create)
	if err != nil || strings.Join(create.Command, " ") != "/bin/sh -l" || create.Cwd != cwd {
		t.Fatalf("default shell %+v %v", create, err)
	}
	a4Capture(t, "", func() { err = runNew(nil) })
	create = proto.PaneCreateParams{}
	srv.params(t, proto.MethodPaneCreate, &create)
	if err != nil || create.Cwd != cwd {
		t.Fatalf("default cwd %+v %v", create, err)
	}

	// -agent: the remaining args are shell-quoted for the agent, and no
	// command is sent. The fake server starts nothing.
	a4Capture(t, "", func() { err = runNew([]string{"-agent", "claude", "--", "--model", "opus plan", "it's"}) })
	create = proto.PaneCreateParams{}
	srv.params(t, proto.MethodPaneCreate, &create)
	if err != nil || create.Agent != "claude" || create.Command != nil || create.AgentArgs != `--model 'opus plan' 'it'\''s' ` {
		t.Fatalf("agent create %+v %v", create, err)
	}

	if err := runSend([]string{"p9", "hello", "world"}); err != nil {
		t.Fatal(err)
	}
	var text proto.PaneSendTextParams
	srv.params(t, proto.MethodPaneSendText, &text)
	if text.ID != "p9" || text.Text != "hello world" {
		t.Fatalf("send text %+v", text)
	}
	if err := runSend([]string{"-keys", "p9", "ctrl+c", "enter"}); err != nil {
		t.Fatal(err)
	}
	var keys proto.PaneSendKeysParams
	srv.params(t, proto.MethodPaneSendKeys, &keys)
	if keys.ID != "p9" || strings.Join(keys.Keys, ",") != "ctrl+c,enter" {
		t.Fatalf("send keys %+v", keys)
	}

	out, _ = a4Capture(t, "", func() { err = runRead([]string{"p9"}) })
	if err != nil || out != "$ ls\n\na b\n" {
		t.Fatalf("read: %q %v", out, err)
	}

	if err := runClose([]string{"p9"}); err == nil || !strings.Contains(err.Error(), "no pane") {
		t.Fatalf("close: %v", err)
	}

	out, _ = a4Capture(t, "", func() { err = runAgent([]string{"explain", "p9"}) })
	var explained map[string]any
	if err != nil || json.Unmarshal([]byte(out), &explained) != nil || explained["state"] != "idle" || !strings.Contains(out, "\n  ") {
		t.Fatalf("explain: %q %v", out, err)
	}
}

func TestA4ReadFailsOnServerError(t *testing.T) {
	a4Env(t)
	srv := startA4Server(t, config.SocketPath())
	srv.setHandle(func(proto.Message, *proto.Conn) (any, *proto.Error) {
		return nil, proto.Errorf(proto.ErrNotFound, "gone")
	})
	for name, run := range map[string]func() error{
		"read":    func() error { return runRead([]string{"p1"}) },
		"new":     func() error { return runNew([]string{"--", "true"}) },
		"explain": func() error { return runAgent([]string{"explain", "p1"}) },
	} {
		var err error
		a4Capture(t, "", func() { err = run() })
		if err == nil || !strings.Contains(err.Error(), "gone") {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestA4ServerStop(t *testing.T) {
	a4Env(t)
	srv := startA4Server(t, config.SocketPath())
	if err := runServer([]string{"stop"}); err != nil {
		t.Fatal(err)
	}
	if m := srv.methods(); len(m) != 1 || m[0] != proto.MethodServerStop {
		t.Fatalf("methods %v", m)
	}
	if _, err := net.Dial("unix", config.SocketPath()); err == nil {
		t.Fatal("still listening")
	}
}

func TestA4ServerStopRefused(t *testing.T) {
	a4Env(t)
	srv := startA4Server(t, config.SocketPath())
	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		return nil, proto.Errorf("denied", "not now")
	})
	if err := runServer([]string{"stop"}); err == nil || !strings.Contains(err.Error(), "not now") {
		t.Fatalf("refused stop: %v", err)
	}
}

func TestA4ServerAlreadyRunning(t *testing.T) {
	a4Env(t)
	startA4Server(t, config.SocketPath())
	err := runServer(nil)
	if !errors.Is(err, server.ErrAlreadyRunning) || !strings.Contains(err.Error(), config.SocketPath()) {
		t.Fatalf("second server: %v", err)
	}
}

func TestA4ServerReload(t *testing.T) {
	a4Env(t)
	srv := startA4Server(t, config.SocketPath())
	srv.setHello(func(n int) proto.HelloResult {
		h := currentHello(n)
		if n > 1 {
			h.Started = h.Started.Add(time.Hour) // the same process, started again
			h.Build = "newbuild"
		}
		return h
	})
	var err error
	out, _ := a4Capture(t, "", func() { err = runServer([]string{"reload", "-binary", "/opt/conch-next"}) })
	if err != nil || out != "server reloaded: build newbuild, pid 1000 (panes kept)\n" {
		t.Fatalf("reload: %q %v", out, err)
	}
	var p proto.ServerReloadParams
	if !srv.params(t, proto.MethodServerReload, &p) || p.Binary != "/opt/conch-next" {
		t.Fatalf("reload params %+v", p)
	}
}

func TestA4ReloadServerOutcomes(t *testing.T) {
	a4Env(t)
	srv := startA4Server(t, config.SocketPath())
	dial := func() *client.Client {
		c, err := client.Dial(config.SocketPath(), "test")
		if err != nil {
			t.Fatal(err)
		}
		return c
	}

	// Another server took the socket: report it instead of trusting it.
	srv.setHello(func(n int) proto.HelloResult {
		h := currentHello(n)
		if n > 1 {
			h.PID = 2000
		}
		return h
	})
	if _, err := reloadServer(dial(), ""); err == nil || !strings.Contains(err.Error(), "a different server answered") || !strings.Contains(err.Error(), config.ServerLogPath()) {
		t.Fatalf("different pid: %v", err)
	}

	// The server refuses to reload.
	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		return nil, proto.Errorf(proto.ErrBadRequest, "no such binary")
	})
	if _, err := reloadServer(dial(), "/nope"); err == nil || !strings.Contains(err.Error(), "no such binary") {
		t.Fatalf("refused: %v", err)
	}
}

func TestA4OfferUpgrade(t *testing.T) {
	a4Env(t)
	srv := startA4Server(t, config.SocketPath())
	var reloaded atomic.Bool
	withReload := func(n int) proto.HelloResult {
		h := currentHello(n)
		h.Capabilities = []string{"pane.v1", "server.reload.v1"}
		if reloaded.Load() {
			h.Capabilities = proto.Capabilities
			h.Started = h.Started.Add(time.Minute)
		}
		return h
	}
	srv.setHello(withReload)
	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		switch msg.Method {
		case proto.MethodPaneList:
			return proto.PaneList{Panes: []proto.PaneInfo{{ID: "a", State: proto.PaneRunning}, {ID: "b", State: proto.PaneExited}}}, nil
		case proto.MethodServerReload:
			reloaded.Store(true)
		}
		return nil, nil
	})
	dial := func() *client.Client {
		c, err := client.Dial(config.SocketPath(), "test")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close() })
		return c
	}

	// Current server: nothing to ask.
	srv.setHello(currentHello)
	c := dial()
	if got, err := offerUpgrade(c); err != nil || got != c {
		t.Fatalf("current: %v %v", got, err)
	}

	// Reloadable, declined.
	srv.setHello(withReload)
	c = dial()
	var got *client.Client
	var err error
	_, errOut := a4Capture(t, "n\n", func() { got, err = offerUpgrade(c) })
	if err != nil || got != c || !strings.Contains(errOut, "lacks: pane.frame.v1") || !strings.Contains(errOut, "Reload it onto this build? Panes keep running. [Y/n]") {
		t.Fatalf("declined: %v %q", err, errOut)
	}
	if strings.Contains(strings.Join(srv.methods(), ","), proto.MethodServerReload) {
		t.Fatal("reloaded although declined")
	}

	// Reloadable, accepted by default: reloads onto this executable.
	c = dial()
	a4Capture(t, "\n", func() { got, err = offerUpgrade(c) })
	if err != nil || got == nil || got == c || len(got.MissingCapabilities(proto.Capabilities)) != 0 {
		t.Fatalf("accepted: %v %v", got, err)
	}
	got.Close()
	exe, _ := os.Executable()
	var p proto.ServerReloadParams
	if !srv.params(t, proto.MethodServerReload, &p) || p.Binary != exe {
		t.Fatalf("reload binary %q, want %q", p.Binary, exe)
	}

	// Not reloadable: restarting stops panes, so the default is no.
	srv.setHello(func(n int) proto.HelloResult {
		h := currentHello(n)
		h.Capabilities = []string{"pane.v1"}
		return h
	})
	c = dial()
	_, errOut = a4Capture(t, "\n", func() { got, err = offerUpgrade(c) })
	if err != nil || got != c || !strings.Contains(errOut, "Restarting it stops its 1 running pane(s).") || !strings.Contains(errOut, "[y/N]") {
		t.Fatalf("restart declined: %v %q", err, errOut)
	}
	if strings.Contains(strings.Join(srv.methods(), ","), proto.MethodServerStop) {
		t.Fatal("stopped although declined")
	}

	// Accepted: stop the server, then start one again. The test binary
	// refuses to be a server, so that start fails.
	c = dial()
	a4Capture(t, "yes\n", func() { got, err = offerUpgrade(c) })
	if err == nil || !strings.Contains(err.Error(), "server exited during startup") {
		t.Fatalf("restart: %v %v", got, err)
	}
	if !strings.Contains(strings.Join(srv.methods(), ","), proto.MethodServerStop) {
		t.Fatal("server not stopped")
	}
}

func TestA4Bridge(t *testing.T) {
	a4Env(t)
	startA4Server(t, config.SocketPath())

	inR, inW, _ := os.Pipe()
	outR, outW, _ := os.Pipe()
	oldIn, oldOut := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = inR, outW
	done := make(chan error, 1)
	go func() { done <- runBridge() }()
	restore := func() { os.Stdin, os.Stdout = oldIn, oldOut }

	enc := json.NewEncoder(inW)
	enc.Encode(proto.Message{ID: "1", Method: proto.MethodHello, Params: proto.Marshal(proto.HelloParams{Client: "t"})})
	line, err := bufio.NewReader(outR).ReadString('\n')
	if err != nil {
		restore()
		t.Fatal(err)
	}
	var reply proto.Message
	var hello proto.HelloResult
	if json.Unmarshal([]byte(line), &reply) != nil || reply.ID != "1" || json.Unmarshal(reply.Result, &hello) != nil || hello.PID != 1000 {
		restore()
		t.Fatalf("reply through bridge %q", line)
	}
	inW.Close() // the client hangs up: the bridge ends
	select {
	case err = <-done:
	case <-time.After(10 * time.Second):
		restore()
		t.Fatal("bridge did not end")
	}
	restore()
	outW.Close()
	if err != nil {
		t.Fatal(err)
	}

	// No server and none can be started.
	os.Remove(config.SocketPath())
	t.Setenv("CONCH_SOCKET", filepath.Join(t.TempDir(), "none.sock"))
	var berr error
	a4Capture(t, "", func() { berr = runBridge() })
	if berr == nil {
		t.Fatal("bridge without a server")
	}
}

func TestA4Report(t *testing.T) {
	a4Env(t)
	srv := startA4Server(t, "")
	t.Setenv("CONCH_SOCKET", srv.sock)

	// Outside a pane: nothing happens.
	t.Setenv("CONCH_PANE_ID", "")
	a4Capture(t, `{"hook_event_name":"Stop"}`, func() { runReport([]string{"claude-hook"}) })
	t.Setenv("CONCH_PANE_ID", "p7")
	a4Capture(t, "", func() { runReport(nil) })
	a4Capture(t, "", func() { runReport([]string{"unknown-agent"}) })
	a4Capture(t, "", func() { runReport([]string{"opencode"}) }) // no event
	a4Capture(t, "not json", func() { runReport([]string{"claude-hook"}) })
	a4Capture(t, `{"session_id":"s"}`, func() { runReport([]string{"gemini-hook"}) }) // no event name
	if m := srv.methods(); len(m) != 0 {
		t.Fatalf("reported %v", m)
	}

	out, errOut := a4Capture(t, `{"hook_event_name":"Notification","session_id":"s1","notification_type":"permission_prompt","message":"Allow?","transcript_path":"/t.jsonl"}`,
		func() { runReport([]string{"claude-hook"}) })
	if out != "" || errOut != "" {
		t.Fatalf("report printed %q %q", out, errOut)
	}
	var p proto.AgentReportParams
	if !srv.params(t, proto.MethodAgentReport, &p) || p.ID != "p7" || p.Agent != "claude" || p.Event != "Notification" ||
		p.SessionID != "s1" || p.NotificationType != "permission_prompt" || p.Message != "Allow?" || p.TranscriptPath != "/t.jsonl" {
		t.Fatalf("claude report %+v", p)
	}

	a4Capture(t, `{"hook_event_name":"AfterAgent","transcript_path":"/g.json"}`, func() { runReport([]string{"gemini-hook"}) })
	p = proto.AgentReportParams{}
	srv.params(t, proto.MethodAgentReport, &p)
	if p.Agent != "gemini" || p.Event != "AfterAgent" || p.TranscriptPath != "" {
		t.Fatalf("gemini report (transcripts are claude-only) %+v", p)
	}

	a4Capture(t, "", func() { runReport([]string{"opencode", "session.idle"}) })
	p = proto.AgentReportParams{}
	srv.params(t, proto.MethodAgentReport, &p)
	if p.Agent != "opencode" || p.Event != "session.idle" || p.ID != "p7" {
		t.Fatalf("opencode report %+v", p)
	}

	// A dead socket is ignored quickly.
	t.Setenv("CONCH_SOCKET", filepath.Join(t.TempDir(), "dead.sock"))
	start := time.Now()
	a4Capture(t, "", func() { runReport([]string{"opencode", "x"}) })
	if time.Since(start) > 3500*time.Millisecond {
		t.Fatal("report blocked on a dead socket")
	}
}

func TestA4ClaudeStatus(t *testing.T) {
	a4Env(t)
	srv := startA4Server(t, "")
	project := t.TempDir()
	os.MkdirAll(filepath.Join(project, ".claude"), 0o755)
	b, _ := json.Marshal(map[string]any{"statusLine": map[string]any{"type": "command", "command": `read line; echo "mine: $line"`}})
	os.WriteFile(filepath.Join(project, ".claude", "settings.json"), b, 0o644)

	input := `{"session_id":"s9","cwd":"` + project + `","context_window":{"total_input_tokens":10,"context_window_size":100}}`

	// Outside a pane: only the user's own status line runs.
	t.Setenv("CONCH_SOCKET", srv.sock)
	t.Setenv("CONCH_PANE_ID", "")
	out, _ := a4Capture(t, input+"\n", func() { runReport([]string{"claude-status"}) })
	if !strings.HasPrefix(out, "mine: {") || !strings.Contains(out, `"session_id":"s9"`) {
		t.Fatalf("user status line got %q", out)
	}
	if len(srv.methods()) != 0 {
		t.Fatal("reported outside a pane")
	}

	// Inside a pane: also reported.
	t.Setenv("CONCH_PANE_ID", "p3")
	a4Capture(t, input+"\n", runClaudeStatus)
	var p proto.AgentReportParams
	if !srv.params(t, proto.MethodAgentReport, &p) || p.ID != "p3" || p.Event != "StatusLine" || p.SessionID != "s9" || p.ContextUsed != 10 || p.ContextSize != 100 {
		t.Fatalf("status report %+v", p)
	}

	// No user status line and a dead socket: prints nothing, returns.
	os.Remove(filepath.Join(project, ".claude", "settings.json"))
	t.Setenv("CONCH_SOCKET", filepath.Join(t.TempDir(), "dead.sock"))
	if out, _ := a4Capture(t, "garbage", runClaudeStatus); out != "" {
		t.Fatalf("printed %q", out)
	}
}

func TestA4UserStatusLineSkipsBadSettings(t *testing.T) {
	a4Env(t)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	project := t.TempDir()
	dir := filepath.Join(project, ".claude")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "settings.local.json"), []byte("{broken"), 0o644)
	os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"statusLine":{"type":"static","command":"nope"}}`), 0o644)
	os.WriteFile(filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "settings.json"), []byte(`{"statusLine":{"type":"command","command":"  "}}`), 0o644)
	var in statusInput
	in.Cwd = project // no project_dir: cwd is used
	if got := userStatusLine(in); got != "" {
		t.Fatalf("got %q", got)
	}
	os.WriteFile(filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "settings.json"), []byte(`{"statusLine":{"type":"command","command":" from-config-dir "}}`), 0o644)
	if got := userStatusLine(in); got != "from-config-dir" {
		t.Fatalf("CLAUDE_CONFIG_DIR: %q", got)
	}
	if got := userStatusLine(statusInput{}); got != "from-config-dir" {
		t.Fatalf("no project: %q", got)
	}
}

func TestA4StatusParamsWindows(t *testing.T) {
	var in statusInput
	json.Unmarshal([]byte(`{"rate_limits":{"spend_limit":{"used_percentage":3},"five_hour":null}}`), &in)
	p := statusParams("p", in, time.Unix(5, 0))
	if p.Limits == nil || p.Limits.FiveHour != nil || p.Limits.Week != nil || p.Limits.Spend == nil ||
		p.Limits.Spend.UsedPct != 3 || !p.Limits.Spend.ResetsAt.IsZero() || !p.Limits.At.Equal(time.Unix(5, 0)) {
		t.Fatalf("limits %+v", p.Limits)
	}
}

func TestA4Confirm(t *testing.T) {
	a4Env(t)
	cases := []struct {
		in   string
		def  bool
		want bool
	}{
		{"y\n", false, true}, {"YES\n", false, true}, {"n\n", true, false}, {"No\n", true, false},
		{"\n", true, true}, {"\n", false, false}, {"maybe\n", true, true}, {"", false, false},
	}
	for _, c := range cases {
		var got bool
		_, errOut := a4Capture(t, c.in, func() { got = confirm("Proceed? ", c.def) })
		if got != c.want || errOut != "Proceed? " {
			t.Errorf("confirm(%q, %v) = %v (prompt %q)", c.in, c.def, got, errOut)
		}
	}
	_, errOut := a4Capture(t, "", func() { progress("working") })
	if errOut != "  working…\n" {
		t.Fatalf("progress %q", errOut)
	}
}

func TestA4FirstNonEmptyStr(t *testing.T) {
	if firstNonEmptyStr() != "" || firstNonEmptyStr("", "") != "" || firstNonEmptyStr("", "b", "c") != "b" {
		t.Fatal("firstNonEmptyStr")
	}
}

// TestA4RealServerPanes drives pane commands against an in-process server
// running /bin/sh panes only.
func TestA4RealServerPanes(t *testing.T) {
	dir := a4Env(t)
	srv := server.New(config.SocketPath(), dir)
	go srv.Run()
	t.Cleanup(func() {
		srv.Stop()
		for i := 0; i < 100; i++ {
			if _, err := os.Stat(config.SocketPath()); err != nil {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
	})
	for i := 0; ; i++ {
		if c, err := client.Dial(config.SocketPath(), "test"); err == nil {
			c.Close()
			break
		}
		if i == 200 {
			t.Fatal("server did not start")
		}
		time.Sleep(20 * time.Millisecond)
	}

	var err error
	out, _ := a4Capture(t, "", func() {
		err = runNew([]string{"-cwd", dir, "-name", "a4pane", "--", "/bin/sh", "-c", "echo ready-$((6*7)); read x; echo got-$x; sleep 30"})
	})
	id := strings.TrimSpace(out)
	if err != nil || id == "" {
		t.Fatalf("new: %q %v", out, err)
	}
	waitScreen := func(want string) string {
		t.Helper()
		var screen string
		for i := 0; i < 300; i++ {
			screen, _ = a4Capture(t, "", func() { err = runRead([]string{id}) })
			if err == nil && strings.Contains(screen, want) {
				return screen
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("screen never showed %q: %q %v", want, screen, err)
		return ""
	}
	waitScreen("ready-42")

	if err := runSend([]string{id, "abc"}); err != nil {
		t.Fatal(err)
	}
	if err := runSend([]string{"-keys", id, "enter"}); err != nil {
		t.Fatal(err)
	}
	waitScreen("got-abc")

	out, _ = a4Capture(t, "", func() { err = runStatus() })
	if err != nil || !strings.Contains(out, id) || !strings.Contains(out, "a4pane") || !strings.Contains(out, "running") {
		t.Fatalf("status: %q %v", out, err)
	}
	out, _ = a4Capture(t, "", func() { err = runAgent([]string{"explain", id}) })
	if err != nil || !strings.HasPrefix(strings.TrimSpace(out), "{") {
		t.Fatalf("explain: %q %v", out, err)
	}

	if err := runClose([]string{id}); err != nil {
		t.Fatal(err)
	}
	if err := runClose([]string{"no-such-pane"}); err == nil {
		t.Fatal("closing an unknown pane succeeded")
	}
	if err := runSend([]string{"no-such-pane", "x"}); err == nil {
		t.Fatal("sending to an unknown pane succeeded")
	}
}

// TestA4MainDispatch runs successful commands through main() in-process
// against a fake server that accepts everything.
func TestA4MainDispatch(t *testing.T) {
	a4Env(t)
	srv := startA4Server(t, config.SocketPath())
	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		switch msg.Method {
		case proto.MethodPaneCreate:
			return proto.PaneInfo{ID: "p1"}, nil
		case proto.MethodProjectAdd:
			return proto.ProjectInfo{ID: "proj"}, nil
		case proto.MethodTaskCreate:
			return proto.PaneInfo{ID: "p2"}, nil
		}
		return nil, nil
	})
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	for _, args := range [][]string{
		{"status"}, {"ls"}, {"new", "--", "true"}, {"send", "p1", "x"}, {"read", "p1"}, {"close", "p1"},
		{"agent", "status"}, {"project", "ls"}, {"task", "-agent", "codex", "do", "it"},
		{"machine", "ls"}, {"machines", "hosts"},
	} {
		os.Args = append([]string{"conch"}, args...)
		a4Capture(t, "", main)
	}
	want := []string{proto.MethodPaneList, proto.MethodPaneList, proto.MethodPaneCreate, proto.MethodPaneSendText, proto.MethodPaneRead,
		proto.MethodPaneClose, proto.MethodAgentStatus, proto.MethodProjectList, proto.MethodProjectAdd, proto.MethodTaskCreate}
	if got := srv.methods(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("methods\n got %v\nwant %v", got, want)
	}
}

// conch inside one of its own panes would draw a TUI inside the pane it
// runs in: keys and the mouse go to the inner one, and closing the pane
// kills it. It is refused, unless the pane belongs to another server.
func TestRunTUIRefusesToNestInItsOwnPane(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("CONCH_HOME", dir)
	sock := filepath.Join(dir, "conch.sock")
	t.Setenv("CONCH_SOCKET", sock)
	t.Setenv("CONCH_PANE_ID", "p7")
	old := machineFlag
	t.Cleanup(func() { machineFlag = old })
	machineFlag = ""

	if got := nestedPane(); got != "p7" {
		t.Fatalf("in a pane of this server: %q", got)
	}
	err := runTUI()
	if err == nil || !strings.Contains(err.Error(), "already conch, in pane p7") || !strings.Contains(err.Error(), "CONCH_PANE_ID= conch") {
		t.Fatalf("nested: %v", err)
	}
	if !strings.Contains(err.Error(), "ctrl+b d") {
		t.Fatalf("names the detach key: %v", err)
	}
	// The configured prefix is used.
	os.WriteFile(filepath.Join(dir, "config.toml"), []byte("[keys]\nprefix = \"ctrl+a\"\n"), 0o600)
	if err := runTUI(); err == nil || !strings.Contains(err.Error(), "ctrl+a d") {
		t.Fatalf("configured prefix: %v", err)
	}
	os.Remove(filepath.Join(dir, "config.toml"))

	// Not nested: no pane, another server's socket, or another machine.
	t.Setenv("CONCH_PANE_ID", "")
	if got := nestedPane(); got != "" {
		t.Fatalf("outside a pane: %q", got)
	}
	t.Setenv("CONCH_PANE_ID", "p7")
	t.Setenv("CONCH_SOCKET", "")
	if got := nestedPane(); got != "" {
		t.Fatal("a pane whose server socket is unset (a separate server) is not nesting")
	}
	t.Setenv("CONCH_SOCKET", sock)
	machineFlag = "devbox"
	if got := nestedPane(); got != "" {
		t.Fatal("a remote machine's TUI is not nesting")
	}
	machineFlag = "local"
	if got := nestedPane(); got != "p7" {
		t.Fatalf("-m local is this server: %q", got)
	}
}

// conch redraw asks the server to draw a pane's screen again, and says so
// plainly when the server is too old or the pane is unknown.
func TestRunRedraw(t *testing.T) {
	a4Env(t)
	if err := runRedraw(nil); err == nil || !strings.Contains(err.Error(), "usage: conch redraw ID") {
		t.Fatalf("no ID: %v", err)
	}
	if err := runRedraw([]string{"p1", "p2"}); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Fatalf("two IDs: %v", err)
	}
	if err := runRedraw([]string{"p1"}); err == nil {
		t.Fatal("redraw without a server succeeded")
	}

	srv := startA4Server(t, config.SocketPath())
	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		if msg.Method == proto.MethodPaneRedraw && strings.Contains(string(msg.Params), `"nope"`) {
			return nil, &proto.Error{Code: proto.ErrNotFound, Message: `no pane "nope"`}
		}
		return proto.PaneInfo{ID: "p1"}, nil
	})
	if err := runRedraw([]string{"p1"}); err != nil {
		t.Fatalf("redraw: %v", err)
	}
	var ref proto.PaneRef
	if !srv.params(t, proto.MethodPaneRedraw, &ref) || ref.ID != "p1" {
		t.Fatalf("sent %+v", ref)
	}
	if err := runRedraw([]string{"nope"}); err == nil || !strings.Contains(err.Error(), "no pane") {
		t.Fatalf("unknown pane: %v", err)
	}

	// A server from before redraw existed is named, with what to do.
	srv.setHello(func(int) proto.HelloResult {
		h := currentHello(0)
		var caps []string
		for _, c := range h.Capabilities {
			if c != "pane.redraw.v1" {
				caps = append(caps, c)
			}
		}
		h.Capabilities = caps
		return h
	})
	if err := runRedraw([]string{"p1"}); err == nil || !strings.Contains(err.Error(), "predates redrawing") || !strings.Contains(err.Error(), "conch server reload") {
		t.Fatalf("old server: %v", err)
	}
}

// A socket left behind by a server that stopped serving reads as a server
// that is there, so `conch status` says what it is and how to get back.
func TestA4StatusUnservedSocket(t *testing.T) {
	dir := a4Env(t)
	sock := filepath.Join(dir, "s.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		var held []net.Conn
		defer func() {
			for _, nc := range held {
				nc.Close()
			}
		}()
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			held = append(held, nc) // accepted, never answered
		}
	}()
	code, out, _ := a4RunMain(t, "", "status")
	if code != 0 || !strings.Contains(out, "not answering") || !strings.Contains(out, "start a fresh server") {
		t.Fatalf("status on an unserved socket: %d %q", code, out)
	}
}

// The server runs with async preemption off, because a reload is a
// syscall.Exec and macOS makes that wait for preemption signals a busy
// server never takes. It starts itself again once to get there, and only
// once.
func TestA4AsyncPreemptOff(t *testing.T) {
	for _, c := range []struct {
		goos, godebug, want string
		again               bool
	}{
		{"darwin", "", "asyncpreemptoff=1", true},
		{"darwin", "madvdontneed=1", "madvdontneed=1,asyncpreemptoff=1", true},
		{"darwin", "asyncpreemptoff=1", "asyncpreemptoff=1", false},
		{"darwin", "madvdontneed=1,asyncpreemptoff=1", "madvdontneed=1,asyncpreemptoff=1", false},
		{"darwin", "asyncpreemptoff=0", "asyncpreemptoff=0,asyncpreemptoff=1", true}, // last wins
		{"linux", "", "", false},
		{"linux", "asyncpreemptoff=1", "asyncpreemptoff=1", false},
	} {
		got, again := asyncPreemptOff(c.goos, c.godebug)
		if got != c.want || again != c.again {
			t.Errorf("asyncPreemptOff(%q, %q) = %q, %v; want %q, %v", c.goos, c.godebug, got, again, c.want, c.again)
		}
	}
}

// A config.toml conch can't parse used to stop the TUI starting at all.
// It starts on the defaults now and says so.
func TestA4ConfigForTUI(t *testing.T) {
	dir := a4Env(t)
	path := filepath.Join(dir, "config.toml")

	// Nothing there: the defaults, no warning.
	cfg, warning := configForTUI()
	if warning != "" || cfg.Keys.Prefix == "" {
		t.Fatalf("no config at all: %q %+v", warning, cfg.Keys)
	}
	// A good one is read, still without a warning.
	if err := os.WriteFile(path, []byte("[ui]\ntheme = \"light\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if cfg, warning = configForTUI(); warning != "" || cfg.UI.Theme != "light" {
		t.Fatalf("good config: %q %+v", warning, cfg.UI)
	}
	// A broken one: the defaults, and the reason to put on screen.
	if err := os.WriteFile(path, []byte("nonsense = [[[\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, warning = configForTUI()
	if !strings.Contains(warning, "settings not read") || !strings.Contains(warning, "using the defaults") {
		t.Fatalf("broken config: %q", warning)
	}
	if cfg.UI.Theme == "light" || cfg.Keys.Prefix == "" {
		t.Fatalf("want the defaults after a broken config: %+v", cfg)
	}
}

// Restarting onto a new build stays on the alternate screen, so the shell
// underneath never shows between the old TUI quitting and the new one
// drawing.
func TestA4RestartScreen(t *testing.T) {
	if !strings.HasPrefix(restartScreen, altScreen) {
		t.Fatalf("the restart screen has to enter the alternate screen first: %q", restartScreen)
	}
	if !strings.Contains(restartScreen, "\x1b[2J") {
		t.Fatalf("it should be cleared: %q", restartScreen)
	}
	if !strings.Contains(restartScreen, "new build") {
		t.Fatalf("it should say what is happening: %q", restartScreen)
	}
	if leaveAltScreen != "\x1b[?1049l" || leaveAltScreen == altScreen {
		t.Fatalf("giving the terminal back: %q", leaveAltScreen)
	}
	// Nothing in it may move the cursor into the scrollback or print a
	// bare newline, which would leave a mark on the shell's screen.
	if strings.Contains(restartScreen, "\n") && !strings.Contains(restartScreen, "\r\n") {
		t.Fatalf("a bare newline would scroll the screen: %q", restartScreen)
	}
}
