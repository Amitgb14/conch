package server_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/server"
)

// a5Env isolates the server from the user's home, config, GitHub and any
// conch server the test runs inside. It returns the temporary home.
func a5Env(t *testing.T) string {
	t.Helper()
	home, _ := filepath.EvalSymlinks(t.TempDir())
	t.Setenv("HOME", home)
	short, err := os.MkdirTemp("", "a5h")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(short) })
	t.Setenv("CONCH_HOME", short)
	for _, k := range []string{"CONCH_SOCKET", "CONCH_PANE_ID", "CONCH_RELOAD_STATE", "CLAUDE_CONFIG_DIR", "CODEX_HOME", "XDG_DATA_HOME", "XDG_CONFIG_HOME", "ZDOTDIR"} {
		t.Setenv(k, "")
	}
	t.Setenv("SHELL", "/bin/sh")
	gh := filepath.Join(home, "fake-gh")
	os.WriteFile(gh, []byte("#!/bin/sh\necho '[]'\n"), 0o755)
	t.Setenv("CONCH_GH", gh)
	return home
}

// a5Collector drains a client's events so a busy subscription never stalls
// the connection, and lets tests wait for a matching one.
type a5Collector struct {
	mu   sync.Mutex
	msgs []proto.Message
	c    *client.Client
}

func a5Collect(c *client.Client) *a5Collector {
	col := &a5Collector{c: c}
	go func() {
		for m := range c.Events {
			col.mu.Lock()
			col.msgs = append(col.msgs, m)
			col.mu.Unlock()
		}
	}()
	return col
}

// wait returns the first event (from the start) that matches.
func (col *a5Collector) wait(t *testing.T, what string, match func(proto.Message) bool) proto.Message {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		col.mu.Lock()
		for _, m := range col.msgs {
			if match(m) {
				col.mu.Unlock()
				return m
			}
		}
		col.mu.Unlock()
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
	return proto.Message{}
}

func (col *a5Collector) reset() {
	col.mu.Lock()
	col.msgs = nil
	col.mu.Unlock()
}

func a5Frame(m proto.Message, id string, ok func(proto.Frame) bool) bool {
	var f proto.Frame
	return m.Event == proto.EventPaneFrame && json.Unmarshal(m.Data, &f) == nil && f.ID == id && ok(f)
}

func a5Code(t *testing.T, err error, code string) {
	t.Helper()
	var pe *proto.Error
	if !errors.As(err, &pe) || pe.Code != code {
		t.Fatalf("got %v, want a %s error", err, code)
	}
}

