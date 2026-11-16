package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
)

const circleURL = "https://app.circleci.com/pipelines/github/acme/api/8812"

// cicardModel is a2Model with feat's pull request red on CircleCI, which
// nothing here reads, beside a Jenkins check that passed and an Actions
// one conch reads itself.
func cicardModel(runs ...proto.PRCheck) *Model {
	m := a2Model()
	if runs == nil {
		runs = []proto.PRCheck{
			{Name: "continuous-integration/jenkins", State: "pass", URL: "https://ci.example.org/job/api/1", Source: "jenkins"},
			{Name: "lint", State: "pass", URL: "https://github.com/acme/api/actions/runs/4", Source: "github-actions", Readable: true},
			{Name: "ci/circleci: build-and-test", State: "fail", URL: circleURL, Source: "circleci"},
		}
	}
	pr := m.machines[0].projects[0].Branches[1].PR
	pr.Checks, pr.Runs, pr.Passed, pr.Total = "fail", runs, 2, len(runs)
	return m
}

func TestCICardRender(t *testing.T) {
	m := cicardModel()
	cv := &changesView{machine: localMachine, projectID: "r1", branch: "feat"}
	out := a2Plain(cv.render(*m, 120, 30))
	for _, want := range []string{
		"⚠ ci/circleci: build-and-test failed — conch can't read CircleCI yet, so the agent wasn't sent the log.",
		"I install CircleCI", "O open in CircleCI", "r re-run failed jobs",
		"✗ ci/circleci: build-and-test — failed", "app.circleci.com",
		"✓ continuous-integration/jenkins — passed", "no integration needed to see it",
		"✓ lint — passed", "github.com",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("render lacks %q:\n%s", want, out)
		}
	}
	// The failing check comes first.
	if strings.Index(out, "✗ ci/circleci") > strings.Index(out, "✓ continuous-integration") {
		t.Fatalf("failure not first:\n%s", out)
	}
	// The branch's agent is named when there is one.
	m.machines[0].panes = append(m.machines[0].panes, proto.PaneInfo{ID: "p9", Name: "claude", State: proto.PaneRunning,
		ProjectID: "r1", Branch: "feat", Agent: &proto.AgentStatus{Name: "claude", State: proto.AgentIdle}})
	if out := a2Plain(cv.render(*m, 120, 30)); !strings.Contains(out, "so Claude Code wasn't sent the log") {
		t.Fatalf("agent not named:\n%s", out)
	}
	// Why conch thinks the project uses it, once detection has said.
	m.ciWhy = map[string]map[string]string{localMachine + "|r1": {"circleci": ".circleci/config.yml"}}
	if out := a2Plain(cv.render(*m, 120, 30)); !strings.Contains(out, "found .circleci/config.yml") {
		t.Fatalf("no reason:\n%s", out)
	}
}

// The files below start where filesTop says, card or not, so a click
// lands on the file drawn there.
func TestCICardFilesTop(t *testing.T) {
	for _, m := range []*Model{cicardModel(), a2Model(), cicardModel(manyChecks(9)...)} {
		cv := &changesView{machine: localMachine, projectID: "r1", branch: "feat"}
		d := a2Changes()
		cv.data = &d
		lines := cv.render(*m, 120, 40)
		top := cv.filesTop(*m)
		if top < 1 || top >= len(lines) || !strings.Contains(ansi.Strip(lines[top-1]), "Uncommitted changes") {
			t.Fatalf("filesTop %d, but the header is elsewhere:\n%s", top, a2Plain(lines))
		}
	}
}

func manyChecks(n int) []proto.PRCheck {
	var out []proto.PRCheck
	for i := range n {
		out = append(out, proto.PRCheck{Name: fmt.Sprintf("check %d", i), State: "pass", URL: "https://github.com/a/b/actions/runs/1", Source: "github-actions", Readable: true})
	}
	out[n-1].State = "fail"
	return out
}

// A long list is cut, failures first, and says how many more.
func TestCICardListCapped(t *testing.T) {
	m := cicardModel(manyChecks(9)...)
	cv := &changesView{machine: localMachine, projectID: "r1", branch: "feat"}
	out := a2Plain(cv.render(*m, 120, 30))
	if !strings.Contains(out, "✗ check 8 — failed") || !strings.Contains(out, "… 5 more checks") || strings.Contains(out, "check 7") {
		t.Fatalf("capped list:\n%s", out)
	}
	if strings.Contains(out, "can't read") {
		t.Fatalf("a readable failure got the card:\n%s", out)
	}
}

