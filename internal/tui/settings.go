package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/brain"
	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/sandbox"
)

// settings is the settings overlay: colour and prompt themes,
// notifications, and the agents installed on each machine. Changes apply
// at once and are saved to config.toml.
type settings struct {
	tab    int
	sel    int
	scroll int
	// provider is the sandbox provider whose own page is open; "" is the
	// list of them. Keeping every provider's settings on one page made the
	// tab longer with each one conch learns.
	provider string

	shell    *proto.ShellThemes // this computer's prompt themes
	shellErr string
}

var settingsTabs = []string{"Theme", "Notifications", "Agents", "Brain", "Sandboxes"}

type shellThemesMsg struct {
	themes proto.ShellThemes
	err    error
}

// settingItem is one line of a settings tab.
type settingItem struct {
	header bool
	label  string
	detail string // right-aligned
	on     *bool  // a toggle
	mark   bool   // the current choice in a list
	page   bool   // opens a page of its own, rather than choosing anything
	run    func(m *Model) tea.Cmd
}

func newSettings(m *Model) (*settings, tea.Cmd) {
	s := &settings{}
	c := m.clientOf(localMachine)
	if c == nil || len(c.MissingCapabilities([]string{"shell.omz.v1"})) > 0 {
		s.shellErr = "prompt themes need the local server to be restarted on this build"
		return s, nil
	}
	return s, func() tea.Msg {
		var th proto.ShellThemes
		err := callCtx(c, proto.MethodShellThemes, nil, &th)
		return shellThemesMsg{themes: th, err: err}
	}
}

// save writes the configuration in the background.
func saveConfig(cfg config.Config) tea.Cmd {
	return func() tea.Msg {
		if err := config.Save(cfg); err != nil {
			return errMsg{fmt.Errorf("saving settings: %w", err)}
		}
		return nil
	}
}

func (s *settings) items(m *Model) []settingItem {
	switch s.tab {
	case 0:
		return s.themeItems(m)
	case 1:
		return s.notifyItems(m)
	case 3:
		return s.brainItems(m)
	case 4:
		return s.sandboxItems(m)
	}
	return s.agentItems(m)
}

func (s *settings) themeItems(m *Model) []settingItem {
	items := []settingItem{{header: true, label: "Colours"}}
	current := themeByName(m.cfg.UI.Theme).name
	for _, t := range themes {
		t := t
		items = append(items, settingItem{label: t.label, detail: t.swatch(), mark: t.name == current,
			run: func(m *Model) tea.Cmd {
				m.cfg.UI.Theme = t.name
				applyTheme(m.cfg.UI.Theme, m.cfg.UI.Accent)
				return saveConfig(m.cfg)
			}})
	}
	if m.cfg.UI.Accent != "" {
		items = append(items, settingItem{label: "Use the theme's accent (now " + m.cfg.UI.Accent + ")",
			run: func(m *Model) tea.Cmd {
				m.cfg.UI.Accent = ""
				applyTheme(m.cfg.UI.Theme, "")
				return saveConfig(m.cfg)
			}})
	}

	items = append(items, settingItem{}, settingItem{header: true, label: "Tree"},
		settingItem{label: "What agents spend", detail: "cost, or tokens when the agent reports none", on: &m.cfg.UI.Cost,
			run: func(m *Model) tea.Cmd {
				m.cfg.UI.Cost = !m.cfg.UI.Cost
				return saveConfig(m.cfg)
			}})

	items = append(items, settingItem{}, settingItem{header: true, label: "File icons · in the file explorer"})
	current = iconMode(m.cfg.UI.Icons)
	for _, c := range []struct{ mode, label string }{
		{iconsText, "Letters, in any font"},
		{iconsNerd, "Nerd Font glyphs"},
		{iconsOff, "None"},
	} {
		c := c
		items = append(items, settingItem{label: c.label, detail: iconSample(c.mode), mark: c.mode == current,
			run: func(m *Model) tea.Cmd {
				m.cfg.UI.Icons = c.mode
				return saveConfig(m.cfg)
			}})
	}

	items = append(items, settingItem{}, settingItem{header: true, label: "Shell prompt · Oh My Zsh theme for new zsh terminals"})
	switch {
	case s.shellErr != "":
		items = append(items, settingItem{label: s.shellErr})
	case s.shell == nil:
		items = append(items, settingItem{label: "loading…"})
	case !s.shell.OMZ:
		items = append(items, settingItem{label: "Oh My Zsh isn't installed on this computer (ohmyz.sh)"})
	default:
		own := "Keep my .zshrc theme"
		if s.shell.Current != "" {
			own += " (" + s.shell.Current + ")"
		}
		items = append(items, settingItem{label: own, mark: m.cfg.Shell.OMZTheme == "",
			run: func(m *Model) tea.Cmd { m.cfg.Shell.OMZTheme = ""; return saveConfig(m.cfg) }})
		for _, name := range s.shell.Themes {
			name := name
			items = append(items, settingItem{label: name, mark: m.cfg.Shell.OMZTheme == name,
				run: func(m *Model) tea.Cmd {
					m.cfg.Shell.OMZTheme = name
					m.setFlash("new zsh terminals use the "+name+" prompt", false)
					return saveConfig(m.cfg)
				}})
		}
	}
	return items
}

