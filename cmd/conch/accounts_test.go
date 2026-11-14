package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
)

// accountServer is a fake server that answers account.* and records what
// each call was sent.
func accountServer(t *testing.T) (*a4Server, *a4Var[map[string]json.RawMessage]) {
	t.Helper()
	a4Env(t)
	srv := startA4Server(t, config.SocketPath())
	var seen a4Var[map[string]json.RawMessage]
	seen.Set(map[string]json.RawMessage{})
	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		m := seen.Get()
		m[msg.Method] = msg.Params
		seen.Set(m)
		switch msg.Method {
		case proto.MethodAccountList:
			soon := time.Now().Add(90 * time.Minute)
			return proto.AccountList{Agents: []string{"claude", "codex"}, FailoverAt: 90,
				Accounts: []proto.AccountInfo{
					{Agent: "claude", Default: true, Agents: 1, Limits: &proto.PlanLimits{Agent: "claude", FiveHour: &proto.LimitWindow{UsedPct: 42, ResetsAt: soon}, Week: &proto.LimitWindow{UsedPct: 18}}},
					{Agent: "claude", Name: "work", Dir: "/h/.conch/accounts/claude/work", Agents: 2, Limits: &proto.PlanLimits{Agent: "claude", Account: "work", FiveHour: &proto.LimitWindow{UsedPct: 97}}},
					{Agent: "codex", Default: true},
				},
				Pins: []proto.AccountPin{{Path: "/src/app", Account: "work"}}}, nil
		case proto.MethodAccountAdd:
			var p proto.AccountAddParams
			json.Unmarshal(msg.Params, &p)
			if p.Agent == "gemini" {
				return nil, proto.Errorf(proto.ErrBadRequest, "gemini keeps one account")
			}
			return proto.AccountInfo{Agent: p.Agent, Name: p.Name, Dir: "/h/.conch/accounts/" + p.Agent + "/" + p.Name}, nil
		case proto.MethodProjectAdd:
			return proto.ProjectInfo{ID: "api", Name: "api", Path: "/src/api", Git: true, Base: "main"}, nil
		case proto.MethodTaskCreate:
			return proto.PaneInfo{ID: "p3", Cwd: "/src/api.worktrees/x", Branch: "x"}, nil
		case proto.MethodPaneCreate:
			return proto.PaneInfo{ID: "p4"}, nil
		}
		return nil, nil
	})
	return srv, &seen
}

func sent[T any](t *testing.T, seen *a4Var[map[string]json.RawMessage], method string) T {
	t.Helper()
	var v T
	raw, ok := seen.Get()[method]
	if !ok {
		t.Fatalf("%s was never called", method)
	}
	json.Unmarshal(raw, &v)
	return v
}

func TestAccountCommands(t *testing.T) {
	_, seen := accountServer(t)

	out, _, err := a4Out(t, func() error { return runAccount([]string{"list"}) })
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"claude", "default", "5h 42%", "7d 18%", "work", "5h 97%", "/h/.conch/accounts/claude/work", "2 agents", "codex",
		"/src/app", "→ work", "Failover at 90%"} {
		if !strings.Contains(out, want) {
			t.Errorf("list has no %q:\n%s", want, out)
		}
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "codex") && strings.Contains(line, "%") {
			t.Errorf("codex, which reported nothing, shows usage: %q", line)
		}
	}

	out, _, err = a4Out(t, func() error { return runAccount([]string{"add", "claude", "work", "-dir", "/x/y"}) })
	if err != nil || !strings.Contains(out, "/h/.conch/accounts/claude/work") || !strings.Contains(out, "conch new -agent claude -account work") {
		t.Fatalf("add: %q %v", out, err)
	}
	if p := sent[proto.AccountAddParams](t, seen, proto.MethodAccountAdd); p != (proto.AccountAddParams{Agent: "claude", Name: "work", Dir: "/x/y"}) {
		t.Errorf("add sent %+v", p)
	}
	if _, _, err := a4Out(t, func() error { return runAccount([]string{"add", "gemini", "work"}) }); err == nil || !strings.Contains(err.Error(), "one account") {
		t.Errorf("server refusal: %v", err)
	}

	if _, _, err := a4Out(t, func() error { return runAccount([]string{"remove", "claude", "work"}) }); err != nil {
		t.Fatal(err)
	}
	if p := sent[proto.AccountRef](t, seen, proto.MethodAccountRemove); p != (proto.AccountRef{Agent: "claude", Name: "work"}) {
		t.Errorf("remove sent %+v", p)
	}

	// pin: the current folder, made absolute, or the one named.
	cwd := t.TempDir()
	t.Chdir(cwd)
	if _, _, err := a4Out(t, func() error { return runAccount([]string{"pin", "work"}) }); err != nil {
		t.Fatal(err)
	}
	if p := sent[proto.AccountPinParams](t, seen, proto.MethodAccountPin); p.Path != cwd || p.Account != "work" {
		t.Errorf("pin sent %+v", p)
	}
	if _, _, err := a4Out(t, func() error { return runAccount([]string{"pin", "/src/app", "home"}) }); err != nil {
		t.Fatal(err)
	}
	if p := sent[proto.AccountPinParams](t, seen, proto.MethodAccountPin); p.Path != "/src/app" || p.Account != "home" {
		t.Errorf("pin with a path sent %+v", p)
	}
	if _, _, err := a4Out(t, func() error { return runAccount([]string{"unpin"}) }); err != nil {
		t.Fatal(err)
	}
	if p := sent[proto.AccountPinParams](t, seen, proto.MethodAccountPin); p.Path != cwd || p.Account != "" {
		t.Errorf("unpin sent %+v", p)
	}

	for arg, want := range map[string]int{"90": 90, "off": 0, "0": 0, "100%": 100} {
		if _, _, err := a4Out(t, func() error { return runAccount([]string{"failover", arg}) }); err != nil {
			t.Fatalf("failover %s: %v", arg, err)
		}
		if p := sent[proto.AccountFailoverParams](t, seen, proto.MethodAccountFailover); p.At != want {
			t.Errorf("failover %s sent %+v", arg, p)
		}
	}

	for _, bad := range [][]string{
		{"add", "claude"}, {"add"}, {"remove", "claude"}, {"pin"}, {"pin", "a", "b", "c"}, {"unpin", "a", "b"},
		{"failover"}, {"failover", "lots"}, {"failover", "101"}, {"failover", "-5"}, {"frobnicate"},
	} {
		if _, _, err := a4Out(t, func() error { return runAccount(bad) }); err == nil {
			t.Errorf("account %v gave no error", bad)
		}
	}
}

