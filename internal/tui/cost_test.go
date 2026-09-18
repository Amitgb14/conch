package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
)

func a5Tokens(in, out int, cost float64) *proto.Tokens {
	return &proto.Tokens{Input: in, CacheRead: in / 2, Output: out, CostUSD: cost, Context: 30_000, ContextSize: 200_000}
}

// a5Model is a machine with two projects: api with two agents that report a
// cost and one that doesn't, and web with none.
func a5Model(t *testing.T) *Model {
	t.Helper()
	m, _ := a1Fixture(t, false)
	mach := m.machines[0]
	mach.projects = append(mach.projects, proto.ProjectInfo{ID: "r2", Name: "web", Path: "/src/web", Git: true, Base: "main"})
	mach.panes = []proto.PaneInfo{
		{ID: "p1", Name: "claude", State: proto.PaneRunning, ProjectID: "r1", Branch: "feat", Cwd: "/src/api-feat",
			Agent: &proto.AgentStatus{Name: "claude", State: proto.AgentWorking, Tokens: a5Tokens(120_000, 12_000, 1.40)}},
		{ID: "p2", Name: "codex", State: proto.PaneRunning, ProjectID: "r1", Branch: "feat", Cwd: "/src/api-feat",
			Agent: &proto.AgentStatus{Name: "codex", State: proto.AgentIdle, Tokens: a5Tokens(40_000, 3_000, 0)}},
		{ID: "p3", Name: "claude", State: proto.PaneRunning, ProjectID: "r1", Branch: "main", Cwd: "/src/api",
			Agent: &proto.AgentStatus{Name: "claude", State: proto.AgentIdle, Tokens: a5Tokens(10_000, 900, 0.35)}},
		{ID: "p4", Name: "zsh", State: proto.PaneRunning, ProjectID: "r1", Cwd: "/src/api"},
	}
	for _, id := range []string{"p1", "p2", "p3"} {
		mach.agents[id] = true
	}
	m.expanded = map[string]bool{}
	m.rebuild()
	return m
}

func TestCostSums(t *testing.T) {
	m := a5Model(t)
	proj := m.projectUsage(localMachine, "r1")
	if proj.agents != 3 || proj.output != 15_900 || proj.input != (120_000+60_000)+(40_000+20_000)+(10_000+5_000) {
		t.Fatalf("project usage: %+v", proj)
	}
	if proj.cost < 1.749 || proj.cost > 1.751 || proj.costFor != 2 {
		t.Fatalf("project cost: %+v", proj)
	}
	if got := proj.chip(); got != "$1.75" {
		t.Fatalf("chip %q", got)
	}
	if line := proj.line("usage"); line != "usage conch has seen from 3 agents · in 255k · out 15k · $1.75 reported by 2 of them" {
		t.Fatalf("line %q", line)
	}

	// A branch's own agents (the tree's branch rows are already busy, so
	// this is for the pages), and one that reports no cost at all.
	if u := m.branchUsage(localMachine, "r1", "feat"); u.agents != 2 || u.chip() != "$1.40" {
		t.Fatalf("branch usage: %+v", u)
	}
	noCost := usage{agents: 1, output: 3000}
	if got := noCost.chip(); got != "3.0k out" {
		t.Fatalf("no cost chip %q", got)
	}
	if line := noCost.line("usage"); !strings.Contains(line, "out 3.0k") || strings.Contains(line, "$") {
		t.Fatalf("no cost line %q", line)
	}

	// Nothing to show: terminals, agents without usage, unknown machines.
	for name, u := range map[string]usage{
		"empty":            {},
		"agent, no tokens": usageOf([]proto.PaneInfo{{ID: "x"}, {ID: "y", Agent: &proto.AgentStatus{}}}),
		"zeroed":           usageOf([]proto.PaneInfo{{ID: "z", Agent: &proto.AgentStatus{Tokens: &proto.Tokens{}}}}),
		"unknown machine":  m.projectUsage("nope", "r1"),
		"unknown project":  m.projectUsage(localMachine, "nope"),
	} {
		if !u.empty() || u.chip() != "" || u.line("usage") != "" || m.costChip(u) != "" {
			t.Fatalf("%s should show nothing: %+v", name, u)
		}
	}
	if u := m.machineUsage(localMachine); u.agents != 3 {
		t.Fatalf("machine usage: %+v", u)
	}
	if u := m.looseUsage(localMachine); !u.empty() {
		t.Fatalf("loose usage: %+v", u)
	}
}

