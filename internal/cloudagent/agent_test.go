package cloudagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/detect"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/relay"
	"github.com/Amitgb14/conch/internal/server"
)

func TestMain(m *testing.M) {
	// client.EnsureServer starts this binary as `conch server`.
	if len(os.Args) > 1 && os.Args[1] == "server" {
		if err := server.New(config.SocketPath(), config.Dir()).Run(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	if os.Getenv("CLOUDAGENT_HELPER") == "keys" {
		keysHelper()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// keysHelper prints every byte it is sent, as hex, in raw mode: what an
// agent's pane is sent shows on its screen.
func keysHelper() {
	stty := exec.Command("stty", "raw", "-echo")
	stty.Stdin = os.Stdin
	if err := stty.Run(); err != nil {
		fmt.Print("stty failed: ", err)
		return
	}
	fmt.Print("ready:")
	one := make([]byte, 1)
	for {
		if n, err := os.Stdin.Read(one); err != nil || n == 0 {
			return
		}
		fmt.Printf("%02x.", one[0])
	}
}

// isolate keeps a test away from the developer's conch and machine.
func isolate(t *testing.T) (home string) {
	t.Helper()
	home, _ = filepath.EvalSymlinks(t.TempDir())
	t.Setenv("HOME", home)
	for _, k := range []string{"CONCH_SOCKET", "CONCH_PANE_ID", "CONCH_RELOAD_STATE", "CONCH_MACHINE",
		"CLAUDE_CONFIG_DIR", "CODEX_HOME", "XDG_DATA_HOME", "XDG_CONFIG_HOME", "ZDOTDIR"} {
		t.Setenv(k, "")
	}
	t.Setenv("SHELL", "/bin/sh")
	gh := filepath.Join(home, "fake-gh")
	os.WriteFile(gh, []byte("#!/bin/sh\necho '[]'\n"), 0o755)
	t.Setenv("CONCH_GH", gh)
	t.Setenv("GIT_CONFIG_SYSTEM", filepath.Join(home, "gitconfig-system"))
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, "gitconfig-global"))
	return home
}

// fast makes the agent's clocks quick for a test.
func fast(t *testing.T) {
	t.Helper()
	fastHello(t)
	saved := []*time.Duration{&heartbeat, &minReport, &busyEvery, &watchEvery, &reportRetry, &bootWait, &serverRetry}
	was := make([]time.Duration, len(saved))
	for i, p := range saved {
		was[i] = *p
	}
	heartbeat, minReport, busyEvery, watchEvery, reportRetry, bootWait, serverRetry =
		time.Hour, 10*time.Millisecond, 50*time.Millisecond, time.Hour, 50*time.Millisecond, 20*time.Millisecond, 50*time.Millisecond
	t.Cleanup(func() {
		for i, p := range saved {
			*p = was[i]
		}
	})
}

// syncLog collects the agent's log; it outlives the test safely.
type syncLog struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *syncLog) logf(format string, args ...any) {
	l.mu.Lock()
	fmt.Fprintf(&l.b, format+"\n", args...)
	l.mu.Unlock()
}

func (l *syncLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// rig is an agent between a fake control plane and a real conch server.
type rig struct {
	t     *testing.T
	home  string
	dir   string // short, for sockets
	cloud *fakeCloud
	opts  Options
	agent *Agent
	log   *syncLog
	c     *client.Client // the test's own connection to the server
	done  chan error
	stop  context.CancelFunc
}

func newRig(t *testing.T) *rig {
	t.Helper()
	home := isolate(t)
	fast(t)
	dir, err := os.MkdirTemp("", "ca")
	if err != nil {
		t.Fatal(err)
	}
	dir, _ = filepath.EvalSymlinks(dir)
	t.Cleanup(func() { os.RemoveAll(dir) })
	t.Setenv("CONCH_HOME", dir)
	r := &rig{t: t, home: home, dir: dir, cloud: newFakeCloud(t, "ccm_first"), log: &syncLog{}}
	state := filepath.Join(home, "state")
	r.opts = Options{
		BootPath:     filepath.Join(home, "run", "boot.json"),
		StateDir:     state,
		ServerSocket: filepath.Join(dir, "s.sock"),
		AgentSocket:  filepath.Join(dir, "a.sock"),
		Logf:         r.log.logf,
		GitConfig:    func(context.Context, ...string) error { return nil },
	}
	r.writeBoot("ccm_first")
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("agent log:\n%s", r.log)
		}
	})
	return r
}

