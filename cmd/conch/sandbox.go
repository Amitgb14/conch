package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/remote"
	"github.com/Amitgb14/conch/internal/sandbox"
)

// sandboxProvider is the provider a command works with. Every command but
// stats must name one: there is no default, since a provider the user
// never chose could bill an account they didn't mean, and a sandbox named
// under the wrong provider is refused rather than quietly taken from its
// machine.
var sandboxProvider string

// takeProvider pulls -provider NAME out of the arguments before the
// subcommand sees them, so it can be given in front of any of them.
func takeProvider(args []string) ([]string, error) {
	sandboxProvider = "" // each command starts clean
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		name := ""
		switch {
		case args[i] == "-provider" || args[i] == "--provider":
			if i+1 >= len(args) {
				return nil, errors.New("-provider needs a name, e.g. -provider boat")
			}
			name = args[i+1]
			i++
		case strings.HasPrefix(args[i], "-provider="):
			name = strings.TrimPrefix(args[i], "-provider=")
		case strings.HasPrefix(args[i], "--provider="):
			name = strings.TrimPrefix(args[i], "--provider=")
		default:
			out = append(out, args[i])
			continue
		}
		if !sandbox.Known(name) {
			return nil, fmt.Errorf("unknown sandbox provider %q; conch knows %s", name, strings.Join(sandbox.Providers, ", "))
		}
		sandboxProvider = name
	}
	return out, nil
}

// sandboxWait bounds creating or starting a sandbox and setting conch up
// in it; tests shorten it.
var sandboxWait = 10 * time.Minute

func runSandbox(args []string) error {
	args, err := takeProvider(args)
	if err != nil {
		return err
	}
	if len(args) == 0 {
		args = []string{"ls"}
	}
	if args[0] == "stats" {
		return sandboxStats(args[1:])
	}
	if sandboxProvider == "" {
		return errNoProvider()
	}
	switch args[0] {
	case "create", "new":
		return sandboxCreate(args[1:])
	case "ls", "list":
		return sandboxList()
	case "start":
		return sandboxStart(args[1:])
	case "stop":
		return sandboxStop(args[1:])
	case "rm", "remove", "delete":
		return sandboxRemove(args[1:])
	case "url", "preview":
		return sandboxURL(args[1:])
	case "snapshot":
		return sandboxSnapshot(args[1:])
	case "snapshots":
		return sandboxSnapshots(args[1:])
	case "usage", "cost":
		return sandboxUsage(args[1:])
	case "ssh", "shell", "exec":
		return sandboxShell(args[1:])
	}
	return fmt.Errorf("unknown sandbox subcommand %q", args[0])
}

// names collects a repeated flag.
type names []string

func (n *names) String() string     { return strings.Join(*n, ",") }
func (n *names) Set(v string) error { *n = append(*n, v); return nil }

// errNoProvider asks for the provider every command but stats needs.
func errNoProvider() error {
	return fmt.Errorf("which provider? give -provider NAME (%s), e.g. conch sandbox -provider daytona ls; conch sandbox stats shows them all",
		strings.Join(sandbox.Providers, ", "))
}

// sandboxCmd is a conch sandbox command line to suggest, with the provider
// it needs.
func sandboxCmd(provider, rest string) string {
	return "conch sandbox -provider " + provider + " " + rest
}

// wrongProvider refuses a sandbox named under a provider that isn't its own.
func wrongProvider(ref, provider string) error {
	return fmt.Errorf("%s is a %s sandbox, not %s: give -provider %s",
		ref, sandbox.ProviderLabel(provider), sandbox.ProviderLabel(sandboxProvider), provider)
}

func openSandboxes() (sandbox.Provider, error) {
	if sandboxProvider == "" {
		return nil, errNoProvider()
	}
	p, err := remote.OpenProvider(sandboxProvider)
	if err != nil {
		return nil, err
	}
	return p, p.Check()
}

