package tui

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/remote"
)

// SSH sessions are terminals on this computer running ssh to another host:
// a plain login, with nothing installed there (unlike adding a machine).

// sshPane reports whether a pane is an ssh session rather than a shell.
func sshPane(p proto.PaneInfo) bool {
	return p.Agent == nil && remote.IsLoginCommand(p.Command)
}

// sshTarget is the host an ssh session was started for: the argument
// after "--" in the command LoginCommand builds.
func sshTarget(p proto.PaneInfo) string {
	if n := len(p.Command); sshPane(p) && n >= 2 && p.Command[n-2] == "--" {
		return p.Command[n-1]
	}
	return ""
}

// idleSavedSSH are the saved hosts no open session is logged in to.
func idleSavedSSH(saved []string, sessions []proto.PaneInfo) []string {
	var out []string
	for _, target := range saved {
		if !slices.ContainsFunc(sessions, func(p proto.PaneInfo) bool { return sshTarget(p) == target }) {
			out = append(out, target)
		}
	}
	return out
}

// cleanSavedSSH drops what a hand-edited ui.json could hold that ssh would
// not take as a host, and repeats.
func cleanSavedSSH(saved []string) []string {
	var out []string
	for _, t := range saved {
		if remote.CheckLoginTarget(t) == nil && !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	return out
}

// savedSSHTarget is the host of a saved SSH row.
func savedSSHTarget(rowID string) string {
	t, _ := strings.CutPrefix(rowID, "sshsaved:")
	return t
}

// saveSSH keeps target in the tree across runs.
func (m *Model) saveSSH(target string) tea.Cmd {
	if slices.Contains(m.savedSSH, target) {
		return nil
	}
	m.savedSSH = append(m.savedSSH, target)
	return tea.Batch(m.rebuild(), m.saveState())
}

// forgetSSH takes a saved host out of the tree. An open session to it
// keeps running.
func (m *Model) forgetSSH(target string) tea.Cmd {
	i := slices.Index(m.savedSSH, target)
	if i < 0 {
		return nil
	}
	m.savedSSH = slices.Delete(slices.Clone(m.savedSSH), i, i+1)
	m.removeRow(savedSSHID(target))
	m.setFlash("forgot "+sshName(target), false)
	return tea.Batch(m.rebuild(), m.saveState())
}

// connectSSH opens a session to target, first asking whether to keep the
// host in the tree when it isn't already. Not saving is the default: enter
// on the question connects without saving.
func (m *Model) connectSSH(target string) tea.Cmd {
	if err := remote.CheckLoginTarget(target); err != nil {
		return func() tea.Msg { return errMsg{err} }
	}
	if slices.Contains(m.savedSSH, target) {
		return m.startSSH(target)
	}
	m.overlay = &menu{title: "Save " + sshName(target) + " in the tree?", x: max(m.width/2-20, 0), y: max(m.height/3, 0), items: []menuItem{
		{"n", "Connect, don't save", func(m *Model) tea.Cmd { return m.startSSH(target) }},
		{"y", "Save and connect (listed under SSH when conch opens)", func(m *Model) tea.Cmd {
			return tea.Batch(m.saveSSH(target), m.startSSH(target))
		}},
	}}
	return nil
}

// sshHosts are the hosts to offer: saved hosts, aliases from ~/.ssh/config,
// then the targets of machines already added, each once.
func (m Model) sshHosts() []string {
	hosts := slices.Clone(m.savedSSH)
	for _, h := range remote.SSHHosts() {
		if !slices.Contains(hosts, h) {
			hosts = append(hosts, h)
		}
	}
	for _, mach := range m.machines {
		if _, _, sandbox := remote.ParseSandboxTarget(mach.target); sandbox {
			continue // there is no ssh host to offer: each login is a fresh token
		}
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
		items = append(items, menuItem{key, h, func(m *Model) tea.Cmd { return m.connectSSH(h) }})
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
	d.submit = func(m *Model, v []string) tea.Cmd { return m.connectSSH(strings.TrimSpace(v[0])) }
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

// savedSSHLines is the page of a saved host with no session open to it.
func savedSSHLines(target string, w int) []string {
	return []string{
		fit(styleBold.Render("ssh "+sshName(target))+styleMuted.Render("  saved · not connected"), w),
		"",
		styleMuted.Render(ansi.Truncate("enter or click connect · x forget", w, "…")),
	}
}
