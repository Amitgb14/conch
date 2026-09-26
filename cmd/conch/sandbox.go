package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
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
	vars, err := sandbox.EnvFrom(append(append([]string{}, cfg.Sandbox.Daytona.Env...), env...))
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
		Env: vars, AutoStop: cfg.Sandbox.Daytona.AutoStop})
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
	for _, m := range ms {
		provider, id, ok := remote.ParseSandboxTarget(m.Target)
		if !ok || provider != sandboxProvider {
			continue
		}
		s, found := byID[id]
		delete(byID, id)
		state := "gone" // deleted outside conch
		if found {
			state = string(s.State)
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
	return tw.Flush()
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