func (s *settings) notifyItems(m *Model) []settingItem {
	n := &m.cfg.Notify
	toggle := func(label, detail string, v *bool, after func(m *Model) tea.Cmd) settingItem {
		return settingItem{label: label, detail: detail, on: v, run: func(m *Model) tea.Cmd {
			*v = !*v
			cmd := saveConfig(m.cfg)
			if after != nil && *v {
				cmd = tea.Batch(cmd, after(m))
			}
			return cmd
		}}
	}
	base := []settingItem{
		toggle("Notifications", "master switch", &n.Enabled, nil),
		{},
		{header: true, label: "How"},
		toggle("Desktop notification", "macOS / notify-send", &n.Desktop, nil),
		toggle("Sound", "a system sound", &n.Sound, func(*Model) tea.Cmd { return func() tea.Msg { playSound(); return nil } }),
		toggle("Terminal beep", "the bell character", &n.Bell, func(*Model) tea.Cmd {
			return func() tea.Msg { _, _ = fmt.Print("\a"); return nil }
		}),
		{},
		{header: true, label: "When"},
		toggle("An agent is waiting for you", "permissions, questions", &n.Waiting, nil),
		toggle("An agent finishes", "while you look elsewhere", &n.Done, nil),
		toggle("A plan limit is nearly used", limitAtText(n.Thresholds()), &n.Limits, nil),
		{label: "A watched pane goes quiet after", detail: fmt.Sprintf("%ds · %s M watches a pane", n.SilenceAfter(), m.cfg.Keys.Prefix),
			run: func(m *Model) tea.Cmd {
				m.cfg.Notify.Silence = nextSilence(m.cfg.Notify.SilenceAfter())
				return saveConfig(m.cfg)
			}},
		{},
		{header: true, label: "Quiet hours", detail: "no alerts; the sidebar still shows who waits"},
	}
	items := base
	for _, q := range quietPresets {
		q := q
		label := "Off"
		if q[0] != "" {
			label = q[0] + " – " + q[1]
		}
		items = append(items, settingItem{label: label, mark: n.QuietStart == q[0] && n.QuietEnd == q[1],
			run: func(m *Model) tea.Cmd {
				m.cfg.Notify.QuietStart, m.cfg.Notify.QuietEnd = q[0], q[1]
				return saveConfig(m.cfg)
			}})
	}
	if custom := !quietPreset(n.QuietStart, n.QuietEnd); custom {
		items = append(items, settingItem{label: n.QuietStart + " – " + n.QuietEnd + styleMuted.Render(" (config.toml)"), mark: true})
	}
	snooze := settingItem{label: "Snooze alerts for 1 hour", run: func(m *Model) tea.Cmd {
		m.snoozeUntil = time.Now().Add(time.Hour)
		m.setFlash("alerts snoozed until "+m.snoozeUntil.Format("15:04"), false)
		return nil
	}}
	if time.Now().Before(m.snoozeUntil) {
		snooze = settingItem{label: "Resume alerts", detail: "snoozed until " + m.snoozeUntil.Format("15:04"), run: func(m *Model) tea.Cmd {
			m.snoozeUntil = time.Time{}
			return nil
		}}
	}
	return append(items, settingItem{}, snooze,
		settingItem{},
		settingItem{label: "Send a test notification", run: func(m *Model) tea.Cmd {
			cfg := m.cfg.Notify
			if !cfg.Enabled {
				m.setFlash("notifications are off", true)
				return nil
			}
			return notify(cfg, "conch", "Notifications work")
		}},
	)
}

// silencePresets are the quiet times enter steps through, in seconds.
var silencePresets = []int{10, 30, 60, 120, 300}

