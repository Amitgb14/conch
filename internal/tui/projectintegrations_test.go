package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amitgb14/conch/internal/integration"
	"github.com/Amitgb14/conch/internal/proto"
)

// As a server answers for a project like mock 5's.
var mock5 = proto.IntegrationDetect{Integrations: []proto.IntegrationUse{
	{Name: "github", Label: "GitHub", Kind: "host", Why: "github.com remote", Installed: true, Connected: true},
	{Name: "circleci", Label: "CircleCI", Kind: "ci", Why: ".circleci/config.yml", Installed: true, KeyEnv: "CIRCLECI_TOKEN"},
	{Name: "github-actions", Label: "GitHub Actions", Kind: "ci", Why: ".github/workflows", Installed: true, Connected: true},
	{Name: "bitbucket-pipelines", Label: "Bitbucket Pipelines", Kind: "ci", Why: "bitbucket-pipelines.yml"},
	{Name: "jira", Label: "Jira (RGW)", Kind: "tickets", Why: "chosen in Settings", Installed: true, Connected: true},
	{Name: "daytona", Label: "Daytona", Kind: "sandbox", Why: "for tasks run off this machine", Installed: true},
}}

func TestProjectIntegrations(t *testing.T) {
	m, mach, _ := limitModel(t)
	c, peer := a1FakeClient(t, proto.CapIntegrationDetect, proto.CapIntegrationTeam)
	mach.c = c
	peer.setResult(proto.MethodIntegrationDetect, mock5)

	// On the project's row menu, after agent setup.
	m.rebuild()
	var r row
	for _, x := range m.rows {
		if x.kind == kindProject && x.projectID == "r1" {
			r = x
		}
	}
	got := labelsOf(newRowMenu(*m, r, 0, 0).items)
	at := strings.Index(strings.Join(got, "|"), "Agent setup (skills, MCP, instructions)|Integrations…")
	if at < 0 {
		t.Fatalf("not on the menu: %q", got)
	}

	cmd := m.openProjectIntegrations(mach.id, "r1")
	if cmd == nil {
		t.Fatalf("nothing asked: %q", m.flash)
	}
	m.receiveProjectIntegrations(cmd().(projectIntegrationsMsg))
	mu, ok := m.overlay.(*menu)
	if !ok || mu.title != "api — Integrations" {
		t.Fatalf("overlay %#v", m.overlay)
	}
	want := []string{
		"Code host · GitHub  connected",
		"CI · CircleCI, GitHub Actions, Bitbucket Pipelines  key not set, connected, not installed",
		"    Connect CircleCI…",
		"    Install Bitbucket Pipelines…",
		"Tickets · Jira (RGW)  connected",
		"Sandboxes · Daytona  set up",
		"Notifications · none — Slack, Discord, ntfy…",
		"Deploy previews · none — Vercel, Netlify…",
		"☐ Save in .conch/integrations.toml for the team",
	}
	if got := labelsOf(mu.items); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("lines:\n%s", strings.Join(got, "\n"))
	}
	// Browse… opens the settings at Integrations.
	mu.items[6].run(m)
	if s, ok := m.overlay.(*settings); !ok || s.tab != integrationsTab {
		t.Fatalf("browse: %#v", m.overlay)
	}

	// Saving writes names — never a sandbox — and it comes back saved.
	saved := mock5
	saved.Team = &proto.IntegrationTeam{Path: ".conch/integrations.toml", Names: []string{"circleci"}, State: "used"}
	peer.setResult(proto.MethodIntegrationTeam, saved)
	msg := mu.items[8].run(m)().(projectIntegrationsMsg)
	var tp proto.IntegrationTeamParams
	sentParams(t, peer, proto.MethodIntegrationTeam, &tp)
	if tp.Action != proto.TeamSave || strings.Join(tp.Names, ",") != "github,circleci,github-actions,jira" || tp.ProjectID != "r1" {
		t.Fatalf("saved %+v", tp)
	}
	m.receiveProjectIntegrations(msg)
	mu = m.overlay.(*menu)
	if last := mu.items[len(mu.items)-1].label; !strings.HasPrefix(last, "☑ Saved in .conch/integrations.toml") {
		t.Fatalf("after saving: %q", last)
	}
	mu.items[len(mu.items)-1].run(m)()
	sentParams(t, peer, proto.MethodIntegrationTeam, &tp)
	if tp.Action != proto.TeamRemove {
		t.Fatalf("unticked: %+v", tp)
	}
}

