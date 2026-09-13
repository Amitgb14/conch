package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
)

func runProject(args []string) error {
	if len(args) == 0 {
		args = []string{"ls"}
	}
	c, err := connect(true)
	if err != nil {
		return err
	}
	defer c.Close()
	switch args[0] {
	case "add":
		if len(args) != 2 {
			return errors.New("usage: conch project add PATH")
		}
		var info proto.ProjectInfo
		if err := call(c, proto.MethodProjectAdd, proto.ProjectAddParams{Path: args[1]}, &info); err != nil {
			return err
		}
		fmt.Printf("%s  %s  %s\n", info.ID, info.Name, info.Path)
		return nil
	case "create", "new":
		fs := flag.NewFlagSet("project create", flag.ContinueOnError)
		noGit := fs.Bool("no-git", false, "make a plain folder instead of a git repository")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 1 {
			return errors.New("usage: conch project create [-no-git] PATH")
		}
		var info proto.ProjectInfo
		if err := call(c, proto.MethodProjectCreate, proto.ProjectCreateParams{Path: fs.Arg(0), Git: !*noGit}, &info); err != nil {
			return err
		}
		fmt.Printf("%s  %s  %s\n", info.ID, info.Name, info.Path)
		return nil
	case "files":
		fs := flag.NewFlagSet("project files", flag.ContinueOnError)
		reset := fs.Bool("reset", false, "restore the default patterns")
		clear := fs.Bool("none", false, "copy no local files")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() < 1 {
			return errors.New("usage: conch project files ID [-reset | -none | PATTERN...]")
		}
		id := fs.Arg(0)
		var info proto.ProjectInfo
		if *reset || *clear || fs.NArg() > 1 {
			params := proto.ProjectFilesParams{ProjectID: id, Patterns: fs.Args()[1:], Reset: *reset}
			if err := call(c, proto.MethodProjectFiles, params, &info); err != nil {
				return err
			}
		} else {
			var list proto.ProjectList
			if err := call(c, proto.MethodProjectList, nil, &list); err != nil {
				return err
			}
			found := false
			for _, p := range list.Projects {
				if p.ID == id {
					info, found = p, true
				}
			}
			if !found {
				return fmt.Errorf("no project %q", id)
			}
		}
		which := "custom"
		if info.LocalFilesDefault {
			which = "default"
		}
		fmt.Printf("local files copied into new worktrees of %s (%s):\n", info.Name, which)
		if len(info.LocalFiles) == 0 {
			fmt.Println("  (none)")
		}
		for _, pat := range info.LocalFiles {
			fmt.Println("  " + pat)
		}
		return nil
	case "rm", "remove":
		if len(args) != 2 {
			return errors.New("usage: conch project rm ID")
		}
		return call(c, proto.MethodProjectRemove, proto.ProjectRef{ID: args[1]}, nil)
	case "ls", "list":
		var list proto.ProjectList
		if err := call(c, proto.MethodProjectList, nil, &list); err != nil {
			return err
		}
		if len(list.Projects) == 0 {
			fmt.Println("no projects")
			return nil
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tNAME\tBASE\tBRANCHES\tWORKTREES\tPATH")
		for _, p := range list.Projects {
			var wts []string
			for _, wt := range p.Worktrees {
				s := wt.Branch
				if wt.Status != nil && !wt.Status.Clean() {
					s += fmt.Sprintf("(+%d-%d)", wt.Status.Added, wt.Status.Deleted)
				}
				wts = append(wts, s)
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\t%s\n", p.ID, p.Name, p.Base, len(p.Branches), strings.Join(wts, ","), p.Path)
		}
		return tw.Flush()
	}
	return fmt.Errorf("unknown project subcommand %q", args[0])
}

func runTask(args []string) error {
	fs := flag.NewFlagSet("task", flag.ContinueOnError)
	cwd := fs.String("cwd", "", "a directory in the project (default: current)")
	branch := fs.String("branch", "", "branch name (default: derived from the prompt)")
	base := fs.String("base", "", "branch to start from (default: the project's base)")
	agent := fs.String("agent", "", "claude, codex, gemini or opencode (default: [agents] default)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	prompt := strings.Join(fs.Args(), " ")
	if prompt == "" {
		return errors.New("usage: conch task [-cwd DIR] [-branch B] [-base B] [-agent NAME] PROMPT")
	}
	if *agent == "" {
		if cfg, err := config.Load(); err == nil {
			*agent = cfg.Agents.Default
		}
	}
	dir := *cwd
	if dir == "" {
		dir, _ = os.Getwd()
	}
	c, err := connect(true)
	if err != nil {
		return err
	}
	defer c.Close()
	var proj proto.ProjectInfo
	if err := call(c, proto.MethodProjectAdd, proto.ProjectAddParams{Path: dir}, &proj); err != nil {
		return err
	}
	var info proto.PaneInfo
	if err := call(c, proto.MethodTaskCreate, proto.TaskCreateParams{
		ProjectID: proj.ID, Prompt: prompt, Branch: *branch, Base: *base, Agent: *agent, Cols: 120, Rows: 40,
	}, &info); err != nil {
		return err
	}
	fmt.Printf("%s  %s  %s\n", info.ID, info.Cwd, info.Branch)
	return nil
}