// An older server has no accounts: said, and nothing sent.
func TestAccountCommandsOnAnOlderServer(t *testing.T) {
	srv, seen := accountServer(t)
	srv.setHello(func(n int) proto.HelloResult {
		h := currentHello(n)
		h.Capabilities = nil
		return h
	})
	for _, args := range [][]string{{"list"}, {"add", "claude", "work"}, {"failover", "90"}} {
		if _, _, err := a4Out(t, func() error { return runAccount(args) }); err == nil || !strings.Contains(err.Error(), "server reload") {
			t.Errorf("account %v on an older server: %v", args, err)
		}
	}
	if _, _, err := a4Out(t, func() error { return runNew([]string{"-agent", "claude", "-account", "work"}) }); err == nil || !strings.Contains(err.Error(), "server reload") {
		t.Errorf("new -account on an older server: %v", err)
	}
	if _, _, err := a4Out(t, func() error { return runTask([]string{"-cwd", t.TempDir(), "-account", "work", "fix"}) }); err == nil || !strings.Contains(err.Error(), "server reload") {
		t.Errorf("task -account on an older server: %v", err)
	}
	for _, m := range []string{proto.MethodAccountAdd, proto.MethodPaneCreate, proto.MethodTaskCreate} {
		if _, ok := seen.Get()[m]; ok {
			t.Errorf("%s was sent to a server that has no accounts", m)
		}
	}
}

func TestNewAndTaskTakeAnAccount(t *testing.T) {
	_, seen := accountServer(t)
	if _, _, err := a4Out(t, func() error { return runNew([]string{"-agent", "claude", "-account", "work"}) }); err != nil {
		t.Fatal(err)
	}
	if p := sent[proto.PaneCreateParams](t, seen, proto.MethodPaneCreate); p.Agent != "claude" || p.Account != "work" {
		t.Errorf("new sent %+v", p)
	}
	if _, _, err := a4Out(t, func() error { return runNew([]string{"-account", "work"}) }); err == nil || !strings.Contains(err.Error(), "-agent") {
		t.Errorf("-account without an agent: %v", err)
	}

	if _, _, err := a4Out(t, func() error {
		return runTask([]string{"-cwd", t.TempDir(), "-agent", "claude", "-account", "work", "fix it"})
	}); err != nil {
		t.Fatal(err)
	}
	if p := sent[proto.TaskCreateParams](t, seen, proto.MethodTaskCreate); p.Agent != "claude" || p.Account != "work" {
		t.Errorf("task sent %+v", p)
	}
	if _, _, err := a4Out(t, func() error {
		return runTask([]string{"-cwd", t.TempDir(), "-account", "work", "-sandbox", "daytona", "fix it"})
	}); err == nil || !strings.Contains(err.Error(), "-account") {
		t.Errorf("-account in a sandbox: %v", err)
	}
}