// The repository's file is asked about before anything is shown, once:
// yes uses it, no ignores it, and the answer's reply is not asked again.
func TestProjectIntegrationsAsksAboutTheTeamFile(t *testing.T) {
	m, mach, _ := limitModel(t)
	c, peer := a1FakeClient(t, proto.CapIntegrationDetect, proto.CapIntegrationTeam)
	mach.c = c
	ask := mock5
	ask.Team = &proto.IntegrationTeam{Path: ".conch/integrations.toml", Names: []string{"circleci", "jira"}, State: "ask"}
	m.receiveProjectIntegrations(projectIntegrationsMsg{machine: mach.id, projectID: "r1", detect: ask})
	d, ok := m.overlay.(*dialog)
	if !ok || !d.confirm || d.text[0] != "This repository's .conch/integrations.toml says it uses CircleCI and Jira. Use them?" {
		t.Fatalf("not asked: %#v", m.overlay)
	}
	for _, c := range []struct {
		answer func() tea.Cmd
		want   string
	}{{func() tea.Cmd { return d.submit(m, nil) }, proto.TeamUse}, {func() tea.Cmd { return d.declined(m) }, proto.TeamIgnore}} {
		peer.setResult(proto.MethodIntegrationTeam, ask) // a server that somehow still says ask
		msg := c.answer()().(projectIntegrationsMsg)
		var tp proto.IntegrationTeamParams
		sentParams(t, peer, proto.MethodIntegrationTeam, &tp)
		if tp.Action != c.want {
			t.Fatalf("answered %q, sent %+v", c.want, tp)
		}
		m.receiveProjectIntegrations(msg)
		if _, ok := m.overlay.(*menu); !ok {
			t.Fatalf("asked again: %#v", m.overlay)
		}
	}

	// A server without integration.team.v1: shown, never asked, no checkbox.
	old, _ := a1FakeClient(t, proto.CapIntegrationDetect)
	mach.c = old
	m.receiveProjectIntegrations(projectIntegrationsMsg{machine: mach.id, projectID: "r1", detect: ask})
	mu, ok := m.overlay.(*menu)
	if !ok || strings.Contains(strings.Join(labelsOf(mu.items), "|"), "integrations.toml") {
		t.Fatalf("old server: %#v", m.overlay)
	}
	// A damaged file says so.
	mach.c = c
	bad := mock5
	bad.Team = &proto.IntegrationTeam{Path: ".conch/integrations.toml", State: "ignored", Problem: "uses = ["}
	m.receiveProjectIntegrations(projectIntegrationsMsg{machine: mach.id, projectID: "r1", detect: bad})
	if mu := m.overlay.(*menu); !strings.Contains(mu.items[len(mu.items)-2].label+mu.items[len(mu.items)-1].label, "uses = [") {
		t.Fatalf("damaged: %q", labelsOf(mu.items))
	}
	// Saving over a file nobody has answered keeps the names it lists.
	ask.Team.State = "ignored"
	ask.Team.Names = []string{"jira", "gitlab-ci"}
	if got := strings.Join(teamNames(ask), ","); got != "github,circleci,github-actions,jira,gitlab-ci" {
		t.Fatalf("names: %s", got)
	}
	if teamQuestion(nil) != "" || teamQuestion(&proto.IntegrationTeam{State: "ask"}) != "" {
		t.Fatal("a question about nothing")
	}
	if q := teamQuestion(&proto.IntegrationTeam{Path: "f", State: "ask", Names: []string{"circleci"}}); q != "This repository's f says it uses CircleCI. Use it?" {
		t.Fatal(q)
	}
}

