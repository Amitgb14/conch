package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/gitx"
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
	agent := fs.String("agent", "", "claude, codex, gemini or opencode, or several separated by commas (default: [agents] default)")
	n := fs.Int("n", 0, "how many attempts at the same prompt, each on its own branch (default: one per agent)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	prompt := strings.Join(fs.Args(), " ")
	if prompt == "" {
		return errors.New("usage: conch task [-cwd DIR] [-branch B] [-base B] [-agent NAME[,NAME...]] [-n N] PROMPT")
	}
	agents := splitAgents(*agent)
	if len(agents) == 0 {
		name := ""
		if cfg, err := config.Load(); err == nil {
			name = cfg.Agents.Default
		}
		agents = []string{name}
	}
	if *n < 0 {
		return errors.New("-n cannot be negative")
	}
	if *n > maxAttempts {
		return fmt.Errorf("-n %d is more than %d attempts", *n, maxAttempts)
	}
	dir := *cwd
	switch {
	case onRemoteMachine() && dir == "":
		// A task needs a project there; this directory is only one here.
		return fmt.Errorf("conch -m %s task needs -cwd: a directory in a project on %s", machineFlag, machineFlag)
	case onRemoteMachine():
		if err := remoteDir(dir); err != nil {
			return err
		}
	case dir == "":
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
	attempts := attemptPlan(proj.Name, agents, *n, *branch, prompt, proj.Branches)
	var failed int
	for _, at := range attempts {
		var info proto.PaneInfo
		params := proto.TaskCreateParams{ProjectID: proj.ID, Prompt: prompt, Branch: at.branch,
			Base: *base, Agent: at.agent, Cols: 120, Rows: 40}
		if err := callFor(c, proto.MethodTaskCreate, params, &info, harvestWait); err != nil {
			if len(attempts) == 1 {
				return err
			}
			// One attempt failing (a taken branch, a missing agent) leaves
			// the others running; say which, and fail at the end.
			fmt.Fprintf(os.Stderr, "conch: %s: %v\n", at.branch, err)
			failed++
			continue
		}
		fmt.Printf("%s  %s  %s\n", info.ID, info.Cwd, info.Branch)
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d attempts could not start", failed, len(attempts))
	}
	return nil
}

// maxAttempts caps -n: each attempt is an agent burning its own quota.
const maxAttempts = 10

// attempt is one try at the prompt.
type attempt struct{ agent, branch string }

// attemptPlan names the attempts: one per agent by default, n of them when
// asked, cycling through the agents. A single attempt keeps the plain
// branch name, so the common case reads as it always did.
func attemptPlan(project string, agents []string, n int, branch, prompt string, existing []proto.BranchInfo) []attempt {
	if n <= 0 {
		n = len(agents)
	}
	base := branch
	if base == "" {
		base = gitx.BranchFromPrompt(project, prompt)
	}
	if n == 1 {
		return []attempt{{agent: agents[0], branch: branch}}
	}
	taken := map[string]bool{}
	for _, b := range existing {
		taken[b.Name] = true
	}
	counts := map[string]int{}
	out := make([]attempt, 0, n)
	for i := 0; i < n; i++ {
		agent := agents[i%len(agents)]
		counts[agent]++
		name := gitx.AttemptBranch(base, agent, counts[agent], func(s string) bool { return taken[s] })
		taken[name] = true
		out = append(out, attempt{agent: agent, branch: name})
	}
	return out
}

// splitAgents parses -agent: "claude,codex" or a single name.
func splitAgents(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.ToLower(strings.TrimSpace(part)); part != "" {
			out = append(out, part)
		}
	}
	return out
}
