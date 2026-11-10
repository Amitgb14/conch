package main

import (
	"encoding/json"
	"errors"
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
	case "ls", "list", "add", "rm", "remove", "rename", "notes":
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
