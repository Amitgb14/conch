package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
)

const groupUsage = `usage: conch group [ls [-json]]
       conch group add NAME PROJECT...        make a group, or add projects to one
       conch group rm NAME [PROJECT...]       take projects out of a group, or the group away
       conch group rename OLD NEW
       conch group notes NAME [TEXT]          notes for the agents working across it ("" clears)
       conch group task [-branch B] [-agent A] [-each] [-only P,P] NAME PROMPT...
                                              one task across its repos: the branch in a worktree
                                              of each, one agent given them all (-each: one per repo)
       conch group tasks NAME                 its tasks, their repos, folder and agents
       conch group task-rm NAME BRANCH        forget a task and remove its folder; the worktrees
                                              and branches stay
       conch group merge [-order P,P] [-remove] NAME BRANCH
                                              merge every pull request of a task once all are green,
                                              in that order (-remove: then the task, its worktrees and
                                              branches); the server waits, with no client open
       conch group merge -stop NAME BRANCH    stop waiting to merge them
A PROJECT is its ID, its name or its folder, as conch project ls lists them.`

// runGroup is conch group: projects the person keeps together because
// they change together (an API, its web app, their specs), each still a
// project of its own. The server keeps them (project.groups.v1), so the
// TUI, Conch.app and the phone show the same ones.
func runGroup(args []string) error {
	if len(args) == 0 {
		args = []string{"ls"}
	}
	switch args[0] {
	case "ls", "list", "add", "rm", "remove", "rename", "notes", "task", "tasks", "task-rm", "merge":
	default:
		return unknownSubcommand("group", args[0])
	}
	c, err := connect(true)
	if err != nil {
		return err
	}
	defer c.Close()
	if len(c.MissingCapabilities([]string{proto.CapProjectGroups})) > 0 {
		return errors.New("this server predates project groups (project.groups.v1): reload it with conch server reload")
	}
	var groups proto.GroupList
	if err := call(c, proto.MethodGroupList, nil, &groups); err != nil {
		return err
	}
	var projects proto.ProjectList
	if err := call(c, proto.MethodProjectList, nil, &projects); err != nil {
		return err
	}
	find := func(name string) (proto.GroupInfo, bool) {
		for _, g := range groups.Groups {
			if g.Name == name {
				return g, true
			}
		}
		return proto.GroupInfo{}, false
	}
	switch args[0] {
	case "ls", "list":
		asJSON := len(args) > 1 && (args[1] == "-json" || args[1] == "--json")
		if len(args) > 2 || len(args) == 2 && !asJSON {
			return errors.New(groupUsage)
		}
		if asJSON {
			b, _ := json.MarshalIndent(groups, "", "  ")
			fmt.Println(string(b))
			return nil
		}
		if len(groups.Groups) == 0 {
			fmt.Println("no groups: conch group add NAME PROJECT PROJECT makes one")
			return nil
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "NAME\tPROJECTS\tNOTES")
		for _, g := range groups.Groups {
			fmt.Fprintf(tw, "%s\t%s\t%s\n", g.Name, strings.Join(projectNames(g.Projects, projects.Projects), ", "), g.Notes)
		}
		return tw.Flush()

	case "add":
		if len(args) < 3 {
			return errors.New(groupUsage)
		}
		g, ok := find(args[1])
		ids := g.Projects
		for _, word := range args[2:] {
			id, err := pickProject(word, projects.Projects)
			if err != nil {
				return err
			}
			if !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
		p := proto.GroupSaveParams{Name: args[1], Projects: ids, Notes: g.Notes}
		if ok {
			p.Old = g.Name
		}
		return saveGroup(c, p, projects.Projects)

	case "rm", "remove":
		if len(args) < 2 {
			return errors.New(groupUsage)
		}
		g, ok := find(args[1])
		if !ok {
			return fmt.Errorf("no group called %s", args[1])
		}
		if len(args) == 2 {
			if err := call(c, proto.MethodGroupRemove, proto.GroupRef{Name: g.Name}, nil); err != nil {
				return err
			}
			fmt.Printf("removed the group %s; its projects stay\n", g.Name)
			return nil
		}
		ids := slices.Clone(g.Projects)
		for _, word := range args[2:] {
			id, err := pickProject(word, projects.Projects)
			if err != nil {
				return err
			}
			if !slices.Contains(ids, id) {
				return fmt.Errorf("%s is not in %s", word, g.Name)
			}
			ids = slices.DeleteFunc(ids, func(x string) bool { return x == id })
		}
		if len(ids) == 0 {
			return fmt.Errorf("that would leave %s empty: conch group rm %s takes the group away", g.Name, g.Name)
		}
		return saveGroup(c, proto.GroupSaveParams{Name: g.Name, Old: g.Name, Projects: ids, Notes: g.Notes}, projects.Projects)

	case "rename":
		if len(args) != 3 {
			return errors.New(groupUsage)
		}
		g, ok := find(args[1])
		if !ok {
			return fmt.Errorf("no group called %s", args[1])
		}
		return saveGroup(c, proto.GroupSaveParams{Name: args[2], Old: g.Name, Projects: g.Projects, Notes: g.Notes}, projects.Projects)

	case "task":
		return runGroupTask(c, args[1:], projects.Projects)

	case "tasks":
		if len(args) != 2 {
			return errors.New(groupUsage)
		}
		g, ok := find(args[1])
		if !ok {
			return fmt.Errorf("no group called %s", args[1])
		}
		if len(g.Tasks) == 0 {
			fmt.Printf("no tasks in %s: conch group task %s PROMPT starts one\n", g.Name, g.Name)
			return nil
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "BRANCH\tREPOS\tAGENTS\tMERGE\tFOLDER")
		for _, t := range g.Tasks {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", t.Branch, strings.Join(projectNames(t.Projects, projects.Projects), ", "), strings.Join(t.Panes, ","),
				mergeWord(t.Merge), t.Folder)
		}
		return tw.Flush()

	case "merge":
		return runGroupMerge(c, args[1:], projects.Projects)

	case "task-rm":
		if len(args) != 3 {
			return errors.New(groupUsage)
		}
		if len(c.MissingCapabilities([]string{proto.CapGroupTask})) > 0 {
			return errors.New("this server predates tasks across a group (group.task.v1): reload it with conch server reload")
		}
		if err := call(c, proto.MethodGroupTaskRemove, proto.GroupTaskRef{Group: args[1], Branch: args[2]}, nil); err != nil {
			return err
		}
		fmt.Printf("removed the task %s of %s and its folder; each repo keeps its worktree and branch\n", args[2], args[1])
		return nil

	case "notes":
		if len(args) < 2 {
			return errors.New(groupUsage)
		}
		g, ok := find(args[1])
		if !ok {
			return fmt.Errorf("no group called %s", args[1])
		}
		if len(args) == 2 {
			fmt.Println(g.Notes)
			return nil
		}
		return saveGroup(c, proto.GroupSaveParams{Name: g.Name, Old: g.Name, Projects: g.Projects, Notes: strings.Join(args[2:], " ")}, projects.Projects)
	}
	return unknownSubcommand("group", args[0])
}

