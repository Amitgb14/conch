package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Amitgb14/conch/internal/adapter"
	"github.com/Amitgb14/conch/internal/gitx"
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
	// saveMu makes saves one at a time, each of the state as it is when
	// its turn comes: two at once shared a temporary file, and the older
	// state could be renamed in last, or a file half of each.
	saveMu sync.Mutex
	// merging is the tasks whose pull request is being merged now
	// (group\x00branch), so a tick does not start a second merge.
	merging map[string]bool
}

type savedGroup struct {
	Name     string      `json:"name"`
	Projects []string    `json:"projects"` // folders, in the person's order
	Notes    string      `json:"notes,omitempty"`
	Tasks    []savedTask `json:"tasks,omitempty"`
}

// savedTask is a task across the group's repos, its projects by folder.
type savedTask struct {
	Branch   string      `json:"branch"`
	Folder   string      `json:"folder"`
	Projects []string    `json:"projects"`
	Panes    []string    `json:"panes,omitempty"`
	Each     bool        `json:"each,omitempty"`
	Created  time.Time   `json:"created"`
	Merge    *savedMerge `json:"merge,omitempty"`
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
		// Kept aside rather than overwritten by the next save, which would
		// make every group the person had into none, silently.
		aside := g.file + ".broken"
		if rerr := os.Rename(g.file, aside); rerr != nil {
			log.Printf("groups: %s cannot be read (%v), and could not be kept aside: %v", g.file, err, rerr)
		} else {
			log.Printf("groups: %s cannot be read (%v); kept as %s, starting with no groups", g.file, err, aside)
		}
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
		info := proto.GroupInfo{Name: sg.Name, Notes: sg.Notes, Projects: g.ids(sg.Projects)}
		for _, t := range sg.Tasks {
			info.Tasks = append(info.Tasks, g.taskInfo(t))
		}
		if len(info.Projects) > 0 {
			out.Groups = append(out.Groups, info)
		}
	}
	return out
}

