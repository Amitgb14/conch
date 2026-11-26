package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/charmbracelet/x/term"

	"github.com/Amitgb14/conch/internal/acp"
	"github.com/Amitgb14/conch/internal/adapter"
)

// `conch acp` is what runs in the pane of an agent that speaks the Agent
// Client Protocol (internal/acp): the protocol's client, drawing the
// conversation and taking typed prompts. The server starts it from a
// manifest's [acp] table; it needs no server itself, which is why a
// person can run one in any terminal to try an agent.

const acpUsage = "usage: conch acp [-agent NAME] [-label LABEL] [-prompt TEXT] [-load SESSION] -- COMMAND [ARGS...]"

// acpArgs splits conch acp's own flags from the agent's command at "--".
func acpArgs(args []string) (flags, command []string, err error) {
	for i, a := range args {
		if a == "--" {
			return args[:i], args[i+1:], nil
		}
	}
	return nil, nil, errors.New(acpUsage)
}

type acpFlags struct {
	agent, label, prompt, load string
	command                    []string
	// env is NAME=value words the command began with, as a shell would
	// take them typed.
	env []string
}

func parseACP(args []string) (acpFlags, error) {
	own, command, err := acpArgs(args)
	if err != nil {
		return acpFlags{}, err
	}
	var f acpFlags
	fs := flag.NewFlagSet("acp", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&f.agent, "agent", "acp", "the agent's name in conch")
	fs.StringVar(&f.label, "label", "", "the name people know it by")
	fs.StringVar(&f.prompt, "prompt", "", "a first message")
	fs.StringVar(&f.load, "load", "", "a saved session to reopen")
	if err := fs.Parse(own); err != nil {
		return acpFlags{}, fmt.Errorf("%v\n%s", err, acpUsage)
	}
	if fs.NArg() > 0 {
		return acpFlags{}, fmt.Errorf("conch acp takes no %q before --\n%s", fs.Arg(0), acpUsage)
	}
	for len(command) > 0 && assignment(command[0]) {
		f.env = append(f.env, command[0])
		command = command[1:]
	}
	if len(command) == 0 {
		return acpFlags{}, errors.New("no command after --: " + acpUsage)
	}
	if f.load != "" && !adapter.SessionIDOK(f.load) {
		return acpFlags{}, fmt.Errorf("%q is not a session id", f.load)
	}
	f.command = command
	return f, nil
}

// assignment is a NAME=value word.
func assignment(w string) bool {
	name, _, ok := strings.Cut(w, "=")
	if !ok || name == "" {
		return false
	}
	for i, r := range name {
		if !(r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || i > 0 && r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

func runACP(args []string) error {
	f, err := parseACP(args)
	if err != nil {
		return err
	}
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	in, out := os.Stdin, os.Stdout
	width := func() int { return 80 }
	if term.IsTerminal(in.Fd()) {
		state, err := term.MakeRaw(in.Fd())
		if err != nil {
			return err
		}
		defer term.Restore(in.Fd(), state) //nolint:errcheck
	}
	if term.IsTerminal(out.Fd()) {
		width = func() int {
			if w, _, err := term.GetSize(out.Fd()); err == nil && w > 0 {
				return w
			}
			return 80
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	err = acp.Run(ctx, acp.RunOptions{Agent: f.agent, Label: f.label, Argv: f.command,
		Env: append(os.Environ(), f.env...), Dir: dir, Prompt: f.prompt, Load: f.load,
		In: in, Out: out, Width: width})
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}
