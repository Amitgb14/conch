package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Amitgb14/conch/internal/cloud"
	"github.com/Amitgb14/conch/internal/remote"
)

// conch cloud machine: the account's cloud machines.

// cloudMachineJSON is a machine as `-json` says it: the contract's
// Machine, and where the web UI shows it.
type cloudMachineJSON struct {
	cloud.Machine
	WebURL string `json:"web_url"`
}

func runCloudMachine(args []string) error {
	if len(args) == 0 {
		args = []string{"ls"}
	}
	c, err := cloud.Open()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	switch args[0] {
	case "ls", "list":
		return cloudMachineList(ctx, c, args[1:])
	case "new", "create":
		return cloudMachineNew(ctx, c, args[1:])
	case "rm", "delete":
		return cloudMachineRemove(ctx, c, args[1:])
	case "wake", "start":
		return cloudMachineWakeSleep(ctx, c, args[1:], true)
	case "sleep", "stop":
		return cloudMachineWakeSleep(ctx, c, args[1:], false)
	case "forget-key":
		if len(args) != 2 {
			return errors.New("usage: conch cloud machine forget-key NAME")
		}
		m, err := findCloudMachine(ctx, c, args[1])
		if err != nil {
			return err
		}
		if err := cloud.ForgetKey(m.ID); err != nil {
			return err
		}
		fmt.Fprintf(cloudOut, "Forgot %s's key; the next connection pins the one Conch Cloud gives.\n", m.Name)
		return nil
	}
	return fmt.Errorf("unknown cloud machine subcommand %q (ls, new, rm, wake, sleep, forget-key)", args[0])
}

// webURL is where the web UI shows machine id: the API serves it.
func webURL(api, id string) string { return strings.TrimRight(api, "/") + "/machines/" + id }

