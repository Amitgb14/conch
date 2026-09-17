package tui

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/remote"
)

// SSH sessions are terminals on this computer running ssh to another host:
// a plain login, with nothing installed there (unlike adding a machine).

// sshPane reports whether a pane is an ssh session rather than a shell.
func sshPane(p proto.PaneInfo) bool {
	return p.Agent == nil && remote.IsLoginCommand(p.Command)
}

// sshHosts are the hosts to offer: aliases from ~/.ssh/config, then the
// targets of machines already added, each once.
func (m Model) sshHosts() []string {
	var hosts []string
	for _, h := range remote.SSHHosts() {
		if !slices.Contains(hosts, h) {
			hosts = append(hosts, h)
		}
	}
	for _, mach := range m.machines {
		if mach.id != localMachine && mach.target != "" && !slices.Contains(hosts, mach.target) {
			hosts = append(hosts, mach.target)
		}
	}
	return hosts
}

// openSSH asks where to ssh to: a menu of known hosts, or straight to typing
// one when there are none.
func (m *Model) openSSH() tea.Cmd {
	if m.clientOf(localMachine) == nil {
		m.setFlash(m.offlineText(localMachine), true)
		return nil
	}
	hosts := m.sshHosts()
	if len(hosts) == 0 {
		d := newSSHDialog(*m)
		m.overlay = d
		return d.focusCmd()
	}
	// The menu doesn't scroll: keep it on screen, typing reaches the rest.
	if limit := max(m.height-3, 1); len(hosts) > limit {
		hosts = hosts[:limit]
	}
	var items []menuItem
	for i, h := range hosts {
		h := h
		key := ""
		if i < 9 {
			key = fmt.Sprint(i + 1)
		}
		items = append(items, menuItem{key, h, func(m *Model) tea.Cmd { return m.startSSH(h) }})
	}
	items = append(items, menuItem{"e", "Enter a host…", func(m *Model) tea.Cmd {
		d := newSSHDialog(*m)
		m.overlay = d
		return d.focusCmd()
	}})
	m.overlay = &menu{title: "SSH from local to", items: items, x: max(m.width/2-20, 0), y: max(m.height/3, 0)}
	return nil
}

func newSSHDialog(m Model) *dialog {
	d := newDialog(m, " SSH from local ", []string{"Opens a terminal here logged in to the host, with your ssh config and keys. Nothing is installed there."},
		[]string{"Host"}, nil)
	d.fields[0].in.Placeholder = "user@host, host alias or ssh://user@host:port"
	d.submit = func(m *Model, v []string) tea.Cmd { return m.startSSH(strings.TrimSpace(v[0])) }
	return d
}

// startSSH opens a terminal on this computer running ssh to target, outside
// every project.
func (m Model) startSSH(target string) tea.Cmd {
	command, err := remote.LoginCommand(target)
	if err != nil {
		return func() tea.Msg { return errMsg{err} }
	}
	c := m.clientOf(localMachine)
	if c == nil {
		return func() tea.Msg { return errMsg{errString(m.offlineText(localMachine))} }
	}
	cols, rows := m.paneArea()
	home, _ := os.UserHomeDir()
	params := proto.PaneCreateParams{Name: "ssh " + sshName(target), Command: command, Cwd: home, Cols: cols, Rows: rows, NoProject: true}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		var info proto.PaneInfo
		if err := c.Call(ctx, proto.MethodPaneCreate, params, &info); err != nil {
			return errMsg{err}
		}
		return createdMsg{machine: localMachine, info: info}
	}
}

// sshName is how a session names its host: an ssh:// URL without its
// scheme, so it doesn't read "ssh ssh://…".
func sshName(target string) string {
	if rest, ok := strings.CutPrefix(target, "ssh://"); ok && strings.TrimRight(rest, "/") != "" {
		return strings.TrimRight(rest, "/")
	}
	return target
}

// newTabMenu is the tab bar's + button: an empty tab, or something new in it.
func newTabMenu(m Model, x, y int) *menu {
	return &menu{title: "New", x: x, y: y, items: []menuItem{
		{"t", "Empty tab", func(m *Model) tea.Cmd { return m.newTab(viewRef{}) }},
		{"n", "Terminal · " + m.placeLabel(), func(m *Model) tea.Cmd { return m.openHere(false) }},
		{"c", "Agent · " + m.placeLabel() + "…", func(m *Model) tea.Cmd {
			pl := m.contextPlace()
			mach := m.machine(pl.machine)
			if mach == nil || mach.c == nil {
				m.setFlash(m.offlineText(pl.machine), true)
				return nil
			}
			m.overlay = newAgentMenu(*m, mach)
			return nil
		}},
		{"H", "SSH to a host…", func(m *Model) tea.Cmd { return m.openSSH() }},
	}}
}