func saveGroup(c *client.Client, p proto.GroupSaveParams, projects []proto.ProjectInfo) error {
	var g proto.GroupInfo
	if err := call(c, proto.MethodGroupSave, p, &g); err != nil {
		return err
	}
	fmt.Printf("%s: %s\n", g.Name, strings.Join(projectNames(g.Projects, projects), ", "))
	return nil
}

// pickProject finds the project a word names: its ID, its folder, or its
// name when only one project has it. A name two projects share is
// refused with both folders, since picking either would be a guess.
func pickProject(word string, projects []proto.ProjectInfo) (string, error) {
	abs := word
	if a, err := filepath.Abs(word); err == nil {
		abs = a
	}
	var named []proto.ProjectInfo
	for _, p := range projects {
		if p.ID == word || p.Path == word || p.Path == abs {
			return p.ID, nil
		}
		if p.Name == word {
			named = append(named, p)
		}
	}
	switch len(named) {
	case 1:
		return named[0].ID, nil
	case 0:
		return "", fmt.Errorf("no project %s: conch project ls lists them, conch project add PATH adds one", word)
	}
	var paths []string
	for _, p := range named {
		paths = append(paths, p.Path)
	}
	return "", fmt.Errorf("%d projects are called %s (%s): name it by its folder or ID", len(named), word, strings.Join(paths, ", "))
}

