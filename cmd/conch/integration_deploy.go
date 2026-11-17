package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/integration"
)

// A deploy integration (Vercel, Netlify) used by hand: a branch's
// previews, what a failed build printed, building one again, and
// releasing one to production — which asks first.

// deployLogLines is how much of a build's log is shown: its end, as an
// agent is sent a failed CI run's.
const deployLogLines = 200

// deployHere is the deploy integration named — or, with no name, the one
// the project in dir uses — connected with its key, and the branch it is
// asked about.
func deployHere(ctx context.Context, dir, name string) (*integration.File, *integration.Conn, integration.Target, error) {
	root, rem, branch := projectHere(ctx, dir)
	t := integration.Target{Remote: rem, Branch: branch}
	all, _ := integration.All()
	var f *integration.File
	if name == "" {
		var used []string
		for _, x := range all {
			if x.Kind == integration.KindDeploy && root != "" && x.Uses(root, rem) {
				f = x
				used = append(used, x.Name)
			}
		}
		switch len(used) {
		case 0:
			return nil, nil, t, errors.New("no deploy integration is used here: name one (conch integration search deploy lists them)")
		case 1:
		default:
			return nil, nil, t, fmt.Errorf("this project uses %s: name one", strings.Join(used, " and "))
		}
	} else {
		for _, x := range all {
			if x.Name == name {
				f = x
			}
		}
		switch {
		case f == nil:
			return nil, nil, t, fmt.Errorf("no deploy integration %s here: conch integration install %s, or conch integration search deploy", name, name)
		case f.Kind != integration.KindDeploy:
			return nil, nil, t, fmt.Errorf("%s is not a deploy integration (it is %s)", name, f.Kind)
		}
	}
	cfg, _ := config.Load()
	user := ""
	if f.Auth.UserEnv != "" {
		user = keyFor(f.Auth.UserEnv)
	}
	conn, err := f.Connect(keyFor(f.KeyEnv), user, cfg.Integrations.Base[f.Name])
	if errors.Is(err, integration.ErrNoKey) {
		return nil, nil, t, fmt.Errorf("%w: conch integration key %s", err, f.Name)
	}
	if err != nil {
		return nil, nil, t, err
	}
	return f, conn, t, nil
}