// No card for a check conch reads, one from a service it cannot name, or
// one from an older server, which names none; and none without a pull
// request at all.
func TestCICardAbsent(t *testing.T) {
	for name, runs := range map[string][]proto.PRCheck{
		"readable": {{Name: "ci/circleci: build", State: "fail", URL: circleURL, Source: "circleci", Readable: true}},
		"unknown":  {{Name: "deploy", State: "fail", URL: "https://deploy.example.org/1"}},
		"passing":  {{Name: "ci/circleci: build", State: "pass", URL: circleURL, Source: "circleci"}},
	} {
		m := cicardModel(runs...)
		cv := &changesView{machine: localMachine, projectID: "r1", branch: "feat"}
		out := a2Plain(cv.render(*m, 120, 30))
		if strings.Contains(out, "can't read") || strings.Contains(out, "I install") {
			t.Errorf("%s: a card:\n%s", name, out)
		}
		if !strings.Contains(out, runs[0].Name) {
			t.Errorf("%s: the check is not listed:\n%s", name, out)
		}
		if _, ok := unreadableFailure(m.branchPR(localMachine, "r1", "feat")); ok {
			t.Errorf("%s: unreadableFailure found one", name)
		}
	}
	if _, ok := unreadableFailure(nil); ok {
		t.Fatal("no pull request, a failure")
	}
	m := a2Model()
	cv := &changesView{machine: localMachine, projectID: "r1", branch: "main"}
	if out := a2Plain(cv.render(*m, 120, 30)); strings.Contains(out, "can't read") {
		t.Fatalf("a branch with no PR got a card:\n%s", out)
	}
}

// The card, the checks and the pull request's line above them keep to
// the width, from tiny to wide.
func TestCICardWidths(t *testing.T) {
	m := cicardModel()
	m.ciWhy = map[string]map[string]string{localMachine + "|r1": {"circleci": ".circleci/config.yml"}}
	for _, w := range []int{1, 20, 39, 80, 200} {
		cv := &changesView{machine: localMachine, projectID: "r1", branch: "feat"}
		lines := append(m.ciLines(cv, w), cv.render(*m, w, 30)[2])
		if len(lines) != 6 {
			t.Fatalf("width %d: %d lines", w, len(lines))
		}
		for i, l := range lines {
			if ansi.StringWidth(l) > w {
				t.Fatalf("width %d, line %d is %d wide: %q", w, i, ansi.StringWidth(l), ansi.Strip(l))
			}
		}
	}
}

func TestCICardKeys(t *testing.T) {
	m := cicardModel()
	c, peer := a1FakeClient(t, proto.CapBranchCIRerun, proto.CapIntegrationDetect)
	m.machines[0].c, m.machines[0].server = c, c.Server
	m.focus = focusMain
	cv := &changesView{machine: localMachine, projectID: "r1", branch: "feat"}
	d := a2Changes()
	cv.data = &d

	// O opens the failing check where it ran.
	lastOpened = ""
	_, cmd := cv.key(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("O")})
	a2Run(cmd)
	if lastOpened != circleURL {
		t.Fatalf("O opened %q", lastOpened)
	}

	// I installs CircleCI in a terminal, checked against this branch's checkout.
	_, cmd = cv.key(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("I")})
	if cmd == nil {
		t.Fatal("I did nothing")
	}
	go a2Run(cmd)
	var p proto.PaneCreateParams
	json.Unmarshal(peer.waitMethod(t, proto.MethodPaneCreate, "").Params, &p)
	script := strings.Join(p.Command, " ")
	if !strings.Contains(script, " integration install -cwd /src/api-feat circleci;") {
		t.Fatalf("install script: %s", script)
	}

	// r re-runs what failed, through the server.
	peer.setResult(proto.MethodBranchCIRerun, proto.BranchCIRerun{Source: "circleci", Service: "CircleCI", Run: "w1"})
	_, cmd = cv.key(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	msgs := a2Run(cmd)
	var rp proto.BranchCILogParams
	json.Unmarshal(peer.waitMethod(t, proto.MethodBranchCIRerun, "").Params, &rp)
	if rp.ProjectID != "r1" || rp.Branch != "feat" {
		t.Fatalf("rerun params %+v", rp)
	}
	if len(msgs) != 1 || fmt.Sprint(msgs[0]) != "re-running CircleCI's failed jobs on feat (run w1)" {
		t.Fatalf("rerun said %v", msgs)
	}
	peer.setError(proto.MethodBranchCIRerun, "CircleCI: its integration cannot re-run")
	msgs = a2Run(cv.rerunCI(m))
	if e, ok := msgs[0].(errMsg); len(msgs) != 1 || !ok || !strings.Contains(e.err.Error(), "cannot re-run") {
		t.Fatalf("a refused rerun said %v", msgs)
	}
}