// projectNames says a group's projects by name, an ID for one not listed.
func projectNames(ids []string, projects []proto.ProjectInfo) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		name := id
		for _, p := range projects {
			if p.ID == id {
				name = p.Name
			}
		}
		out = append(out, name)
	}
	return out
}

// runGroupTask is conch group task: one task across a group's repos.
func runGroupTask(c *client.Client, args []string, projects []proto.ProjectInfo) error {
	fs := flag.NewFlagSet("group task", flag.ContinueOnError)
	branch := fs.String("branch", "", "the branch made in each repo (default: named from the prompt)")
	agent := fs.String("agent", "", "the agent to start (default: the server's first)")
	each := fs.Bool("each", false, "an agent in each repo's worktree, rather than one across them all")
	only := fs.String("only", "", "the repos to include, by name, folder or ID, comma-separated (default: all)")
	// Flags may follow the name, as they do in the usage of every other
	// command: Go's own parsing stopped at NAME, and then "-agent codex
	// -each" became the start of the prompt, run with the default agent.
	// "--" still ends them, for a prompt that starts with a dash.
	words, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(words) < 2 {
		return errors.New(groupUsage)
	}
	if len(c.MissingCapabilities([]string{proto.CapGroupTask})) > 0 {
		return errors.New("this server predates tasks across a group (group.task.v1): reload it with conch server reload")
	}
	p := proto.GroupTaskParams{Group: words[0], Prompt: strings.Join(words[1:], " "), Branch: *branch, Agent: *agent, Each: *each, Cols: 120, Rows: 40}
	if *only != "" {
		for _, w := range strings.Split(*only, ",") {
			id, err := pickProject(strings.TrimSpace(w), projects)
			if err != nil {
				return err
			}
			p.Projects = append(p.Projects, id)
		}
	}
	var res proto.GroupTaskResult
	if err := call(c, proto.MethodGroupTask, p, &res); err != nil {
		return err
	}
	fmt.Printf("%s across %s: %s\n", res.Task.Branch, strings.Join(projectNames(res.Task.Projects, projects), ", "), res.Task.Folder)
	for _, pane := range res.Panes {
		fmt.Println(pane.ID)
	}
	return nil
}

// runGroupMerge is conch group merge: Merge All When Green on a task.
func runGroupMerge(c *client.Client, args []string, projects []proto.ProjectInfo) error {
	fs := flag.NewFlagSet("group merge", flag.ContinueOnError)
	order := fs.String("order", "", "the order to merge in, every repo of the task by name, folder or ID, comma-separated (default: the task's)")
	remove := fs.Bool("remove", false, "once all are merged, remove the task, its worktrees and branches")
	stop := fs.Bool("stop", false, "stop waiting to merge")
	words, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(words) != 2 {
		return errors.New(groupUsage)
	}
	if len(c.MissingCapabilities([]string{proto.CapGroupTaskMerge})) > 0 {
		return errors.New("this server predates merging a task's pull requests (group.task.merge.v1): reload it with conch server reload")
	}
	p := proto.GroupTaskMergeParams{Group: words[0], Branch: words[1], RemoveTask: *remove, Stop: *stop}
	if *order != "" {
		for _, w := range strings.Split(*order, ",") {
			id, err := pickProject(strings.TrimSpace(w), projects)
			if err != nil {
				return err
			}
			p.Order = append(p.Order, id)
		}
	}
	var t proto.GroupTask
	if err := call(c, proto.MethodGroupTaskMerge, p, &t); err != nil {
		return err
	}
	if *stop {
		fmt.Printf("%s: no longer waiting to merge\n", t.Branch)
		return nil
	}
	fmt.Printf("%s: merges %s once every pull request is green\n", t.Branch, strings.Join(projectNames(mergeOrder(t), projects), ", then "))
	return nil
}

func mergeOrder(t proto.GroupTask) []string {
	if t.Merge != nil && len(t.Merge.Order) > 0 {
		return t.Merge.Order
	}
	return t.Projects
}

// mergeWord is where a task's Merge All stands, for conch group tasks.
func mergeWord(m *proto.GroupTaskMerge) string {
	if m == nil {
		return "-"
	}
	w := m.State
	if m.State == proto.MergeWaiting || m.State == proto.MergeMerging {
		w += fmt.Sprintf(" (%d of %d merged)", len(m.Merged), len(m.Order))
	}
	if m.Why != "" {
		w += ": " + m.Why
	}
	return w
}