// nextSilence is the preset after cur, round to the first.
func nextSilence(cur int) int {
	for _, v := range silencePresets {
		if v > cur {
			return v
		}
	}
	return silencePresets[0]
}

var quietPresets = [][2]string{{"", ""}, {"22:00", "08:00"}, {"23:00", "07:00"}, {"20:00", "09:00"}, {"09:00", "18:00"}}

func quietPreset(start, end string) bool {
	for _, q := range quietPresets {
		if q == [2]string{start, end} {
			return true
		}
	}
	return false
}

func (s *settings) agentItems(m *Model) []settingItem {
	items := []settingItem{{header: true, label: "Default agent", detail: "pre-selected when c asks which agent"}}
	for _, name := range knownAgents(m) {
		name := name
		items = append(items, settingItem{label: agentLabel(name), mark: m.defaultAgent() == name,
			run: func(m *Model) tea.Cmd {
				m.cfg.Agents.Default = name
				return saveConfig(m.cfg)
			}})
	}
	// What an agent loads is a property of a checkout, not of conch, so it
	// is not settings — but this is where people look for it.
	items = append(items, settingItem{}, settingItem{header: true, label: "What each agent loads", detail: "per checkout, not here"},
		settingItem{label: styleMuted.Render("  i on a project, branch or pane: instructions, skills, MCP servers")},
		settingItem{label: styleMuted.Render("  s there gives the other agents that one's setup; u undoes it")})

	r := &m.cfg.Remote
	items = append(items, settingItem{}, settingItem{header: true, label: "Remote machines"},
		settingItem{label: "Upload files dropped into remote panes", detail: "screenshots and other files", on: &r.UploadDrops,
			run: func(m *Model) tea.Cmd {
				m.cfg.Remote.UploadDrops = !m.cfg.Remote.UploadDrops
				return saveConfig(m.cfg)
			}},
		settingItem{label: "Largest file to upload", detail: mbText(r.UploadLimit()) + " · enter changes",
			run: func(m *Model) tea.Cmd {
				m.cfg.Remote.UploadMaxMB = nextUploadLimit(m.cfg.Remote.UploadLimit())
				return saveConfig(m.cfg)
			}},
	)
	for _, mach := range m.machines {
		mach := mach
		state := ""
		if mach.state != stateOnline {
			state = mach.state.String()
		}
		items = append(items, settingItem{}, settingItem{header: true, label: mach.label, detail: state})
		switch {
		case mach.state != stateOnline:
			items = append(items, settingItem{label: styleMuted.Render("  agents unknown while " + mach.state.String())})
			continue
		case mach.available == nil:
			items = append(items, settingItem{label: styleMuted.Render("  agents unknown: the server there predates agent checks; upgrade it")})
			continue
		}
		for _, a := range mach.agentList {
			a := a
			item := settingItem{label: "  " + firstNonEmpty(a.Label, agentLabel(a.Name))}
			if a.Installed {
				item.detail = styleOK.Render("✓ " + a.Version)
				item.run = func(m *Model) tea.Cmd {
					m.setFlash(fmt.Sprintf("%s %s on %s at %s", item.label[2:], a.Version, mach.label, a.Path), false)
					return nil
				}
			} else {
				item.detail = styleWarn.Render("not installed · enter installs")
				item.run = func(m *Model) tea.Cmd {
					m.overlay = nil
					return m.installAgent(mach.id, a.Name)
				}
			}
			items = append(items, item)
		}
	}
	items = append(items, settingItem{},
		settingItem{label: "Check again", run: func(m *Model) tea.Cmd {
			var cmds []tea.Cmd
			for _, mach := range m.machines {
				cmds = append(cmds, mach.checkAgents())
			}
			m.setFlash("checking agents on every machine…", false)
			return tea.Batch(cmds...)
		}},
	)
	return items
}