func a5Ctx(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestA5DispatchErrors(t *testing.T) {
	a5Env(t)
	c, dir := startServer(t)
	a5Collect(c)
	ctx := a5Ctx(t)
	bad := json.RawMessage(`"not an object"`)

	for _, m := range []string{
		proto.MethodHello, proto.MethodServerReload, proto.MethodPaneCreate, proto.MethodPaneClose, proto.MethodPaneResize,
		proto.MethodPaneSendText, proto.MethodPaneSendKeys, proto.MethodPaneRead, proto.MethodPaneSubscribe,
		proto.MethodPaneUnsubscribe, proto.MethodPaneMarkSeen, proto.MethodAgentReport, proto.MethodPaneRename,
		proto.MethodPaneScroll, proto.MethodPaneSendMouse, proto.MethodProjectAdd, proto.MethodProjectCreate,
		proto.MethodFSList, proto.MethodFSMkdir, proto.MethodProjectRemove, proto.MethodProjectRefresh,
		proto.MethodProjectChanges, proto.MethodProjectDiff, proto.MethodWorktreeAdd, proto.MethodWorktreeRemove,
		proto.MethodTaskCreate, proto.MethodAgentInstall, proto.MethodAgentExplain, proto.MethodSessionList,
		proto.MethodSessionResume, proto.MethodSessionDelete, proto.MethodSessionDismiss, proto.MethodAgentSetup,
		proto.MethodProjectFiles, proto.MethodWorktreeFiles, proto.MethodSessionSearch, proto.MethodSessionShare,
		proto.MethodFSRead,
	} {
		err := c.Call(ctx, m, bad, nil)
		var pe *proto.Error
		if !errors.As(err, &pe) || pe.Code != proto.ErrBadRequest || !strings.Contains(pe.Message, "invalid params") {
			t.Errorf("%s with bad params: %v", m, err)
		}
	}

	for _, m := range []string{
		proto.MethodPaneResize, proto.MethodPaneSendText, proto.MethodPaneSendKeys, proto.MethodPaneRead,
		proto.MethodPaneSubscribe, proto.MethodPaneMarkSeen, proto.MethodAgentReport, proto.MethodPaneRename,
		proto.MethodPaneScroll, proto.MethodPaneSendMouse, proto.MethodAgentExplain, proto.MethodPaneClose,
	} {
		err := c.Call(ctx, m, map[string]any{"id": "p404"}, nil)
		var pe *proto.Error
		if !errors.As(err, &pe) || pe.Code != proto.ErrNotFound {
			t.Errorf("%s on an unknown pane: %v", m, err)
		}
	}
	for _, m := range []string{proto.MethodProjectRemove, proto.MethodProjectRefresh} {
		a5Code(t, c.Call(ctx, m, proto.ProjectRef{ID: "r404"}, nil), proto.ErrNotFound)
	}
	a5Code(t, c.Call(ctx, proto.MethodWorktreeAdd, proto.WorktreeAddParams{ProjectID: "r404", Branch: "x"}, nil), proto.ErrNotFound)
	a5Code(t, c.Call(ctx, "no.such.method", nil, nil), proto.ErrUnknown)
	a5Code(t, c.Call(ctx, proto.MethodHello, proto.HelloParams{Protocol: proto.ProtocolVersion + 99}, nil), proto.ErrBadRequest)
	a5Code(t, c.Call(ctx, proto.MethodTaskCreate, proto.TaskCreateParams{ProjectID: "r404", Prompt: "  "}, nil), proto.ErrBadRequest)
	a5Code(t, c.Call(ctx, proto.MethodTaskCreate, proto.TaskCreateParams{ProjectID: "r404", Prompt: "do it"}, nil), proto.ErrNotFound)
	a5Code(t, c.Call(ctx, proto.MethodAgentInstall, proto.AgentInstallParams{Agent: "nope"}, nil), proto.ErrBadRequest)
	a5Code(t, c.Call(ctx, proto.MethodSessionResume, proto.SessionRef{Agent: "nope", Dir: dir}, nil), proto.ErrBadRequest)
	a5Code(t, c.Call(ctx, proto.MethodSessionList, proto.SessionListParams{}, nil), proto.ErrBadRequest)
	a5Code(t, c.Call(ctx, proto.MethodServerReload, proto.ServerReloadParams{Binary: filepath.Join(dir, "no-conch")}, nil), proto.ErrBadRequest)
	if err := c.Call(ctx, proto.MethodSessionDismiss, proto.SessionRef{Agent: "claude", Dir: dir}, nil); err != nil {
		t.Fatal(err)
	}

	// A failing notification gets no reply and leaves the connection usable.
	c.Notify(proto.MethodPaneSendText, proto.PaneSendTextParams{ID: "p404", Text: "x"})
	c.Notify("no.such.method", nil)
	var pong map[string]string
	if err := c.Call(ctx, proto.MethodPing, nil, &pong); err != nil || pong["type"] != "pong" {
		t.Fatalf("ping: %v %v", pong, err)
	}
	var hello proto.HelloResult
	if err := c.Call(ctx, proto.MethodHello, proto.HelloParams{}, &hello); err != nil || hello.PID != os.Getpid() || hello.Home == "" {
		t.Fatalf("hello: %+v %v", hello, err)
	}
	var projects proto.ProjectList
	if err := c.Call(ctx, proto.MethodProjectList, nil, &projects); err != nil || len(projects.Projects) != 0 {
		t.Fatalf("projects: %+v %v", projects, err)
	}
	var limits proto.AgentLimitsResult
	if err := c.Call(ctx, proto.MethodAgentLimits, nil, &limits); err != nil || limits.Limits == nil {
		t.Fatalf("limits: %+v %v", limits, err)
	}
	var themes proto.ShellThemes
	if err := c.Call(ctx, proto.MethodShellThemes, nil, &themes); err != nil || themes.OMZ || themes.Shell != "sh" {
		t.Fatalf("themes: %+v %v", themes, err)
	}
}

func TestA5PaneOverSocket(t *testing.T) {
	a5Env(t)
	c, dir := startServer(t)
	events := a5Collect(c)
	ctx := a5Ctx(t)

	// The program's output never contains what is typed, so markers can't
	// match an echoed command line.
	script := `i=0; while [ $i -lt 40 ]; do echo L$i; i=$((i+1)); done; printf 'MARK''ER\n'; ` +
		`read x; printf 'go''t-%s\n' "$x"; read y; exit 5`
	var info proto.PaneInfo
	if err := c.Call(ctx, proto.MethodPaneCreate, proto.PaneCreateParams{
		Name: "scroller", Command: []string{"/bin/sh", "-c", script}, Cwd: dir, Cols: 30, Rows: 5, NoProject: true,
	}, &info); err != nil {
		t.Fatal(err)
	}
	if info.Name != "scroller" || info.ProjectID != "" {
		t.Fatalf("created: %+v", info)
	}
	events.wait(t, "pane.created", func(m proto.Message) bool { return m.Event == proto.EventPaneCreated })

	if err := c.Call(ctx, proto.MethodPaneSubscribe, proto.PaneRef{ID: info.ID}, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(ctx, proto.MethodPaneSubscribe, proto.PaneRef{ID: info.ID}, nil); err != nil {
		t.Fatal(err) // subscribing twice is harmless
	}
	events.wait(t, "marker frame", func(m proto.Message) bool {
		return a5Frame(m, info.ID, func(f proto.Frame) bool {
			return strings.Contains(strings.Join(f.Lines, "\n"), "MARKER") && f.History >= 30
		})
	})

	// Scrolled back, the view stays on the same text as output arrives.
	events.reset()
	if err := c.Call(ctx, proto.MethodPaneScroll, proto.PaneScrollParams{ID: info.ID, Offset: 10}, nil); err != nil {
		t.Fatal(err)
	}
	events.wait(t, "scrolled frame", func(m proto.Message) bool {
		return a5Frame(m, info.ID, func(f proto.Frame) bool { return f.Offset == 10 })
	})
	if err := c.Call(ctx, proto.MethodPaneSendText, proto.PaneSendTextParams{ID: info.ID, Text: "abc"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(ctx, proto.MethodPaneSendKeys, proto.PaneSendKeysParams{ID: info.ID, Keys: []string{"enter"}}, nil); err != nil {
		t.Fatal(err)
	}
	events.wait(t, "anchored frame", func(m proto.Message) bool {
		return a5Frame(m, info.ID, func(f proto.Frame) bool { return f.Offset > 10 })
	})
	events.reset()
	if err := c.Call(ctx, proto.MethodPaneScroll, proto.PaneScrollParams{ID: info.ID, Offset: -3}, nil); err != nil {
		t.Fatal(err)
	}
	events.wait(t, "live frame with output", func(m proto.Message) bool {
		return a5Frame(m, info.ID, func(f proto.Frame) bool {
			return f.Offset == 0 && strings.Contains(strings.Join(f.Lines, "\n"), "got-abc")
		})
	})

	a5Code(t, c.Call(ctx, proto.MethodPaneResize, proto.PaneResizeParams{ID: info.ID, Cols: 0, Rows: 5}, nil), proto.ErrBadRequest)
	if err := c.Call(ctx, proto.MethodPaneResize, proto.PaneResizeParams{ID: info.ID, Cols: 44, Rows: 7}, nil); err != nil {
		t.Fatal(err)
	}
	events.wait(t, "resized frame", func(m proto.Message) bool {
		return a5Frame(m, info.ID, func(f proto.Frame) bool { return f.Cols == 44 && f.Rows == 7 })
	})
	a5Code(t, c.Call(ctx, proto.MethodPaneSendKeys, proto.PaneSendKeysParams{ID: info.ID, Keys: []string{"hyper+q"}}, nil), proto.ErrBadRequest)
	a5Code(t, c.Call(ctx, proto.MethodPaneSendMouse, proto.PaneSendMouseParams{ID: info.ID, Button: "thumb", Action: proto.MousePress}, nil), proto.ErrBadRequest)
	if err := c.Call(ctx, proto.MethodPaneSendMouse, proto.PaneSendMouseParams{ID: info.ID, Button: "left", Action: proto.MousePress}, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(ctx, proto.MethodPaneMarkSeen, proto.PaneRef{ID: info.ID}, nil); err != nil {
		t.Fatal(err)
	}
	var explain map[string]any
	if err := c.Call(ctx, proto.MethodAgentExplain, proto.PaneRef{ID: info.ID}, &explain); err != nil || explain == nil {
		t.Fatalf("explain: %v %v", explain, err)
	}
	var renamed proto.PaneInfo
	if err := c.Call(ctx, proto.MethodPaneRename, proto.PaneRenameParams{ID: info.ID, Name: ""}, &renamed); err != nil || renamed.Name != "scroller" || renamed.CustomName {
		t.Fatalf("rename to default: %+v %v", renamed, err)
	}

	if err := c.Call(ctx, proto.MethodPaneUnsubscribe, proto.PaneRef{ID: info.ID}, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(ctx, proto.MethodPaneUnsubscribe, proto.PaneRef{ID: info.ID}, nil); err != nil {
		t.Fatal(err) // not subscribed: nothing to do
	}
	// Scrolling without a subscription does nothing.
	if err := c.Call(ctx, proto.MethodPaneScroll, proto.PaneScrollParams{ID: info.ID, Offset: 5}, nil); err != nil {
		t.Fatal(err)
	}

	// Exit: the code is reported and input is refused.
	c.Call(ctx, proto.MethodPaneSendKeys, proto.PaneSendKeysParams{ID: info.ID, Keys: []string{"enter"}}, nil)
	events.wait(t, "pane.exited", func(m proto.Message) bool {
		var p proto.PaneInfo
		return m.Event == proto.EventPaneExited && json.Unmarshal(m.Data, &p) == nil && p.ID == info.ID && p.ExitCode == 5 && p.State == proto.PaneExited
	})
	a5Code(t, c.Call(ctx, proto.MethodPaneSendText, proto.PaneSendTextParams{ID: info.ID, Text: "x"}, nil), proto.ErrBadRequest)
	a5Code(t, c.Call(ctx, proto.MethodPaneSendKeys, proto.PaneSendKeysParams{ID: info.ID, Keys: []string{"enter"}}, nil), proto.ErrBadRequest)
	var read proto.PaneReadResult
	if err := c.Call(ctx, proto.MethodPaneRead, proto.PaneRef{ID: info.ID}, &read); err != nil || len(read.Lines) != 7 {
		t.Fatalf("read exited pane: %v %v", read.Lines, err)
	}

	if err := c.Call(ctx, proto.MethodPaneClose, proto.PaneRef{ID: info.ID}, nil); err != nil {
		t.Fatal(err)
	}
	events.wait(t, "pane.closed", func(m proto.Message) bool { return m.Event == proto.EventPaneClosed })
	a5Code(t, c.Call(ctx, proto.MethodPaneClose, proto.PaneRef{ID: info.ID}, nil), proto.ErrNotFound)
}

func TestA5SubscriberDisconnects(t *testing.T) {
	a5Env(t)
	c, dir := startServer(t)
	a5Collect(c)
	ctx := a5Ctx(t)
	var info proto.PaneInfo
	if err := c.Call(ctx, proto.MethodPaneCreate, proto.PaneCreateParams{Command: []string{"/bin/sh", "-c", "sleep 30"}, Cwd: dir, NoProject: true}, &info); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(dir, "s.sock")
	for i := 0; i < 3; i++ {
		other, err := client.Dial(sock, "other")
		if err != nil {
			t.Fatal(err)
		}
		col := a5Collect(other)
		if err := other.Call(ctx, proto.MethodPaneSubscribe, proto.PaneRef{ID: info.ID}, nil); err != nil {
			t.Fatal(err)
		}
		col.wait(t, "first frame", func(m proto.Message) bool {
			return a5Frame(m, info.ID, func(proto.Frame) bool { return true })
		})
		other.Close()
	}
	// The server dropped the subscriptions and still serves the first client.
	var list proto.PaneList
	if err := c.Call(ctx, proto.MethodPaneList, nil, &list); err != nil || len(list.Panes) != 1 {
		t.Fatalf("list: %+v %v", list, err)
	}
}

func TestA5RawProtocol(t *testing.T) {
	a5Env(t)
	c, dir := startServer(t)
	c.Close()
	nc, err := net.Dial("unix", filepath.Join(dir, "s.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	nc.SetDeadline(time.Now().Add(10 * time.Second))
	r := bufio.NewReader(nc)
	// No method: ignored. A notification: no reply. Then a request.
	if _, err := nc.Write([]byte("{}\n{\"method\":\"ping\"}\n{\"id\":\"7\",\"method\":\"ping\"}\n")); err != nil {
		t.Fatal(err)
	}
	line, err := r.ReadString('\n')
	if err != nil || !strings.Contains(line, `"id":"7"`) || !strings.Contains(line, "pong") {
		t.Fatalf("reply %q %v", line, err)
	}
	// Garbage ends the connection.
	nc.Write([]byte("this is not json\n"))
	if _, err := r.ReadString('\n'); err == nil {
		t.Fatal("connection survived garbage")
	}
}

func TestA5SocketPathTooLong(t *testing.T) {
	a5Env(t)
	dir := t.TempDir()
	long := filepath.Join(dir, strings.Repeat("x", 110), "s.sock")
	err := server.New(long, dir).Run()
	if err == nil || !strings.Contains(err.Error(), "unix socket limit") {
		t.Fatalf("long socket path: %v", err)
	}
	// A socket directory that can't be created.
	file := filepath.Join(dir, "f")
	os.WriteFile(file, nil, 0o600)
	if err := server.New(filepath.Join(file, "s.sock"), dir).Run(); err == nil {
		t.Fatal("socket under a file")
	}
}

// a5FakeShell makes every agent command a no-op: agents are launched and
// detected through the login shell, which here does nothing.
func a5FakeShell(t *testing.T, home string) string {
	t.Helper()
	sh := filepath.Join(home, "fake-shell")
	if err := os.WriteFile(sh, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", sh)
	return sh
}

func TestA5AgentsThroughFakeShell(t *testing.T) {
	home := a5Env(t)
	sh := a5FakeShell(t, home)
	c, dir := startServer(t)
	events := a5Collect(c)
	ctx := a5Ctx(t)

	var status proto.AgentStatusResult
	if err := c.Call(ctx, proto.MethodAgentStatus, nil, &status); err != nil {
		t.Fatal(err)
	}
	if len(status.Agents) < 4 {
		t.Fatalf("agents: %+v", status.Agents)
	}
	for _, a := range status.Agents {
		if a.Installed || a.Name == "" || a.Label == "" {
			t.Fatalf("agent should be missing: %+v", a)
		}
	}

	var inst proto.PaneInfo
	if err := c.Call(ctx, proto.MethodAgentInstall, proto.AgentInstallParams{Agent: "codex", Cols: 50, Rows: 10}, &inst); err != nil {
		t.Fatal(err)
	}
	if inst.Name != "install codex" || inst.Command[0] != sh || inst.Cwd != home {
		t.Fatalf("install pane: %+v", inst)
	}

	repo := filepath.Join(dir, "api")
	os.MkdirAll(repo, 0o755)
	git(t, repo, "init", "-q", "-b", "main")
	git(t, repo, "config", "user.name", "t")
	git(t, repo, "config", "user.email", "t@example.com")
	git(t, repo, "commit", "-q", "--allow-empty", "-m", "init")
	var proj proto.ProjectInfo
	if err := c.Call(ctx, proto.MethodProjectAdd, proto.ProjectAddParams{Path: repo}, &proj); err != nil {
		t.Fatal(err)
	}
	events.wait(t, "project loaded", func(m proto.Message) bool {
		var p proto.ProjectInfo
		return m.Event == proto.EventProjectUpdated && json.Unmarshal(m.Data, &p) == nil && p.Base == "main" && len(p.Worktrees) == 1
	})
	if err := c.Call(ctx, proto.MethodProjectRefresh, proto.ProjectRef{ID: proj.ID}, nil); err != nil {
		t.Fatal(err)
	}

	// A task: its own branch and worktree, the default agent in it.
	var task proto.PaneInfo
	if err := c.Call(ctx, proto.MethodTaskCreate, proto.TaskCreateParams{ProjectID: proj.ID, Prompt: "Fix the login bug", Cols: 60, Rows: 10}, &task); err != nil {
		t.Fatal(err)
	}
	if task.Name != "claude" || task.ProjectID != proj.ID || !strings.HasPrefix(task.Cwd, filepath.Join(dir, "api.worktrees")) {
		t.Fatalf("task pane: %+v", task)
	}
	if line := strings.Join(task.Command, " "); !strings.Contains(line, "Fix the login bug") {
		t.Fatalf("task prompt not passed: %s", line)
	}
	// The branch is named after the project it is in, not after conch.
	if task.Branch != "api/fix-login-bug" {
		t.Fatalf("task branch %q, want it under the project's name", task.Branch)
	}
	// The same branch again is refused before anything starts.
	branch := filepath.Base(task.Cwd)
	err := c.Call(ctx, proto.MethodTaskCreate, proto.TaskCreateParams{ProjectID: proj.ID, Prompt: "again", Branch: "main"}, nil)
	a5Code(t, err, proto.ErrBadRequest)
	// An unknown agent fails after the worktree exists.
	err = c.Call(ctx, proto.MethodTaskCreate, proto.TaskCreateParams{ProjectID: proj.ID, Prompt: "other", Branch: "other-" + branch, Agent: "nope"}, nil)
	a5Code(t, err, proto.ErrBadRequest)

	// Resuming a session starts the agent in its folder.
	var resumed proto.PaneInfo
	if err := c.Call(ctx, proto.MethodSessionResume, proto.SessionRef{Agent: "codex", ID: "abc", Dir: repo}, &resumed); err != nil {
		t.Fatal(err)
	}
	if resumed.Cwd != repo || !strings.Contains(strings.Join(resumed.Command, " "), "abc") {
		t.Fatalf("resume: %+v", resumed)
	}

	// Worktree removal refuses while the task pane works there.
	a5Code(t, c.Call(ctx, proto.MethodWorktreeRemove, proto.WorktreeRemoveParams{ProjectID: proj.ID, Path: task.Cwd}, nil), proto.ErrBadRequest)

	if err := c.Call(ctx, proto.MethodProjectRemove, proto.ProjectRef{ID: proj.ID}, nil); err != nil {
		t.Fatal(err)
	}
	events.wait(t, "project.removed", func(m proto.Message) bool { return m.Event == proto.EventProjectRemoved })
}

func TestA5AgentHooksOverSocket(t *testing.T) {
	a5Env(t)
	c, dir := startServer(t)
	events := a5Collect(c)
	ctx := a5Ctx(t)
	var info proto.PaneInfo
	if err := c.Call(ctx, proto.MethodPaneCreate, proto.PaneCreateParams{Agent: "claude", Command: []string{"/bin/sleep", "30"}, Cwd: dir, NoProject: true}, &info); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(ctx, proto.MethodAgentReport, proto.AgentReportParams{ID: info.ID, Agent: "claude", Event: "PermissionRequest", SessionID: "s-9"}, nil); err != nil {
		t.Fatal(err)
	}
	events.wait(t, "blocked agent", func(m proto.Message) bool {
		var p proto.PaneInfo
		return m.Event == proto.EventPaneUpdated && json.Unmarshal(m.Data, &p) == nil && p.Agent != nil &&
			p.Agent.State == proto.AgentBlocked && p.Agent.SessionID == "s-9"
	})
	// The run is on record for resuming after a restart.
	b, _ := os.ReadFile(filepath.Join(dir, "agent-runs.json"))
	if !strings.Contains(string(b), `"s-9"`) {
		t.Fatalf("run log: %s", b)
	}
	// Typing into the pane dismisses the prompt state.
	if err := c.Call(ctx, proto.MethodPaneSendText, proto.PaneSendTextParams{ID: info.ID, Text: "y"}, nil); err != nil {
		t.Fatal(err)
	}
	events.wait(t, "no longer blocked", func(m proto.Message) bool {
		var p proto.PaneInfo
		return m.Event == proto.EventPaneUpdated && json.Unmarshal(m.Data, &p) == nil && p.Agent != nil && p.Agent.State != proto.AgentBlocked
	})
	if err := c.Call(ctx, proto.MethodPaneClose, proto.PaneRef{ID: info.ID}, nil); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(filepath.Join(dir, "agent-runs.json"))
	if strings.Contains(string(b), `"s-9"`) {
		t.Fatalf("closed pane still in the run log: %s", b)
	}
}
