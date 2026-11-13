package main

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
)

const projectGitInitUsage = "usage: conch project git-init ID|PATH"

// projectGitInit makes a project that is a plain folder a git repository —
// git init and an empty first commit, the files already there left
// untracked — so a task can make a branch in it.
func projectGitInit(c *client.Client, args []string) error {
	if len(args) != 1 || args[0] == "" {
		return errors.New(projectGitInitUsage)
	}
	if len(c.MissingCapabilities([]string{proto.CapProjectGitInit})) > 0 {
		return errors.New("this server predates making a project a git repository (project.git_init.v1): reload it (conch server reload), or run git init and commit once in the folder yourself")
	}
	var list proto.ProjectList
	if err := call(c, proto.MethodProjectList, nil, &list); err != nil {
		return err
	}
	p, ok := findProject(list.Projects, args[0])
	if !ok {
		return notFound(fmt.Errorf("no project %q", args[0]), "conch project ls")
	}
	var info proto.ProjectInfo
	if err := call(c, proto.MethodProjectGitInit, proto.ProjectRef{ID: p.ID}, &info); err != nil {
		return err
	}
	fmt.Printf("%s is a git repository now, on %s with an empty first commit; files already there are left uncommitted\n", info.Path, info.Base)
	return nil
}

// findProject is the project whose ID is ref, else whose folder ref names
// (relative to here, symlinks resolved as the server records them).
func findProject(projects []proto.ProjectInfo, ref string) (proto.ProjectInfo, bool) {
	for _, p := range projects {
		if p.ID == ref {
			return p, true
		}
	}
	path, err := filepath.Abs(ref)
	if err != nil {
		return proto.ProjectInfo{}, false
	}
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	for _, p := range projects {
		if p.Path == path {
			return p, true
		}
	}
	return proto.ProjectInfo{}, false
}