func (s *settings) brainItems(m *Model) []settingItem {
	b := &m.cfg.Brain
	items := []settingItem{{header: true, label: "Provider", detail: "the model behind ✦ Ask and summaries"}}
	current := firstNonEmpty(b.Provider, "claude")
	for _, name := range brain.Providers {
		name := name
		cfg := *b
		cfg.Provider = name
		detail := styleOK.Render("✓ ready")
		if p, err := brain.New(cfg); err != nil {
			detail = styleErr.Render(err.Error())
		} else if err := p.Check(); err != nil {
			detail = styleWarn.Render(strings.TrimPrefix(err.Error(), brain.ErrNotConfigured.Error()+": "))
		}
		items = append(items, settingItem{label: brain.ProviderLabel(name), detail: ansi.Truncate(detail, 44, "…"), mark: current == name,
			run: func(m *Model) tea.Cmd {
				if m.cfg.Brain.Provider != name {
					m.cfg.Brain.Provider, m.cfg.Brain.Model, m.cfg.Brain.SummaryModel = name, "", ""
				}
				return saveConfig(m.cfg)
			}})
	}

	var models []string
	switch current {
	case "claude":
		models = []string{"sonnet", "opus", "haiku"}
	case "anthropic":
		models = []string{"claude-sonnet-5", "claude-opus-5", "claude-haiku-4-5"}
	}
	items = append(items, settingItem{}, settingItem{header: true, label: "Model", detail: "for planning"})
	if len(models) == 0 {
		model := firstNonEmpty(b.Model, "not set")
		items = append(items, settingItem{label: "  " + model + styleMuted.Render(" · set [brain] model and base_url in config.toml")})
	} else {
		items = append(items, settingItem{label: "Provider default (" + models[0] + ")", mark: b.Model == "",
			run: func(m *Model) tea.Cmd { m.cfg.Brain.Model = ""; return saveConfig(m.cfg) }})
		for _, name := range models {
			name := name
			items = append(items, settingItem{label: name, mark: b.Model == name,
				run: func(m *Model) tea.Cmd { m.cfg.Brain.Model = name; return saveConfig(m.cfg) }})
		}
	}

	items = append(items, settingItem{}, settingItem{header: true, label: "Summaries"},
		settingItem{label: "Summarise agents when they finish or need you", detail: "a small model request each", on: &b.Summaries,
			run: func(m *Model) tea.Cmd {
				m.cfg.Brain.Summaries = !m.cfg.Brain.Summaries
				return saveConfig(m.cfg)
			}},
		settingItem{label: styleMuted.Render("  S summarises the selected agent on demand · : opens ✦ Ask")},
		settingItem{label: styleMuted.Render("  Summaries send the agent's visible screen to the provider.")},
	)
	return items
}

// idleStopChoices are the minutes a sandbox may sit idle before conch
// stops it. The default is 30: a provider's own timer can't tell an agent
// at work from an empty machine, so conch does the watching.
var idleStopChoices = []int{15, 30, 60, 120, 0}

func nextIdleStop(now int) int {
	for i, v := range idleStopChoices {
		if v == now {
			return idleStopChoices[(i+1)%len(idleStopChoices)]
		}
	}
	return config.IdleStopDefault
}

func idleStopText(min int) string {
	if min <= 0 {
		return "never · it runs, and costs, until you stop it"
	}
	return fmt.Sprintf("after %dm with no agent working and nothing printing", min)
}

// sandboxItems is the Sandboxes tab: the providers conch knows, and the
// page of one of them. Every provider takes the same settings, so the tab
// lists them and opens one rather than growing by a section each time
// conch learns another.
func (s *settings) sandboxItems(m *Model) []settingItem {
	if len(sandbox.Providers) == 0 {
		return []settingItem{
			{header: true, label: "Sandboxes", detail: "M → New sandbox… makes one and adds it as a machine"},
			{label: "  this build knows no sandbox providers"},
		}
	}
	if s.provider != "" && sandbox.Known(s.provider) {
		return s.providerPage(m, s.provider)
	}
	items := []settingItem{
		{header: true, label: "Sandboxes", detail: "M → New sandbox… makes one and adds it as a machine"},
	}
	for _, name := range sandbox.Providers {
		name := name
		items = append(items, settingItem{label: providerLabel(name), detail: providerState(m, name), page: true,
			run: func(m *Model) tea.Cmd { s.open(name); return nil }})
	}
	return append(items,
		settingItem{},
		settingItem{label: styleMuted.Render("  Enter opens a provider: the key it uses, what its sandboxes are made")},
		settingItem{label: styleMuted.Render("  from, and what of your environment goes into them.")})
}

// open shows one provider's page; esc goes back to the list.
func (s *settings) open(provider string) {
	s.provider, s.sel, s.scroll = provider, 0, 0
}