func (r *rig) writeBoot(token string) {
	r.t.Helper()
	os.MkdirAll(filepath.Dir(r.opts.BootPath), 0o700)
	b, _ := json.Marshal(Boot{MachineID: testMachine, API: r.cloud.URL(), MachineToken: token, ConchVersion: "0.1.12"})
	tmp := r.opts.BootPath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		r.t.Fatal(err)
	}
	if err := os.Rename(tmp, r.opts.BootPath); err != nil {
		r.t.Fatal(err)
	}
}

// startServer runs a conch server in this process.
func (r *rig) startServer() {
	r.t.Helper()
	srv := server.New(r.opts.ServerSocket, r.dir)
	go srv.Run()
	r.t.Cleanup(srv.StopAndWait)
	r.connect()
}

func (r *rig) connect() {
	r.t.Helper()
	var err error
	for range 200 {
		if r.c, err = client.Dial(r.opts.ServerSocket, "test"); err == nil {
			c := r.c
			r.t.Cleanup(func() { c.Close() })
			go func() {
				for range c.Events {
				}
			}()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	r.t.Fatal(err)
}

func (r *rig) run() {
	r.t.Helper()
	a, err := New(r.opts)
	if err != nil {
		r.t.Fatal(err)
	}
	r.agent = a
	ctx, cancel := context.WithCancel(context.Background())
	r.stop = cancel
	r.done = make(chan error, 1)
	go func() { r.done <- a.Run(ctx) }()
	r.t.Cleanup(func() {
		cancel()
		select {
		case <-r.done:
		case <-time.After(10 * time.Second):
			r.t.Error("the agent did not stop")
		}
	})
}

// exited waits for Run to return on its own.
func (r *rig) exited(within time.Duration) error {
	r.t.Helper()
	select {
	case err := <-r.done:
		r.done <- err // for the cleanup
		return err
	case <-time.After(within):
		r.t.Fatal("the agent is still running")
		return nil
	}
}

func (r *rig) call(method string, params, out any) {
	r.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := r.c.Call(ctx, method, params, out); err != nil {
		r.t.Fatalf("%s: %v", method, err)
	}
}

func (r *rig) pane(p proto.PaneCreateParams) proto.PaneInfo {
	r.t.Helper()
	if p.Cwd == "" {
		p.Cwd = r.home
	}
	p.Cols, p.Rows = 80, 10
	var info proto.PaneInfo
	r.call(proto.MethodPaneCreate, p, &info)
	return info
}

func (r *rig) screen(id string) string {
	var res proto.PaneReadResult
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r.c.Call(ctx, proto.MethodPaneRead, proto.PaneRef{ID: id}, &res)
	return strings.Join(res.Lines, "\n")
}

func (r *rig) waitScreen(id, marker string) string {
	r.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		s := r.screen(id)
		if strings.Contains(s, marker) {
			return s
		}
		if time.Now().After(deadline) {
			r.t.Fatalf("pane %s never showed %q:\n%s", id, marker, s)
		}
		time.Sleep(30 * time.Millisecond)
	}
}

func (r *rig) report(id, event string) {
	r.t.Helper()
	r.call(proto.MethodAgentReport, proto.AgentReportParams{ID: id, Agent: "claude", Event: event}, nil)
}

// activityWith waits for an activity report ok accepts.
func (r *rig) activityWith(what string, ok func(VMActivity) bool) VMActivity {
	r.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		for _, req := range r.cloud.requests("POST /v1/vm/activity") {
			var act VMActivity
			json.Unmarshal(req.Body, &act)
			if ok(act) {
				return act
			}
		}
		if time.Now().After(deadline) {
			var last string
			if all := r.cloud.requests("POST /v1/vm/activity"); len(all) > 0 {
				last = string(all[len(all)-1].Body)
			}
			r.t.Fatalf("no activity report with %s; the last: %s", what, last)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestAgentSaysHelloAndReports is the machine reaching "running": hello
// with the server's capabilities and this machine's key, then activity as
// its agents change.
func TestAgentSaysHelloAndReports(t *testing.T) {
	r := newRig(t)
	r.startServer()
	r.run()
	hellos := r.cloud.waitFor("POST /v1/vm/hello", 1, 10*time.Second)
	var h map[string]any
	json.Unmarshal(hellos[0].Body, &h)
	caps, _ := h["capabilities"].([]any)
	var capList []string
	for _, c := range caps {
		capList = append(capList, c.(string))
	}
	for _, want := range []string{proto.CapCloudAgent, "pane.v1", ContractCapability} {
		if !slices.Contains(capList, want) {
			t.Errorf("hello's capabilities lack %s: %v", want, capList)
		}
	}
	key, made, err := LoadKey(filepath.Join(r.opts.StateDir, "noise.key"))
	if err != nil || made {
		t.Fatalf("the key was not kept: made %v %v", made, err)
	}
	if h["server_key"] != relay.EncodeKey(key.Public) || h["woke_from"] != WokeBoot || h["conch_version"] != proto.Version {
		t.Fatalf("hello %s", hellos[0].Body)
	}
	if hellos[0].Header.Get("Authorization") != "Bearer ccm_first" {
		t.Fatal("hello without the machine token")
	}
	r.activityWith("nothing running", func(a VMActivity) bool {
		return len(a.Agents) == 0 && !a.ProcessesBusy && a.ClientsAttached == 0
	})

	// An agent starts and works.
	ag := r.pane(proto.PaneCreateParams{Agent: "claude", Command: []string{"/bin/sh", "-c", "stty -echo; exec cat"}})
	r.report(ag.ID, "UserPromptSubmit")
	r.activityWith("a working agent", func(a VMActivity) bool {
		return len(a.Agents) == 1 && a.Agents[0] == AgentActivity{Agent: "claude", State: "working"}
	})
	r.report(ag.ID, "PermissionRequest")
	r.activityWith("a waiting agent", func(a VMActivity) bool { return len(a.Agents) == 1 && a.Agents[0].State == "waiting" })
	r.report(ag.ID, "Stop")
	// done until somebody looks, then idle: either way, not working.
	r.activityWith("a finished agent", func(a VMActivity) bool {
		return len(a.Agents) == 1 && (a.Agents[0].State == "done" || a.Agents[0].State == "idle")
	})

	// A build in a terminal keeps the machine up; its end lets it go.
	sh := r.pane(proto.PaneCreateParams{Command: []string{"/bin/sh"}})
	r.activityWith("a shell at its prompt, not busy", func(a VMActivity) bool { return len(a.Agents) == 1 && !a.ProcessesBusy })
	r.call(proto.MethodPaneSendText, proto.PaneSendTextParams{ID: sh.ID, Text: "exec sleep 30\r"}, nil)
	r.activityWith("a busy terminal", func(a VMActivity) bool { return a.ProcessesBusy })
	r.call(proto.MethodPaneClose, proto.PaneRef{ID: sh.ID}, nil)
	r.call(proto.MethodPaneClose, proto.PaneRef{ID: ag.ID}, nil)
	n := len(r.cloud.requests("POST /v1/vm/activity"))
	act := r.activityWith("everything gone", func(a VMActivity) bool { return len(a.Agents) == 0 && !a.ProcessesBusy })
	_ = act
	if got := r.cloud.requests("POST /v1/vm/activity"); len(got) < n {
		t.Fatal("reports went missing")
	}
	for _, req := range r.cloud.requests("POST /v1/vm/activity") {
		if req.Header.Get("Conch-Contract") != Contract {
			t.Fatalf("activity with Conch-Contract %q", req.Header.Get("Conch-Contract"))
		}
	}
}

// TestActivityOnlyOnChangeAndHeartbeat: the same report is not sent twice
// in a row unless the heartbeat is due, and then it is, unchanged.
func TestActivityOnlyOnChangeAndHeartbeat(t *testing.T) {
	r := newRig(t)
	r.startServer()
	r.run()
	r.cloud.waitFor("POST /v1/vm/activity", 1, 10*time.Second)
	// Nothing changes: no more reports, however often the busy poll runs.
	time.Sleep(300 * time.Millisecond)
	if n := len(r.cloud.requests("POST /v1/vm/activity")); n != 1 {
		t.Fatalf("%d reports with nothing changed", n)
	}
	r.agent.kick() // a change that changes nothing
	time.Sleep(100 * time.Millisecond)
	if n := len(r.cloud.requests("POST /v1/vm/activity")); n != 1 {
		t.Fatalf("%d reports after a change to nothing", n)
	}
}

func TestActivityHeartbeat(t *testing.T) {
	r := newRig(t)
	heartbeat = 100 * time.Millisecond
	r.startServer()
	r.run()
	got := r.cloud.waitFor("POST /v1/vm/activity", 4, 10*time.Second)
	if !bytes.Equal(got[1].Body, got[2].Body) {
		t.Fatalf("heartbeats differ: %s / %s", got[1].Body, got[2].Body)
	}
	if gap := got[3].At.Sub(got[2].At); gap < 50*time.Millisecond {
		t.Fatalf("heartbeats %s apart", gap)
	}
}

// TestPauseAgents: a 1.1 control plane's pause_agents is Esc to every
// working agent, and nothing to one that is not working.
func TestPauseAgents(t *testing.T) {
	r := newRig(t)
	r.startServer()
	exe, _ := os.Executable()
	r.run()
	r.cloud.waitFor("POST /v1/vm/activity", 1, 10*time.Second)
	work := r.pane(proto.PaneCreateParams{Agent: "claude", Command: []string{exe}, Env: []string{"CLOUDAGENT_HELPER=keys"}})
	idle := r.pane(proto.PaneCreateParams{Agent: "claude", Command: []string{exe}, Env: []string{"CLOUDAGENT_HELPER=keys"}})
	r.waitScreen(work.ID, "ready:")
	r.waitScreen(idle.ID, "ready:")
	var paused atomic.Bool
	r.cloud.handle("POST /v1/vm/activity", func(w http.ResponseWriter, req *http.Request) {
		var act VMActivity
		json.NewDecoder(req.Body).Decode(&act)
		cmds := []any{}
		for _, a := range act.Agents {
			if a.State == "working" && paused.CompareAndSwap(false, true) {
				cmds = append(cmds, map[string]any{"type": "pause_agents", "reason": "unattended too long"})
			}
		}
		writeJSON(w, 200, map[string]any{"commands": cmds})
	})
	r.report(work.ID, "UserPromptSubmit")
	r.waitScreen(work.ID, "ready:1b.")
	if s := r.screen(idle.ID); strings.Contains(s, "1b") {
		t.Fatalf("an agent that was not working was sent Esc: %s", s)
	}
	if !strings.Contains(r.log.String(), "paused 1 working agent(s): unattended too long") {
		t.Fatalf("log: %s", r.log)
	}
	// A command of a later contract is ignored (the contract's enum has
	// only pause_agents today, so the fake cannot send one).
	r.agent.command(context.Background(), VMCommand{Type: "reboot_everything"})
	if s := r.screen(work.ID); strings.Count(s, "1b") != 1 {
		t.Fatalf("an unknown command did something: %s", s)
	}
}

// TestActivityTo10Server: a 1.0.0 control plane answers 204, and that is
// not an error.
func TestActivityTo10Server(t *testing.T) {
	r := newRig(t)
	heartbeat = 50 * time.Millisecond
	r.cloud.handle("POST /v1/vm/activity", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
	r.startServer()
	r.run()
	r.cloud.waitFor("POST /v1/vm/activity", 3, 10*time.Second)
	if strings.Contains(r.log.String(), "activity report:") {
		t.Fatalf("a 204 was taken for a failure: %s", r.log)
	}
}

// TestRefusedTokenStops: the token is replaced at every inject, so a token
// the control plane refuses — with no newer one in boot.json — ends the
// agent, with exit status 3.
func TestRefusedTokenStops(t *testing.T) {
	r := newRig(t)
	r.startServer()
	r.run()
	r.cloud.waitFor("POST /v1/vm/activity", 1, 10*time.Second)
	r.cloud.setToken("ccm_someone_else")
	r.agent.kick()
	heartbeat = 50 * time.Millisecond // so a report is sent
	r.pane(proto.PaneCreateParams{Agent: "claude", Command: []string{"/bin/sh", "-c", "exec cat"}})
	err := r.exited(10 * time.Second)
	if !errors.Is(err, ErrUnauthorized) || ExitCode(err) != 3 {
		t.Fatalf("exited with %v (code %d)", err, ExitCode(err))
	}
}

func TestRefusedHelloStops(t *testing.T) {
	r := newRig(t)
	r.cloud.setToken("ccm_newer")
	r.startServer()
	r.run()
	if err := r.exited(10 * time.Second); ExitCode(err) != 3 {
		t.Fatalf("exited with %v", err)
	}
}

// TestNewTokenInBootJSON: a token refused because a newer inject replaced
// it is read again from boot.json and used, with another hello.
func TestNewTokenInBootJSON(t *testing.T) {
	r := newRig(t)
	r.startServer()
	r.run()
	r.cloud.waitFor("POST /v1/vm/hello", 1, 10*time.Second)
	r.cloud.waitFor("POST /v1/vm/activity", 1, 10*time.Second)
	r.writeBoot("ccm_second")
	r.cloud.setToken("ccm_second")
	r.pane(proto.PaneCreateParams{Agent: "claude", Command: []string{"/bin/sh", "-c", "exec cat"}})
	hellos := r.cloud.waitFor("POST /v1/vm/hello", 2, 10*time.Second)
	if hellos[1].Header.Get("Authorization") != "Bearer ccm_second" {
		t.Fatalf("second hello with %q", hellos[1].Header.Get("Authorization"))
	}
	r.activityWith("an agent, reported with the new token", func(a VMActivity) bool { return len(a.Agents) == 1 })
}

// TestWatchSeesANewBootJSON: a boot.json rewritten while the agent runs is
// a new inject; the agent says hello with its token without waiting to be
// refused.
func TestWatchSeesANewBootJSON(t *testing.T) {
	r := newRig(t)
	watchEvery = 30 * time.Millisecond
	r.startServer()
	r.run()
	r.cloud.waitFor("POST /v1/vm/hello", 1, 10*time.Second)
	r.cloud.setToken("ccm_second")
	r.writeBoot("ccm_second")
	hellos := r.cloud.waitFor("POST /v1/vm/hello", 2, 10*time.Second)
	var h VMHello
	json.Unmarshal(hellos[len(hellos)-1].Body, &h)
	if hellos[len(hellos)-1].Header.Get("Authorization") != "Bearer ccm_second" || h.WokeFrom != WokeMemory {
		t.Fatalf("hello %s with %s", hellos[len(hellos)-1].Body, hellos[len(hellos)-1].Header.Get("Authorization"))
	}
}

// TestAgentStartsTheServer: with no server running, the agent starts one
// — the server a machine's clients reach — and registers with it.
func TestAgentStartsTheServer(t *testing.T) {
	r := newRig(t)
	r.run()
	r.cloud.waitFor("POST /v1/vm/hello", 1, 20*time.Second)
	r.connect()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		r.c.Call(ctx, proto.MethodServerStop, nil, nil)
	})
	if !slices.Contains(r.c.Server.Capabilities, proto.CapCloudAgent) {
		t.Fatalf("the server started does not say cloud.agent.v1: %v", r.c.Server.Capabilities)
	}
}

// TestEnvSecretsReachAgentsAndNoDisk: an agent's start fetches the env
// secrets and that agent has them; nothing on disk does.
func TestEnvSecretsReachAgentsAndNoDisk(t *testing.T) {
	const secret = "s3cr3t-v4lue-9d1f"
	r := newRig(t)
	r.cloud.handle("GET /v1/vm/env", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{"env": map[string]string{"DATABASE_URL": secret}})
	})
	r.startServer()
	r.run()
	r.cloud.waitFor("POST /v1/vm/hello", 1, 10*time.Second)
	ag := r.pane(proto.PaneCreateParams{Agent: "claude", Command: []string{"/bin/sh", "-c", `printf 'v=%s.\n' "$DATABASE_URL"; exec cat`}})
	r.waitScreen(ag.ID, "v="+secret+".")
	sh := r.pane(proto.PaneCreateParams{Command: []string{"/bin/sh", "-c", `printf 'shell=%s.\n' "$DATABASE_URL"; exec cat`}})
	if s := r.waitScreen(sh.ID, "shell="); strings.Contains(s, secret) {
		t.Fatal("a shell was given the secret")
	}
	if n := len(r.cloud.requests("GET /v1/vm/env")); n != 1 {
		t.Fatalf("env fetched %d times for one agent", n)
	}
	for _, root := range []string{r.home, r.dir} {
		filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || d.Type()&fs.ModeSocket != 0 {
				return nil
			}
			if b, err := os.ReadFile(path); err == nil && bytes.Contains(b, []byte(secret)) {
				t.Errorf("the secret is on disk in %s", path)
			}
			return nil
		})
	}
	if strings.Contains(r.log.String(), secret) {
		t.Error("the secret is in the agent's log")
	}
}

