package tui

import (
	"fmt"
	"slices"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/configweb"
	"github.com/Amitgb14/conch/internal/sandbox"
)

// Edit in browser: the settings as a page (internal/configweb), served by
// this TUI on 127.0.0.1 for as long as it runs. It is the TUI's rather than
// a process of its own because the TUI holds the configuration in memory
// and writes all of it back whenever a setting changes here — so a page
// saving behind its back would be undone by the next toggle. Served from
// here, every save is handed straight in (configSavedMsg).

// configWebState is the editor while it runs.
type configWebState struct {
	run   *configweb.Running
	saved chan config.Config
	done  chan struct{} // closed when the page stops, ending the wait for a save
}

// configSavedMsg is a configuration the page saved.
type configSavedMsg struct{ cfg config.Config }

// configWebStarted reports the editor listening, or why it isn't.
type configWebStarted struct {
	st  *configWebState
	err error
}

// listenConfigWeb starts the editor; a variable so tests run it on a port
// of their own without a browser.
var listenConfigWeb = func(c configweb.Choices, saved chan config.Config) (*configweb.Running, error) {
	srv, err := configweb.New(c)
	if err != nil {
		return nil, err
	}
	srv.OnSave = func(cfg config.Config) {
		// Never block the page's request on the TUI: a newer save
		// replaces one not yet taken, since each is the whole file.
		select {
		case saved <- cfg:
		default:
			select {
			case <-saved:
			default:
			}
			select {
			case saved <- cfg:
			default:
			}
		}
	}
	return srv.Listen(0)
}

// settingsChoices is what the page offers that only the TUI knows.
func settingsChoices(m *Model) configweb.Choices { return SettingsChoices(knownAgents(m)) }

// SettingsChoices is what the settings page offers: the themes and accents
// this build draws, the sandbox providers it knows, and agents (the four
// supported ones when none are given). conch config web passes none, since
// it has no machine to ask.
func SettingsChoices(agents []string) configweb.Choices {
	c := configweb.Choices{Providers: sandbox.Providers}
	for _, t := range themes {
		c.Themes = append(c.Themes, configweb.Option{Value: t.name, Label: t.label})
	}
	for name := range accentColors {
		c.Accents = append(c.Accents, name)
	}
	slices.Sort(c.Accents)
	if len(agents) == 0 {
		agents = []string{"claude", "codex", "gemini", "opencode", "devin"}
	}
	for _, a := range agents {
		c.Agents = append(c.Agents, configweb.Option{Value: a, Label: agentLabel(a)})
	}
	return c
}

// editInBrowser opens the settings page, starting it the first time.
func (m *Model) editInBrowser() tea.Cmd {
	if m.configWeb != nil {
		return openURL(m.configWeb.run.URL)
	}
	choices := settingsChoices(m)
	return func() tea.Msg {
		saved := make(chan config.Config, 1)
		run, err := listenConfigWeb(choices, saved)
		if err != nil {
			return configWebStarted{err: err}
		}
		return configWebStarted{st: &configWebState{run: run, saved: saved, done: make(chan struct{})}}
	}
}

// waitConfigSaved hands the next save in, or nothing once the page stops.
func waitConfigSaved(st *configWebState) tea.Cmd {
	return func() tea.Msg {
		select {
		case cfg := <-st.saved:
			return configSavedMsg{cfg}
		case <-st.done:
			return nil
		}
	}
}

func (m *Model) takeConfigWeb(msg configWebStarted) tea.Cmd {
	if msg.err != nil {
		return func() tea.Msg { return errMsg{fmt.Errorf("starting the settings page: %w", msg.err)} }
	}
	if m.configWeb != nil { // two presses raced; keep the first
		_ = msg.st.run.Close()
		close(msg.st.done)
		return openURL(m.configWeb.run.URL)
	}
	m.configWeb = msg.st
	return tea.Batch(openURL(msg.st.run.URL), waitConfigSaved(msg.st))
}

// takeSavedConfig is a save from the page: what it changed applies here as
// a change on the settings screen would.
func (m *Model) takeSavedConfig(cfg config.Config) tea.Cmd {
	hoverWas := m.cfg.UI.Hover
	m.cfg = cfg
	applyTheme(cfg.UI.Theme, cfg.UI.Accent)
	if hoverWas && !cfg.UI.Hover {
		m.clearHover()
	}
	m.setFlash("settings saved from the browser", false)
	if m.configWeb == nil {
		return nil
	}
	return waitConfigSaved(m.configWeb)
}

// closeConfigWeb stops the page, as the TUI quits.
func (m *Model) closeConfigWeb() {
	if m.configWeb != nil {
		_ = m.configWeb.run.Close()
		close(m.configWeb.done)
		m.configWeb = nil
	}
}
