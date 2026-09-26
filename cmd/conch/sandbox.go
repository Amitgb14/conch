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
	"text/tabwriter"
	"time"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/remote"
	"github.com/Amitgb14/conch/internal/sandbox"
)

// sandboxProvider is the provider sandboxes are made with; Daytona is the
// only one so far.
const sandboxProvider = "daytona"

// sandboxWait bounds creating or starting a sandbox and setting conch up
// in it; tests shorten it.
var sandboxWait = 10 * time.Minute

func runSandbox(args []string) error {
	if len(args) == 0 {
		args = []string{"ls"}
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
	}
	return fmt.Errorf("unknown sandbox subcommand %q", args[0])
}

// names collects a repeated flag.
type names []string

func (n *names) String() string     { return strings.Join(*n, ",") }
func (n *names) Set(v string) error { *n = append(*n, v); return nil }

func openSandboxes() (sandbox.Provider, error) {
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
	var env names
	fs.Var(&env, "env", "pass this environment variable in (repeat for more)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: conch sandbox create [-label L] [-snapshot S] [-cpu N] [-memory GiB] [-disk GiB] [-env NAME]... [-yes]")
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
	ctx, cancel := context.WithTimeout(context.Background(), sandboxWait)
	defer cancel()

	fmt.Fprintln(os.Stderr, "Creating a Daytona sandbox…")
	s, err := p.Create(ctx, sandbox.Spec{Snapshot: *snapshot, CPU: *cpu, Memory: *memory, Disk: *disk,
		Env: vars, AutoStop: pc.AutoStop})
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
	fmt.Printf("added %s (%s): daytona sandbox %s, server pid %d on %s\n", saved.ID, saved.Label, s.ID, c.Server.PID, c.Server.Hostname)
	return nil
}

// abandonSandbox offers to delete a sandbox whose setting up failed, since
// it costs for as long as it runs.
func abandonSandbox(p sandbox.Provider, id string, yes bool) {
	if !yes && !confirm(fmt.Sprintf("  Setting up sandbox %s failed. Delete it? [Y/n] ", id), true) {
		fmt.Fprintf(os.Stderr, "  kept it; delete it with: conch sandbox rm %s\n", id)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := p.Delete(ctx, id); err != nil && !errors.Is(err, sandbox.ErrNotFound) {
		fmt.Fprintf(os.Stderr, "  couldn't delete it (%v); try: conch sandbox rm %s\n", err, id)
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
	// Made by conch but no longer in the catalog: a create that failed
	// half-way, or a machine removed with conch machine rm.
	for _, s := range boxes {
		if _, left := byID[s.ID]; left {
			fmt.Fprintf(tw, "-\t-\t%s\t%s\t%s\n", s.ID, s.State, size(s))
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if gone > 0 {
		// Deleted in Daytona's own interface, say: the machine is still
		// here, and nothing else says how to be rid of it.
		fmt.Fprintf(os.Stderr, "\n%d gone: deleted outside conch · conch sandbox rm ID removes what is left here\n", gone)
	}
	return nil
}

func size(s sandbox.Sandbox) string {
	if s.CPU == 0 && s.Memory == 0 && s.Disk == 0 {
		return "-"
	}
	return fmt.Sprintf("%d vCPU, %d GiB, %d GiB disk", s.CPU, s.Memory, s.Disk)
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
		if !ok || provider != sandboxProvider {
			return nil, "", fmt.Errorf("%s is not a sandbox", ref)
		}
		return &ms[i], id, nil
	}
	if provider, id, ok := remote.ParseSandboxTarget(ref); ok && provider == sandboxProvider {
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
		return errors.New("usage: conch sandbox start ID")
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
		return errors.New("usage: conch sandbox stop [-y] ID")
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
		return errors.New("usage: conch sandbox url [-expires 1h] [-open] ID PORT")
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
		tool := "xdg-open"
		if runtime.GOOS == "darwin" {
			tool = "open"
		}
		return exec.Command(tool, url).Start()
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
		return errors.New("usage: conch sandbox snapshot [-name N] ID")
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
	fmt.Fprintf(os.Stderr, "it takes a few minutes to become usable · conch sandbox create -snapshot %s makes one from it\n", label)
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
		fmt.Fprintln(os.Stderr, "no snapshots · conch sandbox snapshot ID keeps one")
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

func sandboxRemove(args []string) error {
	fs := flag.NewFlagSet("sandbox rm", flag.ContinueOnError)
	yes := fs.Bool("y", false, "don't ask")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: conch sandbox rm [-y] ID")
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
		fmt.Fprintf(os.Stderr, "  %s was already gone from Daytona\n", name)
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
