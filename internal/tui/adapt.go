package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amitgb14/conch/internal/adapter"
	"github.com/Amitgb14/conch/internal/buildinfo"
	"github.com/Amitgb14/conch/internal/proto"
)

// Adapting a provider conch does not know runs `conch sandbox adapt` in a
// terminal of its own: the command is the whole flow — the docs, the
// agent's pane, the checks and the question whether to save — and a
// terminal is where it can be watched and answered. The terminal waits
// for enter at the end, so what it said is read before it closes.

// adaptScript runs the command and holds the terminal open afterwards.
func adaptScript(exe, url, agent, name string) []string {
	args := []string{adapter.ShellQuote(exe), "sandbox", "adapt"}
	if agent = strings.TrimSpace(agent); agent != "" {
		args = append(args, "-agent", adapter.ShellQuote(agent))
	}
	if name = strings.TrimSpace(name); name != "" {
		args = append(args, "-name", adapter.ShellQuote(name))
	}
	args = append(args, adapter.ShellQuote(strings.TrimSpace(url)))
	return []string{"/bin/sh", "-c", strings.Join(args, " ") + `; printf '\n[enter closes this] '; read _`}
}

// newAdaptDialog asks where the provider's API docs are.
func newAdaptDialog(m Model) *dialog {
	text := []string{
		"conch downloads the provider's API docs, and an agent reads them in a pane of its own and writes a provider file: which calls make, list, start, stop and delete a sandbox, and how ssh gets in.",
		"conch checks the file, and the list call with your key, before asking you to save it. The key is never given to the agent. A provider with no ssh cannot be adapted.",
	}
	d := newDialog(m, " Add a sandbox provider ", text, []string{"API or docs URL", "Agent", "Name"}, []string{"", "claude", ""})
	d.fields[0].in.Placeholder = "https://api.example.dev/sandboxes"
	d.fields[2].in.Placeholder = "from the URL"
	d.submit = func(m *Model, v []string) tea.Cmd {
		if strings.TrimSpace(v[0]) == "" {
			m.setFlash("give the provider's API or docs URL", true)
			return nil
		}
		return conchInPane(m, "adapt a provider", "adapting a provider: follow it in this terminal", func(exe string) []string {
			return adaptScript(exe, v[0], v[1], v[2])
		})
	}
	return d
}

// installScript runs `conch sandbox provider install`, which lists the
// registry's providers, asks which, says where its key and ssh go, and
// asks again before saving.
func installScript(exe string) []string {
	return []string{"/bin/sh", "-c", adapter.ShellQuote(exe) + ` sandbox provider install; printf '\n[enter closes this] '; read _`}
}

// installProvider installs one from the registry, in a terminal of its own.
func installProvider(m *Model) tea.Cmd {
	return conchInPane(m, "install a provider", "installing a provider: pick one in this terminal", installScript)
}

// conchInPane runs a conch command in a terminal on this computer.
func conchInPane(m *Model, name, note string, script func(exe string) []string) tea.Cmd {
	c := m.clientOf(localMachine)
	if c == nil {
		m.setFlash(m.offlineText(localMachine), true)
		return nil
	}
	exe, err := buildinfo.Executable()
	if err != nil {
		m.setFlash("cannot find conch itself: "+err.Error(), true)
		return nil
	}
	cols, rows := m.paneArea()
	params := proto.PaneCreateParams{Name: name, Command: script(exe), NoProject: true, Cols: cols, Rows: rows, Env: []string{"CONCH_PANE_ID="}}
	return func() tea.Msg {
		var info proto.PaneInfo
		if err := callCtx(c, proto.MethodPaneCreate, params, &info); err != nil {
			return errMsg{err}
		}
		return createdMsg{machine: localMachine, info: info, note: note}
	}
}
