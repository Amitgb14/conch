package server

import (
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/Amitgb14/conch/internal/proto"
)

// Project groups: projects the person keeps together because they change
// together — an API, its web app, their specs. A group is only a name, an
// order and notes; each project stays a project of its own, with its own
// git, branches and pull requests. Groups are the server's (groups.json)
// so the TUI, the app, the phone and the CLI see the same ones.
//
// A group remembers its projects by folder, not by ID: an ID is a hash of
// the folder anyway, and a folder reads in the file. A project that is
// not registered (still loading at start, or its folder gone) is left out
// of what is listed but kept in the file; removing a project from conch
// takes it out of every group.

const maxGroupName = 64

type groupBook struct {
	s    *Server
	file string

	mu     sync.Mutex
	groups []savedGroup
}

type savedGroup struct {
	Name     string   `json:"name"`
	Projects []string `json:"projects"` // folders, in the person's order
	Notes    string   `json:"notes,omitempty"`
}

type groupsFile struct {
	Groups []savedGroup `json:"groups"`
}

func loadGroups(s *Server, configDir string) *groupBook {
	g := &groupBook{s: s, file: filepath.Join(configDir, "groups.json")}
	b, err := os.ReadFile(g.file)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Printf("groups: %v", err)
		}
		return g
	}
	var f groupsFile
	if err := json.Unmarshal(b, &f); err != nil {
		log.Printf("groups: %s: %v", g.file, err)
		return g
	}
	for _, sg := range f.Groups {
		if checkGroupName(sg.Name) == nil && len(sg.Projects) > 0 {
			g.groups = append(g.groups, sg)
		}
	}
	return g
}

func checkGroupName(name string) error {
	switch {
	case strings.TrimSpace(name) == "":
		return errors.New("a group needs a name")
	case name != strings.TrimSpace(name):
		return errors.New("a group's name can't start or end with a space")
	case utf8.RuneCountInString(name) > maxGroupName:
		return errors.New("a group's name is at most 64 characters")
	case strings.IndexFunc(name, func(r rune) bool { return r < ' ' || r == 0x7f }) >= 0:
		return errors.New("a group's name can't contain control characters")
	}
	return nil
}

// list is every group with its projects as IDs, those registered only.
// A group none of whose projects is registered is left out.
func (g *groupBook) list() proto.GroupList {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := proto.GroupList{Groups: []proto.GroupInfo{}}
	for _, sg := range g.groups {
		info := proto.GroupInfo{Name: sg.Name, Notes: sg.Notes, Projects: []string{}}
		for _, root := range sg.Projects {
			if id := projectID(root); g.registered(id) {
				info.Projects = append(info.Projects, id)
			}
		}
		if len(info.Projects) > 0 {
			out.Groups = append(out.Groups, info)
		}
	}
	return out
}

func (g *groupBook) registered(id string) bool {
	_, err := g.s.projects.get(id)
	return err == nil
}

// save makes or replaces a group. Its projects are IDs of projects this
// server has, each once, at least one.
func (g *groupBook) save(p proto.GroupSaveParams) (proto.GroupInfo, *proto.Error) {
	if err := checkGroupName(p.Name); err != nil {
		return proto.GroupInfo{}, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	var roots []string
	seen := map[string]bool{}
	for _, id := range p.Projects {
		pr, perr := g.s.projects.get(id)
		if perr != nil {
			return proto.GroupInfo{}, perr
		}
		if !seen[id] {
			seen[id] = true
			roots = append(roots, pr.root)
		}
	}
	if len(roots) == 0 {
		return proto.GroupInfo{}, proto.Errorf(proto.ErrBadRequest, "a group needs at least one project")
	}
	old := p.Old
	if old == "" {
		old = p.Name
	}
	g.mu.Lock()
	at := -1
	for i, sg := range g.groups {
		if sg.Name == old {
			at = i
		} else if sg.Name == p.Name {
			g.mu.Unlock()
			return proto.GroupInfo{}, proto.Errorf(proto.ErrBadRequest, "there is a group called %s already", p.Name)
		}
	}
	if p.Old != "" && at < 0 {
		g.mu.Unlock()
		return proto.GroupInfo{}, proto.Errorf(proto.ErrNotFound, "no group called %s", p.Old)
	}
	sg := savedGroup{Name: p.Name, Projects: roots, Notes: strings.TrimSpace(p.Notes)}
	if at < 0 {
		g.groups = append(g.groups, sg)
	} else {
		g.groups[at] = sg
	}
	g.mu.Unlock()
	g.changed()
	ids := make([]string, len(roots))
	for i, r := range roots {
		ids[i] = projectID(r)
	}
	return proto.GroupInfo{Name: sg.Name, Projects: ids, Notes: sg.Notes}, nil
}

// remove takes a group away; its projects stay, in conch and on disk.
func (g *groupBook) remove(name string) *proto.Error {
	g.mu.Lock()
	at := -1
	for i, sg := range g.groups {
		if sg.Name == name {
			at = i
		}
	}
	if at < 0 {
		g.mu.Unlock()
		return proto.Errorf(proto.ErrNotFound, "no group called %s", name)
	}
	g.groups = append(g.groups[:at], g.groups[at+1:]...)
	g.mu.Unlock()
	g.changed()
	return nil
}

// dropProject takes a project removed from conch out of every group, and
// a group left with none goes too.
func (g *groupBook) dropProject(root string) {
	g.mu.Lock()
	changed := false
	kept := g.groups[:0]
	for _, sg := range g.groups {
		var ps []string
		for _, r := range sg.Projects {
			if r == root {
				changed = true
				continue
			}
			ps = append(ps, r)
		}
		sg.Projects = ps
		if len(ps) > 0 {
			kept = append(kept, sg)
		}
	}
	g.groups = kept
	g.mu.Unlock()
	if changed {
		g.changed()
	}
}

// changed writes the file and tells every client.
func (g *groupBook) changed() {
	g.mu.Lock()
	b, _ := json.MarshalIndent(groupsFile{Groups: append([]savedGroup{}, g.groups...)}, "", "  ")
	g.mu.Unlock()
	tmp := g.file + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		log.Printf("groups: save: %v", err)
	} else if err := os.Rename(tmp, g.file); err != nil {
		log.Printf("groups: save: %v", err)
	}
	g.s.broadcast(proto.EventGroups, g.list())
}
