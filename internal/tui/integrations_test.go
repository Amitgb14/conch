package tui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/integration"
	"github.com/Amitgb14/conch/internal/proto"
)

func integrationsModel(t *testing.T) (*Model, *settings, string) {
	t.Helper()
	a2Isolate(t)
	integration.Forget()
	t.Cleanup(integration.Forget)
	was := integrationKey
	t.Cleanup(func() { integrationKey = was })
	integrationKey = func(env string) bool { return env == "GITLAB_TOKEN" }
	m := a2Model()
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".circleci"), 0o755)
	os.WriteFile(filepath.Join(root, ".circleci", "config.yml"), nil, 0o644)
	m.machines[0].projects = []proto.ProjectInfo{
		{ID: "r1", Name: "ceph", Path: root, Remote: "https://github.com/ceph/ceph.git"},
		{ID: "r2", Name: "pdp-web", Path: t.TempDir(), Remote: "git@gitlab.com:pdp/web.git"},
	}
	s := &settings{}
	s.setTab(integrationsTab)
	return m, s, root
}

func rowsByLabel(items []settingItem) map[string]settingItem {
	out := map[string]settingItem{}
	for _, it := range items {
		out[strings.TrimSpace(ansi.Strip(it.label))] = it
	}
	return out
}

func TestIntegrationsTab(t *testing.T) {
	m, s, _ := integrationsModel(t)
	if settingsTabs[integrationsTab] != "Integrations" {
		t.Fatalf("tab %d is %q", integrationsTab, settingsTabs[integrationsTab])
	}
	items := rowsByLabel(s.tabItems(m))
	for _, want := range []string{"✓ GitHub Actions", "○ CircleCI", "✓ GitLab CI", "○ Buildkite", "○ Jenkins", "✓ GitHub", "✓ GitHub Issues", "○ Linear", "○ Jira",
		"○ Shortcut", "✓ GitLab issues", "Notifications", "✓ Desktop", "○ Slack", "○ Discord", "○ ntfy",
		"* Install one from the registry…", "↻ Update installed ones…"} {
		if _, ok := items[want]; !ok {
			t.Errorf("no row %q", want)
		}
	}
	// Each under its kind: trackers after Jira, webhooks after the desktop.
	var order []string
	for _, it := range s.tabItems(m) {
		order = append(order, strings.TrimSpace(ansi.Strip(it.label)))
	}
	at := func(l string) int { return slices.Index(order, l) }
	if !(at("○ Jira") < at("○ Shortcut") && at("○ Shortcut") < at("Notifications") && at("Notifications") < at("○ Slack") && at("○ CircleCI") < at("Code hosts")) {
		t.Errorf("order: %q", order)
	}
	if d := ansi.Strip(items["○ CircleCI"].detail); !strings.Contains(d, "ships with conch") || !strings.Contains(d, "$CIRCLECI_TOKEN not set") || !strings.Contains(d, "used by ceph") {
		t.Fatalf("circleci: %q", d)
	}
	if d := ansi.Strip(items["✓ GitLab CI"].detail); !strings.Contains(d, "key set") || !strings.Contains(d, "used by pdp-web") {
		t.Fatalf("gitlab: %q", d)
	}

	// Its page: what it does, where the key goes, and the way back.
	items["○ CircleCI"].run(m)
	if s.page != "integration:circleci" {
		t.Fatalf("page %q", s.page)
	}
	page := s.tabItems(m)
	text := ""
	for _, it := range page {
		text += ansi.Strip(it.label) + " | " + ansi.Strip(it.detail) + "\n"
	}
	for _, want := range []string{"What it does in conch", "✓ A failed job's output goes to the branch's agent", "◐ CI panel statistics",
		"circleci.com | API · $CIRCLECI_TOKEN", "circle-production-action-output.s3.amazonaws.com | read without your key",
		"nowhere else", "Personal API Tokens", "* Give its key…"} {
		if !strings.Contains(text, want) {
			t.Errorf("page lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "x Remove") {
		t.Error("a shipped file offered for removal")
	}
	if closed, _ := s.update(m, a2Key("esc")); closed || s.page != "" {
		t.Fatalf("esc: closed %v page %q", closed, s.page)
	}

	// A page for a file that has gone is the list again.
	s.page = "integration:gone"
	if items := rowsByLabel(s.tabItems(m)); s.page != "" || items["Integrations"].label == "" {
		t.Fatalf("a gone file's page: %q", s.page)
	}
}

func TestIntegrationsTabInstalledAndBroken(t *testing.T) {
	m, s, _ := integrationsModel(t)
	dir := filepath.Join(os.Getenv("CONCH_HOME"), "integrations")
	os.MkdirAll(dir, 0o755)
	data, _, _ := integration.Source("circleci")
	os.WriteFile(filepath.Join(dir, "acme.toml"), []byte(strings.Replace(string(data), `name = "circleci"`, `name = "acme"`, 1)), 0o600)
	os.WriteFile(filepath.Join(dir, "broken.toml"), []byte("kind = "), 0o600)
	items := s.tabItems(m)
	var acme settingItem
	broken := false
	for _, it := range items {
		l := ansi.Strip(it.label)
		if strings.Contains(l, "not used:") && strings.Contains(l, "broken.toml") {
			broken = true
		}
		if strings.HasSuffix(l, "CircleCI") && strings.Contains(ansi.Strip(it.detail), "installed") {
			acme = it
		}
	}
	if !broken || acme.run == nil {
		t.Fatalf("broken %v, acme %+v", broken, acme)
	}
	acme.run(m)
	if text := rowsByLabel(s.tabItems(m)); text["x Remove CircleCI"].run == nil {
		t.Fatal("an installed file cannot be removed from its page")
	}
}

// Every size AGENTS.md lists: nothing wider than the box, list or page.
func TestIntegrationsTabSizes(t *testing.T) {
	m, s, _ := integrationsModel(t)
	for _, size := range [][2]int{{1, 1}, {20, 5}, {39, 20}, {80, 24}, {240, 60}} {
		for _, page := range []string{"", "integration:circleci", "integration:jenkins"} {
			m.width, m.height = size[0], size[1]
			s.page, s.sel, s.scroll = page, 0, 0
			b := s.render(*m)
			for i, l := range b.lines {
				if w := ansi.StringWidth(l); w > max(m.width, b.width()) {
					t.Fatalf("%v %q: line %d is %d wide: %q", size, page, i, w, ansi.Strip(l))
				}
			}
		}
	}
}

// The owner clicks: a click on a row opens its page, as enter does.
func TestIntegrationsTabClick(t *testing.T) {
	m, s, _ := integrationsModel(t)
	m.width, m.height = 120, 50
	b := s.render(*m)
	row := -1
	for i, l := range b.lines {
		if strings.Contains(ansi.Strip(l), "○ CircleCI") {
			row = i
		}
	}
	if row < 0 {
		t.Fatal("no CircleCI row on screen")
	}
	s.mouse(m, tea.MouseMsg{X: b.x + 4, Y: b.y + row, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}, b)
	if s.page != "integration:circleci" {
		t.Fatalf("a click opened %q", s.page)
	}
}

func TestIntegrationScripts(t *testing.T) {
	got := strings.Join(integrationScript("remove", "acme-ci")("/opt/conch bin/conch"), " ")
	if !strings.Contains(got, "'/opt/conch bin/conch' integration remove acme-ci") || !strings.Contains(got, "read _") {
		t.Fatalf("remove: %s", got)
	}
	got = strings.Join(integrationInstallScript("")("/x/conch"), " ")
	if !strings.Contains(got, "/x/conch integration search") || !strings.Contains(got, `integration install "$n"`) {
		t.Fatalf("pick and install: %s", got)
	}
	got = strings.Join(integrationInstallScript("acme-ci")("/x/conch"), " ")
	if !strings.Contains(got, "/x/conch integration install acme-ci") {
		t.Fatalf("install one: %s", got)
	}
	// A name that is not one never reaches a shell unquoted.
	got = strings.Join(integrationScript("key", "a;rm -rf ~")("/x/conch"), " ")
	if !strings.Contains(got, "'a;rm -rf ~'") {
		t.Fatalf("quoting: %s", got)
	}
}

// Nine tabs outgrew the box: at every width the bar fits, the open tab is
// named, and a click on any tab's label opens that tab.
func TestSettingsTabBarFitsAndEveryTabIsFound(t *testing.T) {
	a2Isolate(t)
	m := a2Model()
	for _, width := range []int{20, 44, 86, 90, 120, 200} {
		m.width, m.height = width, 30
		for open := range settingsTabs {
			s := &settings{}
			s.setTab(open)
			b := s.render(*m)
			bar := b.lines[1]
			if w := ansi.StringWidth(bar); w > b.width() {
				t.Fatalf("width %d, tab %d: the bar is %d wide in a %d box", width, open, w, b.width())
			}
			if !strings.Contains(ansi.Strip(bar), settingsTabs[open]) {
				t.Fatalf("width %d: the open tab %q is not named: %q", width, settingsTabs[open], ansi.Strip(bar))
			}
			x := b.x + 1
			for i, label := range settingsTabLabels(open, b.width()-2) {
				s.mouse(m, tea.MouseMsg{X: x + 1, Y: b.y + 1, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}, b)
				if s.tab != i {
					t.Fatalf("width %d, open %d: a click on %q opened tab %d", width, open, label, s.tab)
				}
				s.setTab(open)
				x += ansi.StringWidth(label)
			}
		}
	}
}
