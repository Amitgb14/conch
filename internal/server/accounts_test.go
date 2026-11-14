package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
)

// startOn starts a stand-in for agent that writes the folder variable it
// was given into a file of its own, and waits for that file.
func startOn(t *testing.T, c *client.Client, cp proto.PaneCreateParams) (proto.PaneInfo, string) {
	t.Helper()
	out := filepath.Join(t.TempDir(), "env")
	cp.Command = []string{"/bin/sh", "-c", `printf 'claude=[%s] codex=[%s]\n' "$CLAUDE_CONFIG_DIR" "$CODEX_HOME" > "$0"; exec cat`, out}
	if cp.Cols == 0 {
		cp.Cols, cp.Rows = 80, 10
	}
	var info proto.PaneInfo
	if err := c.Call(t.Context(), proto.MethodPaneCreate, cp, &info); err != nil {
		t.Fatal(err)
	}
	return info, waitRan(t, out, "]\n")
}

func accountList(t *testing.T, c *client.Client) proto.AccountList {
	t.Helper()
	var l proto.AccountList
	if err := c.Call(t.Context(), proto.MethodAccountList, nil, &l); err != nil {
		t.Fatal(err)
	}
	return l
}

func badRequest(t *testing.T, err error, what string) {
	t.Helper()
	var perr *proto.Error
	if !errors.As(err, &perr) || perr.Code != proto.ErrBadRequest {
		t.Errorf("%s: %v, want a bad request", what, err)
	}
}

// Accounts over the wire: add one, start an agent on it by name and by a
// pin, and the agent is given its account's folder and says which it is.
func TestAccountsOverTheSocket(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")
	c, dir := startServer(t)
	if miss := c.MissingCapabilities([]string{proto.CapAccounts}); len(miss) > 0 {
		t.Fatalf("not announced: %v", miss)
	}
	l := accountList(t, c)
	if len(l.Accounts) != 2 || !l.Accounts[0].Default || l.Accounts[0].Agent != "claude" || l.Accounts[1].Agent != "codex" {
		t.Fatalf("a fresh machine lists %+v", l.Accounts)
	}
	if len(l.Agents) != 2 {
		t.Errorf("agents that can have accounts: %v", l.Agents)
	}

	var acc proto.AccountInfo
	if err := c.Call(t.Context(), proto.MethodAccountAdd, proto.AccountAddParams{Agent: "claude", Name: "work"}, &acc); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "accounts", "claude", "work")
	if acc.Dir != want {
		t.Errorf("account folder %q, want %q", acc.Dir, want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Errorf("folder not made: %v", err)
	}
	badRequest(t, c.Call(t.Context(), proto.MethodAccountAdd, proto.AccountAddParams{Agent: "gemini", Name: "work"}, nil), "gemini account")
	badRequest(t, c.Call(t.Context(), proto.MethodAccountAdd, proto.AccountAddParams{Agent: "claude", Name: "work"}, nil), "same name twice")

	// Its own account: nothing set, nothing said.
	info, env := startOn(t, c, proto.PaneCreateParams{Agent: "claude", Cwd: dir})
	if info.Account != "" || env != "claude=[] codex=[]\n" {
		t.Errorf("default account: %q, %q", info.Account, env)
	}
	// By name.
	info, env = startOn(t, c, proto.PaneCreateParams{Agent: "claude", Account: "work", Cwd: dir})
	if info.Account != "work" || env != "claude=["+want+"] codex=[]\n" {
		t.Errorf("work account: %q, %q", info.Account, env)
	}
	badRequest(t, c.Call(t.Context(), proto.MethodPaneCreate, proto.PaneCreateParams{Agent: "claude", Account: "nope", Cwd: dir, Command: []string{"/bin/cat"}}, nil), "unknown account")
	badRequest(t, c.Call(t.Context(), proto.MethodPaneCreate, proto.PaneCreateParams{Account: "work", Cwd: dir, Command: []string{"/bin/cat"}}, nil), "an account for no agent")

	// By a pin, for the folder and below it; Codex has no "work", so it
	// keeps its own.
	sub := filepath.Join(dir, "app", "cmd")
	os.MkdirAll(sub, 0o755)
	if err := c.Call(t.Context(), proto.MethodAccountPin, proto.AccountPinParams{Path: filepath.Join(dir, "app"), Account: "work"}, nil); err != nil {
		t.Fatal(err)
	}
	info, env = startOn(t, c, proto.PaneCreateParams{Agent: "claude", Cwd: sub})
	if info.Account != "work" || env != "claude=["+want+"] codex=[]\n" {
		t.Errorf("pinned: %q, %q", info.Account, env)
	}
	info, env = startOn(t, c, proto.PaneCreateParams{Agent: "codex", Cwd: sub})
	if info.Account != "" || env != "claude=[] codex=[]\n" {
		t.Errorf("codex in a folder pinned to an account it lacks: %q, %q", info.Account, env)
	}
	// Asked for by name beats the pin; "default" is the agent's own.
	info, env = startOn(t, c, proto.PaneCreateParams{Agent: "claude", Account: "default", Cwd: sub})
	if info.Account != "" || env != "claude=[] codex=[]\n" {
		t.Errorf("default over a pin: %q, %q", info.Account, env)
	}
	// A plain shell is no agent: no account, pinned folder or not.
	info, env = startOn(t, c, proto.PaneCreateParams{Cwd: sub})
	if info.Account != "" || env != "claude=[] codex=[]\n" {
		t.Errorf("a shell: %q, %q", info.Account, env)
	}

	l = accountList(t, c)
	if len(l.Pins) != 1 || l.Pins[0].Account != "work" || len(l.Accounts) != 3 {
		t.Errorf("list after: %+v", l)
	}
	var work *proto.AccountInfo
	for i := range l.Accounts {
		if l.Accounts[i].Name == "work" {
			work = &l.Accounts[i]
		}
	}
	if work == nil || work.Agents != 2 {
		t.Errorf("work account runs %+v, want 2 agents", work)
	}

	// Kept: a new server reads the same book.
	b, err := os.ReadFile(filepath.Join(dir, "accounts.json"))
	if err != nil || !json.Valid(b) {
		t.Fatalf("accounts.json: %v %s", err, b)
	}

	if err := c.Call(t.Context(), proto.MethodAccountFailover, proto.AccountFailoverParams{At: 90}, nil); err != nil {
		t.Fatal(err)
	}
	badRequest(t, c.Call(t.Context(), proto.MethodAccountFailover, proto.AccountFailoverParams{At: 101}, nil), "failover past 100")
	badRequest(t, c.Call(t.Context(), proto.MethodAccountFailover, proto.AccountFailoverParams{At: -1}, nil), "negative failover")
	if l := accountList(t, c); l.FailoverAt != 90 {
		t.Errorf("failover %d", l.FailoverAt)
	}

	if err := c.Call(t.Context(), proto.MethodAccountRemove, proto.AccountRef{Agent: "claude", Name: "work"}, nil); err != nil {
		t.Fatal(err)
	}
	badRequest(t, c.Call(t.Context(), proto.MethodAccountRemove, proto.AccountRef{Agent: "claude", Name: "work"}, nil), "removed twice")
	if l := accountList(t, c); len(l.Accounts) != 2 || len(l.Pins) != 0 {
		t.Errorf("after remove: %+v", l)
	}
	if _, err := os.Stat(want); err != nil {
		t.Errorf("removing an account deleted its folder: %v", err)
	}
}

