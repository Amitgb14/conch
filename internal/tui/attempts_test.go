package tui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

func attemptNames(plan []attempt) string {
	var out []string
	for _, a := range plan {
		out = append(out, a.agent+"@"+a.branch)
	}
	return strings.Join(out, " ")
}

func TestAttemptPlan(t *testing.T) {
	// One agent, no count: the plain task it has always been.
	if got := attemptNames(attemptPlan("conch", []string{"claude"}, 0, "", "Fix the flaky login test", nil)); got != "claude@" {
		t.Fatalf("single: %q", got)
	}
	if got := attemptNames(attemptPlan("conch", nil, 1, "feat", "prompt", nil)); got != "@feat" {
		t.Fatalf("no agent named: %q", got)
	}
	// Several agents: one attempt each, under the branch the prompt gives.
	got := attemptNames(attemptPlan("conch", []string{"claude", "codex"}, 0, "", "Fix the flaky login test", nil))
	want := "claude@conch/fix-flaky-login-test/claude codex@conch/fix-flaky-login-test/codex"
	if got != want {
		t.Fatalf("two agents:\n got %q\nwant %q", got, want)
	}
	// More attempts than agents: they cycle, and repeats are numbered.
	got = attemptNames(attemptPlan("conch", []string{"claude", "codex"}, 3, "feat", "p", nil))
	if got != "claude@feat/claude codex@feat/codex claude@feat/claude-2" {
		t.Fatalf("cycling: %q", got)
	}
	// Branches already in the project are never reused.
	existing := []proto.BranchInfo{{Name: "feat/claude"}, {Name: "feat/claude-2"}}
	got = attemptNames(attemptPlan("conch", []string{"claude"}, 2, "feat", "p", existing))
	if got != "claude@feat/claude-3 claude@feat/claude-4" {
		t.Fatalf("avoiding taken names: %q", got)
	}
}

func TestAttemptsField(t *testing.T) {
	for in, want := range map[string]int{"": 0, " ": 0, "1": 1, "3": 3, "10": 10} {
		if got, err := attemptsField(in); err != nil || got != want {
			t.Errorf("attemptsField(%q) = %d, %v", in, got, err)
		}
	}
	for _, in := range []string{"x", "0", "-2", "11", "1.5"} {
		if _, err := attemptsField(in); err == nil {
			t.Errorf("attemptsField(%q) was accepted", in)
		}
	}
	if got := splitAgents(" Claude , codex ,,"); strings.Join(got, ",") != "claude,codex" {
		t.Fatalf("splitAgents: %v", got)
	}
	if got := splitAgents("  "); got != nil {
		t.Fatalf("splitAgents empty: %v", got)
	}
}

func TestTaskDialogAttempts(t *testing.T) {
	m, peer := harvestModel(t, harvestCapability)
	proj := m.machines[0].projects[0]
	m.machines[0].available = map[string]proto.AgentAvailability{
		"claude": {Name: "claude", Installed: true}, "codex": {Name: "codex", Installed: true}}
	d := newTaskDialog(*m, localMachine, proj)
	m.overlay = d
	text := func() string { return strings.Join(d.text, " ") }

	// One attempt: no extra words.
	a2Type(m, d, "Fix the flaky login test")
	if strings.Contains(text(), "attempts") {
		t.Fatalf("single attempt: %q", text())
	}
	// Naming two agents shows the branches before anything starts.
	d.setFocus(3)
	a2Type(m, d, "claude,codex")
	if !strings.Contains(text(), "2 attempts of the same prompt, one per branch: api/fix-flaky-login-test/claude, api/fix-flaky-login-test/codex.") {
		t.Fatalf("two agents: %q", text())
	}
	// A bad count is caught while typing, and refused on submit.
	d.setFocus(4)
	a2Type(m, d, "99")
	if !strings.Contains(text(), "99 is more than 10") {
		t.Fatalf("too many: %q", text())
	}
	if msg := a2ErrText(a2Run(d.submit(m, []string{"p", "", "", "claude", "99"}))); !strings.Contains(msg, "more than 10") {
		t.Fatalf("submit with a bad count: %q", msg)
	}
	// Three attempts across two agents.
	for i := 0; i < 2; i++ {
		d.update(m, a2Key("backspace"))
	}
	a2Type(m, d, "3")
	if !strings.Contains(text(), "3 attempts") || !strings.Contains(text(), "claude-2") {
		t.Fatalf("three attempts: %q", text())
	}
	// Each agent's plan limit is named once, however many attempts it makes.
	a5Limits(m, "claude", 97, 25*time.Minute)
	d.onChange(d)
	if n := strings.Count(text(), "Claude's 5-hour limit"); n != 1 {
		t.Fatalf("%d warnings in %q", n, text())
	}

	// Submitting starts one task per attempt, in order.
	peer.setResult(proto.MethodTaskCreate, proto.PaneInfo{ID: "np", Cwd: "/src/api-x", Branch: "conch/fix-flaky-login-test/claude"})
	cmd := d.submit(m, []string{"Fix the flaky login test", "", "", "claude,codex", "3"})
	msgs := a2Run(cmd)
	done, ok := msgs[0].(attemptsDoneMsg)
	if !ok || len(done.panes) != 3 || len(done.errs) != 0 {
		t.Fatalf("attempts: %#v", msgs)
	}
	var branches []string
	for _, msg := range peer.snapshot() {
		if msg.Method == proto.MethodTaskCreate {
			var p proto.TaskCreateParams
			if err := json.Unmarshal(msg.Params, &p); err != nil {
				t.Fatal(err)
			}
			branches = append(branches, p.Agent+"@"+p.Branch)
		}
	}
	if strings.Join(branches, " ") != "claude@api/fix-flaky-login-test/claude codex@api/fix-flaky-login-test/codex claude@api/fix-flaky-login-test/claude-2" {
		t.Fatalf("task calls: %v", branches)
	}
	next, _ := m.update(done)
	*m = next.(Model)
	if !strings.Contains(m.flash, "started 3 attempts") {
		t.Fatalf("flash %q", m.flash)
	}
}

func TestAttemptsPartlyFail(t *testing.T) {
	m, peer := harvestModel(t, harvestCapability)
	peer.setError(proto.MethodTaskCreate, "branch feat/codex is already checked out")
	plan := []attempt{{agent: "codex", branch: "feat/codex"}}
	msgs := a2Run(m.startAttempts(localMachine, "r1", "p", "", plan, 80, 24))
	done := msgs[0].(attemptsDoneMsg)
	if len(done.panes) != 0 || len(done.errs) != 1 || !strings.Contains(done.errs[0], "already checked out") {
		t.Fatalf("failed attempt: %+v", done)
	}
	m.receiveAttempts(done)
	if !m.flashIsErr || !strings.Contains(m.flash, "already checked out") {
		t.Fatalf("flash %q", m.flash)
	}
	// Some started, some didn't: both are said.
	done = attemptsDoneMsg{machine: localMachine, panes: []proto.PaneInfo{{ID: "p9", Branch: "feat/claude"}},
		errs: []string{"feat/codex: nope"}}
	m.receiveAttempts(done)
	if !m.flashIsErr || !strings.Contains(m.flash, "started 1 attempt;") || !strings.Contains(m.flash, "feat/codex: nope") {
		t.Fatalf("partial flash %q", m.flash)
	}
	// Offline machines never start anything.
	offline, _ := a1Fixture(t, false)
	if msg := a2ErrText(a2Run(offline.startAttempts(localMachine, "r1", "p", "", plan, 80, 24))); !strings.Contains(msg, "local is") {
		t.Fatalf("offline: %q", msg)
	}
}