// providerPage is one provider's own page, with the way back at the top so
// the mouse has one as well as esc.
func (s *settings) providerPage(m *Model, provider string) []settingItem {
	items := []settingItem{
		{header: true, label: providerLabel(provider), detail: providerState(m, provider)},
		{label: styleMuted.Render("‹ Sandboxes"), detail: styleMuted.Render("esc"),
			run: func(m *Model) tea.Cmd { s.open(""); return nil }},
	}
	items = append(items, s.providerItems(m, provider)...)
	return append(items,
		settingItem{},
		settingItem{label: styleMuted.Render("  These are the defaults for new sandboxes; the dialog that makes one can")},
		settingItem{label: styleMuted.Render("  change them, and pass in more, for that sandbox only.")})
}

// providerState says whether a provider is ready to be used, without
// showing the key.
func providerState(m *Model, provider string) string {
	cfg := m.cfg.Sandbox.Of(provider)
	keyEnv := firstNonEmpty(cfg.APIKeyEnv, defaultKeyEnv(provider))
	if p, err := openSandboxProvider(provider); err != nil {
		return styleErr.Render(ansi.Truncate(err.Error(), 44, "…"))
	} else if err := p.Check(); err != nil {
		return styleWarn.Render("no key · $" + keyEnv + " is not set")
	}
	if strings.TrimSpace(cfg.APIKey) != "" {
		return styleOK.Render("✓ key kept in the settings")
	}
	return styleOK.Render("✓ $" + keyEnv + " is set")
}

// providerItems are one provider's settings. Every provider takes the same
// ones, so a new one needs nothing here — except a life, which only a
// provider that gives its sandboxes one is asked for.
func (s *settings) providerItems(m *Model, provider string) []settingItem {
	cfg := m.cfg.Sandbox.Of(provider)
	label := providerLabel(provider)
	keyEnv := firstNonEmpty(cfg.APIKeyEnv, defaultKeyEnv(provider))
	// set stores one field, leaving the provider's others as they are.
	set := func(change func(c *config.ProviderCfg)) func(m *Model) tea.Cmd {
		return func(m *Model) tea.Cmd {
			c := m.cfg.Sandbox.Of(provider)
			change(&c)
			m.cfg.Sandbox.Set(provider, c)
			return saveConfig(m.cfg)
		}
	}
	field := func(name, detail, help, current string, save func(c *config.ProviderCfg, v string) error) settingItem {
		return settingItem{label: name, detail: detail, run: func(m *Model) tea.Cmd {
			d := newDialog(*m, " "+label+" · "+name+" ", []string{help}, []string{name}, []string{current})
			d.back = s // esc, and saving, come back to the settings screen
			d.submit = func(m *Model, v []string) tea.Cmd {
				c := m.cfg.Sandbox.Of(provider)
				if err := save(&c, strings.TrimSpace(v[0])); err != nil {
					return func() tea.Msg { return errMsg{err} }
				}
				m.cfg.Sandbox.Set(provider, c)
				return saveConfig(m.cfg)
			}
			m.overlay = d
			return d.focusCmd()
		}}
	}
	// A provider that gives a sandbox a length of life, counted from when
	// it was made, is asked how long, beside the idle stop that is conch's
	// own. One that stops an idle sandbox itself is not asked: conch leaves
	// that timer off on purpose, since an agent working quietly looks idle
	// to it.
	var life []settingItem
	if p, err := openSandboxProvider(provider); err == nil {
		if l, ok := p.(sandbox.Lifetime); ok {
			life = append(life, field("Life", lifeText(cfg.AutoStop, l.Life()),
				"How long a new sandbox is given, in minutes, counted from when it is made — "+label+"'s own clock, whatever is happening inside. Empty means "+
					label+"'s default of "+shortDuration(l.Life())+". A plan that allows less says so, and conch asks again for the longest it named; conch's own idle watch stops a sandbox nobody is using well before either.",
				lifeValue(cfg.AutoStop), func(c *config.ProviderCfg, v string) error {
					n, err := parseMinutes(v)
					if err != nil {
						return err
					}
					c.AutoStop = n
					return nil
				}))
		}
	}
	items := []settingItem{
		field("API key", keyDetail(cfg.APIKey, keyEnv),
			"The key itself, kept in config.toml in your home — written 0600, but anything running as you can read it, and it travels with a backup or a synced dotfile. Empty leaves it to the variable below, which is what conch does otherwise.",
			cfg.APIKey, func(c *config.ProviderCfg, v string) error { c.APIKey = v; return nil }),
		field("API key variable", keyEnv,
			"Which variable of your environment "+label+"'s key is read from, when no key is kept above. Empty means "+defaultKeyEnv(provider)+".",
			cfg.APIKeyEnv, func(c *config.ProviderCfg, v string) error { c.APIKeyEnv = v; return nil }),
		field("Snapshot", firstNonEmpty(cfg.Snapshot, styleMuted.Render(label+"'s default")),
			"What new sandboxes start from. Empty means "+label+"'s default.",
			cfg.Snapshot, func(c *config.ProviderCfg, v string) error { c.Snapshot = v; return nil }),
		field("Region", firstNonEmpty(cfg.Target, styleMuted.Render("the account's default")),
			"Where sandboxes are made, e.g. us or eu. Empty means the account's default.",
			cfg.Target, func(c *config.ProviderCfg, v string) error { c.Target = v; return nil }),
		{label: "Bring back what was running", detail: restoreText(cfg.RestoresRunning()), on: boolOf(cfg.RestoresRunning()),
			run: set(func(c *config.ProviderCfg) {
				v := !c.RestoresRunning()
				c.Restore = &v
			})},
		{label: "Stop when idle", detail: idleStopText(cfg.IdleMinutes()), run: set(func(c *config.ProviderCfg) {
			n := nextIdleStop(c.IdleMinutes())
			c.IdleStop = &n
		})},
	}
	items = append(items, life...)
	items = append(items,
		settingItem{label: "Price an hour", detail: priceText(cfg), run: func(m *Model) tea.Cmd {
			c := m.cfg.Sandbox.Of(provider)
			d := newDialog(*m, " "+label+" · price an hour ",
				[]string{"What an hour costs, so conch can say what a sandbox has run up. conch ships no price list — providers change theirs — so take these from " + label + "'s own pricing page. Empty or 0 shows running time alone."},
				[]string{"Per vCPU", "Per GiB memory", "Per GiB disk"},
				[]string{priceValue(c.PriceCPUHour), priceValue(c.PriceGiBHour), priceValue(c.PriceDiskGiBHour)})
			d.back = s
			d.submit = func(m *Model, v []string) tea.Cmd {
				c := m.cfg.Sandbox.Of(provider)
				for i, into := range []*float64{&c.PriceCPUHour, &c.PriceGiBHour, &c.PriceDiskGiBHour} {
					got, err := parsePrice(v[i])
					if err != nil {
						return func() tea.Msg { return errMsg{err} }
					}
					*into = got
				}
				m.cfg.Sandbox.Set(provider, c)
				return saveConfig(m.cfg)
			}
			m.overlay = d
			return d.focusCmd()
		}},
		field("Pass in", envText(cfg.Env),
			"Names of your environment variables to pass into every new "+label+" sandbox, separated by spaces or commas — an agent's token, say (CLAUDE_CODE_OAUTH_TOKEN). Their values are read when a sandbox is made, never stored here.",
			strings.Join(cfg.Env, " "), func(c *config.ProviderCfg, v string) error {
				names, err := envNames(v)
				if err != nil {
					return err
				}
				c.Env = names
				return nil
			}))
	return items
}

