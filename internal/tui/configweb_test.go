package tui

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/configweb"
)

// settingsModel is a model with the settings open, on a CONCH_HOME of its
// own, opening nothing in a browser but saying what it would have.
func settingsModel(t *testing.T) (*Model, *settings, *[]string) {
	t.Helper()
	t.Setenv("CONCH_HOME", t.TempDir())
	t.Cleanup(func() { applyTheme("conch", "") })
	var opened []string
	old := openInBrowser
	openInBrowser = func(url string) error { opened = append(opened, url); return nil }
	t.Cleanup(func() { openInBrowser = old })
	m := &Model{cfg: config.Default(), width: 100, height: 40}
	s := &settings{}
	m.overlay = s
	t.Cleanup(m.closeConfigWeb)
	return m, s, &opened
}

// runCmds runs a command and every command its messages lead to, the way
// Bubble Tea would, handing the page's messages to the model. A command
// still running after a moment is the wait for a save, which blocks until
// there is one: it is left, and ends when the page closes.
func runCmds(t *testing.T, m *Model, cmd tea.Cmd) []tea.Msg {
	t.Helper()
	var msgs []tea.Msg
	queue := []tea.Cmd{cmd}
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		got := make(chan tea.Msg, 1)
		go func() { got <- c() }()
		var msg tea.Msg
		select {
		case msg = <-got:
		case <-time.After(200 * time.Millisecond):
			continue
		}
		switch msg := msg.(type) {
		case tea.BatchMsg:
			queue = append(queue, msg...)
			continue
		case configWebStarted:
			queue = append(queue, m.takeConfigWeb(msg))
		case configSavedMsg:
			queue = append(queue, m.takeSavedConfig(msg.cfg))
		}
		msgs = append(msgs, msg)
	}
	return msgs
}

func TestSettingsEditInBrowserStartsThePageOnce(t *testing.T) {
	m, s, opened := settingsModel(t)
	_, cmd := s.update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	runCmds(t, m, cmd)
	if m.configWeb == nil {
		t.Fatal("e didn't start the page")
	}
	url := m.configWeb.run.URL
	if len(*opened) != 1 || (*opened)[0] != url || !strings.Contains(url, "127.0.0.1") || !strings.Contains(url, "#t=") {
		t.Fatalf("opened %v, want %q", *opened, url)
	}
	if m.overlay != s {
		t.Fatal("the settings closed")
	}

	// Again: the same page, not a second one.
	_, cmd = s.update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	runCmds(t, m, cmd)
	if len(*opened) != 2 || (*opened)[1] != url {
		t.Fatalf("opened %v", *opened)
	}
}

// A save in the browser reaches the TUI's own copy, so the next change on
// the settings screen doesn't write the older one back over it.
func TestSettingsSavedInBrowserReachTheTUI(t *testing.T) {
	m, _, _ := settingsModel(t)
	// Started by hand, leaving out the wait for a save that runCmds would
	// start in the background: that one would take the save this test
	// waits for itself.
	started, ok := m.editInBrowser()().(configWebStarted)
	if !ok || started.err != nil {
		t.Fatalf("not started: %+v", started)
	}
	m.takeConfigWeb(started)
	if m.configWeb == nil {
		t.Fatal("not started")
	}
	base, token, _ := strings.Cut(m.configWeb.run.URL, "#t=")
	api := strings.TrimSuffix(base, "/") + "/api/config"

	req, _ := http.NewRequest(http.MethodGet, api, nil)
	req.Header.Set(configweb.TokenHeader, token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var p configweb.Payload
	_ = json.NewDecoder(res.Body).Decode(&p)
	res.Body.Close()
	// The page offers this build's themes.
	var themesOffered string
	for _, sec := range p.Schema {
		for _, f := range sec.Fields {
			if f.Key == "theme" {
				for _, o := range f.Options {
					themesOffered += o.Value + " "
				}
			}
		}
	}
	if !strings.Contains(themesOffered, "nord") || !strings.Contains(themesOffered, "dracula") {
		t.Fatalf("themes offered: %q", themesOffered)
	}

	m.cfg.UI.Hover = true
	m.hoverRow = "row"
	p.Config["ui"].(map[string]any)["theme"] = "nord"
	p.Config["ui"].(map[string]any)["hover"] = false
	body, _ := json.Marshal(map[string]any{"version": p.Version, "config": p.Config})
	req, _ = http.NewRequest(http.MethodPut, api, bytes.NewReader(body))
	req.Header.Set(configweb.TokenHeader, token)
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	out, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("PUT: %d %s", res.StatusCode, out)
	}

	msg := waitConfigSaved(m.configWeb)()
	saved, ok := msg.(configSavedMsg)
	if !ok {
		t.Fatalf("got %T", msg)
	}
	next := m.takeSavedConfig(saved.cfg)
	if m.cfg.UI.Theme != "nord" || colorAccent != themeByName("nord").accent {
		t.Fatalf("the save wasn't taken: %q", m.cfg.UI.Theme)
	}
	if m.hoverRow != "" {
		t.Fatal("hover was turned off but the row stays lit")
	}
	if next == nil {
		t.Fatal("stopped listening for the next save")
	}
	if !strings.Contains(m.flash, "browser") {
		t.Fatalf("flash %q", m.flash)
	}
}