func sandboxCreate(args []string) error {
	fs := flag.NewFlagSet("sandbox create", flag.ContinueOnError)
	label := fs.String("label", "", "name shown in the sidebar (default: sandbox-ID)")
	snapshot := fs.String("snapshot", "", "snapshot to start from (default: [sandbox.daytona] snapshot, or Daytona's)")
	cpu := fs.Int("cpu", 0, "vCPUs (default: the snapshot's)")
	memory := fs.Int("memory", 0, "memory in GiB")
	disk := fs.Int("disk", 0, "disk in GiB")
	yes := fs.Bool("yes", false, "delete the sandbox without asking if setting it up fails")
	dir := fs.String("dir", "", "a folder of this computer to mount at /workspace, where the provider can (sandbox-cli on this computer)")
	var env names
	fs.Var(&env, "env", "pass this environment variable in (repeat for more)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: conch sandbox -provider P create [-label L] [-snapshot S] [-cpu N] [-memory GiB] [-disk GiB] [-dir DIR] [-env NAME]... [-yes]")
	}
	if *cpu < 0 || *memory < 0 || *disk < 0 {
		return errors.New("-cpu, -memory and -disk can't be negative")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	pc := cfg.Sandbox.Of(sandboxProvider)
	vars, err := sandbox.EnvFrom(append(append([]string{}, pc.Env...), env...))
	if err != nil {
		return err
	}
	p, err := openSandboxes()
	if err != nil {
		return err
	}
	if b, ok := p.(sandbox.Binder); *dir != "" && (!ok || !b.CanBind()) {
		return fmt.Errorf("-dir: a %s sandbox can't mount a folder of this computer", sandbox.ProviderLabel(sandboxProvider))
	}
	ctx, cancel := context.WithTimeout(context.Background(), sandboxWait)
	defer cancel()

	fmt.Fprintln(os.Stderr, "Creating a "+sandbox.ProviderLabel(sandboxProvider)+" sandbox…")
	s, err := p.Create(ctx, sandbox.Spec{Snapshot: *snapshot, CPU: *cpu, Memory: *memory, Disk: *disk,
		Env: vars, AutoStop: pc.AutoStop, Dir: *dir})
	if err != nil {
		if s.ID != "" {
			abandonSandbox(p, s.ID, *yes)
		}
		return err
	}
	fmt.Fprintf(os.Stderr, "  sandbox %s started\n", s.ID)
	m := remote.Machine{Label: strings.TrimSpace(*label), Target: remote.SandboxTarget(sandboxProvider, s.ID)}
	if m.Label == "" {
		m.Label = remote.DefaultSandboxLabel(s.ID)
	}
	c, err := remote.SetUpSandbox(ctx, m, progress)
	if err != nil {
		abandonSandbox(p, s.ID, *yes)
		return err
	}
	defer c.Close()
	saved, err := remote.SaveMachine(m)
	if err != nil {
		return err
	}
	fmt.Printf("added %s (%s): %s sandbox %s, server pid %d on %s\n", saved.ID, saved.Label, sandboxProvider, s.ID, c.Server.PID, c.Server.Hostname)
	return nil
}

