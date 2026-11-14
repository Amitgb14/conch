package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
)

// Each account has a plan of its own: a report from one leaves the
// other's alone, the status bar names the account of the agent in view,
// and the limits window lists every account.
func TestLimitsPerAccountInTheTUI(t *testing.T) {
	m := a2Model()
	now := time.Now()
	mach := m.machines[0]
	mach.panes[0].Account = "work"
	m.rows = []row{{id: "pane:p1", kind: kindPane, machine: localMachine, paneID: "p1"}, {id: "pane:p2", kind: kindPane, machine: localMachine, paneID: "p2"}}
	m.cursor = "pane:p1"
	mach.setLimits(proto.PlanLimits{Agent: "claude", FiveHour: &proto.LimitWindow{UsedPct: 12}})
	mach.setLimits(proto.PlanLimits{Agent: "claude", Account: "work", FiveHour: &proto.LimitWindow{UsedPct: 88}})
	if len(mach.limits) != 2 {
		t.Fatalf("one account's report replaced the other's: %+v", mach.limits)
	}

	// The agent in view is on work: its chip is work's.
	if got := ansi.Strip(m.statusLimits(now)[0].text); got != "Claude work 5h 88%" {
		t.Fatalf("chip for the work agent: %q", got)
	}
	// A shell pane falls back to the agent's own account, as before.
	m.cursor = "pane:p2"
	if got := ansi.Strip(m.statusLimits(now)[0].text); got != "Claude 5h 12%" {
		t.Fatalf("fallback chip: %q", got)
	}
	// An agent on an account that has reported nothing falls back too,
	// rather than showing another account's numbers as its own name.
	m.cursor = "pane:p1"
	mach.panes[0].Account = "home"
	if got := ansi.Strip(m.statusLimits(now)[0].text); got != "Claude 5h 12%" {
		t.Fatalf("chip for an account with no report: %q", got)
	}

	v := &limitsView{mid: localMachine, fill: 1}
	text := a2Plain(v.render(*m).lines)
	for _, want := range []string{"Claude Code", "Claude Code · work", "12%", "88%"} {
		if !strings.Contains(text, want) {
			t.Errorf("the limits window does not say %q:\n%s", want, text)
		}
	}
	for _, w := range []int{20, 40, 76} {
		m.width = w
		for _, line := range v.render(*m).lines {
			if ansi.StringWidth(line) > w {
				t.Errorf("width %d: %q is %d wide", w, ansi.Strip(line), ansi.StringWidth(line))
			}
		}
	}
}

// An alert says which account passed the line, and each account alerts
// on its own.
func TestLimitAlertsPerAccount(t *testing.T) {
	seen := map[string]int{}
	now := time.Now()
	own := limitCrossings(seen, localMachine, proto.PlanLimits{Agent: "claude", FiveHour: &proto.LimitWindow{UsedPct: 85}}, []int{80}, now)
	work := limitCrossings(seen, localMachine, proto.PlanLimits{Agent: "claude", Account: "work", FiveHour: &proto.LimitWindow{UsedPct: 85}}, []int{80}, now)
	if len(own) != 1 || len(work) != 1 {
		t.Fatalf("own %+v, work %+v: each account alerts once", own, work)
	}
	if got := work[0].text("", now); !strings.HasPrefix(got, "Claude work 5-hour limit 85% used") {
		t.Errorf("work's alert: %q", got)
	}
	if got := own[0].text("", now); !strings.HasPrefix(got, "Claude 5-hour limit") {
		t.Errorf("own account's alert: %q", got)
	}
}

// The machine page's plan block lists each account under its name.
func TestMachinePageLimitsPerAccount(t *testing.T) {
	m := a2Model()
	mach := m.machines[0]
	mach.setLimits(proto.PlanLimits{Agent: "claude", FiveHour: &proto.LimitWindow{UsedPct: 12}})
	mach.setLimits(proto.PlanLimits{Agent: "claude", Account: "work", FiveHour: &proto.LimitWindow{UsedPct: 88}})
	text := a2Plain(m.limitsLines(mach, 80))
	if !strings.Contains(text, "Claude Code plan usage") || !strings.Contains(text, "Claude Code · work plan usage") || !strings.Contains(text, "88%") {
		t.Fatalf("machine page:\n%s", text)
	}
}

