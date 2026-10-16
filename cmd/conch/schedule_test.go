package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
)

func TestEveryText(t *testing.T) {
	for min, want := range map[int]string{0: "-", 30: "every 30m", 120: "every 2h", 1440: "every 1d", 10080: "every 1w", 90: "every 90m"} {
		if got := everyText(min); got != want {
			t.Errorf("%d: %q, want %q", min, got, want)
		}
	}
}

func TestScheduleCommands(t *testing.T) {
	dir := a4Env(t)
	srv := startA4Server(t, config.SocketPath())
	at := time.Date(2026, 10, 15, 9, 0, 0, 0, time.Local)
	items := []proto.ScheduledItem{
		{ID: "s1", At: at, Pane: "p3", Agent: "claude", Text: "continue", Note: "after the 5-hour limit", Waiting: "the agent is working"},
		{ID: "s2", At: at.Add(time.Hour), EveryMinutes: 1440, Task: &proto.TaskCreateParams{Prompt: "bump the dependencies"}, LastError: "dirty worktree"},
	}
	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		switch msg.Method {
		case proto.MethodScheduleList:
			return proto.ScheduleList{Items: items}, nil
		case proto.MethodScheduleRemove:
			var it proto.ScheduledItem
			json.Unmarshal(msg.Params, &it)
			if it.ID != "s1" {
				return nil, proto.Errorf(proto.ErrNotFound, "nothing scheduled as %q", it.ID)
			}
			return nil, nil
		case proto.MethodPaneList:
			return proto.PaneList{Panes: []proto.PaneInfo{{ID: "p3", Name: "claude", State: proto.PaneRunning}}}, nil
		case proto.MethodScheduleAdd:
			var it proto.ScheduledItem
			json.Unmarshal(msg.Params, &it)
			it.ID = "s9"
			return it, nil
		case proto.MethodProjectAdd:
			return proto.ProjectInfo{ID: "proj1", Name: "api", Git: true}, nil
		}
		return nil, proto.Errorf(proto.ErrUnknown, "?")
	})
	run := func(fn func() error) (string, error) {
		var err error
		out, _ := a4Capture(t, "", func() { err = fn() })
		return out, err
	}
	out, err := run(func() error { return runScheduleCmd(nil) })
	if err != nil || !strings.Contains(out, "s1") || !strings.Contains(out, "to p3 (claude): continue — after the 5-hour limit [waiting: the agent is working]") ||
		!strings.Contains(out, "every 1d") || !strings.Contains(out, "task: bump the dependencies [last run failed: dirty worktree]") {
		t.Fatalf("ls:\n%s %v", out, err)
	}
	if _, err := run(func() error { return runScheduleCmd([]string{"rm", "s1"}) }); err != nil {
		t.Fatal(err)
	}
	if _, err := run(func() error { return runScheduleCmd([]string{"rm", "zz"}) }); err == nil || !strings.Contains(err.Error(), "nothing scheduled") {
		t.Fatalf("rm unknown: %v", err)
	}
	for _, args := range [][]string{{"rm"}, {"ls", "x"}, {"frob"}} {
		if _, err := run(func() error { return runScheduleCmd(args) }); err == nil || !strings.Contains(err.Error(), "usage: conch schedule") {
			t.Errorf("%v: %v", args, err)
		}
	}
	items = nil
	if out, _ := run(func() error { return runScheduleCmd(nil) }); out != "nothing scheduled\n" {
		t.Fatalf("empty: %q", out)
	}

	// agent prompt -at hands the message to the server instead.
	out, err = run(func() error { return agentPrompt([]string{"-at", "+30m", "p3", "continue", "please"}) })
	if err != nil || !strings.HasPrefix(out, "s9 scheduled for ") {
		t.Fatalf("prompt -at: %q %v", out, err)
	}
	var it proto.ScheduledItem
	srv.params(t, proto.MethodScheduleAdd, &it)
	if it.Pane != "p3" || it.Text != "continue please" || time.Until(it.At) < 29*time.Minute {
		t.Fatalf("scheduled %+v", it)
	}
	if _, err := run(func() error { return agentPrompt([]string{"-at", "+30m", "-wait", "p3", "x"}) }); err == nil || !strings.Contains(err.Error(), "-wait") {
		t.Fatalf("-at with -wait: %v", err)
	}
	if _, err := run(func() error { return agentPrompt([]string{"-at", "soon", "p3", "x"}) }); err == nil || !strings.Contains(err.Error(), "give a time") {
		t.Fatalf("bad -at: %v", err)
	}

	// task -every schedules a task, which starts at once and then daily.
	os.WriteFile(filepath.Join(dir, "config.toml"), []byte("[agents]\ndefault = \"codex\"\n"), 0o600)
	t.Chdir(t.TempDir())
	out, err = run(func() error { return runTask([]string{"-every", "daily", "bump", "the", "deps"}) })
	if err != nil || !strings.Contains(out, "then every 1d") {
		t.Fatalf("task -every: %q %v", out, err)
	}
	it = proto.ScheduledItem{}
	srv.params(t, proto.MethodScheduleAdd, &it)
	if it.EveryMinutes != 1440 || it.Task == nil || it.Task.Prompt != "bump the deps" || it.Task.Agent != "codex" || it.Task.ProjectID != "proj1" {
		t.Fatalf("scheduled %+v %+v", it, it.Task)
	}
	// task -queue holds it until the project has room.
	out, err = run(func() error { return runTask([]string{"-queue", "-wip", "2", "next", "job"}) })
	if err != nil || !strings.Contains(out, "queued: it starts when fewer than 2 agents") {
		t.Fatalf("task -queue: %q %v", out, err)
	}
	it = proto.ScheduledItem{}
	srv.params(t, proto.MethodScheduleAdd, &it)
	if !it.Queue || it.WIP != 2 || it.Task.Prompt != "next job" {
		t.Fatalf("queued %+v", it)
	}
	for _, args := range [][]string{
		{"-wip", "2", "x"},
		{"-queue", "-every", "1d", "x"},
		{"-at", "+1h", "-n", "2", "x"},
		{"-at", "+1h", "-agent", "claude,codex", "x"},
		{"-every", "1m", "x"},
		{"-at", "later", "x"},
	} {
		if _, err := run(func() error { return runTask(args) }); err == nil {
			t.Errorf("task %v was scheduled", args)
		}
	}

	// A server from before scheduling is named, with what to do.
	srv.setHello(func(int) proto.HelloResult {
		h := currentHello(0)
		h.Capabilities = slices.DeleteFunc(slices.Clone(h.Capabilities), func(c string) bool { return c == proto.CapSchedule })
		return h
	})
	for _, fn := range []func() error{
		func() error { return runScheduleCmd(nil) },
		func() error { return agentPrompt([]string{"-at", "+30m", "p3", "x"}) },
		func() error { return runTask([]string{"-at", "+1h", "x"}) },
	} {
		if _, err := run(fn); err == nil || !strings.Contains(err.Error(), "predates scheduling") {
			t.Errorf("old server: %v", err)
		}
	}
}