// abandonSandbox offers to delete a sandbox whose setting up failed, since
// it costs for as long as it runs.
func abandonSandbox(p sandbox.Provider, id string, yes bool) {
	if !yes && !confirm(fmt.Sprintf("  Setting up sandbox %s failed. Delete it? [Y/n] ", id), true) {
		fmt.Fprintf(os.Stderr, "  kept it; delete it with: %s\n", sandboxCmd(p.Name(), "rm "+id))
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := p.Delete(ctx, id); err != nil && !errors.Is(err, sandbox.ErrNotFound) {
		fmt.Fprintf(os.Stderr, "  couldn't delete it (%v); try: %s\n", err, sandboxCmd(p.Name(), "rm "+id))
		return
	}
	fmt.Fprintf(os.Stderr, "  deleted sandbox %s\n", id)
}

func sandboxList() error {
	ms, err := remote.Machines()
	if err != nil {
		return err
	}
	p, err := openSandboxes()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	boxes, err := p.List(ctx)
	if err != nil {
		return err
	}
	byID := map[string]sandbox.Sandbox{}
	for _, s := range boxes {
		byID[s.ID] = s
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tLABEL\tSANDBOX\tSTATE\tSIZE")
	gone := 0
	for _, m := range ms {
		provider, id, ok := remote.ParseSandboxTarget(m.Target)
		if !ok || provider != sandboxProvider {
			continue
		}
		s, found := byID[id]
		delete(byID, id)
		state := "gone" // deleted outside conch, or on its way out
		if found {
			state = string(s.State)
		} else {
			gone++
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", m.ID, m.Label, id, state, size(s))
	}
	// No machine here: a create that failed half-way, a machine removed
	// with conch machine rm, or — where a provider lists the whole account
	// — a sandbox somebody made themselves.
	stray := 0
	for _, s := range boxes {
		if _, left := byID[s.ID]; left {
			stray++
			fmt.Fprintf(tw, "-\t-\t%s\t%s\t%s\n", s.ID, s.State, size(s))
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if stray > 0 {
		fmt.Fprintf(os.Stderr, "\n%d with no machine here: made outside conch, or its machine was removed · %s deletes one, and it costs until then\n", stray, sandboxCmd(sandboxProvider, "rm ID"))
	}
	if gone > 0 {
		// Deleted in Daytona's own interface, say: the machine is still
		// here, and nothing else says how to be rid of it.
		fmt.Fprintf(os.Stderr, "\n%d gone: deleted outside conch · %s removes what is left here\n", gone, sandboxCmd(sandboxProvider, "rm ID"))
	}
	return nil
}

// size is what a sandbox is, naming only what the provider reported: boat
// says nothing about disk, and "0 GiB disk" would read like a machine with
// none.
func size(s sandbox.Sandbox) string {
	var parts []string
	for _, part := range []struct {
		n    int
		unit string
	}{{s.CPU, "vCPU"}, {s.Memory, "GiB"}, {s.Disk, "GiB disk"}} {
		if part.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", part.n, part.unit))
		}
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ", ")
}

// resolveSandbox finds a sandbox by its machine's ID, label or target, or
// by the provider's own ID. m is nil when conch has no machine for it.
func resolveSandbox(ref string) (m *remote.Machine, id string, err error) {
	if ref == "" {
		return nil, "", errors.New("which sandbox?")
	}
	ms, err := remote.Machines()
	if err != nil {
		return nil, "", err
	}
	for i := range ms {
		if ms[i].ID != ref && ms[i].Label != ref && ms[i].Target != ref {
			continue
		}
		provider, id, ok := remote.ParseSandboxTarget(ms[i].Target)
		if !ok {
			return nil, "", fmt.Errorf("%s is not a sandbox", ref)
		}
		if provider != sandboxProvider {
			return nil, "", wrongProvider(ref, provider)
		}
		return &ms[i], id, nil
	}
	if provider, id, ok := remote.ParseSandboxTarget(ref); ok {
		if provider != sandboxProvider {
			return nil, "", wrongProvider(ref, provider)
		}
		return nil, id, nil
	}
	if strings.ContainsAny(ref, "@/: ") {
		return nil, "", fmt.Errorf("no sandbox %q", ref)
	}
	return nil, ref, nil
}

func sandboxName(m *remote.Machine, id string) string {
	if m != nil {
		return m.Label
	}
	return id
}

func sandboxStart(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: conch sandbox -provider P start ID")
	}
	m, id, err := resolveSandbox(args[0])
	if err != nil {
		return err
	}
	p, err := openSandboxes()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), sandboxWait)
	defer cancel()
	fmt.Fprintf(os.Stderr, "Starting %s…\n", sandboxName(m, id))
	if _, err := p.Start(ctx, id); err != nil {
		return err
	}
	fmt.Printf("%s started\n", sandboxName(m, id))
	return nil
}

func sandboxStop(args []string) error {
	fs := flag.NewFlagSet("sandbox stop", flag.ContinueOnError)
	yes := fs.Bool("y", false, "don't ask")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: conch sandbox -provider P stop [-y] ID")
	}
	m, id, err := resolveSandbox(fs.Arg(0))
	if err != nil {
		return err
	}
	p, err := openSandboxes()
	if err != nil {
		return err
	}
	name := sandboxName(m, id)
	if !*yes && !confirm(fmt.Sprintf("Stop %s? Its agents and terminals end; its files stay. [y/N] ", name), false) {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), sandboxWait)
	defer cancel()
	if err := p.Stop(ctx, id); err != nil {
		return err
	}
	fmt.Printf("%s stopped\n", name)
	return nil
}

