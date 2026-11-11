package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/Amitgb14/conch/internal/cloudagent"
	"github.com/Amitgb14/conch/internal/proto"
)

// conch cloud: Conch Cloud's machines from this device (machine, bridge,
// exec), and what runs inside one (agent, install, git-credential), which
// its host starts.
func runCloud(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: conch cloud machine ls|new|rm|wake|sleep|forget-key … | conch cloud agent [flags] | conch cloud install [-user NAME] | conch cloud git-credential get")
	}
	switch args[0] {
	case "machine", "machines":
		return runCloudMachine(args[1:])
	case "bridge":
		return runCloudBridge(args[1:])
	case "exec":
		return runCloudExec(args[1:])
	case "agent":
		return runCloudAgent(args[1:])
	case "install":
		return runCloudInstall(args[1:])
	case "git-credential":
		return cloudagent.GitCredential(args[1:], os.Stdin, os.Stdout)
	}
	return fmt.Errorf("unknown cloud subcommand %q (machine, bridge, exec, agent, install, git-credential)", args[0])
}

// runCloudInstall readies a machine image: the agent's systemd unit,
// enabled, and its state directory (scripts/cloud-image.sh runs it).
func runCloudInstall(args []string) error {
	fs := flag.NewFlagSet("cloud install", flag.ContinueOnError)
	userName := fs.String("user", "conch", "the machine's user, which must exist")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if os.Geteuid() != 0 {
		return errors.New("conch cloud install writes the system's units: run it as root")
	}
	path, err := cloudagent.Install(context.Background(), cloudagent.InstallOptions{User: *userName})
	if err != nil {
		return err
	}
	fmt.Printf("installed %s; the machine's host starts it after writing %s\n", path, cloudagent.DefaultBootPath)
	return nil
}

// cloudAgentExit is the agent's exit, a variable so a test can see it.
var cloudAgentExit = os.Exit

func runCloudAgent(args []string) error {
	fs := flag.NewFlagSet("cloud agent", flag.ContinueOnError)
	boot := fs.String("boot", cloudagent.DefaultBootPath, "boot.json, written by the machine's host")
	state := fs.String("state", cloudagent.DefaultStateDir, "where the machine's Noise key and state are kept")
	key := fs.String("key", "", "the Noise static key (default STATE/noise.key)")
	userName := fs.String("user", "", "who the conch server, its panes and scripts run as (default conch when run as root)")
	serverSock := fs.String("server-socket", "", "the conch server's socket (default that user's)")
	agentSock := fs.String("socket", "", "this agent's own socket (default /run/conch/cloud-agent.sock as root)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("conch cloud agent takes no arguments, only flags (got %q)", fs.Arg(0))
	}
	opts := cloudagent.Options{BootPath: *boot, StateDir: *state, KeyPath: *key, ServerSocket: *serverSock, AgentSocket: *agentSock}
	name := *userName
	if name == "" && os.Geteuid() == 0 {
		name = "conch"
	}
	if name != "" {
		u, err := cloudagent.LookupUser(name)
		if err != nil {
			return fmt.Errorf("the machine's user %s: %w (pass -user)", name, err)
		}
		opts.User = u
	}
	flags, prefix := log.Flags(), log.Prefix()
	defer func() { log.SetFlags(flags); log.SetPrefix(prefix) }()
	log.SetFlags(log.LstdFlags | log.LUTC)
	log.SetPrefix("conch-cloud-agent ")
	a, err := cloudagent.New(opts)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log.Printf("conch %s cloud agent (contract %s) starting", proto.Version, cloudagent.Contract)
	err = a.Run(ctx)
	if err == nil {
		log.Printf("stopped")
		return nil
	}
	switch code := cloudagent.ExitCode(err); code {
	case 3:
		log.Printf("the control plane refused this machine's token: %v. Each inject gives the machine a new token in %s and restarts this agent; this one is from an earlier inject, so it stops.", err, *boot)
		cloudAgentExit(code)
	case 4:
		log.Printf("stopping: %v", err)
		cloudAgentExit(code)
	}
	return err
}
