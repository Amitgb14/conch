package main

import (
	"errors"
	"flag"
	"fmt"
	"strings"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
)

// agentSwitchUsage is conch agent switch's usage line.
const agentSwitchUsage = "usage: conch agent switch [-now] [-preview] [-handoff] [-no-continue] ID AGENT | -cancel ID | -undo RECORD"

// agentSwitch is conch agent switch ID AGENT: the pane's conversation
// switched to another agent, which resumes it in the same pane.
func agentSwitch(args []string) error {
	fs := flag.NewFlagSet("agent switch", flag.ContinueOnError)
	now := fs.Bool("now", false, "switch at once rather than wait for background commands, which stop with it (still never mid-turn)")
	preview := fs.Bool("preview", false, "say what would cross and how, changing nothing")
	handoff := fs.Bool("handoff", false, "hand the conversation over as a Markdown document even where conch writes the agent's own session")
	noCont := fs.Bool("no-continue", false, "leave the resumed agent waiting rather than send it \"continue\"")
	cancel := fs.Bool("cancel", false, "call off a switch that is waiting")
	undo := fs.String("undo", "", "remove the session files the switch RECORD wrote, those not written to since")
	words, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	switch {
	case *undo != "" && len(words) != 0, *cancel && len(words) != 1, *undo == "" && !*cancel && len(words) != 2:
		return errors.New(agentSwitchUsage)
	}
	c, err := connect(false)
	if err != nil {
		return err
	}
	defer c.Close()
	if len(c.MissingCapabilities([]string{proto.CapAgentSwitch})) > 0 {
		return errors.New("the conch server there cannot switch an agent's conversation to another agent; reload it with `conch server reload`")
	}
	if *undo != "" {
		var res proto.AgentSwitchUndoResult
		if err := call(c, proto.MethodAgentSwitchUndo, proto.AgentSwitchUndoParams{Record: *undo}, &res); err != nil {
			return err
		}
		for _, f := range res.Removed {
			fmt.Println("removed", f)
		}
		for _, f := range res.Kept {
			fmt.Println("kept (written to since):", f)
		}
		if len(res.Removed)+len(res.Kept) == 0 {
			fmt.Println("Nothing to remove: what it wrote is gone already.")
		}
		return nil
	}
	id, err := resolvePane(c, words[0])
	if err != nil {
		return err
	}
	if *cancel {
		if err := call(c, proto.MethodAgentSwitchCancel, proto.AgentSwitchCancelParams{ID: id}, nil); err != nil {
			return err
		}
		fmt.Printf("%s: switch called off\n", id)
		return nil
	}
	res, err := switchPane(c, proto.AgentSwitchParams{ID: id, To: words[1], Now: *now, Preview: *preview, Handoff: *handoff, NoContinue: *noCont})
	if err != nil {
		return err
	}
	fmt.Println(describeSwitch(id, res))
	return nil
}

func switchPane(c *client.Client, p proto.AgentSwitchParams) (proto.AgentSwitchResult, error) {
	var res proto.AgentSwitchResult
	err := call(c, proto.MethodAgentSwitch, p, &res)
	return res, err
}

// describeSwitch says what a switch did, will do, or would do.
func describeSwitch(id string, res proto.AgentSwitchResult) string {
	var b strings.Builder
	switch {
	case res.Preview:
		fmt.Fprintf(&b, "%s: switching from %s to %s would ", id, res.From, res.To)
		if res.Crossed == proto.SwitchNative {
			fmt.Fprintf(&b, "write %s's own session, resumed with the history", res.To)
		} else {
			fmt.Fprintf(&b, "hand the conversation over as a document (%s)", res.Why)
		}
	case res.Pending:
		fmt.Fprintf(&b, "%s: will switch from %s to %s once it has %s (conch agent switch -cancel %s calls it off)",
			id, res.From, res.To, strings.Join(res.Waits, " and "), id)
	case res.Crossed == proto.SwitchNative:
		fmt.Fprintf(&b, "%s: switched from %s to %s, which resumed session %s with the history", id, res.From, res.To, res.Session)
		if res.Record != "" {
			fmt.Fprintf(&b, " (undo: conch agent switch -undo %s)", res.Record)
		}
	default:
		fmt.Fprintf(&b, "%s: switched from %s to %s, handed the conversation in %s", id, res.From, res.To, res.Document)
		if res.Why != "" {
			fmt.Fprintf(&b, " (%s)", res.Why)
		}
	}
	if cr := res.Crossing; cr != nil && !res.Pending {
		fmt.Fprintf(&b, "\n  crosses: %d messages, %d commands, %d notes", cr.Messages, cr.Commands, cr.Notes)
		if cr.Plan {
			b.WriteString(", the plan")
		}
		if len(cr.Left) > 0 {
			fmt.Fprintf(&b, "\n  left behind: %s", strings.Join(cr.Left, "; "))
		}
	}
	return b.String()
}