func TestSettingsEditInBrowserSaysWhyNot(t *testing.T) {
	m, _, opened := settingsModel(t)
	old := listenConfigWeb
	listenConfigWeb = func(configweb.Choices, chan config.Config) (*configweb.Running, error) {
		return nil, errors.New("no ports left")
	}
	t.Cleanup(func() { listenConfigWeb = old })
	msgs := runCmds(t, m, m.editInBrowser())
	var said string
	for _, msg := range msgs {
		if e, ok := msg.(errMsg); ok {
			said = e.err.Error()
		}
	}
	if !strings.Contains(said, "no ports left") || m.configWeb != nil || len(*opened) != 0 {
		t.Fatalf("said %q, page %v, opened %v", said, m.configWeb, *opened)
	}
}

// Two presses before the first started: the second page is closed, not left running.
func TestSettingsEditInBrowserRace(t *testing.T) {
	m, _, _ := settingsModel(t)
	first, second := m.editInBrowser()(), m.editInBrowser()()
	runCmds(t, m, func() tea.Msg { return first })
	keep := m.configWeb
	runCmds(t, m, func() tea.Msg { return second })
	if m.configWeb != keep {
		t.Fatal("the second page replaced the first")
	}
	other := second.(configWebStarted).st.run.URL
	base, _, _ := strings.Cut(other, "#")
	if res, err := http.Get(base); err == nil {
		res.Body.Close()
		t.Fatal("the second page is still listening")
	}
}

func TestQuittingClosesThePage(t *testing.T) {
	m, _, _ := settingsModel(t)
	runCmds(t, m, m.editInBrowser())
	base, _, _ := strings.Cut(m.configWeb.run.URL, "#")
	m.quitting()
	if m.configWeb != nil {
		t.Fatal("still held after quitting")
	}
	if res, err := http.Get(base); err == nil {
		res.Body.Close()
		t.Fatal("still listening after quitting")
	}
	m.closeConfigWeb() // and again is nothing
}

// The link under the list is drawn, fits, and opens the page when clicked.
func TestSettingsEditInBrowserLink(t *testing.T) {
	for _, size := range [][2]int{{100, 40}, {44, 12}, {30, 8}, {200, 60}} {
		m, s, opened := settingsModel(t)
		m.width, m.height = size[0], size[1]
		b := s.render(*m)
		row := settingsWebRow(m.settingsListHeight())
		line := ansi.Strip(b.lines[row+1]) // the frame's top border is line 0
		if !strings.Contains(line, "Edit in browser") {
			t.Fatalf("%v: row %d is %q", size, row, line)
		}
		for i, l := range b.lines {
			if w := ansi.StringWidth(l); w > max(m.width, b.width()) {
				t.Fatalf("%v: line %d is %d wide", size, i, w)
			}
		}
		// A click on the link opens the page.
		cmd := s.mouse(m, tea.MouseMsg{X: b.x + 3, Y: b.y + 1 + row, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}, b)
		runCmds(t, m, cmd)
		if m.configWeb == nil || len(*opened) != 1 {
			t.Fatalf("%v: a click on the link didn't open the page (%v)", size, *opened)
		}
		// Past the link's words, on the same row, is not the link.
		m.closeConfigWeb()
		cmd = s.mouse(m, tea.MouseMsg{X: b.x + 1 + ansi.StringWidth(settingsWebLabel) + 4, Y: b.y + 1 + row, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}, b)
		if cmd != nil {
			runCmds(t, m, cmd)
		}
		if m.configWeb != nil {
			t.Fatalf("%v: a click beside the link opened the page", size)
		}
	}
}

func TestSettingsChoices(t *testing.T) {
	c := SettingsChoices(nil)
	if len(c.Themes) != len(themes) || len(c.Accents) != len(accentColors) || len(c.Providers) == 0 {
		t.Fatalf("%+v", c)
	}
	for i := 1; i < len(c.Accents); i++ {
		if c.Accents[i-1] > c.Accents[i] {
			t.Fatalf("accents not sorted: %v", c.Accents)
		}
	}
	if len(c.Agents) < 4 || c.Agents[0].Label != "Claude Code" {
		t.Fatalf("agents %+v", c.Agents)
	}
	if c := SettingsChoices([]string{"aider"}); len(c.Agents) != 1 || c.Agents[0].Label != "Aider" {
		t.Fatalf("agents %+v", c.Agents)
	}
}