// ids are the projects of those folders this server has, in order.
func (g *groupBook) ids(roots []string) []string {
	out := []string{}
	for _, root := range roots {
		if id := projectID(root); g.registered(id) {
			out = append(out, id)
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
		sg.Tasks = g.groups[at].Tasks // its tasks go with it, renamed or not
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
	g.write()
	g.s.broadcast(proto.EventGroups, g.list())
}

// write writes groups.json whole, through a temporary file of its own
// renamed into place, so a reader never meets half a file.
func (g *groupBook) write() {
	g.saveMu.Lock()
	defer g.saveMu.Unlock()
	g.mu.Lock()
	b, err := json.MarshalIndent(groupsFile{Groups: append([]savedGroup{}, g.groups...)}, "", "  ")
	g.mu.Unlock()
	if err != nil {
		log.Printf("groups: save: %v", err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(g.file), 0o700); err != nil {
		log.Printf("groups: save: %v", err)
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(g.file), ".groups-*.json")
	if err != nil {
		log.Printf("groups: save: %v", err)
		return
	}
	name := tmp.Name()
	_, werr := tmp.Write(append(b, '\n'))
	cerr := tmp.Close()
	switch {
	case werr != nil:
		err = werr
	case cerr != nil:
		err = cerr
	default:
		err = os.Rename(name, g.file)
	}
	if err != nil {
		os.Remove(name)
		log.Printf("groups: save: %v", err)
	}
}

// A task across a group's repos: the same branch in a worktree of each of
// its projects, made as any task's worktree is — so each repo's branch,
// changes, pull request and checks are that project's as ever — and a
// task folder ($CONCH_HOME/tasks/GROUP/BRANCH) holding a link to each
// worktree under the repo's name and CONCH-TASK.md, the prompt and the
// repos. One agent works from the folder, given every worktree (Claude's
// and Codex's --add-dir), or each worktree gets an agent of its own told
// about the others. Before anything is made, everything that would refuse
// is asked: a worktree made and then a failure would leave half a task.
func (g *groupBook) task(tp proto.GroupTaskParams) (proto.GroupTaskResult, *proto.Error) {
	s := g.s
	if strings.TrimSpace(tp.Prompt) == "" {
		return proto.GroupTaskResult{}, proto.Errorf(proto.ErrBadRequest, "a task needs a prompt")
	}
	g.mu.Lock()
	at := slices.IndexFunc(g.groups, func(sg savedGroup) bool { return sg.Name == tp.Group })
	var sg savedGroup
	if at >= 0 {
		sg = g.groups[at]
	}
	g.mu.Unlock()
	if at < 0 {
		return proto.GroupTaskResult{}, proto.Errorf(proto.ErrNotFound, "no group called %s", tp.Group)
	}
	// Its projects, in the group's order: those asked for, or all.
	var projects []*project
	for _, root := range sg.Projects {
		id := projectID(root)
		if len(tp.Projects) > 0 && !slices.Contains(tp.Projects, id) {
			continue
		}
		p, perr := s.projects.get(id)
		if perr != nil {
			continue // not registered now: not part of this task
		}
		if !p.snapshot().Git {
			return proto.GroupTaskResult{}, proto.Errorf(proto.ErrBadRequest, "%s is not a git repository: a task needs a branch in each (%s)", p.snapshot().Name, notGitHint)
		}
		projects = append(projects, p)
	}
	for _, id := range tp.Projects {
		if !slices.ContainsFunc(projects, func(p *project) bool { return p.id == id }) {
			return proto.GroupTaskResult{}, proto.Errorf(proto.ErrBadRequest, "%s is not a repo of %s", id, tp.Group)
		}
	}
	if len(projects) == 0 {
		return proto.GroupTaskResult{}, proto.Errorf(proto.ErrBadRequest, "%s has no repo to start a task in", tp.Group)
	}
	if tp.Agent == "" {
		tp.Agent = s.defaultAgent()
	}
	if ad, ok := s.adapters.Get(tp.Agent); ok {
		if perr := s.notInstalled(ad); perr != nil {
			return proto.GroupTaskResult{}, perr
		}
	} else {
		return proto.GroupTaskResult{}, proto.Errorf(proto.ErrBadRequest, "no agent called %s", tp.Agent)
	}
	if tp.Branch == "" {
		tp.Branch = gitx.BranchFromPrompt(tp.Group, tp.Prompt)
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	err := gitx.ValidBranchName(ctx, projects[0].root, tp.Branch)
	cancel()
	if err != nil {
		return proto.GroupTaskResult{}, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	if slices.ContainsFunc(sg.Tasks, func(t savedTask) bool { return t.Branch == tp.Branch }) {
		return proto.GroupTaskResult{}, proto.Errorf(proto.ErrBadRequest, "%s already has a task on %s", tp.Group, tp.Branch)
	}
	for _, p := range projects {
		if wt, ok := p.branchWorktree(tp.Branch); ok {
			return proto.GroupTaskResult{}, proto.Errorf(proto.ErrBadRequest, "%s already has %s checked out at %s", p.snapshot().Name, tp.Branch, wt.Path)
		}
	}
	folder := filepath.Join(filepath.Dir(g.file), "tasks", safeSegment(tp.Group), safeSegment(tp.Branch))
	if _, err := os.Stat(folder); err == nil {
		return proto.GroupTaskResult{}, proto.Errorf(proto.ErrBadRequest, "%s is there already", folder)
	}

	// Made now: a worktree in each, undone if a later one fails.
	paths := make([]string, len(projects))
	for i, p := range projects {
		path, _, perr := s.projects.addWorktree(p, tp.Branch, "")
		if perr != nil {
			for j := 0; j < i; j++ {
				if rerr := s.projects.removeWorktree(proto.WorktreeRemoveParams{ProjectID: projects[j].id, Path: paths[j]}); rerr != nil {
					log.Printf("group task: undoing %s: %v", paths[j], rerr.Message)
				}
			}
			return proto.GroupTaskResult{}, proto.Errorf(perr.Code, "%s: %s (nothing was kept)", p.snapshot().Name, perr.Message)
		}
		paths[i] = path
		s.startSetup(p, path, tp.Branch)
	}
	names := linkNames(projects)
	if err := os.MkdirAll(folder, 0o755); err != nil {
		return proto.GroupTaskResult{}, proto.Errorf(proto.ErrInternal, "%v", err)
	}
	for i, path := range paths {
		if err := os.Symlink(path, filepath.Join(folder, names[i])); err != nil {
			log.Printf("group task: link %s: %v", names[i], err)
		}
	}
	brief := taskBrief(tp, sg.Notes, projects, names, paths)
	if err := os.WriteFile(filepath.Join(folder, "CONCH-TASK.md"), []byte(brief), 0o644); err != nil {
		log.Printf("group task: %v", err)
	}

	var started []proto.PaneInfo
	if tp.Each {
		for i, p := range projects {
			prompt := tp.Prompt + fmt.Sprintf("\n\nThis is one part of a task across %d repos of %s, all on the branch %s; the others are worked on by other agents at the same time. What each repo is for and where its worktree is: %s.",
				len(projects), tp.Group, tp.Branch, filepath.Join(folder, "CONCH-TASK.md")) + s.shipNote(p.id)
			info, perr := s.create(proto.PaneCreateParams{Agent: tp.Agent, Prompt: prompt, Cwd: paths[i], Cols: tp.Cols, Rows: tp.Rows})
			if perr != nil {
				return proto.GroupTaskResult{}, perr
			}
			started = append(started, info)
		}
	} else {
		repos := fmt.Sprintf("%d repos", len(projects))
		if len(projects) == 1 {
			repos = "1 repo"
		}
		prompt := tp.Prompt + fmt.Sprintf("\n\nThis task spans %s of %s, each checked out on the branch %s in a folder of its own here (%s). Read CONCH-TASK.md first. Commit and push in each repo on its own: they are separate repositories.",
			repos, tp.Group, tp.Branch, strings.Join(names, ", "))
		info, perr := s.create(proto.PaneCreateParams{Agent: tp.Agent, AgentArgs: adapter.DirArgs(tp.Agent, paths), Prompt: prompt,
			Cwd: folder, NoProject: true, Cols: tp.Cols, Rows: tp.Rows})
		if perr != nil {
			return proto.GroupTaskResult{}, perr
		}
		started = append(started, info)
	}

	t := savedTask{Branch: tp.Branch, Folder: folder, Each: tp.Each, Created: time.Now()}
	for _, p := range projects {
		t.Projects = append(t.Projects, p.root)
	}
	for _, info := range started {
		t.Panes = append(t.Panes, info.ID)
	}
	g.mu.Lock()
	if i := slices.IndexFunc(g.groups, func(x savedGroup) bool { return x.Name == tp.Group }); i >= 0 {
		g.groups[i].Tasks = append(g.groups[i].Tasks, t)
	}
	g.mu.Unlock()
	g.changed()
	return proto.GroupTaskResult{Task: proto.GroupTask{Branch: t.Branch, Folder: t.Folder, Projects: g.ids(t.Projects), Panes: t.Panes, Each: t.Each, Created: t.Created},
		Panes: started}, nil
}

// removeTask forgets a task and takes its folder away — links and the
// brief, never what they point at: each repo's worktree and branch stay,
// to be merged or deleted as any branch is.
func (g *groupBook) removeTask(ref proto.GroupTaskRef) *proto.Error {
	g.mu.Lock()
	gi := slices.IndexFunc(g.groups, func(sg savedGroup) bool { return sg.Name == ref.Group })
	if gi < 0 {
		g.mu.Unlock()
		return proto.Errorf(proto.ErrNotFound, "no group called %s", ref.Group)
	}
	ti := slices.IndexFunc(g.groups[gi].Tasks, func(t savedTask) bool { return t.Branch == ref.Branch })
	if ti < 0 {
		g.mu.Unlock()
		return proto.Errorf(proto.ErrNotFound, "%s has no task on %s", ref.Group, ref.Branch)
	}
	folder := g.groups[gi].Tasks[ti].Folder
	g.groups[gi].Tasks = slices.Delete(g.groups[gi].Tasks, ti, ti+1)
	g.mu.Unlock()
	if folder != "" && strings.HasPrefix(folder, filepath.Join(filepath.Dir(g.file), "tasks")+string(filepath.Separator)) {
		// os.RemoveAll takes the links themselves, not what they name.
		if err := os.RemoveAll(folder); err != nil {
			log.Printf("group task: %v", err)
		}
	}
	g.changed()
	return nil
}

// linkNames are the names of each worktree's link in the task folder: the
// repo's name, made unique when two repos share one.
func linkNames(projects []*project) []string {
	out := make([]string, len(projects))
	seen := map[string]int{}
	for i, p := range projects {
		n := safeSegment(p.snapshot().Name)
		seen[n]++
		if seen[n] > 1 {
			n = fmt.Sprintf("%s-%d", n, seen[n])
		}
		out[i] = n
	}
	return out
}

// safeSegment is a name made safe as one folder name.
func safeSegment(name string) string {
	r := strings.NewReplacer("/", "-", "\\", "-", string(filepath.Separator), "-", "\x00", "")
	n := strings.Trim(r.Replace(name), ". ")
	if n == "" {
		return "task"
	}
	return n
}

func taskBrief(tp proto.GroupTaskParams, notes string, projects []*project, names, paths []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s — a task across %s\n\n%s\n\n## Repos\n\n", tp.Branch, tp.Group, strings.TrimSpace(tp.Prompt))
	for i, p := range projects {
		info := p.snapshot()
		from := "" // not read yet when the project was added a moment ago
		if info.Base != "" {
			from = fmt.Sprintf(" from `%s`", info.Base)
		}
		fmt.Fprintf(&b, "- `%s/` — %s, branch `%s`%s; its worktree is %s\n", names[i], info.Name, tp.Branch, from, paths[i])
	}
	if strings.TrimSpace(notes) != "" {
		fmt.Fprintf(&b, "\n## Notes for the agents\n\n%s\n", strings.TrimSpace(notes))
	}
	b.WriteString("\nEach repo is its own git repository: commit, push and open a pull request in each one that changed.\n")
	return b.String()
}
