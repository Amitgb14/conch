package tui

import (
	"context"
	"errors"
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

// Saved ssh hosts can carry a name, a group and extra ssh options, and be
// sorted into groups — eng, staging, prod — by editing them or by dragging
// them onto a group in the tree. ui.json keeps the plain list of hosts
// (saved_ssh) as older builds read it, and the rest beside it, by host.

// sshHostInfo is what is kept about a saved host besides its target.
type sshHostInfo struct {
	Name  string   `json:"name,omitempty"`  // shown in place of the host
	Group string   `json:"group,omitempty"` // "" is no group
	Args  []string `json:"args,omitempty"`  // extra ssh options, as NormalizeLoginArgs gives them
}

func (i sshHostInfo) empty() bool { return i.Name == "" && i.Group == "" && len(i.Args) == 0 }

const maxSSHGroupName = 40

func sshGroupID(name string) string { return "sshgroup:" + name }

// checkSSHGroupName says what is wrong with a group's name.
func checkSSHGroupName(name string) error {
	switch {
	case name == "":
		return errors.New("a group needs a name")
	case len(name) > maxSSHGroupName:
		return fmt.Errorf("a group's name is at most %d characters", maxSSHGroupName)
	case strings.IndexFunc(name, func(r rune) bool { return r < ' ' || r == 0x7f }) >= 0:
		return errors.New("a group's name can't contain control characters")
	}
	return nil
}

// cleanSSHInfo keeps the details of saved hosts only, dropping what a
// hand-edited or older ui.json could hold that no longer applies: hosts
// forgotten by an older build, options ssh would refuse, bad group names.
func cleanSSHInfo(saved []string, info map[string]sshHostInfo) map[string]sshHostInfo {
	out := map[string]sshHostInfo{}
	for target, i := range info {
		if !slices.Contains(saved, target) {
			continue
		}
		i.Name = strings.TrimSpace(i.Name)
		if strings.IndexFunc(i.Name, func(r rune) bool { return r < ' ' || r == 0x7f }) >= 0 {
			i.Name = ""
		}
		i.Group = strings.TrimSpace(i.Group)
		if checkSSHGroupName(i.Group) != nil {
			i.Group = ""
		}
		if args, err := remote.NormalizeLoginArgs(i.Args); err == nil {
			i.Args = args
		} else {
			i.Args = nil
		}
		if !i.empty() {
			out[target] = i
		}
	}
	return out
}

// cleanSSHGroups is the order groups are listed in: the saved order, then
// any group a host names that the list lacks.
func cleanSSHGroups(groups []string, info map[string]sshHostInfo, saved []string) []string {
	var out []string
	for _, g := range groups {
		g = strings.TrimSpace(g)
		if checkSSHGroupName(g) == nil && !slices.Contains(out, g) {
			out = append(out, g)
		}
	}
	for _, target := range saved { // saved order, so it doesn't change run to run
		if g := info[target].Group; g != "" && !slices.Contains(out, g) {
			out = append(out, g)
		}
	}
	return out
}

// sshDisplay is how a host is named in the tree and in titles.
func sshDisplay(target string, info sshHostInfo) string {
	if info.Name != "" {
		return info.Name
	}
	return "ssh " + sshName(target)
}

// sshArgsText writes options back as they would be typed, quoting words
// with spaces in them.
func sshArgsText(args []string) string {
	words := make([]string, len(args))
	for i, a := range args {
		if a == "" || strings.ContainsAny(a, " \t'\"\\") {
			a = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		}
		words[i] = a
	}
	return strings.Join(words, " ")
}

// parseSSHArgs reads options as typed in a dialog.
func parseSSHArgs(text string) ([]string, error) {
	words, _, ok := shellWords(strings.TrimSpace(text))
	if !ok {
		return nil, errors.New("ssh options: a quote is not closed")
	}
	return remote.NormalizeLoginArgs(words)
}

// sshGroupOfRow is the group a dragged host would go to when dropped on r:
// a group itself, a host in it, or the SSH section for no group.
func (m Model) sshGroupOfRow(r row) (string, bool) {
	if r.machine != localMachine {
		return "", false
	}
	switch r.kind {
	case kindSSHGroup:
		return r.branch, true
	case kindSSH:
		return "", r.projectID == ""
	case kindSavedSSH:
		return m.sshInfo[savedSSHTarget(r.id)].Group, true
	case kindPane:
		if p := m.pane(r.machine, r.paneID); p != nil && p.ProjectID == "" {
			if t := sshTarget(*p); t != "" {
				return m.sshInfo[t].Group, true
			}
		}
	}
	return "", false
}

// sshTargetOfRow is the host a saved host's or an ssh session's row is for.
func (m Model) sshTargetOfRow(r row) string {
	switch {
	case r.machine != localMachine:
		return ""
	case r.kind == kindSavedSSH:
		return savedSSHTarget(r.id)
	case r.kind == kindPane:
		if p := m.pane(r.machine, r.paneID); p != nil {
			return sshTarget(*p)
		}
	}
	return ""
}

// putSSHHost saves a host's details under target, replacing old (which may
// be "" for a host not saved yet, or another target when the host itself
// was edited). It keeps the host's place in the list.
func (m *Model) putSSHHost(old, target string, info sshHostInfo) error {
	if err := remote.CheckLoginTarget(target); err != nil {
		return err
	}
	if info.Group != "" {
		if err := checkSSHGroupName(info.Group); err != nil {
			return err
		}
	}
	args, err := remote.NormalizeLoginArgs(info.Args)
	if err != nil {
		return err
	}
	info.Args = args
	if target != old && slices.Contains(m.savedSSH, target) {
		return fmt.Errorf("%s is already saved", sshName(target))
	}
	saved := slices.Clone(m.savedSSH)
	if i := slices.Index(saved, old); old != "" && i >= 0 {
		saved[i] = target
	} else if !slices.Contains(saved, target) {
		saved = append(saved, target)
	}
	sshInfo := map[string]sshHostInfo{}
	for k, v := range m.sshInfo {
		if k != old {
			sshInfo[k] = v
		}
	}
	if !info.empty() {
		sshInfo[target] = info
	}
	m.savedSSH, m.sshInfo = saved, sshInfo
	if info.Group != "" && !slices.Contains(m.sshGroups, info.Group) {
		m.sshGroups = append(slices.Clone(m.sshGroups), info.Group)
	}
	if old != "" && old != target {
		m.removeRow(savedSSHID(old))
	}
	return nil
}

// moveSSHToGroup puts a host in a group ("" for none), saving it first when
// only a session to it was open.
func (m *Model) moveSSHToGroup(target, group string) tea.Cmd {
	info := m.sshInfo[target]
	if slices.Contains(m.savedSSH, target) && info.Group == group {
		return nil
	}
	info.Group = group
	if err := m.putSSHHost(target, target, info); err != nil {
		return func() tea.Msg { return errMsg{err} }
	}
	where := "out of its group"
	if group != "" {
		where = "to " + group
		m.expanded[sshGroupID(group)] = true
	}
	m.setFlash("moved "+sshName(target)+" "+where, false)
	return tea.Batch(m.rebuild(), m.saveState())
}

// addSSHGroup makes an empty group, to drag hosts into.
func (m *Model) addSSHGroup(name string) tea.Cmd {
	name = strings.TrimSpace(name)
	if err := checkSSHGroupName(name); err != nil {
		return func() tea.Msg { return errMsg{err} }
	}
	if slices.Contains(m.sshGroups, name) {
		return func() tea.Msg { return errMsg{fmt.Errorf("there is already a group %q", name)} }
	}
	m.sshGroups = append(slices.Clone(m.sshGroups), name)
	m.expanded[looseSSHID(localMachine)] = true
	m.expanded[cliID(localMachine)] = true
	m.setFlash("group "+name+" made: drag hosts onto it, or edit a host with e", false)
	cmd := tea.Batch(m.rebuild(), m.saveState())
	if i := indexOfRow(m.rows, sshGroupID(name)); i >= 0 {
		m.cursor = m.rows[i].id
		m.keepCursorVisible()
	}
	return cmd
}

// renameSSHGroup renames a group and moves its hosts with it. Renaming onto
// another group's name merges the two.
func (m *Model) renameSSHGroup(old, name string) tea.Cmd {
	name = strings.TrimSpace(name)
	if err := checkSSHGroupName(name); err != nil {
		return func() tea.Msg { return errMsg{err} }
	}
	if name == old {
		return nil
	}
	groups := slices.Clone(m.sshGroups)
	if slices.Contains(groups, name) {
		groups = slices.DeleteFunc(groups, func(g string) bool { return g == old })
	} else if i := slices.Index(groups, old); i >= 0 {
		groups[i] = name
	}
	m.sshGroups = groups
	m.regroupSSH(old, name)
	if open, ok := m.expanded[sshGroupID(old)]; ok {
		m.expanded[sshGroupID(name)] = open
		delete(m.expanded, sshGroupID(old))
	}
	if m.cursor == sshGroupID(old) {
		m.cursor = sshGroupID(name)
	}
	m.setFlash("renamed group to "+name, false)
	return tea.Batch(m.rebuild(), m.saveState())
}

// removeSSHGroup takes a group away. Its hosts stay saved, in no group.
func (m *Model) removeSSHGroup(name string) tea.Cmd {
	m.sshGroups = slices.DeleteFunc(slices.Clone(m.sshGroups), func(g string) bool { return g == name })
	m.regroupSSH(name, "")
	delete(m.expanded, sshGroupID(name))
	m.removeRow(sshGroupID(name))
	m.setFlash("removed group "+name+"; its hosts are kept", false)
	return tea.Batch(m.rebuild(), m.saveState())
}

// regroupSSH moves every host in group from to group to.
func (m *Model) regroupSSH(from, to string) {
	info := map[string]sshHostInfo{}
	for k, v := range m.sshInfo {
		if v.Group == from {
			v.Group = to
		}
		if !v.empty() {
			info[k] = v
		}
	}
	m.sshInfo = info
}

// sshHostsIn are the saved hosts in a group, in saved order.
func (m Model) sshHostsIn(group string) []string {
	var out []string
	for _, t := range m.savedSSH {
		if m.sshInfo[t].Group == group {
			out = append(out, t)
		}
	}
	return out
}

// openEditSSH opens the editor for a saved host or an ssh session's host.
func (m *Model) openEditSSH(target string) tea.Cmd {
	d := newSSHHostDialog(*m, target, m.sshInfo[target].Group)
	m.overlay = d
	return d.focusCmd()
}

// newSSHHostDialog edits a host — or, with target "", adds one to group
// without connecting. Saving an unsaved session's host saves it.
func newSSHHostDialog(m Model, target, group string) *dialog {
	info := m.sshInfo[target]
	title := " Edit ssh host "
	if target == "" {
		title = " Add ssh host "
	} else if !slices.Contains(m.savedSSH, target) {
		title = " Save ssh host "
	}
	d := newDialog(m, title, []string{
		"Options are passed to ssh before the host, e.g. -o KexAlgorithms=+diffie-hellman-group14-sha1 -p 2222 (or KexAlgorithms=… alone).",
		"A new group is made by typing its name. Sessions open now keep the options they started with.",
	}, []string{"Host", "Name", "Group", "SSH options"}, []string{target, info.Name, group, sshArgsText(info.Args)})
	d.fields[0].in.Placeholder = "user@host, host alias or ssh://user@host:port"
	d.fields[1].in.Placeholder = "optional, e.g. prod db 1"
	d.fields[2].in.Placeholder = "optional, e.g. prod"
	d.fields[3].in.Placeholder = "optional"
	old := ""
	if slices.Contains(m.savedSSH, target) {
		old = target
	}
	d.submit = func(m *Model, v []string) tea.Cmd {
		reopen := func(err error) tea.Cmd {
			m.overlay = d // keep what was typed
			m.setFlash(err.Error(), true)
			return nil
		}
		host := strings.TrimSpace(v[0])
		args, err := parseSSHArgs(v[3])
		if err != nil {
			return reopen(err)
		}
		info := sshHostInfo{Name: strings.TrimSpace(v[1]), Group: strings.TrimSpace(v[2]), Args: args}
		if err := m.putSSHHost(old, host, info); err != nil {
			return reopen(err)
		}
		if info.Group != "" {
			m.expanded[sshGroupID(info.Group)] = true
		}
		m.expanded[looseSSHID(localMachine)] = true
		m.expanded[cliID(localMachine)] = true
		m.setFlash("saved "+sshDisplay(host, info), false)
		return tea.Batch(m.rebuild(), m.saveState())
	}
	return d
}

// newSSHGroupDialog names a new group, or renames old.
func newSSHGroupDialog(m Model, old string) *dialog {
	title, text := " New ssh group ", "Groups sort saved hosts, e.g. eng, staging, prod. Drag hosts onto a group to move them."
	if old != "" {
		title, text = " Rename ssh group ", "Its hosts move with it. Using another group's name merges the two."
	}
	d := newDialog(m, title, []string{text}, []string{"Name"}, []string{old})
	d.submit = func(m *Model, v []string) tea.Cmd {
		if old != "" {
			return m.renameSSHGroup(old, v[0])
		}
		return m.addSSHGroup(v[0])
	}
	return d
}

// confirmRemoveSSHGroup asks before taking a group away.
func (m *Model) confirmRemoveSSHGroup(name string) {
	n := len(m.sshHostsIn(name))
	q := fmt.Sprintf("Remove group %s? It is empty.", name)
	if n > 0 {
		q = fmt.Sprintf("Remove group %s? Its %d saved host(s) are kept, in no group.", name, n)
	}
	m.overlay = newConfirm(q, func(m *Model) tea.Cmd { return m.removeSSHGroup(name) })
}

// copySSHKey opens a terminal that installs this computer's public key on
// target, so later logins need no password. It asks for the password the
// one time, and makes a key first when there is none.
func (m Model) copySSHKey(target string) tea.Cmd {
	command, key, err := remote.CopyKeyCommand(target, m.sshInfo[target].Args)
	if err != nil {
		return func() tea.Msg { return errMsg{err} }
	}
	c := m.clientOf(localMachine)
	if c == nil {
		return func() tea.Msg { return errMsg{errString(m.offlineText(localMachine))} }
	}
	cols, rows := m.paneArea()
	home, _ := os.UserHomeDir()
	params := proto.PaneCreateParams{Name: "copy key to " + sshName(target), Command: command, Cwd: home, Cols: cols, Rows: rows, NoProject: true}
	note := "copying " + m.tildify(localMachine, key) + " to " + sshName(target)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		var info proto.PaneInfo
		if err := c.Call(ctx, proto.MethodPaneCreate, params, &info); err != nil {
			return errMsg{err}
		}
		return createdMsg{machine: localMachine, info: info, note: note}
	}
}