func TestTaskAfter(t *testing.T) {
	a4Env(t)
	srv := startA4Server(t, config.SocketPath())
	var items []proto.ScheduledItem
	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		switch msg.Method {
		case proto.MethodScheduleList:
			return proto.ScheduleList{Items: items}, nil
		case proto.MethodScheduleAdd:
			var it proto.ScheduledItem
			json.Unmarshal(msg.Params, &it)
			if it.After == "nope" {
				return nil, proto.Errorf(proto.ErrNotFound, "nope is neither a branch of api nor a task on its schedule")
			}
			it.ID = "s9"
			if it.After == "api/model" {
				it.AfterBranch = it.After
			}
			return it, nil
		case proto.MethodProjectAdd:
			return proto.ProjectInfo{ID: "proj1", Name: "api", Git: true}, nil
		}
		return nil, proto.Errorf(proto.ErrUnknown, "?")
	})
	run := func(args ...string) (string, error) {
		var err error
		out, _ := a4Capture(t, "", func() { err = runTask(args) })
		return out, err
	}
	t.Chdir(t.TempDir())

	out, err := run("-after", " api/model ", "add", "the", "endpoint")
	if err != nil || !strings.Contains(out, "s9 waits for api/model to finish, then starts from its branch") {
		t.Fatalf("-after: %q %v", out, err)
	}
	var it proto.ScheduledItem
	srv.params(t, proto.MethodScheduleAdd, &it)
	if it.After != "api/model" || it.Task == nil || it.Task.Prompt != "add the endpoint" || it.Queue || it.At.IsZero() {
		t.Fatalf("scheduled %+v", it)
	}
	// With -queue it waits for both; with -base it starts there.
	out, err = run("-after", "s1a2b3c4", "-queue", "-base", "main", "x")
	if err != nil || !strings.Contains(out, "waits for s1a2b3c4 to finish, and fewer than 3 agents") || !strings.Contains(out, "starts from main") {
		t.Fatalf("-after -queue -base: %q %v", out, err)
	}
	if _, err := run("-after", "nope", "x"); err == nil || !strings.Contains(err.Error(), "neither a branch") {
		t.Fatalf("unknown: %v", err)
	}
	for _, args := range [][]string{
		{"-after", "main", "-every", "1d", "x"},
		{"-after", "main", "-n", "2", "x"},
		{"-after", "main", "-sandbox", "daytona", "x"},
	} {
		if _, err := run(args...); err == nil {
			t.Errorf("task %v was scheduled", args)
		}
	}

	// The list says what each waits for.
	items = []proto.ScheduledItem{
		{ID: "s1", At: time.Now(), After: "s0", Task: &proto.TaskCreateParams{Prompt: "add a client"}, Waiting: "waiting for s0 to start, then finish"},
		{ID: "s2", At: time.Now(), After: "s0", AfterBranch: "api/model", Task: &proto.TaskCreateParams{Prompt: "add docs"}},
	}
	var lsOut string
	lsOut, _ = a4Capture(t, "", func() { err = runScheduleCmd(nil) })
	if err != nil || !strings.Contains(lsOut, "task after s0: add a client [waiting: waiting for s0") || !strings.Contains(lsOut, "task after api/model: add docs") {
		t.Fatalf("ls:\n%s %v", lsOut, err)
	}

	// A server from before -after would start the task at once: refused.
	srv.setHello(func(int) proto.HelloResult {
		h := currentHello(0)
		h.Capabilities = slices.DeleteFunc(slices.Clone(h.Capabilities), func(c string) bool { return c == proto.CapTaskAfter })
		return h
	})
	if _, err := run("-after", "main", "x"); err == nil || !strings.Contains(err.Error(), "predates -after") {
		t.Fatalf("old server: %v", err)
	}
}
