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

	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/remote"
)

// Saved ssh hosts can carry a name to show and extra ssh options, and sit
// in the SSH section's folders (folders.go) like any pane. ui.json keeps the
// plain list of hosts (saved_ssh) as older builds read it, the rest beside
// it by host, and a host's folder as a member of that folder.
//
// A folder of the SSH section holds a saved host by its target, not by a
// pane: the host is listed in it while no session is open to it, and every
// session to it is listed there instead — so a host and its sessions are in
// one place, and connecting again doesn't move anything.

// sshHostInfo is what is kept about a saved host besides its target.
type sshHostInfo struct {
	Name string   `json:"name,omitempty"` // shown in place of the host
	Args []string `json:"args,omitempty"` // extra ssh options, as NormalizeLoginArgs gives them
	// Group is a host's group from a build before groups became folders;
	// it is read once, moved into a folder, and never written again.
	Group string `json:"group,omitempty"`
}

func (i sshHostInfo) empty() bool { return i.Name == "" && len(i.Args) == 0 && i.Group == "" }

const maxFolderName = 40

// checkFolderName says what is wrong with a folder's name typed for a host.
func checkFolderName(name string) error {
	switch {
	case name == "":
		return errors.New("a folder needs a name")
	case len(name) > maxFolderName:
		return fmt.Errorf("a folder's name is at most %d characters", maxFolderName)
	case strings.IndexFunc(name, func(r rune) bool { return r < ' ' || r == 0x7f }) >= 0:
		return errors.New("a folder's name can't contain control characters")
	}
	return nil
}

// sshFolderKey is where the folders of this computer's SSH section are kept.
func sshFolderKey() string { return folderKey(localMachine, "", kindSSH) }