// sshDrag is a saved host or ssh session being dragged in the tree, to drop
// on a group.
type sshDrag struct {
	row    string // the row it was picked up from
	target string
	over   string // the row the pointer is over
	moved  bool   // the pointer left the row it started on
}

// sshDropRow is the row the drag would drop on, or "" when letting go
// there would do nothing.
func (m Model) sshDropRow() string {
	d := m.sshDrag
	if d == nil || !d.moved || d.over == d.row {
		return ""
	}
	i := indexOfRow(m.rows, d.over)
	if i < 0 {
		return ""
	}
	if _, ok := m.sshGroupOfRow(m.rows[i]); !ok {
		return ""
	}
	return d.over
}

// sshDragMouse carries a drag until the button is let go, wherever that is.
func (m Model) sshDragMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	d := m.sshDrag
	rowAt := func() string {
		i := m.scroll + msg.Y - 2
		if m.zoom || msg.X >= m.sidebarW || msg.Y < 2 || i < 0 || i >= len(m.rows) {
			return ""
		}
		return m.rows[i].id
	}
	switch msg.Action {
	case tea.MouseActionMotion:
		d.over = rowAt()
		if d.over != d.row {
			d.moved = true
		}
		return m, nil
	case tea.MouseActionRelease:
		d.over = rowAt()
		drop := m.sshDropRow()
		m.sshDrag = nil
		if !d.moved {
			// Never left the row: a click. A saved host connects; a
			// session was already shown on the press.
			if d.over == d.row && strings.HasPrefix(d.row, "sshsaved:") {
				return m, m.connectSSH(d.target)
			}
			return m, nil
		}
		if drop == "" {
			return m, nil // let go where it started, or where nothing takes it
		}
		group, _ := m.sshGroupOfRow(m.rows[indexOfRow(m.rows, drop)])
		return m, m.moveSSHToGroup(d.target, group)
	}
	return m, nil
}

