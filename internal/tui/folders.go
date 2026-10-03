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

// stands reports whether a folder's member is this pane. A member that was
// written down with a name is found by that name alone: ids are a running
// server's and are handed out again by the next one, so matching an id as
// well once put a folder's pane and whatever later took its id both in it.
// Only a pane that never had a name of its own is held by id, and it leaves
// its folder when the server goes, there being nothing else to know it by.
func (mem savedMember) stands(p proto.PaneInfo) bool {
	if mem.Name != "" {
		return p.Name != "" && mem.Name == p.Name
	}
	return mem.ID != "" && mem.ID == p.ID
}

func (f savedFolder) holds(p proto.PaneInfo) bool {
	for _, mem := range f.Members {
		if mem.stands(p) {
			return true
		}
	}
	return false
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

// takeOutOfFolders removes a pane from every folder of a section, leaving
// the folders themselves alone: an empty one stays until it is removed.
func (m *Model) takeOutOfFolders(machine, projectID string, section nodeKind, p proto.PaneInfo) bool {
	key := folderKey(machine, projectID, section)
	fs, out := m.folders[key], false
	for i := range fs {
		kept := fs[i].Members[:0]
		for _, mem := range fs[i].Members {
			if mem.stands(p) {
				out = true
				continue
			}
			kept = append(kept, mem)
		}
		fs[i].Members = kept
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
