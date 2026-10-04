package tui

import (
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amitgb14/conch/internal/proto"
)

// Folders are groups of your own in the tree: a name, and the panes you put
// in it, inside one section of one project. conch groups panes by what they
// are and by the tab they are open in; neither says what you think belongs
// together, and names repeat across environments — two machines called vm1
// in eng and in prod are different machines.
//
// A folder holds each pane by name and by id. Ids die with the server, and
// the name is the one you typed, so a folder finds its panes again after a
// restart by name and keeps up with them by id in between. A pane with no
// name of its own is held by id alone and leaves its folder when the server
// goes.

// folderKey names the section a folder sits in: a machine's own section, or
// one of a project's.
func folderKey(machine, projectID string, section nodeKind) string {
	return scoped(machine, projectID) + "/" + itoa(int(section))
}

// folderMember is how a pane is written down in a folder.
func folderMember(p proto.PaneInfo) savedMember {
	m := savedMember{ID: p.ID}
	if p.Name != "" {
		m.Name = p.Name
	}
	return m
}

// claim works out which of a section's panes a folder holds, taking each
// one it claims out of taken so no pane is listed in two folders.
//
// The id decides while it is still that pane — the name written down beside
// it agreeing — because two panes may share a name and the id is the only
// thing that tells them apart: a folder holding one vm1 must not swallow the
// other. The name finds a pane again when the id has gone, which is what
// happens on every server restart, but only when one pane answers to it:
// with two called vm1 and no id to choose by, the folder holds neither
// rather than both. An id whose pane now has another name was handed out
// again by a new server and is not this pane at all.
func (f savedFolder) claim(panes []proto.PaneInfo, taken map[string]bool) []proto.PaneInfo {
	var out []proto.PaneInfo
	for _, c := range f.claims(panes, taken) {
		out = append(out, c.pane)
	}
	return out
}

// folderClaim is one of a folder's members and the pane it stands for.
type folderClaim struct {
	member int
	pane   proto.PaneInfo
}

// claims is which member holds which pane, so that taking a pane out knows
// *which* line to delete. Deciding that by name alone made a folder of two
// panes called zsh impossible: putting the second one in took the first one
// out, their names being the same.
func (f savedFolder) claims(panes []proto.PaneInfo, taken map[string]bool) []folderClaim {
	byID := make(map[string]proto.PaneInfo, len(panes))
	for _, p := range panes {
		byID[p.ID] = p
	}
	var out []folderClaim
	for i, mem := range f.Members {
		if p, ok := byID[mem.ID]; mem.ID != "" && ok && !taken[p.ID] && (mem.Name == "" || p.Name == mem.Name) {
			taken[p.ID] = true
			out = append(out, folderClaim{i, p})
			continue
		}
		if mem.Name == "" {
			continue // nothing but an id, and that id is not here any more
		}
		var only proto.PaneInfo
		n := 0
		for _, p := range panes {
			if !taken[p.ID] && p.Name == mem.Name {
				only, n = p, n+1
			}
		}
		if n == 1 {
			taken[only.ID] = true
			out = append(out, folderClaim{i, only})
		}
	}
	return out
}

// foldersIn is the folders of one section, in the order they were made.
func (m *Model) foldersIn(machine, projectID string, section nodeKind) []savedFolder {
	return m.folders[folderKey(machine, projectID, section)]
}

// newFolder adds an empty folder, or does nothing when one of that name is
// already there: two folders called eng in one section would be two places
// to look for the same thing.
func (m *Model) newFolder(machine, projectID string, section nodeKind, name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	key := folderKey(machine, projectID, section)
	for _, f := range m.folders[key] {
		if f.Name == name {
			return false
		}
	}
	if m.folders == nil {
		m.folders = map[string][]savedFolder{}
	}
	m.folders[key] = append(m.folders[key], savedFolder{Name: name})
	return true
}