// sshGroupLines is the page of a group: its hosts, and what to do with it.
func (m Model) sshGroupLines(group string, w int) []string {
	lines := []string{fit(styleBold.Render("ssh group "+group), w), ""}
	hosts := m.sshHostsIn(group)
	if len(hosts) == 0 {
		lines = append(lines, styleMuted.Render(ansi.Truncate("No hosts yet. Drag a saved host or an ssh session here, or press a to add one.", w, "…")))
	}
	var sessions []proto.PaneInfo
	if mach := m.machine(localMachine); mach != nil {
		sessions = mach.panes
	}
	for _, t := range hosts {
		info := m.sshInfo[t]
		state := styleMuted.Render("not connected")
		if slices.ContainsFunc(sessions, func(p proto.PaneInfo) bool { return p.State == proto.PaneRunning && sshTarget(p) == t }) {
			state = styleOK.Render("connected")
		}
		line := sshDisplay(t, info)
		if info.Name != "" {
			line += styleMuted.Render("  " + sshName(t))
		}
		if len(info.Args) > 0 {
			line += styleMuted.Render("  " + sshArgsText(info.Args))
		}
		lines = append(lines, spread(ansi.Truncate(line, max(w-15, 1), "…"), state, w))
	}
	return append(lines, "", styleMuted.Render(ansi.Truncate("a add host · r rename · x remove group · drag hosts in or out", w, "…")))
}