// sandboxURL prints a link to a port inside a sandbox, and can open it.
func sandboxURL(args []string) error {
	fs := flag.NewFlagSet("sandbox url", flag.ContinueOnError)
	expires := fs.Duration("expires", time.Hour, "how long the link lasts (up to 24h)")
	open := fs.Bool("open", false, "open it in the browser as well")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return errors.New("usage: conch sandbox -provider P url [-expires 1h] [-open] ID PORT")
	}
	port, err := strconv.Atoi(fs.Arg(1))
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("%q: give a port between 1 and 65535", fs.Arg(1))
	}
	m, id, err := resolveSandbox(fs.Arg(0))
	if err != nil {
		return err
	}
	p, err := openSandboxes()
	if err != nil {
		return err
	}
	pv, ok := p.(sandbox.Previewer)
	if !ok {
		return fmt.Errorf("%s cannot give a link to a port", sandboxProvider)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	url, err := pv.PreviewURL(ctx, id, port, *expires)
	if err != nil {
		return err
	}
	fmt.Println(url)
	if *open {
		// Anyone with the link can reach that port, so say so once.
		fmt.Fprintf(os.Stderr, "opening %s · the link works for anyone until it expires\n", sandboxName(m, id))
		return openInBrowser(url)
	}
	return nil
}

// snapshotter is the provider's snapshot side, or an error saying it has
// none.
func snapshotter() (sandbox.Snapshotter, error) {
	p, err := openSandboxes()
	if err != nil {
		return nil, err
	}
	sn, ok := p.(sandbox.Snapshotter)
	if !ok {
		return nil, fmt.Errorf("%s cannot keep snapshots", sandboxProvider)
	}
	return sn, nil
}