func cloudMachineList(ctx context.Context, c *cloud.Client, args []string) error {
	fs := flag.NewFlagSet("cloud machine ls", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print them as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ms, err := c.Machines(ctx)
	if err != nil {
		return err
	}
	_ = remote.SyncCloudMachines(ms) // conch's machines follow the account's
	if *asJSON {
		out := make([]cloudMachineJSON, 0, len(ms))
		for _, m := range ms {
			out = append(out, cloudMachineJSON{Machine: m, WebURL: webURL(c.API, m.ID)})
		}
		return writeJSONOut(map[string]any{"machines": out})
	}
	if len(ms) == 0 {
		fmt.Fprintln(cloudOut, "No cloud machines yet. conch cloud machine new NAME makes one.")
		return nil
	}
	tw := tabwriter.NewWriter(cloudOut, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tSTATE\tSIZE\tREGION\tPROVIDER\tID")
	for _, m := range ms {
		state := m.State
		if m.StateDetail != "" && m.State == cloud.StateError {
			state += ": " + m.StateDetail
		}
		prov := "-"
		if m.Provider != nil {
			prov = m.Provider.Name
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", m.Name, state, m.Size, m.Region, prov, m.ID)
	}
	return tw.Flush()
}

// findCloudMachine is the account's machine by id or name.
func findCloudMachine(ctx context.Context, c *cloud.Client, ref string) (cloud.Machine, error) {
	if cloud.IsMachineID(ref) {
		return c.Machine(ctx, ref)
	}
	ms, err := c.Machines(ctx)
	if err != nil {
		return cloud.Machine{}, err
	}
	for _, m := range ms {
		if m.Name == ref {
			return m, nil
		}
	}
	return cloud.Machine{}, fmt.Errorf("no cloud machine %q (conch cloud machine ls lists them)", ref)
}

func cloudMachineNew(ctx context.Context, c *cloud.Client, args []string) error {
	fs := flag.NewFlagSet("cloud machine new", flag.ContinueOnError)
	size := fs.String("size", "standard", "small, standard, large or xl")
	region := fs.String("region", "", "where (default: the account's default)")
	provider := fs.String("provider", "", "a provider id (default: the account's default)")
	wait := fs.Bool("wait", false, "wait until it is running")
	if err := fs.Parse(reorderFlags(args)); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: conch cloud machine new NAME [-size S] [-region R] [-provider ID] [-wait]")
	}
	b := make([]byte, 12)
	rand.Read(b)
	m, err := c.CreateMachine(ctx, cloud.MachineCreate{Name: fs.Arg(0), Size: *size, Region: *region, Provider: *provider}, hex.EncodeToString(b))
	if errors.Is(err, cloud.ErrNoProvider) {
		return fmt.Errorf("%w — the account has nowhere to run machines yet: add a provider at %s/providers", err, strings.TrimRight(c.API, "/"))
	}
	if err != nil {
		return err
	}
	syncCatalog(ctx, c)
	fmt.Fprintf(cloudOut, "Creating %s (%s, %s).\n", m.Name, m.ID, m.Size)
	if *wait {
		for m.State != cloud.StateRunning {
			if m.State == cloud.StateError {
				return fmt.Errorf("%s failed: %s", m.Name, m.StateDetail)
			}
			time.Sleep(2 * time.Second)
			if m, err = c.Machine(ctx, m.ID); err != nil {
				return err
			}
		}
		fmt.Fprintf(cloudOut, "%s is running.\n", m.Name)
	}
	return nil
}

func cloudMachineRemove(ctx context.Context, c *cloud.Client, args []string) error {
	fs := flag.NewFlagSet("cloud machine rm", flag.ContinueOnError)
	yes := fs.Bool("y", false, "don't ask")
	if err := fs.Parse(reorderFlags(args)); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: conch cloud machine rm NAME [-y]")
	}
	m, err := findCloudMachine(ctx, c, fs.Arg(0))
	if err != nil {
		return err
	}
	if !*yes && !confirm(fmt.Sprintf("Destroy %s and its disk? Work there that is not pushed is lost. [y/N] ", m.Name), false) {
		return nil
	}
	if err := c.DeleteMachine(ctx, m.ID); err != nil {
		return err
	}
	_ = cloud.ForgetKey(m.ID)
	syncCatalog(ctx, c)
	fmt.Fprintf(cloudOut, "Destroying %s.\n", m.Name)
	return nil
}

func cloudMachineWakeSleep(ctx context.Context, c *cloud.Client, args []string, wake bool) error {
	fs := flag.NewFlagSet("cloud machine wake/sleep", flag.ContinueOnError)
	force := fs.Bool("force", false, "sleep even while agents work")
	if err := fs.Parse(reorderFlags(args)); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: conch cloud machine wake|sleep NAME [-force]")
	}
	m, err := findCloudMachine(ctx, c, fs.Arg(0))
	if err != nil {
		return err
	}
	if wake {
		m, err = c.Wake(ctx, m.ID)
	} else {
		m, err = c.Sleep(ctx, m.ID, *force)
		if errors.Is(err, cloud.ErrAgentsWorking) {
			return fmt.Errorf("%w (with -force)", err)
		}
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(cloudOut, "%s is %s.\n", m.Name, m.State)
	return nil
}

// reorderFlags puts flags before the words, so `new box -size large`
// parses as `new -size large box` does.
func reorderFlags(args []string) []string {
	var flags, words []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			words = append(words, args[i+1:]...)
			break
		}
		if strings.HasPrefix(a, "-") && len(a) > 1 {
			flags = append(flags, a)
			if !strings.Contains(a, "=") && i+1 < len(args) && !boolFlag(a) {
				i++
				flags = append(flags, args[i])
			}
			continue
		}
		words = append(words, a)
	}
	return append(flags, words...)
}

func boolFlag(a string) bool {
	switch strings.TrimLeft(a, "-") {
	case "y", "force", "wait", "json":
		return true
	}
	return false
}