func TestCostInTheTree(t *testing.T) {
	m := a5Model(t)
	for _, id := range []string{machineID(localMachine), workspaceID(localMachine), projectNodeID(localMachine, "r1"),
		sectionID(localMachine, "r1", "branches"), sectionID(localMachine, "r1", "agents")} {
		m.expanded[id] = true
	}
	m.rebuild()
	out := ansi.Strip(strings.Join(m.sidebarLines(38, 30), "\n"))
	t.Log("\n" + out)
	for _, want := range []string{"local", "api", "$1.75", "feat", "$1.40", "claude", "codex", "3.0k out"} {
		if !strings.Contains(out, want) {
			t.Fatalf("tree lacks %q:\n%s", want, out)
		}
	}
	// A project with no agents, and terminals, stay as they were.
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "web") && strings.Contains(line, "$") {
			t.Fatalf("a project with no agents shows a cost: %q", line)
		}
		if strings.Contains(line, "zsh") && strings.Contains(line, "$") {
			t.Fatalf("a terminal shows a cost: %q", line)
		}
	}
	// Rows never outgrow the sidebar, at any width.
	for _, w := range []int{20, 30, 38, 60} {
		for _, line := range m.sidebarLines(w, 30) {
			if ansi.StringWidth(line) > w {
				t.Fatalf("width %d: %q is %d wide", w, ansi.Strip(line), ansi.StringWidth(line))
			}
		}
	}
}

func TestCostOnPages(t *testing.T) {
	m := a5Model(t)
	proj := m.machines[0].projects[0]
	out := ansi.Strip(strings.Join(m.projectLines(localMachine, proj, 100), "\n"))
	t.Log("\n" + out)
	if !strings.Contains(out, "usage conch has seen from 3 agents") || !strings.Contains(out, "$1.40") {
		t.Fatalf("project page:\n%s", out)
	}
	mach := m.machines[0]
	mach.state = stateOnline
	page := ansi.Strip(strings.Join(m.machineLines(mach, 100, 30), "\n"))
	if !strings.Contains(page, "usage conch has seen from 3 agents") {
		t.Fatalf("machine page:\n%s", page)
	}
	// A machine with nothing running says nothing about usage.
	mach.panes = nil
	if page := ansi.Strip(strings.Join(m.machineLines(mach, 100, 30), "\n")); strings.Contains(page, "usage conch has seen") {
		t.Fatalf("idle machine page:\n%s", page)
	}
}

func TestCostFallsBackToTokens(t *testing.T) {
	// Codex and Gemini report no cost at all; an agent that has only read
	// still shows what it used.
	for name, tc := range map[string]struct {
		u    usage
		want string
	}{
		"cost":        {usage{agents: 1, input: 1000, output: 500, cost: 0.2, costFor: 1}, "$0.20"},
		"output only": {usage{agents: 1, input: 1000, output: 500}, "500 out"},
		"input only":  {usage{agents: 1, input: 120_000}, "120k in"},
		"nothing yet": {usage{agents: 1}, ""},
	} {
		if got := tc.u.chip(); got != tc.want {
			t.Errorf("%s: chip %q, want %q", name, got, tc.want)
		}
	}
	// A mixed project says how many of its agents the money covers.
	mixed := usage{agents: 3, input: 10, output: 10, cost: 1, costFor: 1}
	if mixed.reportsCost() {
		t.Error("one of three reporting is not all of them")
	}
	if line := mixed.line("usage"); !strings.Contains(line, "$1.00 reported by 1 of them") {
		t.Errorf("mixed line %q", line)
	}
	all := usage{agents: 2, output: 10, cost: 2, costFor: 2}
	if !all.reportsCost() || !strings.Contains(all.line("usage"), "$2.00 reported") || strings.Contains(all.line("usage"), "of them") {
		t.Errorf("all line %q", all.line("usage"))
	}
}

func a5Limits(m *Model, agent string, pct float64, resets time.Duration) {
	m.machines[0].setLimits(proto.PlanLimits{Agent: agent, At: time.Now(),
		FiveHour: &proto.LimitWindow{UsedPct: pct, ResetsAt: time.Now().Add(resets)}})
}

