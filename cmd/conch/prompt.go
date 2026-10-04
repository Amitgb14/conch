package main

import (
	"errors"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
)

// agentBlocked is a prompt refused because the agent waits on a question,
// so main exits 3 and a script (or an agent) can tell it from a failure.
type agentBlocked struct{ err error }

func (e agentBlocked) Error() string { return e.err.Error() }

const agentPromptUsage = "usage: conch agent prompt [-wait] [-until done,idle,waiting] [-timeout 30m] ID TEXT..."

// agentPrompt submits a message to one agent and, with -wait, waits for the
// work it starts to end: what an agent uses to drive another, where
// `conch send` would type onto a question as readily as into a prompt.
//
//	conch agent prompt -wait p4 "review the diff on this branch"
func agentPrompt(args []string) error {
	fs := flag.NewFlagSet("agent prompt", flag.ContinueOnError)
	wait := fs.Bool("wait", false, "wait until the agent has finished the work the message starts")
	until := fs.String("until", "done,idle,waiting", "with -wait, the states that end it")
	timeout := fs.Duration("timeout", 0, "with -wait, give up after this long (0: wait forever)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 2 {
		return errors.New(agentPromptUsage)
	}
	want, err := wantedStates(*until)
	if err != nil {
		return err
	}
	text := strings.Join(fs.Args()[1:], " ")

	c, err := connect(false)
	if err != nil {
		return errors.New("server is not running")
	}
	defer c.Close()
	if miss := c.MissingCapabilities([]string{proto.CapAgentPrompt}); len(miss) > 0 {
		return errors.New("the server there predates `conch agent prompt`; update it, or use `conch send`")
	}
	id, err := resolvePane(c, fs.Arg(0))
	if err != nil {
		return err
	}
	// Connected before the message goes in, so the updates that answer it
	// can only be queued for the loop below, never missed.
	var res proto.AgentPromptResult
	if err := call(c, proto.MethodAgentPrompt, proto.AgentPromptParams{ID: id, Text: text}, &res); err != nil {
		var perr *proto.Error
		if errors.As(err, &perr) && perr.Code == proto.ErrAgentBlocked {
			return agentBlocked{fmt.Errorf("%s; read it with `conch read %s`", perr.Message, id)}
		}
		return err
	}
	if !*wait {
		fmt.Printf("%s %s sent\n", res.ID, res.Agent)
		return nil
	}
	return waitTurn(c, res, want, *until, *timeout)
}

// waitTurn waits for the work that answers a prompt and says how it ended.
func waitTurn(c *client.Client, res proto.AgentPromptResult, want map[string]bool, until string, timeout time.Duration) error {
	info, state, err := awaitTurn(c, res, want, until, timeout)
	if err != nil {
		return err
	}
	return printWaited(info, state)
}
