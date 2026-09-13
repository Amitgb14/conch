package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/amitghadge/conch/internal/buildinfo"
	"github.com/amitghadge/conch/internal/client"
	"github.com/amitghadge/conch/internal/config"
	"github.com/amitghadge/conch/internal/proto"
	"github.com/amitghadge/conch/internal/remote"
)

// machineFlag is the -m/--machine value: commands then talk to that
// machine's server instead of the local one.
var machineFlag = os.Getenv("CONCH_MACHINE")

func runVersion(args []string) {
	if len(args) > 0 && args[0] == "--json" {
		_ = json.NewEncoder(os.Stdout).Encode(buildinfo.Current())
		return
	}
	fmt.Printf("conch %s (build %s, %s)\n", proto.Version, buildinfo.Build(), buildinfo.Platform())
}

// runBridge connects stdio to this machine's server, starting it if needed.
// It is what a remote client runs over ssh, so stdout carries only protocol.
func runBridge() error {
	sock := config.SocketPath()
	if err := client.EnsureServer(sock, config.ServerLogPath()); err != nil {
		return err
	}
	nc, err := net.Dial("unix", sock)
	if err != nil {
		return err
	}
	defer nc.Close()
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(nc, os.Stdin); done <- struct{}{} }()
	go func() { _, _ = io.Copy(os.Stdout, nc); done <- struct{}{} }()
	<-done // either side ending ends the bridge
	return nil
}

func runMachine(args []string) error {
	if len(args) == 0 {
		args = []string{"ls"}
	}
	switch args[0] {
	case "add":
		return machineAdd(args[1:])
	case "ls", "list":
		return machineList()
	case "rm", "remove":
		if len(args) != 2 {
			return errors.New("usage: conch machine rm ID")
		}
		return remote.RemoveMachine(args[1])
	case "upgrade":
		if len(args) != 2 {
			return errors.New("usage: conch machine upgrade ID")
		}
		return machineUpgrade(remote.FindMachine(args[1]))
	case "hosts":
		for _, h := range remote.SSHHosts() {
			fmt.Println(h)
		}
		return nil
	}
	return fmt.Errorf("unknown machine subcommand %q", args[0])
}

func machineAdd(args []string) error {
	fs := flag.NewFlagSet("machine add", flag.ContinueOnError)
	label := fs.String("label", "", "name shown in the sidebar (default: the host)")
	yes := fs.Bool("yes", false, "install conch on the machine without asking")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: conch machine add [-label NAME] [-yes] SSH_TARGET")
	}
	target := fs.Arg(0)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	fmt.Fprintf(os.Stderr, "Checking %s…\n", target)
	probe, err := remote.ProbeMachine(ctx, target, true) // ssh may ask about host keys or passwords
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "  platform %s\n", probe.Platform)
	if missing := probe.Missing(); probe.Bin == "" || len(missing) > 0 {
		what := "conch is not installed there"
		if probe.Bin != "" {
			what = "conch there is from an older build"
		}
		if !*yes && !confirm(fmt.Sprintf("  %s. Install it to ~/.local/bin/conch? [Y/n] ", what), true) {
			return errors.New("not installed")
		}
		path, err := remote.Install(ctx, target, probe.Platform, true, progress)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "  installed %s\n", path)
	} else {
		fmt.Fprintf(os.Stderr, "  found %s (build %s)\n", probe.Bin, probe.Info.Build)
	}

	c, err := remote.Connect(ctx, target, remote.Options{})
	var outdated *remote.OutdatedServerError
	if errors.As(err, &outdated) {
		fmt.Fprintf(os.Stderr, "  %v\n", err)
		if confirm("  Restart the server there now? Its panes stop. [y/N] ", false) {
			if err := stopServer(c); err != nil {
				return err
			}
			c, err = remote.Connect(ctx, target, remote.Options{})
		} else {
			err = nil
		}
	}
	if err != nil {
		return err
	}
	defer c.Close()
	m, err := remote.SaveMachine(remote.Machine{Label: *label, Target: target})
	if err != nil {
		return err
	}
	fmt.Printf("added %s (%s): %s, server pid %d on %s\n", m.ID, m.Label, c.Server.Platform, c.Server.PID, c.Server.Hostname)
	return nil
}

func machineList() error {
	ms, err := remote.Machines()
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tLABEL\tTARGET\tENABLED")
	fmt.Fprintf(tw, "%s\t%s\t%s\t%v\n", remote.LocalID, remote.LocalID, "-", true)
	for _, m := range ms {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%v\n", m.ID, m.Label, m.Target, m.Enabled)
	}
	return tw.Flush()
}

func machineUpgrade(m remote.Machine) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	probe, err := remote.ProbeMachine(ctx, m.Target, true)
	if err != nil {
		return err
	}
	path, err := remote.Install(ctx, m.Target, probe.Platform, true, progress)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "installed %s\n", path)
	c, err := remote.Connect(ctx, m.Target, remote.Options{})
	var outdated *remote.OutdatedServerError
	switch {
	case errors.As(err, &outdated):
		if !confirm(fmt.Sprintf("The server on %s is still the old build. Restart it now? Its panes stop. [y/N] ", m.Label), false) {
			c.Close()
			return nil
		}
		if err := stopServer(c); err != nil {
			return err
		}
		if c, err = remote.Connect(ctx, m.Target, remote.Options{}); err != nil {
			return err
		}
	case err != nil:
		return err
	}
	defer c.Close()
	fmt.Printf("%s: server pid %d, build %s\n", m.Label, c.Server.PID, c.Server.Build)
	return nil
}

// connectMachine reaches the -m machine's server. It never installs:
// that is `conch machine add` / `upgrade`, which ask first.
func connectMachine(ref string) (*client.Client, error) {
	m := remote.FindMachine(ref)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	c, err := remote.Connect(ctx, m.Target, remote.Options{})
	var needs *remote.InstallError
	var outdated *remote.OutdatedServerError
	switch {
	case errors.As(err, &needs):
		return nil, fmt.Errorf("%v; run: conch machine add %s", err, m.Target)
	case errors.As(err, &outdated):
		fmt.Fprintf(os.Stderr, "conch: warning: %s: %v\n", m.Label, err)
		return c, nil
	}
	return c, err
}

func progress(msg string) { fmt.Fprintf(os.Stderr, "  %s…\n", msg) }

func confirm(prompt string, def bool) bool {
	fmt.Fprint(os.Stderr, prompt)
	answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return true
	case "n", "no":
		return false
	}
	return def
}