// putInFolder moves a pane into the folder of that name, taking it out of
// any other folder of the same section first: a pane is in one place.
func (m *Model) putInFolder(machine, projectID string, section nodeKind, name string, p proto.PaneInfo) bool {
	if p.ID == "" {
		return false // nothing to put in, and a member of nothing holds nothing
	}
	key := folderKey(machine, projectID, section)
	fs := m.folders[key]
	at := slices.IndexFunc(fs, func(f savedFolder) bool { return f.Name == name })
	if at < 0 {
		return false
	}
	m.takeOutOfFolders(machine, projectID, section, p)
	fs = m.folders[key]
	fs[at].Members = append(fs[at].Members, folderMember(p))
	m.folders[key] = fs
	return true
}

// takeOutOfFolders removes a pane from the folders of a section, leaving
// the folders themselves alone: an empty one stays until it is removed. It
// deletes the member that stands for this pane and no other, worked out the
// same way the tree works out what a folder holds — by id while the pane
// lives, by name only where the id has gone — so putting one of two panes
// called zsh in a folder leaves the other where it is.
func (m *Model) takeOutOfFolders(machine, projectID string, section nodeKind, p proto.PaneInfo) bool {
	key := folderKey(machine, projectID, section)
	fs, out := m.folders[key], false
	panes := m.sectionPanes(machine, projectID, section)
	for i := range fs {
		taken := map[string]bool{}
		for _, c := range fs[i].claims(panes, taken) {
			if c.pane.ID != p.ID {
				continue
			}
			fs[i].Members = slices.Delete(fs[i].Members, c.member, c.member+1)
			out = true
			break
		}
	}
	if out {
		m.folders[key] = fs
	}
	return out
}

// removeFolder takes a folder away; the panes in it go back to the section's
// own list. It never closes a pane.
func (m *Model) removeFolder(machine, projectID string, section nodeKind, name string) bool {
	key := folderKey(machine, projectID, section)
	fs := m.folders[key]
	at := slices.IndexFunc(fs, func(f savedFolder) bool { return f.Name == name })
	if at < 0 {
		return false
	}
	m.folders[key] = slices.Delete(fs, at, at+1)
	if len(m.folders[key]) == 0 {
		delete(m.folders, key)
	}
	return true
}

// folderSection is the section a row belongs to for folders: the section
// itself, a folder's own, or the one a pane is listed under. It gives the
// machine and project with it, since a folder lives in one of those.
func (m *Model) folderSection(r row) (machine, projectID string, kind nodeKind, ok bool) {
	switch r.kind {
	case kindAgents, kindTerminals, kindSSH:
		return r.machine, r.projectID, r.kind, true
	case kindFolder:
		return r.machine, r.projectID, r.section, true
	case kindSavedSSH: // a saved host is listed in its machine's SSH
		return r.machine, "", kindSSH, true
	case kindPane:
		p := m.pane(r.machine, r.paneID)
		if p == nil {
			return "", "", 0, false
		}
		return r.machine, r.projectID, m.paneSection(r.machine, r.paneID), true
	}
	return "", "", 0, false
}

// openNewFolder asks for a folder's name, for the section the cursor is in.
func (m *Model) openNewFolder() tea.Cmd {
	r, ok := m.selectedRow()
	if !ok {
		return nil
	}
	mid, pid, kind, ok := m.folderSection(r)
	if !ok {
		m.setFlash("folders go in Agents, Terminals and SSH", true)
		return nil
	}
	d := newDialog(*m, " New folder ", []string{"A group of your own in " + sectionWord(kind) + ", to drag panes into."},
		[]string{"Name"}, []string{""})
	d.submit = func(m *Model, v []string) tea.Cmd {
		name := strings.TrimSpace(v[0])
		if name == "" {
			m.setFlash("a folder needs a name", true)
			return nil
		}
		if !m.newFolder(mid, pid, kind, name) {
			m.setFlash("there is already a folder called "+name+" there", true)
			return nil
		}
		m.expanded[folderRowID(mid, pid, kind, name)] = true
		return tea.Batch(m.rebuild(), m.saveState())
	}
	m.overlay = d
	return d.focusCmd()
}

// sectionWord names a section as the tree does, for what conch says about it.
func sectionWord(kind nodeKind) string {
	switch kind {
	case kindAgents:
		return "Agents"
	case kindSSH:
		return "SSH"
	default:
		return "Terminals"
	}
}