// lifeText says how long a new sandbox lives, and whether that is the
// provider's own length or one you set.
func lifeText(minutes int, life time.Duration) string {
	if minutes <= 0 {
		return styleMuted.Render(shortDuration(life) + " · the provider's own")
	}
	return shortDuration(time.Duration(minutes)*time.Minute) + " from when it is made"
}

func lifeValue(minutes int) string {
	if minutes <= 0 {
		return ""
	}
	return strconv.Itoa(minutes)
}

// parseMinutes reads a number of minutes; empty is the provider's default.
func parseMinutes(v string) (int, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%q: give a number of minutes, or nothing for the provider's own", v)
	}
	return n, nil
}

// keyDetail says whether a key is kept here, without showing it: enough to
// tell one from another, and nothing more.
func keyDetail(key, env string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return styleMuted.Render("not set · $" + env + " is used")
	}
	shown := "…"
	if r := []rune(key); len(r) > 4 {
		shown = "…" + string(r[len(r)-4:])
	}
	return styleWarn.Render("kept in config.toml " + shown)
}

// boolOf is a value a toggle can point at: settings are rebuilt on every
// look, so the pointer lives as long as the line it draws.
func boolOf(v bool) *bool { return &v }

// restoreText says what remembering does, and what it does not.
func restoreText(on bool) string {
	if !on {
		return styleMuted.Render("no note is kept of what ran")
	}
	return "offers the agents and terminals back, resumed where they left off"
}

