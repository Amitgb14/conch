package tui

import (
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amitgb14/conch/internal/adapter"
	"github.com/Amitgb14/conch/internal/integration"
)

// The Integrations tab of the settings: what conch talks to besides
// itself, by kind — built in, shipped, installed — with each one's key
// and the projects here that use it. Enter opens one's page; installing,
// updating and removing run `conch integration` in a terminal of their
// own, where a key can be typed without echo and hosts agreed to, as the
// sandbox providers' install does.

// integrationKey is whether a key's variable is set here, read without a
// process — the Keychain is the command's to ask, not the TUI's, which
// would block a frame on security. A variable so tests can say.
var integrationKey = func(env string) bool { return strings.TrimSpace(os.Getenv(env)) != "" }

func (s *settings) integrationItems(m *Model) []settingItem {
	files, bad := integration.All()
	if name, ok := strings.CutPrefix(s.page, "integration:"); ok {
		for _, f := range files {
			if f.Name == name {
				return s.integrationPage(m, f)
			}
		}
		s.page = ""
	}
	installed := 0
	for _, f := range files {
		if !f.Shipped() {
			installed++
		}
	}
	items := []settingItem{
		{header: true, label: "Integrations", detail: countOf(len(files), "file") + " · " + itoa(installed) + " installed"},
		{header: true, label: "CI"},
		{label: "  ✓ GitHub Actions", detail: "built in · through gh"},
	}
	items = append(items, s.fileRows(m, files, integration.KindCI)...)
	items = append(items,
		settingItem{header: true, label: "Code hosts"},
		settingItem{label: "  ✓ GitHub", detail: "built in · through gh"},
		settingItem{header: true, label: "Ticket trackers"},
		settingItem{label: "  ✓ GitHub Issues", detail: "built in · through gh"},
		settingItem{label: "  " + trackerMark(m.cfg.Trackers.Linear.APIKey, firstNonEmpty(m.cfg.Trackers.Linear.APIKeyEnv, "LINEAR_API_KEY")) + " Linear", detail: "built in"},
		settingItem{label: "  " + trackerMark(m.cfg.Trackers.Jira.APIToken, firstNonEmpty(m.cfg.Trackers.Jira.APITokenEnv, "JIRA_API_TOKEN")) + " Jira", detail: "built in"},
	)
	items = append(items, s.fileRows(m, files, integration.KindTickets)...)
	items = append(items, settingItem{header: true, label: "Notifications"},
		settingItem{label: "  ✓ Desktop", detail: "built in · Settings → Notifications"})
	items = append(items, s.fileRows(m, files, integration.KindNotify)...)
	items = append(items, settingItem{header: true, label: "Deploy previews"})
	if deploys := s.fileRows(m, files, integration.KindDeploy); len(deploys) > 0 {
		items = append(items, deploys...)
	} else {
		// None ships with conch: Vercel's and Netlify's are the registry's.
		items = append(items, settingItem{label: styleMuted.Render("  Vercel, Netlify — install one from the registry"),
			detail: "a branch's preview, its failed build to the agent"})
	}
	items = append(items,
		settingItem{},
		settingItem{label: "* Install one from the registry…", detail: "agree to its hosts, then give its key",
			run: func(m *Model) tea.Cmd {
				return conchInPane(m, "install an integration", "installing an integration: pick one in this terminal", integrationInstallScript(""))
			}},
		settingItem{label: "↻ Update installed ones…", detail: "new hosts are always asked about",
			run: func(m *Model) tea.Cmd {
				return conchInPane(m, "update integrations", "updating integrations in this terminal", integrationScript("update"))
			}},
	)
	for _, b := range bad {
		items = append(items, settingItem{label: styleWarn.Render("  not used: ") + styleMuted.Render(b)})
	}
	return append(items,
		settingItem{},
		settingItem{label: styleMuted.Render("  A file describes a service's API — never code. Its key goes only to the")},
		settingItem{label: styleMuted.Render("  hosts it names, and is read from its variable, or the Keychain on a Mac.")})
}

// fileRows are the files of kind, each opening its page.
func (s *settings) fileRows(m *Model, files []*integration.File, kind string) []settingItem {
	var items []settingItem
	for _, f := range files {
		if f.Kind != kind {
			continue
		}
		f := f
		mark := "○"
		if integrationKey(f.KeyEnv) {
			mark = "✓"
		}
		items = append(items, settingItem{label: "  " + mark + " " + f.DisplayName(), detail: integrationDetail(m, f), page: true,
			run: func(m *Model) tea.Cmd { s.openPage("integration:" + f.Name); return nil }})
	}
	return items
}

