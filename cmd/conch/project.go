package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/Amitgb14/conch/internal/sandbox"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/gitx"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/when"
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
	case "setup":
		return projectSetup(c, args[1:])
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

// projectSetup shows or sets the command run in each new worktree of a
// project. The command is the words after the ID, as typed — quote it to
// keep the shell from running half of it here.
func projectSetup(c *client.Client, args []string) error {
	fs := flag.NewFlagSet("project setup", flag.ContinueOnError)
	none := fs.Bool("none", false, "run nothing in new worktrees")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 1 || (*none && fs.NArg() > 1) {
		return errors.New("usage: conch project setup ID [-none | COMMAND]")
	}
	if len(c.MissingCapabilities([]string{proto.CapWorktreeSetup})) > 0 {
		return errors.New("the conch server there predates worktree setup; reload it with `conch server reload`")
	}
	id := fs.Arg(0)
	var info proto.ProjectInfo
	if *none || fs.NArg() > 1 {
		cmd := strings.Join(fs.Args()[1:], " ")
		if err := call(c, proto.MethodProjectSetup, proto.ProjectSetupParams{ProjectID: id, Command: cmd}, &info); err != nil {
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
	if info.Setup == "" {
		fmt.Printf("new worktrees of %s run no setup\n", info.Name)
		return nil
	}
	fmt.Printf("new worktrees of %s run, in a terminal of their own:\n  %s\n", info.Name, info.Setup)
	return nil
}

func runTask(args []string) error {
	fs := flag.NewFlagSet("task", flag.ContinueOnError)
	cwd := fs.String("cwd", "", "a directory in the project (default: current)")
	branch := fs.String("branch", "", "branch name (default: derived from the prompt)")
	base := fs.String("base", "", "branch to start from (default: the project's base)")
	agent := fs.String("agent", "", "claude, codex, gemini or opencode, or several separated by commas (default: [agents] default)")
	n := fs.Int("n", 0, "how many attempts at the same prompt, each on its own branch (default: one per agent)")
	name := fs.String("name", "", "name the pane, to address it by in place of its ID")
	at := fs.String("at", "", "start it later: 14:05, +30m, 2026-10-15 09:00 — kept by the server")
	every := fs.String("every", "", "start it again this often, each on a branch of its own: 1h, 1d, weekly")
	inSandbox := fs.String("sandbox", "", "run it in a new sandbox from this provider (daytona, boat…), cloned from the project's origin")
	issue := fs.Int("issue", 0, "work on this GitHub issue: its title and body are the prompt (any PROMPT is added to it)")
	queue := fs.Bool("queue", false, "add it to the project's queue: it starts when fewer than -wip agents are running there")
	wip := fs.Int("wip", 0, "with -queue, how many agents the project runs before it waits (default 3)")
	after := fs.String("after", "", "start it once this has finished, on top of its branch: a branch, a pane, or a task on the schedule (its ID or -branch)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	prompt := strings.Join(fs.Args(), " ")
	if prompt == "" && *issue <= 0 {
		return errors.New("usage: conch task [-cwd DIR] [-branch B] [-base B] [-agent NAME[,NAME...]] [-n N] [-name N] [-at TIME] [-every SPAN] [-queue [-wip N]] [-after REF] [-issue N] [-sandbox PROVIDER] PROMPT")
	}
	if *issue < 0 {
		return errors.New("-issue takes an issue's number")
	}
	*after = strings.TrimSpace(*after)
	if *inSandbox != "" && (*at != "" || *every != "" || *queue || *after != "") {
		return errors.New("-sandbox starts the task now, in a sandbox made for it; it doesn't go with -at, -every, -queue or -after")
	}
	if *after != "" && *every != "" {
		return errors.New("-after waits for one piece of work, so the task runs once; it doesn't go with -every")
	}
	if *inSandbox != "" && onRemoteMachine() {
		return errors.New("-sandbox makes a machine of its own; leave out -m")
	}
	if *wip != 0 && !*queue {
		return errors.New("-wip goes with -queue")
	}
	if *queue && *every != "" {
		return errors.New("-queue runs a task once; -every repeats it on a schedule — choose one")
	}
	later := *at != "" || *every != "" || *queue || *after != ""
	var start time.Time
	everyMin := 0
	if later {
		var err error
		start = time.Now()
		if *at != "" {
			if start, err = when.Parse(*at, time.Now()); err != nil {
				return fmt.Errorf("-at %w", err)
			}
		}
		if *every != "" {
			if everyMin, err = when.Every(*every); err != nil {
				return fmt.Errorf("-every %w", err)
			}
		}
	}
	*name = strings.TrimSpace(*name)
	if proto.IsPaneID(*name) {
		return fmt.Errorf("-name %q is shaped like a pane ID; choose another name", *name)
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
		// Nothing here says where on that machine: the agent starts in its
		// home, as a machine-level pane, like conch -m M new -agent.
		return runPlainTask(nil, "", agents, *n, *branch, *base, *name, prompt)
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
	if *issue > 0 && !proj.Git {
		return errors.New("-issue needs a git repository with a GitHub remote")
	}
	if *issue > 0 && len(c.MissingCapabilities([]string{proto.CapIssues})) > 0 {
		return errors.New("the conch server there predates tasks from issues; reload it with `conch server reload`")
	}
	if !proj.Git {
		if later {
			return errors.New("-at, -every, -queue and -after start a task on a branch of its own, so they need a git repository")
		}
		// No repository to branch: the agent works in the folder itself.
		return runPlainTask(c, dir, agents, *n, *branch, *base, *name, prompt)
	}
	branchFrom := prompt
	if *issue > 0 && *branch == "" {
		// One attempt's branch is the server's, named from the issue's
		// title; several need a name to number from here, which is this.
		branchFrom = fmt.Sprintf("issue %d", *issue)
	}
	attempts := attemptPlan(proj.Name, agents, *n, *branch, branchFrom, proj.Branches)
	if *inSandbox != "" {
		if len(attempts) > 1 {
			return errors.New("-sandbox takes one attempt; leave out -n and give one agent")
		}
		task := proto.TaskCreateParams{Prompt: prompt, Branch: *branch, Base: *base, Agent: attempts[0].agent,
			Name: *name, Cols: 120, Rows: 40, Issue: *issue}
		return runSandboxTask(*inSandbox, proj, task)
	}
	if later {
		if len(attempts) > 1 {
			return errors.New("-at, -every, -queue and -after take one attempt; leave out -n and give one agent")
		}
		if err := needSchedule(c); err != nil {
			return err
		}
		if *after != "" && len(c.MissingCapabilities([]string{proto.CapTaskAfter})) > 0 {
			return errors.New("the conch server there predates -after, and would start the task at once; reload it with `conch server reload`")
		}
		task := proto.TaskCreateParams{ProjectID: proj.ID, Prompt: prompt, Branch: *branch, Base: *base,
			Agent: attempts[0].agent, Name: *name, Cols: 120, Rows: 40, Issue: *issue}
		var it proto.ScheduledItem
		if err := call(c, proto.MethodScheduleAdd, proto.ScheduledItem{At: start, EveryMinutes: everyMin, Task: &task, Queue: *queue, WIP: *wip, After: *after}, &it); err != nil {
			return err
		}
		scheduleOut(it)
		return nil
	}
	if *name != "" && len(attempts) > 1 {
		return fmt.Errorf("-name names one pane, and this starts %d; leave it out, or rename them after", len(attempts))
	}
	// An older server drops Name, so the pane is renamed once it starts —
	// and can't refuse a taken name, so that is checked here first.
	renameAfter := *name != "" && len(c.MissingCapabilities([]string{proto.CapTaskName})) > 0
	if renameAfter {
		var list proto.PaneList
		if err := call(c, proto.MethodPaneList, nil, &list); err != nil {
			return err
		}
		for _, p := range list.Panes {
			if p.State == proto.PaneRunning && p.Name == *name {
				return fmt.Errorf("pane %s is already named %q", p.ID, *name)
			}
		}
	}
	var failed int
	for _, at := range attempts {
		var info proto.PaneInfo
		params := proto.TaskCreateParams{ProjectID: proj.ID, Prompt: prompt, Branch: at.branch,
			Base: *base, Agent: at.agent, Name: *name, Cols: 120, Rows: 40, Issue: *issue}
		if renameAfter {
			params.Name = ""
		}
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
		if renameAfter {
			if err := call(c, proto.MethodPaneRename, proto.PaneRenameParams{ID: info.ID, Name: *name}, &info); err != nil {
				return fmt.Errorf("%s started, but naming it failed: %v", info.ID, err)
			}
		}
		fmt.Printf("%s  %s  %s\n", info.ID, info.Cwd, info.Branch)
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d attempts could not start", failed, len(attempts))
	}
	return nil
}

// runPlainTask starts the agents on the prompt in dir, with no branch or
// worktree: dir is not a git repository, or is "" for the machine's home.
// c is nil when nothing has connected yet; name, when given, names the
// one pane it starts.
func runPlainTask(c *client.Client, dir string, agents []string, n int, branch, base, name, prompt string) error {
	where := dir
	if where == "" {
		where = "the home directory of " + machineFlag
	}
	if branch != "" || base != "" {
		return fmt.Errorf("-branch and -base need a git repository, and %s is not one", where)
	}
	if n <= 0 {
		n = len(agents)
	}
	if name != "" && n > 1 {
		return fmt.Errorf("-name names one pane, and this starts %d; leave it out, or rename them after", n)
	}
	if n > 1 {
		// Nothing keeps them apart without branches.
		fmt.Fprintf(os.Stderr, "conch: %s is not a git repository, so all %d attempts work in the same folder\n", where, n)
	}
	if c == nil {
		var err error
		if c, err = connect(true); err != nil {
			return err
		}
		defer c.Close()
	}
	if name != "" {
		// pane.create doesn't refuse a taken name as task.create does, and
		// a name is only worth having if it picks out this pane.
		var list proto.PaneList
		if err := call(c, proto.MethodPaneList, nil, &list); err != nil {
			return err
		}
		for _, p := range list.Panes {
			if p.State == proto.PaneRunning && p.Name == name {
				return fmt.Errorf("pane %s is already named %q", p.ID, name)
			}
		}
	}
	var failed int
	for i := 0; i < n; i++ {
		// The server would start a shell for no agent, where a task's
		// server picks its first: claude.
		agent := firstNonEmptyStr(agents[i%len(agents)], "claude")
		params := proto.PaneCreateParams{Name: name, Agent: agent, Prompt: prompt, Cwd: dir, NoProject: dir == "", Cols: 120, Rows: 40}
		var info proto.PaneInfo
		if err := call(c, proto.MethodPaneCreate, params, &info); err != nil {
			if n == 1 {
				return err
			}
			fmt.Fprintf(os.Stderr, "conch: %s: %v\n", agent, err)
			failed++
			continue
		}
		fmt.Printf("%s  %s\n", info.ID, info.Cwd)
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d attempts could not start", failed, n)
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

// runSandboxTask starts a task in a sandbox made for it: created with the
// provider named, the project cloned there from its origin, and the agent
// started on its own branch — the agent works on a machine of its own, and
// nothing it runs touches this one. The sandbox stays, as any other machine
// in the sidebar does, until it is stopped or removed.
func runSandboxTask(provider string, proj proto.ProjectInfo, task proto.TaskCreateParams) error {
	if proj.Remote == "" {
		return fmt.Errorf("-sandbox clones %s from its origin remote, and it has none; push it somewhere the sandbox can reach first", proj.Name)
	}
	if _, err := takeProvider([]string{"-provider", provider}); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), sandboxWait+10*time.Minute)
	defer cancel()
	label := proj.Name + "-task"
	saved, sb, c, err := makeSandbox(ctx, label, sandbox.Spec{}, nil, false)
	if err != nil {
		return err
	}
	defer c.Close()
	fmt.Fprintf(os.Stderr, "  cloning %s into it…\n", proj.Remote)
	var there proto.ProjectInfo
	if err := callFor(c, proto.MethodProjectClone, proto.ProjectCloneParams{URL: proj.Remote, Path: "~/" + proj.Name}, &there, 10*time.Minute); err != nil {
		return fmt.Errorf("the sandbox (%s, %s %s) is up, but cloning failed: %w", saved.ID, provider, sb.ID, err)
	}
	if task.Issue > 0 && len(c.MissingCapabilities([]string{proto.CapIssues})) > 0 {
		return fmt.Errorf("the conch in the sandbox predates tasks from issues; run conch machine upgrade %s", saved.ID)
	}
	task.ProjectID = there.ID
	var info proto.PaneInfo
	if err := callFor(c, proto.MethodTaskCreate, task, &info, harvestWait); err != nil {
		return fmt.Errorf("the sandbox (%s) has the project, but the task didn't start: %w", saved.ID, err)
	}
	fmt.Printf("%s  %s  %s  on %s\n", info.ID, info.Cwd, info.Branch, saved.ID)
	fmt.Fprintf(os.Stderr, "  it works from %s's %s; follow it in the sidebar, or: conch -m %s read %s\n", proj.Remote, there.Base, saved.ID, info.ID)
	return nil
}
