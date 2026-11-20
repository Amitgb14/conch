package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/proto"
)

// TestAgentAccountsWithCloudHidden: an agent's accounts are no part of
// Conch Cloud, so with Cloud hidden — every release — conch account's
// subcommands still run, as a person types them, through main. Listing
// `account` among Cloud's commands once refused every one of them, which
// tests calling runAccount directly never saw.
func TestAgentAccountsWithCloudHidden(t *testing.T) {
	_, seen := accountServer(t)
	for _, args := range [][]string{{"account", "list"}, {"account", "ls"}, {"account"}} {
		code, out, errOut := a4RunMain(t, "", args...)
		if code != 0 || strings.Contains(errOut, "Conch Cloud") || !strings.Contains(out, "work") || !strings.Contains(out, "Failover at 90%") {
			t.Errorf("%v: %d %q %q", args, code, out, errOut)
		}
	}
	code, out, errOut := a4RunMain(t, "", "account", "add", "claude", "home")
	if code != 0 || strings.Contains(errOut, "Conch Cloud") || !strings.Contains(out, "claude account home") {
		t.Errorf("account add: %d %q %q", code, out, errOut)
	}
	if p := sent[struct{ Agent, Name string }](t, seen, "account.add"); p.Agent != "claude" || p.Name != "home" {
		t.Errorf("account add sent %+v", p)
	}
	for _, args := range [][]string{{"account", "pin", "/src/app", "work"}, {"account", "unpin", "/src/app"}, {"account", "failover", "90"},
		{"account", "remove", "claude", "home"}, {"account", "rm", "claude", "home"}} {
		if code, _, errOut := a4RunMain(t, "", args...); code != 0 || strings.Contains(errOut, "Conch Cloud") {
			t.Errorf("%v: %d %q", args, code, errOut)
		}
	}
	// Cloud's own account is still refused, and says how to turn it on.
	code, _, errOut = a4RunMain(t, "", "account", "-json")
	if code != 1 || !strings.Contains(errOut, "Conch Cloud is not enabled") {
		t.Errorf("account -json: %d %q", code, errOut)
	}
}

// TestAgentAccountsInHelpWithCloudHidden: help and completion offer an
// agent's accounts with Cloud hidden, and Cloud's own account only once
// it is on.
func TestAgentAccountsInHelpWithCloudHidden(t *testing.T) {
	a4Env(t)
	for name, text := range map[string]string{"help all": fullUsage(), "help account": commandHelp("account")} {
		for _, want := range []string{"conch account list", "conch account pin", "conch account failover"} {
			if !strings.Contains(text, want) {
				t.Errorf("%s has no %q:\n%s", name, want, text)
			}
		}
		if strings.Contains(text, cloudAccountEntry) || strings.Contains(text, "Conch Cloud") {
			t.Errorf("%s shows Cloud's account with Cloud hidden:\n%s", name, text)
		}
	}
	if !slices.Contains(compWords(compCandidates(nil, "")), "account") {
		t.Error("conch <tab> does not offer account")
	}
	if got := compWords(compCandidates([]string{"account"}, "")); !slices.Contains(got, "failover") || !slices.Contains(got, "pin") {
		t.Errorf("conch account <tab> offers %v", got)
	}

	t.Setenv("CONCH_CLOUD", "1")
	if help := commandHelp("account"); !strings.Contains(help, cloudAccountEntry) || !strings.Contains(help, "conch account list") {
		t.Errorf("on: help account:\n%s", help)
	}
	if err := cloudGate("account", nil); err != nil {
		t.Errorf("on: bare account refused: %v", err)
	}
	if cloudAccount([]string{"list"}) || !cloudAccount(nil) || !cloudAccount([]string{"-json"}) {
		t.Error("on: which account is Cloud's")
	}
}

// TestFailoverForOneAgent: -agent sends that agent's line, default takes it
// away, the list says which agents have one, and an older server — which
// would take an agent's line as everyone's — is not asked.
func TestFailoverForOneAgent(t *testing.T) {
	srv, seen := accountServer(t)
	for _, c := range []struct {
		args []string
		want proto.AccountFailoverParams
	}{
		{[]string{"failover", "85", "-agent", "codex"}, proto.AccountFailoverParams{At: 85, Agent: "codex"}},
		{[]string{"failover", "-agent", "claude", "off"}, proto.AccountFailoverParams{Agent: "claude"}},
		{[]string{"failover", "default", "-agent", "codex"}, proto.AccountFailoverParams{Agent: "codex", Shared: true}},
		{[]string{"failover", "90%"}, proto.AccountFailoverParams{At: 90}},
	} {
		if _, _, err := a4Out(t, func() error { return runAccount(c.args) }); err != nil {
			t.Fatalf("%v: %v", c.args, err)
		}
		if p := sent[proto.AccountFailoverParams](t, seen, proto.MethodAccountFailover); p != c.want {
			t.Errorf("%v sent %+v, want %+v", c.args, p, c.want)
		}
	}
	for _, args := range [][]string{{"failover", "default"}, {"failover", "85", "90", "-agent", "codex"}, {"failover", "-agent", "codex"}} {
		if _, _, err := a4Out(t, func() error { return runAccount(args) }); err == nil {
			t.Errorf("%v: no error", args)
		}
	}
	// The list names each agent's own line.
	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		return proto.AccountList{Agents: []string{"claude", "codex"}, FailoverAt: 80, FailoverFor: map[string]int{"codex": 90, "claude": 0},
			Accounts: []proto.AccountInfo{{Agent: "claude", Default: true}, {Agent: "codex", Default: true}}}, nil
	})
	out, _, err := a4Out(t, func() error { return runAccount([]string{"list"}) })
	if err != nil || !strings.Contains(out, "Failover at 80%") || !strings.Contains(out, "codex's own: at 90%") || !strings.Contains(out, "claude's own: off") {
		t.Errorf("list: %v\n%s", err, out)
	}
	if strings.Index(out, "claude's own") > strings.Index(out, "codex's own") {
		t.Errorf("lines not in the agents' order:\n%s", out)
	}
}

func TestFailoverForOneAgentOnAnOlderServer(t *testing.T) {
	srv, seen := accountServer(t)
	srv.setHello(func(n int) proto.HelloResult {
		h := currentHello(n)
		var caps []string
		for _, c := range h.Capabilities {
			if c != proto.CapAccountFailoverAgent {
				caps = append(caps, c)
			}
		}
		h.Capabilities = caps
		return h
	})
	_, _, err := a4Out(t, func() error { return runAccount([]string{"failover", "85", "-agent", "codex"}) })
	if err == nil || !strings.Contains(err.Error(), "one failover line for every agent") {
		t.Errorf("older server: %v", err)
	}
	if _, ok := seen.Get()[proto.MethodAccountFailover]; ok {
		t.Error("an agent's line was sent to a server that would make it everyone's")
	}
	// Every agent's line still goes.
	if _, _, err := a4Out(t, func() error { return runAccount([]string{"failover", "85"}) }); err != nil {
		t.Errorf("shared line on an older server: %v", err)
	}
}