func TestLimitWarning(t *testing.T) {
	m := a5Model(t)
	now := time.Now()
	// Nothing known: no warning, ever. An old server reports no limits.
	for name, got := range map[string]string{
		"no limits":       m.limitWarning(localMachine, "claude", now),
		"unknown machine": m.limitWarning("nope", "claude", now),
		"no agent":        m.limitWarning(localMachine, "", now),
	} {
		if got != "" {
			t.Fatalf("%s warned: %q", name, got)
		}
	}
	a5Limits(m, "claude", 42, time.Hour)
	if got := m.limitWarning(localMachine, "claude", now); got != "" {
		t.Fatalf("under the threshold: %q", got)
	}
	if got := m.limitWarning(localMachine, "codex", now); got != "" {
		t.Fatalf("another agent's plan: %q", got)
	}
	a5Limits(m, "claude", 83.4, 47*time.Minute)
	// resetText rounds up to the next whole minute.
	want := "Claude's 5-hour limit is 83% used and resets in 48m. Starting more agents spends the rest of it faster."
	if got := m.limitWarning(localMachine, "claude", now); got != want {
		t.Fatalf("warning %q", got)
	}
	// Past the limit reads differently.
	a5Limits(m, "claude", 100, 20*time.Minute)
	if got := m.limitWarning(localMachine, "claude", now); !strings.Contains(got, "may get nothing done until then") {
		t.Fatalf("spent: %q", got)
	}
	// A window whose reset has passed is stale, not a warning.
	m.machines[0].setLimits(proto.PlanLimits{Agent: "claude", At: now.Add(-6 * time.Hour),
		FiveHour: &proto.LimitWindow{UsedPct: 99, ResetsAt: now.Add(-time.Minute)}})
	if got := m.limitWarning(localMachine, "claude", now); got != "" {
		t.Fatalf("stale window warned: %q", got)
	}
	// The worst live window wins, and a custom threshold is respected.
	m.machines[0].setLimits(proto.PlanLimits{Agent: "claude", At: now,
		FiveHour: &proto.LimitWindow{UsedPct: 81, ResetsAt: now.Add(time.Hour)},
		Week:     &proto.LimitWindow{UsedPct: 93, ResetsAt: now.Add(72 * time.Hour)}})
	if got := m.limitWarning(localMachine, "claude", now); !strings.Contains(got, "week limit is 93% used") {
		t.Fatalf("worst window: %q", got)
	}
	m.cfg.Notify.LimitAt = []int{95}
	if got := m.limitWarning(localMachine, "claude", now); got != "" {
		t.Fatalf("under a raised threshold: %q", got)
	}
}

func TestTaskDialogWarnsAboutLimits(t *testing.T) {
	m := a5Model(t)
	proj := m.machines[0].projects[0]
	text := func(d *dialog) string { return ansi.Strip(strings.Join(d.text, " ")) }

	d := newTaskDialog(*m, localMachine, proj)
	if strings.Contains(text(d), "⚠") {
		t.Fatalf("warned without limits: %q", text(d))
	}
	a5Limits(m, "claude", 96, 30*time.Minute)
	d = newTaskDialog(*m, localMachine, proj)
	if !strings.Contains(text(d), "⚠ Claude's 5-hour limit is 96% used") {
		t.Fatalf("no warning: %q", text(d))
	}
	if !strings.Contains(text(d), "Creates a branch and worktree") {
		t.Fatalf("lost the intro: %q", text(d))
	}

	// Typing another agent's name drops the warning: its plan is its own.
	a2Type(m, d, "fix the flaky test")
	d.update(m, a2Key("tab"))
	d.update(m, a2Key("tab"))
	d.update(m, a2Key("tab"))
	a2Type(m, d, "codex")
	if strings.Contains(text(d), "⚠") {
		t.Fatalf("codex warned from claude's plan: %q", text(d))
	}
	// And it comes back for claude, still only a warning: enter starts it.
	for i := 0; i < len("codex"); i++ {
		d.update(m, a2Key("backspace"))
	}
	a2Type(m, d, "claude")
	if !strings.Contains(text(d), "⚠ Claude's 5-hour limit") {
		t.Fatalf("warning did not return: %q", text(d))
	}
	if d.fields[1].in.Placeholder != "conch/fix-flaky-test" {
		t.Fatalf("branch placeholder %q", d.fields[1].in.Placeholder)
	}
	out := a2Plain(d.render(*m).lines)
	if !strings.Contains(out, "96% used") || !strings.Contains(out, "enter confirm") {
		t.Fatalf("dialog:\n%s", out)
	}
	t.Log("\n" + out)
}