// priceText says whether conch can work out what a sandbox costs.
func priceText(cfg config.ProviderCfg) string {
	if !cfg.Priced() {
		return styleMuted.Render("not set · running time is shown without a cost")
	}
	return fmt.Sprintf("$%g vCPU · $%g GiB · $%g disk", cfg.PriceCPUHour, cfg.PriceGiBHour, cfg.PriceDiskGiBHour)
}

// priceValue is a price for the dialog to start from; nothing for zero,
// rather than a 0 to delete.
func priceValue(v float64) string {
	if v <= 0 {
		return ""
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// parsePrice reads a price a person typed, with or without its currency.
func parsePrice(s string) (float64, error) {
	s = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "$"))
	if s == "" {
		return 0, nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v < 0 {
		return 0, fmt.Errorf("%q: give a price an hour, e.g. 0.0504", s)
	}
	return v, nil
}

// defaultKeyEnv is the variable a provider reads its key from when the
// settings name none.
func defaultKeyEnv(provider string) string {
	return strings.ToUpper(provider) + "_API_KEY"
}

// envText says what is passed into a new sandbox.
func envText(names []string) string {
	if len(names) == 0 {
		return styleMuted.Render("nothing")
	}
	return strings.Join(names, " ")
}

// envNames splits a list of environment variable names, refusing anything
// that isn't one: a value pasted in with it, or a name with spaces.
func envNames(s string) ([]string, error) {
	var names []string
	for _, name := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' }) {
		if strings.ContainsAny(name, "=\"'") {
			return nil, fmt.Errorf("%s: give the name of a variable, not its value", name)
		}
		names = append(names, name)
	}
	return names, nil
}

// uploadLimits are the choices for the largest file dropped into a remote
// pane, in MB. The server takes up to 256.
var uploadLimits = []int{10, 25, 50, 100, 250}

// nextUploadLimit is the choice after limit (bytes), wrapping around.
func nextUploadLimit(limit int64) int {
	for _, mb := range uploadLimits {
		if int64(mb)<<20 > limit {
			return mb
		}
	}
	return uploadLimits[0]
}