// A session says whose account it is in, a search finds it by the
// account's name, and resuming it asks for that account.
func TestSessionsOnAnAccount(t *testing.T) {
	m, sv := a2SessionsModel()
	key := sessionsKey(localMachine, "r1")
	m.sessions[key].list[0].Account = "work"
	m.focus = focusMain
	w := 90
	lines := sv.render(*m, w, 20)
	out := a2Plain(lines)
	if !strings.Contains(out, "Claude Code · work  Refactor auth") {
		t.Fatalf("the account is not said:\n%s", out)
	}
	for i, l := range lines[3:] {
		if lw := ansi.StringWidth(l); lw != w {
			t.Fatalf("row %d is %d wide:\n%s", i, lw, out)
		}
	}
	if !sv.matches(m.sessions[key].list[0], []string{"work"}) || sv.matches(m.sessions[key].list[2], []string{"work"}) {
		t.Error("a search for the account's name")
	}

	c, peer := a1FakeClient(t)
	m.machines[0].c, m.machines[0].server = c, c.Server
	peer.setResult(proto.MethodSessionResume, proto.PaneInfo{ID: "p9"})
	a2Run(m.resumeSession(localMachine, "r1", m.sessions[key].list[0]))
	if p := lastParams[proto.SessionRef](t, peer, proto.MethodSessionResume); p.ID != "s1" || p.Account != "work" {
		t.Fatalf("resume sent %+v", p)
	}
}

// Settings → Agents → Agent accounts: every machine's accounts with their
// plan usage and agents, the pins, and failover, which a press changes.
func TestSettingsAccountsPage(t *testing.T) {
	m := a3Model(t)
	c, peer := a1FakeClient(t, proto.CapAccounts)
	m.machines[0].c, m.machines[0].server = c, c.Server
	m.machines = m.machines[:1]
	s := &settings{tab: 2}
	row, ok := settingItem{}, false
	for _, it := range s.agentItems(m) {
		if ansi.Strip(it.label) == "Agent accounts…" {
			row, ok = it, true
		}
	}
	if !ok || !row.page {
		t.Fatal("no Agent accounts… row on the Agents tab")
	}
	peer.setResult(proto.MethodAccountList, proto.AccountList{Agents: []string{"claude", "codex"}, FailoverAt: 0,
		Accounts: []proto.AccountInfo{
			{Agent: "claude", Default: true, Limits: &proto.PlanLimits{Agent: "claude", FiveHour: &proto.LimitWindow{UsedPct: 12}}},
			{Agent: "claude", Name: "work", Dir: "/h/accounts/claude/work", Agents: 2, Limits: &proto.PlanLimits{Agent: "claude", Account: "work", FiveHour: &proto.LimitWindow{UsedPct: 88}}},
			{Agent: "codex", Default: true},
		},
		Pins: []proto.AccountPin{{Path: "/src/app", Account: "work"}}})
	for _, msg := range a2Run(row.run(m)) {
		if am, ok := msg.(accountsMsg); ok {
			m.receiveAccounts(am)
		}
	}
	if s.page != "accounts" {
		t.Fatalf("page %q", s.page)
	}
	rows := a3Rows(s, m)
	for label, want := range map[string]string{
		"Claude Code":        "5h 12%",
		"Claude Code · work": "5h 88% · 2 agents",
		"Codex":              "",
		"/src/app":           "→ work",
		"Failover":           "off",
	} {
		got, ok := rows[label]
		if !ok || !strings.Contains(got, want) {
			t.Errorf("row %q = %q (there: %v), want %q\n%v", label, got, ok, want, rows)
		}
	}

	// A press on failover moves it on: off → 80%.
	peer.setResult(proto.MethodAccountFailover, nil)
	for _, it := range s.agentItems(m) {
		if ansi.Strip(it.label) == "Failover" {
			a2Run(it.run(m))
		}
	}
	if p := lastParams[proto.AccountFailoverParams](t, peer, proto.MethodAccountFailover); p.At != 80 {
		t.Errorf("failover sent %+v", p)
	}

}

func TestSettingsAccountsOlderServer(t *testing.T) {
	m := a3Model(t)
	c, _ := a1FakeClient(t)
	m.machines[0].c, m.machines[0].server = c, c.Server
	m.machines = m.machines[:1]
	s := &settings{tab: 2}
	s.openPage("accounts")
	m.loadAccounts(localMachine)
	if got := a3Rows(s, m)["local"]; !strings.Contains(got, "predates") {
		t.Fatalf("older server: %v", a3Rows(s, m))
	}
	if next := nextFailover(0); next != 80 || nextFailover(80) != 90 || nextFailover(90) != 95 || nextFailover(95) != 0 || nextFailover(42) != 80 {
		t.Error("failover cycle")
	}
}