func TestProjectIntegrationsRefusals(t *testing.T) {
	m, mach, _ := limitModel(t)
	if cmd := m.openProjectIntegrations(mach.id, "r1"); cmd != nil || !strings.Contains(m.flash, "predates integrations") {
		t.Fatalf("old server: %q", m.flash)
	}
	m.receiveProjectIntegrations(projectIntegrationsMsg{machine: mach.id, projectID: "r1", err: errString("no project r1")})
	if !strings.Contains(m.flash, "no project r1") {
		t.Fatalf("an error: %q", m.flash)
	}
	mach.projects[0].Git = false
	if cmd := m.openProjectIntegrations(mach.id, "r1"); cmd != nil || !strings.Contains(m.flash, "git project") {
		t.Fatalf("plain folder: %q", m.flash)
	}
	if m.openProjectIntegrations(mach.id, "nope") != nil {
		t.Fatal("an unknown project")
	}
	// Nothing found: a line a kind, each to browse; nothing to save.
	c, _ := a1FakeClient(t, proto.CapIntegrationDetect, proto.CapIntegrationTeam)
	mach.c = c
	items := m.projectIntegrationItems(mach.id, "r1", proto.IntegrationDetect{}, true)
	if len(items) != 6 || !strings.HasSuffix(items[3].label, "Daytona, E2B, boat.dev…") || !strings.HasSuffix(items[5].label, "Vercel, Netlify…") {
		t.Fatalf("empty: %q", labelsOf(items))
	}
	items[3].run(m)
	if s, ok := m.overlay.(*settings); !ok || s.tab != sandboxTab {
		t.Fatalf("browse sandboxes: %#v", m.overlay)
	}
}

// With a deploy integration installed, the sheet has a switch for it:
// turned on, the project's folder is kept in config.toml for it (saved as
// any setting is), and turned off again. One the project's files show
// has no switch: its files say it.
func TestProjectIntegrationsDeploySwitch(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CONCH_HOME", home)
	integration.Forget()
	t.Cleanup(integration.Forget)
	data, err := os.ReadFile("../integration/testdata/deploy/vercel.toml")
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(home, "integrations"), 0o755)
	os.WriteFile(filepath.Join(home, "integrations", "vercel.toml"), data, 0o600)
	m, mach, _ := limitModel(t)
	c, _ := a1FakeClient(t, proto.CapIntegrationDetect, proto.CapIntegrationTeam)
	mach.c = c
	root := m.project(mach.id, "r1").Path
	label := func(items []menuItem, want string) *menuItem {
		for i := range items {
			if strings.TrimSpace(items[i].label) == want {
				return &items[i]
			}
		}
		t.Fatalf("no %q in %q", want, labelsOf(items))
		return nil
	}
	items := m.projectIntegrationItems(mach.id, "r1", mock5, false)
	on := label(items, "☐ Vercel deploys this project")
	on.run(m)
	if got := m.cfg.Integrations.Chosen("", root); strings.Join(got, ",") != "vercel" {
		t.Fatalf("on: %v", got)
	}
	chosen := mock5
	chosen.Integrations = append(append([]proto.IntegrationUse{}, mock5.Integrations...),
		proto.IntegrationUse{Name: "vercel", Label: "Vercel", Kind: "deploy", Why: "chosen for this project", Installed: true})
	off := label(m.projectIntegrationItems(mach.id, "r1", chosen, false), "☑ Vercel deploys this project")
	off.run(m)
	if got := m.cfg.Integrations.Chosen("", root); len(got) != 0 {
		t.Fatalf("off: %v", got)
	}
	// Found by its file: listed, no switch.
	found := mock5
	found.Integrations = append(append([]proto.IntegrationUse{}, mock5.Integrations...),
		proto.IntegrationUse{Name: "vercel", Label: "Vercel", Kind: "deploy", Why: "vercel.json", Installed: true})
	for _, l := range labelsOf(m.projectIntegrationItems(mach.id, "r1", found, false)) {
		if strings.Contains(l, "deploys this project") {
			t.Fatalf("a switch for a use its file shows: %q", l)
		}
	}
	// Another machine's project: its config is that machine's.
	for _, l := range labelsOf(m.projectIntegrationItems("busybox", "r1", mock5, false)) {
		if strings.Contains(l, "deploys this project") {
			t.Fatalf("a switch for another machine: %q", l)
		}
	}
}