// knownAgents lists agent names any connected machine reported, in order,
// falling back to the ones conch ships adapters for.
func knownAgents(m *Model) []string {
	seen := map[string]bool{}
	var names []string
	for _, mach := range m.machines {
		for _, a := range mach.agentList {
			if !seen[a.Name] {
				seen[a.Name] = true
				names = append(names, a.Name)
			}
		}
	}
	if len(names) == 0 {
		names = []string{"claude", "codex", "gemini", "opencode"}
	}
	return names
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func (s *settings) update(m *Model, msg tea.Msg) (bool, tea.Cmd) {
	switch msg := msg.(type) {
	case shellThemesMsg:
		if msg.err != nil {
			s.shellErr = msg.err.Error()
		} else {
			s.shell = &msg.themes
		}
		return false, nil
	case tea.KeyMsg:
		items := s.items(m)
		switch msg.String() {
		case "esc", "q", ",":
			// Inside a provider's page, esc is the way back to the list.
			if s.provider != "" && msg.String() == "esc" {
				s.open("")
				return false, nil
			}
			m.overlay = nil
			return true, nil
		case "tab", "right", "l":
			s.setTab((s.tab + 1) % len(settingsTabs))
		case "shift+tab", "left", "h":
			s.setTab((s.tab + len(settingsTabs) - 1) % len(settingsTabs))
		case "1", "2", "3", "4", "5":
			s.setTab(min(int(msg.String()[0]-'1'), len(settingsTabs)-1))
		case "up", "k":
			s.move(items, -1)
		case "down", "j":
			s.move(items, 1)
		case "pgup":
			s.move(items, -10)
		case "pgdown":
			s.move(items, 10)
		case "enter", " ":
			if s.sel < len(items) && items[s.sel].run != nil {
				return false, items[s.sel].run(m)
			}
		}
	}
	return false, nil
}

func (s *settings) setTab(t int) {
	s.tab, s.sel, s.scroll, s.provider = t, 0, 0, ""
}

// move steps the selection by delta, skipping lines that do nothing.
func (s *settings) move(items []settingItem, delta int) {
	if len(items) == 0 {
		return
	}
	step := 1
	if delta < 0 {
		step = -1
	}
	target := clamp(s.sel+delta, 0, len(items)-1)
	// The first selectable line at or past the target; at a list edge, the
	// nearest one back toward the current selection.
	for i := target; i >= 0 && i < len(items); i += step {
		if items[i].run != nil {
			s.sel = i
			return
		}
	}
	for i := target - step; i != s.sel && i >= 0 && i < len(items); i -= step {
		if items[i].run != nil {
			s.sel = i
			return
		}
	}
}

func (m Model) settingsListHeight() int { return clamp(m.height-10, 6, 24) }

func (s *settings) render(m Model) box {
	w := clamp(78, 40, max(m.width-4, 40))
	items := s.items(&m)
	if s.sel >= len(items) || items[s.sel].run == nil {
		s.move(items, 0)
		if s.sel < len(items) && items[s.sel].run == nil {
			s.move(items, 1)
		}
	}
	listH := m.settingsListHeight()
	if s.sel < s.scroll {
		s.scroll = s.sel
	}
	if s.sel >= s.scroll+listH {
		s.scroll = s.sel - listH + 1
	}

	var tabs strings.Builder
	for i, name := range settingsTabs {
		label := fmt.Sprintf(" %d %s ", i+1, name)
		if i == s.tab {
			tabs.WriteString(styleSel.Render(label))
		} else {
			tabs.WriteString(styleMuted.Render(label))
		}
		tabs.WriteString(" ")
	}
	lines := []string{tabs.String(), ""}
	for i := s.scroll; i < s.scroll+listH; i++ {
		if i >= len(items) {
			lines = append(lines, "")
			continue
		}
		lines = append(lines, s.itemLine(items[i], i == s.sel, w))
	}
	if more := len(items) - (s.scroll + listH); more > 0 {
		lines[len(lines)-1] = styleMuted.Render(fmt.Sprintf("  … %d more", more))
	}
	hint := " tab switch · ↑↓ move · enter choose/toggle · esc close"
	if s.provider != "" {
		hint = " tab switch · ↑↓ move · enter choose/toggle · esc back to the providers"
	}
	lines = append(lines, "", styleMuted.Render(hint),
		styleMuted.Render(" saved to "+ansi.Truncate(config.Dir()+"/config.toml", w-10, "…")))
	b := box{lines: frameLines(" ⚙ Settings ", lines, w, colorAccent)}
	b.x = max((m.width-b.width())/2, 0)
	b.y = max((m.height-len(b.lines))/4, 0)
	return b
}

func (s *settings) itemLine(it settingItem, selected bool, w int) string {
	if it.header {
		return spread(" "+styleBold.Render(it.label), styleMuted.Render(it.detail), w)
	}
	prefix := "   "
	switch {
	case it.on != nil && *it.on:
		prefix = " " + styleOK.Render("■") + " "
	case it.on != nil:
		prefix = " " + styleMuted.Render("□") + " "
	case it.mark:
		prefix = " " + styleAccent.Render("●") + " "
	case it.page:
		prefix = " " + styleMuted.Render("›") + " "
	case it.run != nil:
		prefix = " " + styleMuted.Render("○") + " "
	}
	if selected {
		plain := ansi.Strip(prefix) + it.label
		return styleSel.Render(spread(plain, ansi.Strip(it.detail), w))
	}
	return spread(prefix+it.label, it.detail, w)
}

func (s *settings) mouse(m *Model, msg tea.MouseMsg, b box) tea.Cmd {
	if !b.contains(msg.X, msg.Y) {
		if msg.Action == tea.MouseActionPress {
			m.overlay = nil
		}
		return nil
	}
	items := s.items(m)
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		s.move(items, -3)
		return nil
	case tea.MouseButtonWheelDown:
		s.move(items, 3)
		return nil
	}
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return nil
	}
	row := msg.Y - b.y - 1
	if row == 0 { // the tab bar
		x := msg.X - b.x - 1
		for i, name := range settingsTabs {
			width := len(fmt.Sprintf(" %d %s ", i+1, name)) + 1
			if x < width {
				s.setTab(i)
				return nil
			}
			x -= width
		}
		return nil
	}
	i := s.scroll + row - 2
	if row >= 2 && i < len(items) && items[i].run != nil {
		s.sel = i
		return items[i].run(m)
	}
	return nil
}

// limitAtText describes the alert thresholds, e.g. "Claude/Codex at 80%, 95%".
func limitAtText(at []int) string {
	var parts []string
	for _, p := range at {
		parts = append(parts, strconv.Itoa(p)+"%")
	}
	return "Claude/Codex at " + strings.Join(parts, ", ")
}
