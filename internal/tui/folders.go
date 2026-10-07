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
// The id decides, and only the id — the name written down beside it has to
// agree, because an id whose pane now has another name was handed out again
// by a new server and is not this pane at all. Finding a pane again *by*
// name happens once, when a machine's panes first arrive (settleFolders),
// and never after: a folder that went looking on every rebuild would take a
// pane it was never given. Two terminals called zsh, one of them closed,
// and the survivor walked into the folder the other had left — because the
// name stopped being ambiguous, not because anybody put it there.
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
// *which* line to delete. By id: deciding it by name made a folder of two
// panes called zsh impossible — putting the second one in took the first one
// out, their names being the same — and made a folder able to take a pane
// nobody gave it.
func (f savedFolder) claims(panes []proto.PaneInfo, taken map[string]bool) []folderClaim {
	byID := make(map[string]proto.PaneInfo, len(panes))
	for _, p := range panes {
		byID[p.ID] = p
	}
	var out []folderClaim
	for i, mem := range f.Members {
		p, ok := byID[mem.ID]
		if mem.ID == "" || !ok || taken[p.ID] || (mem.Name != "" && p.Name != mem.Name) {
			continue
		}
		taken[p.ID] = true
		out = append(out, folderClaim{i, p})
	}
	return out
}

// settleFolders is the one moment a folder looks for its panes by name: a
// machine's panes have arrived on a new connection, and the ids written
// down are from whatever server was there before. A member whose id is
// among them is already right. One whose id is gone takes the pane of its
// name when exactly one answers; with two of that name, or none, the member
// is dropped — not kept to be claimed later, which is how a folder came to
// swallow a pane that merely outlived its namesake.
//
// It runs once per connection, so a reload (which keeps the panes and their
// ids) settles nothing, and a restart (which does not) finds them again.
func (m *Model) settleFolders(machine string, panes []proto.PaneInfo) bool {
	live := make(map[string]proto.PaneInfo, len(panes))
	for _, p := range panes {
		live[p.ID] = p
	}
	// Panes a member already holds by id are nobody else's to take.
	taken := map[string]bool{}
	for key, folders := range m.folders {
		if folderKeyMachine(key) != machine {
			continue
		}
		for _, f := range folders {
			for _, mem := range f.Members {
				if p, ok := live[mem.ID]; ok && (mem.Name == "" || p.Name == mem.Name) {
					taken[p.ID] = true
				}
			}
		}
	}
	changed := false
	for key, folders := range m.folders {
		if folderKeyMachine(key) != machine {
			continue
		}
		for fi := range folders {
			kept := folders[fi].Members[:0]
			for _, mem := range folders[fi].Members {
				if p, ok := live[mem.ID]; ok && (mem.Name == "" || p.Name == mem.Name) {
					kept = append(kept, mem)
					continue
				}
				changed = true
				if mem.Name == "" {
					continue // an id and nothing else, and that id has gone
				}
				var only proto.PaneInfo
				n := 0
				for _, p := range panes {
					if !taken[p.ID] && p.Name == mem.Name {
						only, n = p, n+1
					}
				}
				if n != 1 {
					continue // none of that name, or more than one: let it go
				}
				taken[only.ID] = true
				kept = append(kept, savedMember{ID: only.ID, Name: only.Name})
			}
			folders[fi].Members = kept
		}
		m.folders[key] = folders
	}
	return changed
}

// folderKeyMachine reads the machine out of a folder's key, which is
// scoped(machine, project) + "/" + section — and scoped leaves this
// computer's name out, so a key with no "~" before the section is local.
func folderKeyMachine(key string) string {
	if i := strings.IndexByte(key, '/'); i >= 0 {
		key = key[:i]
	}
	if i := strings.IndexByte(key, '~'); i >= 0 {
		return key[:i]
	}
	return localMachine
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
	s := m.cur()
	for _, f := range m.folders[key] {
		if f.Name == name {
			// One made in another workspace and not shown in this one is
			// shown here from now on, rather than refused as a name that
			// is nowhere to be seen.
			if s != nil && !m.showsFolder(key, f) {
				m.keepInFirst(m.activeSpace, foldersOf, spaceFolder(key, name), m.hasFolder(0, key, f))
				s.folders[spaceFolder(key, name)] = true
				return true
			}
			return false
		}
	}
	if m.folders == nil {
		m.folders = map[string][]savedFolder{}
	}
	m.folders[key] = append(m.folders[key], savedFolder{Name: name})
	if s != nil {
		s.folders[spaceFolder(key, name)] = true
	}
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
	for _, s := range m.spaces {
		delete(s.folders, spaceFolder(key, name))
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
