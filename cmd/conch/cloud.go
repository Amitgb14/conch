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

// conch cloud: what runs inside a Conch Cloud machine. The commands for
// using Conch Cloud from a laptop (login, machines) come later; `agent` is
// the machine's side, started by its host (systemd's conch-cloud-agent
// unit, or the inject script without systemd).
func runCloud(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: conch cloud agent [flags] | conch cloud git-credential get")
	}
	switch args[0] {
	case "agent":
		return runCloudAgent(args[1:])
	case "git-credential":
		return cloudagent.GitCredential(args[1:], os.Stdin, os.Stdout)
	}
	return fmt.Errorf("unknown cloud subcommand %q (agent, git-credential)", args[0])
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