// sandboxSnapshot keeps a sandbox as it stands, to make others from.
func sandboxSnapshot(args []string) error {
	fs := flag.NewFlagSet("sandbox snapshot", flag.ContinueOnError)
	name := fs.String("name", "", "what to call it (default: the sandbox's label and the date)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: conch sandbox -provider P snapshot [-name N] ID")
	}
	m, id, err := resolveSandbox(fs.Arg(0))
	if err != nil {
		return err
	}
	sn, err := snapshotter()
	if err != nil {
		return err
	}
	label := *name
	if label == "" {
		who := id
		if m != nil {
			who = m.Label
		}
		label = fmt.Sprintf("%s-%s", who, time.Now().Format("2006-01-02-1504"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err := sn.Snapshot(ctx, id, label); err != nil {
		return err
	}
	fmt.Printf("keeping %s as %s\n", sandboxName(m, id), label)
	fmt.Fprintf(os.Stderr, "it takes a few minutes to become usable · %s makes one from it\n", sandboxCmd(sandboxProvider, "create -snapshot "+label))
	return nil
}

// sandboxSnapshots lists what has been kept, and removes one.
func sandboxSnapshots(args []string) error {
	fs := flag.NewFlagSet("sandbox snapshots", flag.ContinueOnError)
	rm := fs.String("rm", "", "forget this one instead of listing")
	yes := fs.Bool("y", false, "don't ask before forgetting")
	if err := fs.Parse(args); err != nil {
		return err
	}
	sn, err := snapshotter()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if *rm != "" {
		if !*yes && !confirm(fmt.Sprintf("Forget snapshot %s? Sandboxes already made from it are not touched. [y/N] ", *rm), false) {
			return nil
		}
		if err := sn.ForgetSnapshot(ctx, *rm); err != nil {
			return err
		}
		fmt.Printf("forgot %s\n", *rm)
		return nil
	}
	list, err := sn.Snapshots(ctx)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Fprintln(os.Stderr, "no snapshots · "+sandboxCmd(sandboxProvider, "snapshot ID")+" keeps one")
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tSTATE\tSIZE\tKEPT")
	for _, s := range list {
		kept := "-"
		if !s.Created.IsZero() {
			kept = s.Created.Local().Format("2006-01-02 15:04")
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", s.Name, firstNonEmptyStr(s.State, "-"), firstNonEmptyStr(s.Size, "-"), kept)
	}
	return tw.Flush()
}

// periodWhat says what the sandbox was doing for a billed period, naming
// only what the provider reported: one that charges for machine time alone
// says nothing about disk.
func periodWhat(p sandbox.UsagePeriod) string {
	var parts []string
	for _, part := range []struct {
		n    int
		unit string
	}{{p.CPU, "vCPU"}, {p.MemGiB, "GiB"}, {p.DiskGiB, "GiB disk"}} {
		if part.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", part.n, part.unit))
		}
	}
	state := "stopped"
	if p.Running() {
		state = "running"
	}
	if len(parts) == 0 {
		return state
	}
	return state + ", " + strings.Join(parts, ", ")
}

// openInBrowser hands a link to the desktop. A variable so tests can check
// what would open without a window appearing on whoever runs them.
var openInBrowser = func(url string) error {
	tool := "xdg-open"
	if runtime.GOOS == "darwin" {
		tool = "open"
	}
	return exec.Command(tool, url).Start()
}

// sandboxUsage prints what a sandbox has cost, and where it went.
func sandboxUsage(args []string) error {
	fs := flag.NewFlagSet("sandbox usage", flag.ContinueOnError)
	since := fs.Duration("since", 30*24*time.Hour, "how far back to ask")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: conch sandbox -provider P usage [-since 720h] ID")
	}
	m, id, err := resolveSandbox(fs.Arg(0))
	if err != nil {
		return err
	}
	p, err := openSandboxes()
	if err != nil {
		return err
	}
	metered, ok := p.(sandbox.Metered)
	if !ok {
		return fmt.Errorf("%s does not say what a sandbox has cost", sandboxProvider)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	now := time.Now()
	u, err := metered.Usage(ctx, id, now.Add(-*since), now)
	if err != nil {
		return err
	}
	if !u.Known || len(u.Periods) == 0 {
		fmt.Fprintf(os.Stderr, "%s has nothing to report for %s yet; its figures settle hours behind\n",
			sandboxProvider, sandboxName(m, id))
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "FROM\tFOR\tCOST\tWHAT")
	for _, pd := range u.Periods {
		fmt.Fprintf(tw, "%s\t%s\t$%.6f\t%s\n", pd.From.Local().Format("2006-01-02 15:04"),
			pd.To.Sub(pd.From).Round(time.Second), pd.Cost, periodWhat(pd))
	}
	fmt.Fprintf(tw, "\t\t$%.6f\ttotal since %s\n", u.Cost, u.From.Local().Format("2006-01-02 15:04"))
	return tw.Flush()
}

func sandboxRemove(args []string) error {
	fs := flag.NewFlagSet("sandbox rm", flag.ContinueOnError)
	yes := fs.Bool("y", false, "don't ask")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: conch sandbox -provider P rm [-y] ID")
	}
	m, id, err := resolveSandbox(fs.Arg(0))
	if err != nil {
		return err
	}
	p, err := openSandboxes()
	if err != nil {
		return err
	}
	name := sandboxName(m, id)
	if !*yes {
		if m != nil {
			for _, line := range unsavedWork(*m) {
				fmt.Fprintf(os.Stderr, "  %s\n", line)
			}
		}
		if !confirm(fmt.Sprintf("Delete %s and everything in it? This can't be undone. [y/N] ", name), false) {
			return nil
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	switch err := p.Delete(ctx, id); {
	case errors.Is(err, sandbox.ErrNotFound):
		fmt.Fprintf(os.Stderr, "  %s was already gone from %s\n", name, sandbox.ProviderLabel(sandboxProvider))
	case err != nil:
		return err
	}
	if m != nil {
		if err := remote.RemoveMachine(m.ID); err != nil {
			return err
		}
	}
	fmt.Printf("deleted %s\n", name)
	return nil
}

// unsavedWork lists what deleting the sandbox would lose: branches with
// commits no remote has, and uncommitted changes. It says so when it can't
// tell, rather than implying there is nothing.
func unsavedWork(m remote.Machine) []string {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tr, err := remote.TransportFor(ctx, m.Label, m.Target, false)
	var stopped *remote.SandboxStoppedError
	switch {
	case errors.As(err, &stopped):
		return []string{fmt.Sprintf("%s is %s, so conch can't check it for work that isn't pushed", m.Label, stopped.State)}
	case err != nil:
		return []string{"couldn't check for work that isn't pushed: " + err.Error()}
	}
	c, err := remote.Connect(ctx, tr, remote.Options{})
	var outdated *remote.OutdatedServerError
	if err != nil && !errors.As(err, &outdated) {
		return []string{"couldn't check for work that isn't pushed: " + err.Error()}
	}
	defer c.Close()
	var list proto.ProjectList
	if err := c.Call(ctx, proto.MethodProjectList, nil, &list); err != nil {
		return []string{"couldn't check for work that isn't pushed: " + err.Error()}
	}
	return remote.UnsavedWork(list.Projects)
}

// exitStatus is a command run elsewhere that exited non-zero: its own
// output has said why, so main exits with its status and adds nothing.
type exitStatus int

func (e exitStatus) Error() string { return fmt.Sprintf("exit status %d", int(e)) }

// sandboxShellCommand builds the ssh for conch sandbox ssh; tests replace
// the ssh binary through $CONCH_SSH, not this.
var sandboxShellCommand = remote.SandboxShell

// sandboxShell opens a shell in a sandbox, or runs a command there, over
// the same fresh access conch itself connects with.
func sandboxShell(args []string) error {
	fs := flag.NewFlagSet("sandbox ssh", flag.ContinueOnError)
	tty := fs.Bool("t", false, "give the command a terminal, for one that draws a screen")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return errors.New("usage: conch sandbox -provider P ssh [-t] ID [COMMAND...]")
	}
	m, id, err := resolveSandbox(fs.Arg(0))
	if err != nil {
		return err
	}
	label, target := sandboxName(m, id), remote.SandboxTarget(sandboxProvider, id)
	if m != nil {
		target = m.Target
	}
	command := strings.Join(fs.Args()[1:], " ")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd, err := sandboxShellCommand(ctx, label, target, command, *tty)
	var stopped *remote.SandboxStoppedError
	if errors.As(err, &stopped) {
		return fmt.Errorf("%w; run: %s", err, sandboxCmd(sandboxProvider, "start "+fs.Arg(0)))
	}
	if err != nil {
		return err
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err = cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() > 0 {
		return exitStatus(exit.ExitCode())
	}
	return err
}

// providerStats is what one provider said for conch sandbox stats.
type providerStats struct {
	name  string
	boxes []sandbox.Sandbox
	err   error // nil, or why it couldn't be asked
}

// sandboxStats lists every provider's sandboxes together, with a line per
// provider, so nothing running and costing hides behind a provider nobody
// thought to ask. -provider narrows it to one.
func sandboxStats(args []string) error {
	if len(args) != 0 {
		return errors.New("usage: conch sandbox stats [-provider P]")
	}
	ms, err := remote.Machines()
	if err != nil {
		return err
	}
	names := sandbox.Providers
	if sandboxProvider != "" {
		names = []string{sandboxProvider}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	// Ask them all at once: one that hangs costs its own wait, not theirs.
	stats := make([]providerStats, len(names))
	var wg sync.WaitGroup
	for i, name := range names {
		wg.Add(1)
		go func() {
			defer wg.Done()
			stats[i] = providerStats{name: name}
			p, err := remote.OpenProvider(name)
			if err == nil {
				err = p.Check()
			}
			if err == nil {
				stats[i].boxes, err = p.List(ctx)
			}
			stats[i].err = err
		}()
	}
	wg.Wait()

	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "PROVIDER\tID\tLABEL\tSANDBOX\tSTATE\tSIZE")
	var summary []string
	failed := 0
	for _, st := range stats {
		label := sandbox.ProviderLabel(st.name)
		byID := map[string]sandbox.Sandbox{}
		for _, s := range st.boxes {
			byID[s.ID] = s
		}
		states := map[string]int{}
		var order []string
		count := func(state string) {
			if states[state] == 0 {
				order = append(order, state)
			}
			states[state]++
		}
		cpu, mem := 0, 0
		machines := 0
		for _, m := range ms {
			provider, id, ok := remote.ParseSandboxTarget(m.Target)
			if !ok || provider != st.name {
				continue
			}
			machines++
			s, found := byID[id]
			delete(byID, id)
			state := "gone"
			switch {
			case st.err != nil:
				state = "?" // the provider couldn't be asked
			case found:
				state = string(s.State)
			}
			if st.err == nil {
				count(state)
			}
			if s.State == sandbox.StateStarted {
				cpu, mem = cpu+s.CPU, mem+s.Memory
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", label, m.ID, m.Label, id, state, size(s))
		}
		stray := 0
		for _, s := range st.boxes {
			if _, left := byID[s.ID]; !left {
				continue
			}
			stray++
			count(string(s.State))
			if s.State == sandbox.StateStarted {
				cpu, mem = cpu+s.CPU, mem+s.Memory
			}
			fmt.Fprintf(tw, "%s\t-\t-\t%s\t%s\t%s\n", label, s.ID, s.State, size(s))
		}
		switch {
		case errors.Is(st.err, sandbox.ErrNotConfigured) && machines == 0:
			summary = append(summary, fmt.Sprintf("%s: not set up (%v)", label, st.err))
		case st.err != nil:
			failed++
			summary = append(summary, fmt.Sprintf("%s: couldn't ask it: %v", label, st.err))
		default:
			line := fmt.Sprintf("%s: %s", label, counted(machines+stray, "sandbox"))
			var parts []string
			for _, state := range order {
				parts = append(parts, fmt.Sprintf("%d %s", states[state], state))
			}
			if len(parts) > 0 {
				line += " · " + strings.Join(parts, ", ")
			}
			if cpu > 0 || mem > 0 {
				line += fmt.Sprintf(" · %d vCPU, %d GiB running", cpu, mem)
			}
			if stray > 0 {
				line += fmt.Sprintf(" · %d with no machine here", stray)
			}
			summary = append(summary, line)
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	fmt.Println()
	for _, line := range summary {
		fmt.Println(line)
	}
	if failed > 0 {
		return fmt.Errorf("%s of %d couldn't be asked", counted(failed, "provider"), len(stats))
	}
	return nil
}

// counted is n and what, made plural when n isn't one.
func counted(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	if strings.HasSuffix(what, "x") {
		return fmt.Sprintf("%d %ses", n, what)
	}
	return fmt.Sprintf("%d %ss", n, what)
}