// Without a card, I and O say why nothing happens; r needs a red pull
// request and a server that re-runs.
func TestCICardKeysRefused(t *testing.T) {
	m := a2Model()
	cv := &changesView{machine: localMachine, projectID: "r1", branch: "feat"}
	for _, k := range []string{"I", "O", "r"} {
		m.flash = ""
		if _, cmd := cv.key(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}); cmd != nil {
			t.Fatalf("%s ran something on a green branch", k)
		}
		if m.flash == "" {
			t.Fatalf("%s said nothing", k)
		}
	}
	m = cicardModel()
	c, _ := a1FakeClient(t) // an older server: no re-run
	m.machines[0].c, m.machines[0].server = c, c.Server
	m.flash = ""
	if _, cmd := cv.key(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")}); cmd != nil || !strings.Contains(m.flash, "predates") {
		t.Fatalf("an older server: %q", m.flash)
	}
	// A remote branch's integration is installed on that machine, not here.
	m.machines[0].id = "busybox"
	cv.machine = "busybox"
	m.flash = ""
	if _, cmd := cv.key(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("I")}); cmd != nil || !strings.Contains(m.flash, "conch integration install circleci") {
		t.Fatalf("remote install: %q", m.flash)
	}
}

// The reason a service was detected is asked once per project, only when
// a card needs it and the server can say.
func TestCICardDetect(t *testing.T) {
	m := cicardModel()
	c, peer := a1FakeClient(t, proto.CapIntegrationDetect)
	m.machines[0].c, m.machines[0].server = c, c.Server
	cv := &changesView{machine: localMachine, projectID: "r1", branch: "feat"}
	peer.setResult(proto.MethodIntegrationDetect, proto.IntegrationDetect{Integrations: []proto.IntegrationUse{
		{Name: "circleci", Label: "CircleCI", Kind: "ci", Why: ".circleci/config.yml"},
		{Name: "github-actions", Kind: "ci", Why: ".github/workflows", Installed: true},
	}})
	cmd := m.detectCI(cv)
	if cmd == nil {
		t.Fatal("no detection asked")
	}
	if m.detectCI(cv) != nil {
		t.Fatal("asked twice")
	}
	for _, msg := range a2Run(cmd) {
		if dm, ok := msg.(ciDetectMsg); ok {
			m.receiveCIDetect(dm)
		}
	}
	if m.ciWhy[localMachine+"|r1"]["circleci"] != ".circleci/config.yml" {
		t.Fatalf("why %+v", m.ciWhy)
	}
	// Nothing to explain, or a server that cannot: not asked.
	g := a2Model()
	g.machines[0].c, g.machines[0].server = c, c.Server
	if g.detectCI(cv) != nil {
		t.Fatal("asked with no card")
	}
	old := cicardModel()
	oc, _ := a1FakeClient(t)
	old.machines[0].c, old.machines[0].server = oc, oc.Server
	if old.detectCI(cv) != nil {
		t.Fatal("asked an older server")
	}
}

// A server that says which checks can be run again is taken at its word:
// r on a branch whose failing check's integration has no re-run says so
// and asks nothing.
func TestCICardRerunFollowsTheChecks(t *testing.T) {
	m := cicardModel()
	c, peer := a1FakeClient(t, proto.CapBranchCIRerun, proto.CapIntegrationDetect, proto.CapPRCheckRerun)
	m.machines[0].c, m.machines[0].server = c, c.Server
	cv := &changesView{machine: localMachine, projectID: "r1", branch: "feat"}
	pr := m.branchPR(localMachine, "r1", "feat")
	if pr == nil || len(pr.Runs) == 0 {
		t.Fatal("no pull request with checks")
	}
	for i := range pr.Runs {
		pr.Runs[i].Rerun = false
	}
	m.flash = ""
	if cmd := cv.rerunCI(m); cmd != nil {
		t.Fatal("asked the server to re-run what it said cannot be")
	}
	if !strings.Contains(m.flash, "cannot re-run from conch") {
		t.Fatalf("said %q", m.flash)
	}
	// One that can: asked.
	for i := range pr.Runs {
		if pr.Runs[i].State == "fail" {
			pr.Runs[i].Rerun = true
		}
	}
	peer.setResult(proto.MethodBranchCIRerun, proto.BranchCIRerun{Source: "circleci", Service: "CircleCI", Run: "w1"})
	if msgs := a2Run(cv.rerunCI(m)); len(msgs) != 1 || !strings.Contains(fmt.Sprint(msgs[0]), "re-running CircleCI") {
		t.Fatalf("rerun said %v", msgs)
	}
}