func integrationPreviews(args []string) error {
	fs := flag.NewFlagSet("integration previews", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print JSON")
	dir := fs.String("cwd", "", "the project's checkout (default: here)")
	branch := fs.String("branch", "", "the branch (default: the checkout's)")
	head := fs.String("head", "", "only deploys of this commit, as at a pull request's head")
	if err := fs.Parse(reorderFlags(args)); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return errors.New("usage: conch integration previews [-json] [-cwd DIR] [-branch B] [-head SHA] [NAME]")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	f, conn, t, err := deployHere(ctx, *dir, fs.Arg(0))
	if err != nil {
		return err
	}
	if *branch != "" {
		t.Branch = *branch
	}
	t.Head = strings.TrimSpace(*head)
	switch {
	case t.Remote.Host == "":
		return errors.New("the checkout has no remote to find its project by")
	case t.Branch == "":
		return errors.New("no branch: give -branch")
	}
	deps, err := conn.Previews(ctx, t)
	if errors.Is(err, integration.ErrUnlinked) {
		return errors.New(integration.Unlinked(f, t.Remote))
	}
	if err != nil {
		return err
	}
	if *asJSON {
		if deps == nil {
			deps = []integration.Deploy{}
		}
		return json.NewEncoder(os.Stdout).Encode(deps)
	}
	at := ""
	if t.Head != "" {
		at = " at " + t.Head
	}
	if len(deps) == 0 {
		fmt.Printf("%s: no deploys of %s%s\n", f.DisplayName(), t.Branch, at)
		return nil
	}
	fmt.Printf("%s · %s%s\n", f.DisplayName(), t.Branch, at)
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, d := range deps {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", d.State, d.ID, firstSetStr(d.Environment, "-"), firstSetStr(short(d.Commit), "-"), d.URL)
	}
	tw.Flush()
	for _, d := range deps {
		if d.State == "failed" && d.Error != "" {
			fmt.Printf("%s failed: %s\n", d.ID, d.Error)
		}
	}
	if d := deps[0]; d.Inspect != "" {
		fmt.Printf("newest on %s: %s\n", f.DisplayName(), d.Inspect)
	}
	return nil
}

// short is a commit as people write it.
func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// deployArgs parses an operation on one deploy: [NAME] DEPLOY.
func deployArgs(fs *flag.FlagSet, args []string, usage string) (name, deploy string, err error) {
	if err := fs.Parse(reorderFlags(args)); err != nil {
		return "", "", err
	}
	switch fs.NArg() {
	case 1:
		return "", fs.Arg(0), nil
	case 2:
		return fs.Arg(0), fs.Arg(1), nil
	}
	return "", "", errors.New("usage: conch integration " + usage)
}

func integrationDeployLog(args []string) error {
	fs := flag.NewFlagSet("integration deploy-log", flag.ContinueOnError)
	dir := fs.String("cwd", "", "the project's checkout (default: here)")
	lines := fs.Int("lines", deployLogLines, "how many lines of its end to print; 0 for all")
	name, deploy, err := deployArgs(fs, args, "deploy-log [-lines N] [-cwd DIR] [NAME] DEPLOY")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	f, conn, t, err := deployHere(ctx, *dir, name)
	if err != nil {
		return err
	}
	lg, err := conn.FailedLog(ctx, t, deploy)
	if errors.Is(err, integration.ErrNothing) {
		fmt.Printf("%s: %s left no log\n", f.DisplayName(), deploy)
		return nil
	}
	if err != nil {
		return err
	}
	fmt.Print(lastLines(lg.Text, *lines))
	return nil
}

// lastLines is the last n lines of s (all of it for n <= 0), ending in a
// newline.
func lastLines(s string, n int) string {
	s = strings.TrimRight(s, "\n")
	if n > 0 {
		all := strings.Split(s, "\n")
		if len(all) > n {
			s = strings.Join(all[len(all)-n:], "\n")
		}
	}
	return s + "\n"
}

func integrationRedeploy(args []string) error {
	fs := flag.NewFlagSet("integration redeploy", flag.ContinueOnError)
	dir := fs.String("cwd", "", "the project's checkout (default: here)")
	name, deploy, err := deployArgs(fs, args, "redeploy [-cwd DIR] [NAME] DEPLOY")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	f, conn, t, err := deployHere(ctx, *dir, name)
	if err != nil {
		return err
	}
	if err := conn.Redeploy(ctx, t, deploy); err != nil {
		return err
	}
	fmt.Printf("Building %s again on %s.\n", strings.TrimSpace(deploy), f.DisplayName())
	return nil
}

// integrationPromote releases a deploy to production. That changes what
// everybody using it is shown, so it asks, and -y is the person saying
// yes beforehand; anything but yes releases nothing, and says so with a
// failing status so a script knows.
func integrationPromote(args []string) error {
	fs := flag.NewFlagSet("integration promote", flag.ContinueOnError)
	dir := fs.String("cwd", "", "the project's checkout (default: here)")
	yes := fs.Bool("y", false, "release without asking")
	name, deploy, err := deployArgs(fs, args, "promote [-y] [-cwd DIR] [NAME] DEPLOY")
	if err != nil {
		return err
	}
	deploy = strings.TrimSpace(deploy)
	if deploy == "" {
		return errors.New("no deploy given")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	f, conn, t, err := deployHere(ctx, *dir, name)
	if err != nil {
		return err
	}
	if !f.Promote.Has() {
		return fmt.Errorf("%s: its file cannot promote", f.DisplayName())
	}
	// What everybody is shown is the person's to change, never an
	// agent's: asked of this machine's server, which knows from the
	// kernel which pane is asking (a guard, as scoping is, not a lock).
	if who, err := localCaller(); err != nil {
		return err
	} else if who.Scoped {
		return fmt.Errorf("the %s agent in %s may not release to production: only the person may — from conch's window or a terminal of their own", who.Agent, who.Pane)
	}
	if !*yes && !confirm(fmt.Sprintf("Release %s to production on %s? Everybody using it sees it. [y/N] ", deploy, f.DisplayName()), false) {
		return errors.New("not released")
	}
	if err := conn.Promote(ctx, t, deploy); err != nil {
		return err
	}
	fmt.Printf("Releasing %s to production on %s.\n", deploy, f.DisplayName())
	return nil
}

// integrationUse turns a deploy integration on (or, -off, off) for the
// project here, for a service that links the repository from its own side
// and leaves no file conch could find (Vercel's dashboard): kept in
// config.toml's [integrations.projects] by the project's folder, which the
// server reads when it looks for a branch's previews.
func integrationUse(args []string) error {
	fs := flag.NewFlagSet("integration use", flag.ContinueOnError)
	dir := fs.String("cwd", "", "the project's checkout, or a worktree of it (default: here)")
	off := fs.Bool("off", false, "stop using it for this project")
	if err := fs.Parse(reorderFlags(args)); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: conch integration use [-off] [-cwd DIR] NAME")
	}
	name := fs.Arg(0)
	var f *integration.File
	all, _ := integration.All()
	for _, x := range all {
		if x.Name == name {
			f = x
		}
	}
	switch {
	case f == nil:
		return fmt.Errorf("no integration %s here: conch integration install %s", name, name)
	case f.Kind != integration.KindDeploy:
		return fmt.Errorf("%s is not a deploy integration (it is %s): only deploy integrations are chosen per project", name, f.Kind)
	}
	root, err := mainCheckout(*dir)
	if err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	cfg.Integrations.SetChosen(root, name, !*off)
	if err := config.Save(cfg); err != nil {
		return err
	}
	if *off {
		fmt.Printf("%s is no longer used for %s (unless its files show it)\n", f.DisplayName(), filepath.Base(root))
	} else {
		fmt.Printf("%s is used for %s: kept in config.toml, [integrations.projects]\n", f.DisplayName(), filepath.Base(root))
	}
	return nil
}

// mainCheckout is the project dir belongs to: the main checkout, even
// from a worktree of it, since that is the folder a project is known by.
func mainCheckout(dir string) (string, error) {
	if dir == "" {
		dir, _ = os.Getwd()
	}
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if err != nil {
		return "", fmt.Errorf("%s is not in a git checkout", dir)
	}
	common := strings.TrimSpace(string(out))
	root := filepath.Dir(common)
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	return root, nil
}