func TestCostInTheSessionsList(t *testing.T) {
	m, sv := a2SessionsModel()
	m.sessions[sessionsKey(localMachine, "r1")] = &sessionsData{list: []proto.SessionInfo{
		{Agent: "claude", ID: "s1", Dir: "/src/api", Title: "Refactor auth", Updated: time.Now(), CostUSD: 1.4},
		{Agent: "codex", ID: "s2", Dir: "/src/api", Title: "Rate limiting", Updated: time.Now(), Output: 3_000},
		{Agent: "gemini", ID: "s3", Dir: "/src/api", Title: "Explain the flow", Updated: time.Now()},
	}}
	out := a2Plain(sv.render(*m, 100, 20))
	t.Log("\n" + out)
	for _, want := range []string{"Refactor auth", "$1.40", "Rate limiting", "3.0k out", "Explain the flow"} {
		if !strings.Contains(out, want) {
			t.Fatalf("sessions list lacks %q:\n%s", want, out)
		}
	}
	// A store that records nothing says nothing, rather than "$0.00".
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "Explain the flow") && (strings.Contains(line, "$") || strings.Contains(line, "out")) {
			t.Fatalf("gemini row invented usage: %q", line)
		}
	}
	// It survives narrow views.
	for _, w := range []int{40, 60, 100} {
		for _, l := range sv.render(*m, w, 20) {
			if ansi.StringWidth(l) > w {
				t.Fatalf("width %d: %q is %d wide", w, ansi.Strip(l), ansi.StringWidth(l))
			}
		}
	}
}

func TestSessionUsageShape(t *testing.T) {
	if got := sessionUsage(proto.SessionInfo{CostUSD: 0.5}); got.chip() != "$0.50" || !got.reportsCost() {
		t.Fatalf("cost: %+v", got)
	}
	if got := sessionUsage(proto.SessionInfo{Output: 900}); got.chip() != "900 out" || got.reportsCost() {
		t.Fatalf("tokens: %+v", got)
	}
	if got := sessionUsage(proto.SessionInfo{}); !got.empty() || got.chip() != "" {
		t.Fatalf("nothing: %+v", got)
	}
}

