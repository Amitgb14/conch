package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
)

// resolvePane turns what a command was given — a pane's ID or its name, as
// `conch status` shows it — into the ID. An ID goes through untouched and
// unlisted, so it means what it always did. A name is looked up now and
// the command then holds to that pane: one whose name moves on meanwhile
// is not followed. A name several running panes share picks none of them.
func resolvePane(c *client.Client, ref string) (string, error) {
	if ref == "" {
		return "", errors.New("no pane given")
	}
	if proto.IsPaneID(ref) {
		return ref, nil
	}
	var list proto.PaneList
	if err := call(c, proto.MethodPaneList, nil, &list); err != nil {
		return "", err
	}
	return paneNamed(list.Panes, ref)
}

// paneNamed finds the pane called name. Running panes come first: a name
// given again after its first pane ended means the new one. With none
// running, an ended pane still answers, so a wait on it says it ended.
func paneNamed(panes []proto.PaneInfo, name string) (string, error) {
	var running, ended []string
	for _, p := range panes {
		if p.Name != name {
			continue
		}
		if p.State == proto.PaneRunning {
			running = append(running, p.ID)
		} else {
			ended = append(ended, p.ID)
		}
	}
	for _, ids := range [][]string{running, ended} {
		switch len(ids) {
		case 0:
			continue
		case 1:
			return ids[0], nil
		}
		return "", fmt.Errorf("%d panes are named %q (%s); give an ID", len(ids), name, strings.Join(ids, ", "))
	}
	return "", fmt.Errorf("no pane %q", name)
}

// runRename names a pane, or with no name gives it back its own.
//
//	conch rename p4 reviewer
func runRename(args []string) error {
	if len(args) < 1 || len(args) > 2 {
		return errors.New("usage: conch rename ID|NAME [NEW-NAME]")
	}
	name := ""
	if len(args) == 2 {
		if name = strings.TrimSpace(args[1]); name == "" {
			return errors.New("the new name is empty; leave it out to give the pane back its own")
		}
		if proto.IsPaneID(name) {
			return fmt.Errorf("%q is shaped like a pane ID; choose another name", name)
		}
	}
	c, err := connect(false)
	if err != nil {
		return err
	}
	defer c.Close()
	var list proto.PaneList
	if err := call(c, proto.MethodPaneList, nil, &list); err != nil {
		return err
	}
	id := args[0]
	if !proto.IsPaneID(id) {
		if id, err = paneNamed(list.Panes, id); err != nil {
			return err
		}
	}
	if name != "" {
		for _, p := range list.Panes {
			if p.ID != id && p.State == proto.PaneRunning && p.Name == name {
				return fmt.Errorf("pane %s is already named %q", p.ID, name)
			}
		}
	}
	var info proto.PaneInfo
	if err := call(c, proto.MethodPaneRename, proto.PaneRenameParams{ID: id, Name: name}, &info); err != nil {
		return err
	}
	fmt.Printf("%s %s\n", info.ID, info.Name)
	return nil
}