// integrationDetail is a file's line: where it came from, its key, and
// the projects here that use it.
func integrationDetail(m *Model, f *integration.File) string {
	source := "ships with conch"
	if !f.Shipped() {
		source = "installed"
	}
	key := styleOK.Render("key set")
	if !integrationKey(f.KeyEnv) {
		key = styleWarn.Render("$" + f.KeyEnv + " not set")
	}
	d := source + " · " + key
	if used := usedBy(m, f); used != "" {
		d += " · used by " + used
	}
	return d
}

// usedBy names this computer's projects that use f, from what each
// checkout holds and its remote.
func usedBy(m *Model, f *integration.File) string {
	var names []string
	for _, mach := range m.machines {
		if mach.id != localMachine {
			continue
		}
		for _, p := range mach.projects {
			rem, _ := integration.ParseRemote(p.Remote)
			if f.Uses(p.Path, rem) {
				names = append(names, p.Name)
			}
		}
	}
	return strings.Join(names, ", ")
}

func trackerMark(kept, env string) string {
	if strings.TrimSpace(kept) != "" || integrationKey(env) {
		return "✓"
	}
	return "○"
}

// integrationPage is one integration's own page: what it does in conch,
// where the key goes, how to make it — the mock's page, as text.
func (s *settings) integrationPage(m *Model, f *integration.File) []settingItem {
	items := []settingItem{
		{header: true, label: f.DisplayName(), detail: f.Kind + " · " + integrationDetail(m, f)},
		{label: styleMuted.Render("‹ Integrations"), detail: styleMuted.Render("esc"),
			run: func(m *Model) tea.Cmd { s.openPage(""); return nil }},
		{header: true, label: "What it does in conch"},
	}
	for _, d := range integration.Does(f.Kind, f.Operations()) {
		mark := map[string]string{"yes": styleOK.Render("✓"), "partly": styleWarn.Render("◐"), "no": styleMuted.Render("✗"), "built-in": styleMuted.Render("—")}[d.Level]
		items = append(items, settingItem{label: "  " + mark + " " + d.What, detail: d.Where})
	}
	api := f.API
	if f.BaseURL == "ask" {
		api = firstNonEmpty(m.cfg.Integrations.Base[f.Name], api, "the address you give in config.toml")
	}
	items = append(items,
		settingItem{header: true, label: "Your key goes to"},
		settingItem{label: "  " + hostOnly(api), detail: "API · $" + f.KeyEnv})
	for _, h := range f.FollowHosts {
		items = append(items, settingItem{label: "  " + h, detail: "read without your key"})
	}
	items = append(items, settingItem{label: styleMuted.Render("  nowhere else · no programs run: HTTP only")})
	if f.KeyHelp != "" {
		items = append(items, settingItem{header: true, label: "Setting it up"}, settingItem{label: "  " + f.KeyHelp})
	}
	if f.Note != "" {
		items = append(items, settingItem{label: "  " + styleMuted.Render(f.Note)})
	}
	name := f.Name
	items = append(items, settingItem{})
	items = append(items, settingItem{label: "* Give its key…", detail: "in a terminal, without echo",
		run: func(m *Model) tea.Cmd {
			return conchInPane(m, "integration key", "giving "+name+" its key in this terminal", integrationScript("key", name))
		}})
	if !f.Shipped() {
		items = append(items, settingItem{label: "x Remove " + f.DisplayName(), detail: "deletes the installed file",
			run: func(m *Model) tea.Cmd {
				return conchInPane(m, "remove an integration", "removing "+name, integrationScript("remove", name))
			}})
	}
	return items
}

func hostOnly(u string) string {
	if i := strings.Index(u, "://"); i >= 0 {
		u = u[i+3:]
	}
	h, _, _ := strings.Cut(u, "/")
	return h
}

func countOf(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return itoa(n) + " " + what + "s"
}

// integrationScript runs conch integration ARGS in a terminal, which
// waits for enter so what it said can be read.
func integrationScript(args ...string) func(exe string) []string {
	return func(exe string) []string {
		q := []string{adapter.ShellQuote(exe), "integration"}
		for _, a := range args {
			q = append(q, adapter.ShellQuote(a))
		}
		return []string{"/bin/sh", "-c", strings.Join(q, " ") + `; printf '\n[enter closes this] '; read _`}
	}
}

// integrationInstallScript installs name, or lists what the registries
// have and asks which.
func integrationInstallScript(name string) func(exe string) []string {
	return func(exe string) []string {
		if name != "" {
			return integrationScript("install", name)(exe)
		}
		x := adapter.ShellQuote(exe)
		return []string{"/bin/sh", "-c", x + ` integration search; printf '\nInstall which? '; read n; [ -n "$n" ] && ` + x +
			` integration install "$n"; printf '\n[enter closes this] '; read _`}
	}
}