func TestWaitingFilter(t *testing.T) {
	m, _ := a1Fixture(t, false)
	mach := m.machines[0]
	mach.panes = []proto.PaneInfo{
		{ID: "p1", Name: "claude", State: proto.PaneRunning, ProjectID: "r1", Branch: "feat",
			Agent: &proto.AgentStatus{Name: "claude", State: proto.AgentBlocked}},
		{ID: "p2", Name: "codex", State: proto.PaneRunning, ProjectID: "r1",
			Agent: &proto.AgentStatus{Name: "codex", State: proto.AgentWorking}},
		{ID: "p3", Name: "gemini", State: proto.PaneRunning, ProjectID: "r1",
			Agent: &proto.AgentStatus{Name: "gemini", State: proto.AgentDone}},
		{ID: "p4", Name: "zsh", State: proto.PaneRunning, ProjectID: "r1"},
		{ID: "p5", Name: "claude", State: proto.PaneRunning, Cwd: "/tmp",
			Agent: &proto.AgentStatus{Name: "claude", State: proto.AgentBlocked}},
	}
	m.filter = waitingFilter
	m.rebuild()
	out := ansi.Strip(strings.Join(m.sidebarLines(40, 30), "\n"))
	t.Log("\n" + out)
	// Blocked and done agents, wherever they are; nothing else.
	for _, want := range []string{"agents waiting for you", "claude", "gemini"} {
		if !strings.Contains(out, want) {
			t.Fatalf("waiting filter lacks %q:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"codex", "zsh", "Branches", "Sessions"} {
		if strings.Contains(out, unwanted) {
			t.Fatalf("waiting filter shows %q:\n%s", unwanted, out)
		}
	}
	if n := strings.Count(out, "claude"); n != 2 { // one in a project, one outside
		t.Fatalf("%d claude rows:\n%s", n, out)
	}
	// Nothing waiting: the tree empties rather than showing everything.
	for i := range mach.panes {
		if mach.panes[i].Agent != nil {
			mach.panes[i].Agent.State = proto.AgentWorking
		}
	}
	m.rebuild()
	out = ansi.Strip(strings.Join(m.sidebarLines(40, 30), "\n"))
	for _, unwanted := range []string{"claude", "gemini", "zsh"} {
		if strings.Contains(out, unwanted) {
			t.Fatalf("nothing waiting, yet %q shows:\n%s", unwanted, out)
		}
	}
	// A plain filter still matches by name.
	m.filter = "zsh"
	m.rebuild()
	if out := ansi.Strip(strings.Join(m.sidebarLines(40, 30), "\n")); !strings.Contains(out, "zsh") {
		t.Fatalf("name filter:\n%s", out)
	}
}

func TestCostCanBeTurnedOff(t *testing.T) {
	m := a5Model(t)
	mach := m.machines[0]
	mach.state = stateOnline
	for _, id := range []string{machineID(localMachine), workspaceID(localMachine), projectNodeID(localMachine, "r1"),
		sectionID(localMachine, "r1", "agents")} {
		m.expanded[id] = true
	}
	m.sessions = map[string]*sessionsData{sessionsKey(localMachine, "r1"): {list: []proto.SessionInfo{
		{Agent: "claude", ID: "s1", Dir: "/src/api", Title: "Refactor auth", Updated: time.Now(), CostUSD: 1.4}}}}
	sv := &sessionsView{machine: localMachine, projectID: "r1"}
	proj := mach.projects[0]

	// On by default, everywhere.
	shown := []string{
		ansi.Strip(strings.Join(m.sidebarLines(40, 30), "\n")),
		ansi.Strip(strings.Join(m.projectLines(localMachine, proj, 100), "\n")),
		ansi.Strip(strings.Join(m.machineLines(mach, 100, 30), "\n")),
		ansi.Strip(strings.Join(sv.render(*m, 100, 20), "\n")),
	}
	for i, out := range shown {
		if !strings.Contains(out, "$") {
			t.Fatalf("view %d shows no cost with the setting on:\n%s", i, out)
		}
	}

	// Off: nothing shows it, and the rest of each view is untouched.
	m.cfg.UI.Cost = false
	m.rebuild()
	for _, c := range []struct{ out, keeps string }{
		{ansi.Strip(strings.Join(m.sidebarLines(40, 30), "\n")), "claude"},
		{ansi.Strip(strings.Join(m.projectLines(localMachine, proj, 100), "\n")), "Agents"},
		{ansi.Strip(strings.Join(m.machineLines(mach, 100, 30), "\n")), "2 projects"},
		{ansi.Strip(strings.Join(sv.render(*m, 100, 20), "\n")), "Refactor auth"},
	} {
		if strings.Contains(c.out, "$") || strings.Contains(c.out, "3.0k out") || strings.Contains(c.out, "usage conch has seen") {
			t.Fatalf("still shows usage with the setting off:\n%s", c.out)
		}
		if !strings.Contains(c.out, c.keeps) {
			t.Fatalf("lost %q with the setting off:\n%s", c.keeps, c.out)
		}
	}
	if line := m.usageLine(m.projectUsage(localMachine, "r1"), "usage"); line != "" {
		t.Fatalf("usage line with the setting off: %q", line)
	}

	// The settings screen has the toggle, and it writes the config.
	s := &settings{shellErr: "no server"}
	items := s.themeItems(m)
	var toggle *settingItem
	for i := range items {
		if items[i].on == &m.cfg.UI.Cost {
			toggle = &items[i]
		}
	}
	if toggle == nil || !strings.Contains(toggle.label, "spend") {
		t.Fatalf("no cost toggle in the theme tab: %+v", items)
	}
	toggle.run(m) // saving writes to CONCH_HOME, which the fixture isolates
	if !m.cfg.UI.Cost {
		t.Fatal("the toggle did not turn it back on")
	}
}