func TestBootJSONIsWaitedFor(t *testing.T) {
	r := newRig(t)
	os.Remove(r.opts.BootPath)
	r.startServer()
	r.run()
	time.Sleep(100 * time.Millisecond)
	if n := len(r.cloud.requests("POST /v1/vm/hello")); n != 0 {
		t.Fatalf("hello without boot.json")
	}
	r.writeBoot("ccm_first")
	r.cloud.waitFor("POST /v1/vm/hello", 1, 10*time.Second)
}

func TestBadBootJSONStops(t *testing.T) {
	r := newRig(t)
	os.Chmod(r.opts.BootPath, 0o644)
	r.startServer()
	r.run()
	if err := r.exited(5 * time.Second); err == nil || !strings.Contains(err.Error(), "readable by others") {
		t.Fatalf("exited with %v", err)
	}
}

// TestGitCredentialFromMemory: the agent's socket hands git the token in
// use, and is the person's user's alone.
func TestGitCredentialFromMemory(t *testing.T) {
	r := newRig(t)
	r.startServer()
	r.run()
	r.cloud.waitFor("POST /v1/vm/hello", 1, 10*time.Second)
	ans, err := Ask(r.opts.AgentSocket, proto.CloudAgentAsk{Op: proto.CloudAskGitCredential})
	if err != nil || ans.Password != "ccm_first" || ans.Username == "" {
		t.Fatalf("%+v %v", ans, err)
	}
	if st, _ := os.Stat(r.opts.AgentSocket); st.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode %v", st.Mode())
	}
	if _, err := Ask(r.opts.AgentSocket, proto.CloudAgentAsk{Op: "launch-missiles"}); err == nil {
		t.Fatal("an unknown question was answered")
	}
}