// cleanSSHInfo keeps the details of saved hosts only, dropping what a
// hand-edited or older ui.json could hold that no longer applies: hosts
// forgotten by an older build, and options ssh would refuse.
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
		if checkFolderName(i.Group) != nil {
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

// migrateSSHGroups turns the ssh groups of a build before folders into
// folders of the SSH section: each group a folder, in the order it was
// listed, each host in it a member. A host already in a folder stays where
// it is. The groups are dropped from the hosts' details afterwards.
func migrateSSHGroups(folders map[string][]savedFolder, groups []string, saved []string, info map[string]sshHostInfo) map[string][]savedFolder {
	order := slices.Clone(groups)
	for _, t := range saved {
		if g := info[t].Group; g != "" && !slices.Contains(order, g) {
			order = append(order, g)
		}
	}
	if len(order) == 0 {
		return folders
	}
	if folders == nil {
		folders = map[string][]savedFolder{}
	}
	key := sshFolderKey()
	fs := slices.Clone(folders[key])
	inFolder := map[string]bool{}
	for _, f := range fs {
		for _, mem := range f.Members {
			if mem.Host != "" {
				inFolder[mem.Host] = true
			}
		}
	}
	for _, g := range order {
		g = strings.TrimSpace(g)
		if checkFolderName(g) != nil {
			continue
		}
		at := slices.IndexFunc(fs, func(f savedFolder) bool { return f.Name == g })
		if at < 0 {
			fs = append(fs, savedFolder{Name: g})
			at = len(fs) - 1
		}
		for _, t := range saved {
			if info[t].Group == g && !inFolder[t] {
				fs[at].Members = append(fs[at].Members, savedMember{Host: t})
				inFolder[t] = true
			}
		}
	}
	folders[key] = fs
	for t, i := range info {
		if i.Group != "" {
			i.Group = ""
			if i.empty() {
				delete(info, t)
			} else {
				info[t] = i
			}
		}
	}
	return folders
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

// sshTargetOfRow is the host a saved host's or an ssh session's row is for,
// on this computer.
func (m Model) sshTargetOfRow(r row) string {
	switch {
	case r.machine != localMachine:
		return ""
	case r.kind == kindSavedSSH:
		return savedSSHTarget(r.id)
	case r.kind == kindPane:
		if p := m.pane(r.machine, r.paneID); p != nil && p.ProjectID == "" {
			return sshTarget(*p)
		}
	}
	return ""
}

// hostFolder is the SSH folder a saved host is in, or "".
func (m Model) hostFolder(target string) string {
	for _, f := range m.folders[sshFolderKey()] {
		if slices.ContainsFunc(f.Members, func(mem savedMember) bool { return mem.Host == target }) {
			return f.Name
		}
	}
	return ""
}

// takeHostOutOfFolders removes a host from the SSH folders; the folders
// themselves stay.
func (m *Model) takeHostOutOfFolders(target string) bool {
	key := sshFolderKey()
	fs, out := slices.Clone(m.folders[key]), false
	for i := range fs {
		n := len(fs[i].Members)
		fs[i].Members = slices.DeleteFunc(slices.Clone(fs[i].Members), func(mem savedMember) bool { return mem.Host == target })
		out = out || len(fs[i].Members) != n
	}
	if out {
		m.folders[key] = fs
	}
	return out
}

// putHostInFolder moves a saved host into the SSH folder of that name,
// making the folder when there is none. Its sessions go with it, so any of
// them held in a folder as a pane is taken out of there.
func (m *Model) putHostInFolder(target, name string) {
	m.takeHostOutOfFolders(target)
	if mach := m.machine(localMachine); mach != nil {
		for _, p := range mach.panes {
			if p.ProjectID == "" && sshTarget(p) == target {
				m.takeOutOfFolders(localMachine, "", kindSSH, p)
			}
		}
	}
	m.newFolder(localMachine, "", kindSSH, name) // nothing when it is there already
	key := sshFolderKey()
	fs := m.folders[key]
	at := slices.IndexFunc(fs, func(f savedFolder) bool { return f.Name == name })
	fs[at].Members = append(slices.Clone(fs[at].Members), savedMember{Host: target})
	m.folders[key] = fs
	m.expanded[folderRowID(localMachine, "", kindSSH, name)] = true
}

// renameHostInFolders follows a host whose target was edited.
func (m *Model) renameHostInFolders(old, target string) {
	key := sshFolderKey()
	fs := slices.Clone(m.folders[key])
	for i := range fs {
		mems := slices.Clone(fs[i].Members)
		for j := range mems {
			if mems[j].Host == old {
				mems[j].Host = target
			}
		}
		fs[i].Members = mems
	}
	if len(fs) > 0 {
		m.folders[key] = fs
	}
}

// putSSHHost saves a host's details and folder under target, replacing old
// (which may be "" for a host not saved yet, or another target when the
// host itself was edited). It keeps the host's place in the list. folder ""
// takes it out of any folder.
func (m *Model) putSSHHost(old, target string, info sshHostInfo, folder string) error {
	if err := remote.CheckLoginTarget(target); err != nil {
		return err
	}
	if folder != "" {
		if err := checkFolderName(folder); err != nil {
			return err
		}
	}
	args, err := remote.NormalizeLoginArgs(info.Args)
	if err != nil {
		return err
	}
	info.Args, info.Group = args, ""
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
	if old != "" && old != target {
		m.renameHostInFolders(old, target)
		m.removeRow(savedSSHID(old))
	}
	if folder == "" {
		m.takeHostOutOfFolders(target)
	} else {
		m.putHostInFolder(target, folder)
	}
	return nil
}

// moveSSHToFolder puts what a row stands for in an SSH folder ("" for
// none): a saved host, with its sessions; or a session to a host that isn't
// saved, as a pane, the way any pane goes in a folder.
func (m *Model) moveSSHToFolder(r row, folder string) tea.Cmd {
	target := m.sshTargetOfRow(r)
	if target == "" {
		return nil
	}
	if !slices.Contains(m.savedSSH, target) {
		p := m.pane(r.machine, r.paneID)
		if p == nil {
			return nil
		}
		if folder == "" {
			m.takeOutOfFolders(localMachine, "", kindSSH, *p)
		} else {
			m.newFolder(localMachine, "", kindSSH, folder)
			m.putInFolder(localMachine, "", kindSSH, folder, *p)
			m.expanded[folderRowID(localMachine, "", kindSSH, folder)] = true
		}
	} else {
		if m.hostFolder(target) == folder {
			return nil
		}
		if err := m.putSSHHost(target, target, m.sshInfo[target], folder); err != nil {
			return func() tea.Msg { return errMsg{err} }
		}
	}
	where := "out of its folder"
	if folder != "" {
		where = "to " + folder
	}
	m.setFlash("moved "+sshName(target)+" "+where, false)
	return tea.Batch(m.rebuild(), m.saveState())
}

// dropOnSSHFolder is a saved host, or a session to one, let go on an SSH
// folder or on the SSH section itself. ok is false for any other drop,
// which the panes' own rules handle.
func (m *Model) dropOnSSHFolder(from, to row) (tea.Cmd, bool) {
	target := m.sshTargetOfRow(from)
	if target == "" || !slices.Contains(m.savedSSH, target) || to.machine != localMachine || to.projectID != "" {
		return nil, false
	}
	switch {
	case to.kind == kindFolder && to.section == kindSSH:
		return m.moveSSHToFolder(from, to.label), true
	case to.kind == kindSSH:
		return m.moveSSHToFolder(from, ""), true
	}
	return nil, false
}

// openEditSSH opens the editor for a saved host or an ssh session's host.
func (m *Model) openEditSSH(target string) tea.Cmd {
	d := newSSHHostDialog(*m, target, m.hostFolder(target))
	m.overlay = d
	return d.focusCmd()
}

// newSSHHostDialog edits a host — or, with target "", adds one to folder
// without connecting. Saving an unsaved session's host saves it.
func newSSHHostDialog(m Model, target, folder string) *dialog {
	info := m.sshInfo[target]
	title := " Edit ssh host "
	if target == "" {
		title = " Add ssh host "
	} else if !slices.Contains(m.savedSSH, target) {
		title = " Save ssh host "
	}
	d := newDialog(m, title, []string{
		"Options are passed to ssh before the host, e.g. -o KexAlgorithms=+diffie-hellman-group14-sha1 -p 2222 (or KexAlgorithms=… alone).",
		"Typing a folder that isn't there makes it. Sessions open now keep the options they started with.",
	}, []string{"Host", "Name", "Folder", "SSH options"}, []string{target, info.Name, folder, sshArgsText(info.Args)})
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
		info := sshHostInfo{Name: strings.TrimSpace(v[1]), Args: args}
		if err := m.putSSHHost(old, host, info, strings.TrimSpace(v[2])); err != nil {
			return reopen(err)
		}
		m.expanded[looseSSHID(localMachine)] = true
		m.expanded[cliID(localMachine)] = true
		m.setFlash("saved "+sshDisplay(host, info), false)
		return tea.Batch(m.rebuild(), m.saveState())
	}
	return d
}

// newMoveSSHMenu picks an SSH folder for a host or session: each folder,
// none, or a new one — what dragging does, from the keyboard.
func newMoveSSHMenu(m Model, r row, x, y int) *menu {
	target := m.sshTargetOfRow(r)
	current := m.hostFolder(target)
	if !slices.Contains(m.savedSSH, target) {
		current = ""
		if p := m.pane(r.machine, r.paneID); p != nil {
			for _, f := range m.folders[sshFolderKey()] {
				if len(f.claim([]proto.PaneInfo{*p}, map[string]bool{})) > 0 {
					current = f.Name
				}
			}
		}
	}
	var items []menuItem
	for i, f := range m.folders[sshFolderKey()] {
		name := f.Name
		key, label := "", name
		if i < 9 {
			key = fmt.Sprint(i + 1)
		}
		if name == current {
			label += "  (now)"
		}
		items = append(items, menuItem{key, label, func(m *Model) tea.Cmd { return m.moveSSHToFolder(r, name) }})
	}
	if current != "" {
		items = append(items, menuItem{"0", "No folder", func(m *Model) tea.Cmd { return m.moveSSHToFolder(r, "") }})
	}
	items = append(items, menuItem{"N", "New folder…", func(m *Model) tea.Cmd {
		d := newDialog(*m, " New folder ", []string{"Moves " + sshName(target) + " into it."}, []string{"Name"}, nil)
		d.submit = func(m *Model, v []string) tea.Cmd {
			name := strings.TrimSpace(v[0])
			if err := checkFolderName(name); err != nil {
				return func() tea.Msg { return errMsg{err} }
			}
			return m.moveSSHToFolder(r, name)
		}
		m.overlay = d
		return d.focusCmd()
	}})
	return &menu{title: "Move " + sshName(target) + " to", items: items, x: x, y: y}
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