// Limits are the account's: a status line from an agent on "work" is
// work's, and the agent's own account keeps what it had.
func TestLimitsPerAccount(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	c, dir := startServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := c.Call(ctx, proto.MethodAccountAdd, proto.AccountAddParams{Agent: "claude", Name: "work"}, nil); err != nil {
		t.Fatal(err)
	}
	own, _ := startOn(t, c, proto.PaneCreateParams{Agent: "claude", Cwd: dir})
	work, _ := startOn(t, c, proto.PaneCreateParams{Agent: "claude", Account: "work", Cwd: dir})
	report := func(id string, pct float64) {
		t.Helper()
		if err := c.Call(ctx, proto.MethodAgentReport, proto.AgentReportParams{ID: id, Agent: "claude", Event: "StatusLine",
			Limits: &proto.PlanLimits{FiveHour: &proto.LimitWindow{UsedPct: pct}}}, nil); err != nil {
			t.Fatal(err)
		}
	}
	report(own.ID, 10)
	report(work.ID, 95)
	waitEvent(t, c, func(m proto.Message) bool {
		var l proto.PlanLimits
		return m.Event == proto.EventAgentLimits && json.Unmarshal(m.Data, &l) == nil && l.Account == "work" && l.FiveHour.UsedPct == 95
	})
	var res proto.AgentLimitsResult
	if err := c.Call(ctx, proto.MethodAgentLimits, nil, &res); err != nil {
		t.Fatal(err)
	}
	got := map[string]float64{}
	for _, l := range res.Limits {
		got[l.Agent+"/"+l.Account] = l.FiveHour.UsedPct
	}
	if len(got) != 2 || got["claude/"] != 10 || got["claude/work"] != 95 {
		t.Fatalf("limits: %v", got)
	}
	for _, a := range accountList(t, c).Accounts {
		if a.Agent == "claude" && (a.Limits == nil || a.Limits.FiveHour.UsedPct != map[string]float64{"": 10, "work": 95}[a.Name]) {
			t.Errorf("account %q lists limits %+v", a.Name, a.Limits)
		}
	}

	// With failover at 90, a new agent pinned nowhere starts on its own
	// account (10%); pinned to work (95%), it moves to the next one.
	if err := c.Call(ctx, proto.MethodAccountFailover, proto.AccountFailoverParams{At: 90}, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(ctx, proto.MethodAccountPin, proto.AccountPinParams{Path: dir, Account: "work"}, nil); err != nil {
		t.Fatal(err)
	}
	info, env := startOn(t, c, proto.PaneCreateParams{Agent: "claude", Cwd: dir})
	if info.Account != "" || env != "claude=[] codex=[]\n" {
		t.Errorf("failover from a full work account: %q %q", info.Account, env)
	}
	// Asked for by name, it is what was asked for.
	if info, _ := startOn(t, c, proto.PaneCreateParams{Agent: "claude", Account: "work", Cwd: dir}); info.Account != "work" {
		t.Errorf("asked for work, got %q", info.Account)
	}
}

// A task starts its agent on the account asked for, and one that cannot
// be had is refused before a worktree is made.
func TestTaskOnAnAccount(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	c, dir, proj := setupRepo(t)
	// The agent: a stand-in claude where the adapter's PATH looks first.
	bin := filepath.Join(dir, ".local", "bin")
	os.MkdirAll(bin, 0o755)
	out := filepath.Join(dir, "ran")
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\nprintf 'claude=[%s]\\n' \"$CLAUDE_CONFIG_DIR\" > "+out+"\nexec cat\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	var acc proto.AccountInfo
	if err := c.Call(t.Context(), proto.MethodAccountAdd, proto.AccountAddParams{Agent: "claude", Name: "work"}, &acc); err != nil {
		t.Fatal(err)
	}

	err := c.Call(t.Context(), proto.MethodTaskCreate, proto.TaskCreateParams{ProjectID: proj.ID, Prompt: "x", Branch: "nope-task", Agent: "claude", Account: "nope"}, nil)
	badRequest(t, err, "a task on an unknown account")
	if _, err := os.Stat(filepath.Join(dir, "api.worktrees", "nope-task")); err == nil {
		t.Error("a refused account left a worktree behind")
	}
	if b, _ := exec.Command("git", "-C", filepath.Join(dir, "api"), "branch", "--list", "nope-task").Output(); len(b) > 0 {
		t.Error("a refused account left a branch behind")
	}

	var info proto.PaneInfo
	if err := c.Call(t.Context(), proto.MethodTaskCreate, proto.TaskCreateParams{ProjectID: proj.ID, Prompt: "fix it", Branch: "fix", Agent: "claude", Account: "work", Cols: 80, Rows: 10}, &info); err != nil {
		t.Fatal(err)
	}
	if info.Account != "work" {
		t.Errorf("task pane on %q", info.Account)
	}
	if got := waitRan(t, out, "]\n"); got != "claude=["+acc.Dir+"]\n" {
		t.Errorf("task agent saw %q", got)
	}
}

// A session lives in its account's folder: the list says whose it is, and
// resuming it — with the account or, as an older client does, without —
// starts on that account even where the folder is pinned to another.
func TestResumeOnTheSessionsAccount(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	c, dir := startServer(t)
	t.Setenv("HOME", dir)
	t.Setenv("SHELL", "/bin/sh")
	bin := filepath.Join(dir, ".local", "bin")
	os.MkdirAll(bin, 0o755)
	out := filepath.Join(dir, "ran")
	os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\nprintf 'claude=[%s] %s\\n' \"$CLAUDE_CONFIG_DIR\" \"$*\" >> "+out+"\nexec cat\n"), 0o755)
	var work, home proto.AccountInfo
	c.Call(t.Context(), proto.MethodAccountAdd, proto.AccountAddParams{Agent: "claude", Name: "work"}, &work)
	c.Call(t.Context(), proto.MethodAccountAdd, proto.AccountAddParams{Agent: "claude", Name: "home"}, &home)
	proj := filepath.Join(dir, "app")
	os.MkdirAll(proj, 0o755)
	enc := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, proj)
	session := filepath.Join(work.Dir, "projects", enc, "s-work.jsonl")
	os.MkdirAll(filepath.Dir(session), 0o755)
	os.WriteFile(session, []byte(`{"type":"user","cwd":"`+proj+`","message":{"role":"user","content":"on work"}}`+"\n"), 0o644)
	c.Call(t.Context(), proto.MethodAccountPin, proto.AccountPinParams{Path: proj, Account: "home"}, nil)

	var list proto.SessionList
	if err := c.Call(t.Context(), proto.MethodSessionList, proto.SessionListParams{Dir: proj}, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Sessions) != 1 || list.Sessions[0].Account != "work" {
		t.Fatalf("sessions %+v", list.Sessions)
	}
	var info proto.PaneInfo
	if err := c.Call(t.Context(), proto.MethodSessionResume, proto.SessionRef{Agent: "claude", ID: "s-work", Dir: proj, Cols: 80, Rows: 10}, &info); err != nil {
		t.Fatal(err)
	}
	if info.Account != "work" {
		t.Errorf("resumed on %q", info.Account)
	}
	if got := waitRan(t, out, "--resume s-work\n"); !strings.Contains(got, "claude=["+work.Dir+"]") {
		t.Errorf("resumed as %q", got)
	}
}