func TestWokeFrom(t *testing.T) {
	dir := t.TempDir()
	was := bootIDPath
	t.Cleanup(func() { bootIDPath = was })
	bootIDPath = filepath.Join(dir, "boot_id")
	os.WriteFile(bootIDPath, []byte("aaaa\n"), 0o644)
	state := filepath.Join(dir, "state")
	if w := wokeFrom(state, true); w != WokeBoot {
		t.Fatalf("a key made now: %s", w)
	}
	if w := wokeFrom(state, false); w != WokeMemory {
		t.Fatalf("the same boot: %s", w)
	}
	os.WriteFile(bootIDPath, []byte("bbbb\n"), 0o644)
	if w := wokeFrom(state, false); w != WokeDisk {
		t.Fatalf("a new boot: %s", w)
	}
	bootIDPath = filepath.Join(dir, "absent")
	if w := wokeFrom(state, false); w != WokeDisk {
		t.Fatalf("no boot id: %s", w)
	}
}

func TestSlept(t *testing.T) {
	now := time.Now()
	if slept(now, now.Add(5*time.Second), 5*time.Second) {
		t.Fatal("an on-time tick")
	}
	if slept(now, now.Add(20*time.Second), 5*time.Second) {
		t.Fatal("a tick 15 s late")
	}
	if !slept(now, now.Add(5*time.Minute), 5*time.Second) {
		t.Fatal("five minutes late")
	}
	// Only the wall clock moved, as when a paused machine's monotonic
	// clock stood still.
	if !slept(now.Round(0), now.Round(0).Add(10*time.Minute), 5*time.Second) {
		t.Fatal("ten minutes by the wall clock")
	}
}

func TestIsShell(t *testing.T) {
	for p, want := range map[*detect.Process]bool{
		{Name: "bash"}: true, {Name: "-zsh"}: true, {Name: "fish"}: true, {Name: "sh"}: true,
		{Name: "sleep"}: false, {Name: "node", Args: []string{"node", "server.js"}}: false, {Name: "make"}: false,
	} {
		if isShell(*p) != want {
			t.Errorf("%+v: %v", *p, !want)
		}
	}
}

func TestOlder(t *testing.T) {
	for _, c := range []struct {
		have, want string
		older      bool
	}{
		{"0.1.10", "0.1.12", true}, {"0.1.12", "0.1.12", false}, {"0.1.13", "0.1.12", false},
		{"0.1.10-dev", "0.1.10", true}, {"0.1.10", "dev", false}, {"0.1.10", "", false}, {"0.1.10", "v0.2.0", true},
	} {
		if got := older(c.have, c.want); got != c.older {
			t.Errorf("older(%q, %q) = %v", c.have, c.want, got)
		}
	}
}