// newMoveSSHMenu picks a group for a host: each group, no group, or a new
// one — what dragging does, from the keyboard.
func newMoveSSHMenu(m Model, target string, x, y int) *menu {
	current, saved := m.sshInfo[target].Group, slices.Contains(m.savedSSH, target)
	var items []menuItem
	for i, g := range m.sshGroups {
		g := g
		key, label := "", g
		if i < 9 {
			key = fmt.Sprint(i + 1)
		}
		if saved && g == current {
			label += "  (now)"
		}
		items = append(items, menuItem{key, label, func(m *Model) tea.Cmd { return m.moveSSHToGroup(target, g) }})
	}
	if current != "" || !saved {
		items = append(items, menuItem{"0", "No group", func(m *Model) tea.Cmd { return m.moveSSHToGroup(target, "") }})
	}
	items = append(items, menuItem{"N", "New group…", func(m *Model) tea.Cmd {
		d := newDialog(*m, " New ssh group ", []string{"Moves " + sshName(target) + " into it."}, []string{"Name"}, nil)
		d.submit = func(m *Model, v []string) tea.Cmd {
			name := strings.TrimSpace(v[0])
			if err := checkSSHGroupName(name); err != nil {
				return func() tea.Msg { return errMsg{err} }
			}
			return m.moveSSHToGroup(target, name)
		}
		m.overlay = d
		return d.focusCmd()
	}})
	return &menu{title: "Move " + sshName(target) + " to", items: items, x: x, y: y}
}
