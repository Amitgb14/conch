package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// agentMove is conch agent move ID [-account NAME]: the agent's
// conversation carried to another of its accounts and resumed there.
func agentMove(args []string) error {
	fs := flag.NewFlagSet("agent move", flag.ContinueOnError)
	account := fs.String("account", "", "the account to move it to (default: the one with most room; default is the agent's own)")
	cont := fs.Bool("continue", false, "send it \"continue\" once it is back, as for an agent its limit stopped")
	force := fs.Bool("force", false, "move it even with background commands running; they stop with it")
	words, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(words) != 1 {
		return errors.New("usage: conch agent move ID [-account NAME] [-continue] [-force]")
	}
	c, err := connect(false)
	if err != nil {
		return err
	}
	defer c.Close()
	if len(c.MissingCapabilities([]string{proto.CapAgentMove})) > 0 {
		return errors.New("the conch server there cannot move an agent between accounts; reload it with `conch server reload`")
	}
	id, err := resolvePane(c, words[0])
	if err != nil {
		return err
	}
	var nw proto.PaneInfo
	if err := call(c, proto.MethodAgentMove, proto.AgentMoveParams{ID: id, Account: *account, Continue: *cont, Force: *force}, &nw); err != nil {
		return err
	}
	to := nw.Account
	if to == "" {
		to = "default"
	}
	fmt.Printf("%s: conversation carried to %s and resumed there as %s\n", id, to, nw.ID)
	return nil
}

// accountSuggest is conch account suggest [AGENT]: its accounts as a move
// would weigh them, the pick first.
func accountSuggest(args []string) error {
	agent := "claude"
	if len(args) > 1 {
		return errors.New("usage: conch account suggest [AGENT]")
	}
	if len(args) == 1 {
		agent = args[0]
	}
	c, err := connect(true)
	if err != nil {
		return err
	}
	defer c.Close()
	if len(c.MissingCapabilities([]string{proto.CapAgentMove})) > 0 {
		return errors.New("the conch server there cannot rank accounts; reload it with `conch server reload`")
	}
	var sg proto.AccountSuggestion
	if err := call(c, proto.MethodAccountSuggest, proto.AccountSuggestParams{Agent: agent}, &sg); err != nil {
		return err
	}
	printSuggestion(sg, time.Now())
	return nil
}

func printSuggestion(sg proto.AccountSuggestion, now time.Time) {
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	picked := false
	for _, a := range sg.Accounts {
		name := a.Account
		if name == "" {
			name = "default"
		}
		mark := ""
		if a.Usable && !picked {
			mark, picked = "← conch's pick", true
		}
		agents := ""
		if a.Agents > 0 {
			agents = fmt.Sprintf("%d agent", a.Agents)
			if a.Agents > 1 {
				agents += "s"
			}
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", name, a.Why, agents, mark)
	}
	tw.Flush()
	if !picked {
		fmt.Println(strings.TrimSpace(fmt.Sprintf("No %s account has room for a move.", sg.Agent)))
	}
}

// accountBack is conch account back [ACCOUNT]: every agent moved off the
// account (default: the agent's own) moved back to it, once it has room.
func accountBack(args []string) error {
	if len(args) > 1 {
		return errors.New("usage: conch account back [ACCOUNT]")
	}
	to := "default"
	if len(args) == 1 {
		to = args[0]
	}
	c, err := connect(false)
	if err != nil {
		return err
	}
	defer c.Close()
	if len(c.MissingCapabilities([]string{proto.CapAgentMove})) > 0 {
		return errors.New("the conch server there cannot move agents between accounts; reload it with `conch server reload`")
	}
	var list proto.PaneList
	if err := call(c, proto.MethodPaneList, nil, &list); err != nil {
		return err
	}
	var moved []proto.PaneInfo
	for _, p := range list.Panes {
		if p.State == proto.PaneRunning && p.MovedFrom == to {
			moved = append(moved, p)
		}
	}
	if len(moved) == 0 {
		fmt.Printf("No agent was moved off %s.\n", to)
		return nil
	}
	// Room is the agent's own question: each agent's accounts are ranked
	// apart, so ask for the first's and refuse them all if there is none.
	agent := "claude"
	if moved[0].Agent != nil && moved[0].Agent.Name != "" {
		agent = moved[0].Agent.Name
	}
	var sg proto.AccountSuggestion
	if err := call(c, proto.MethodAccountSuggest, proto.AccountSuggestParams{Agent: agent}, &sg); err != nil {
		return err
	}
	for _, a := range sg.Accounts {
		name := a.Account
		if name == "" {
			name = "default"
		}
		if name == to && !a.Usable {
			return fmt.Errorf("%s has no room yet (%s): its %d agent(s) stay where they are", to, a.Why, len(moved))
		}
	}
	failed := 0
	for _, p := range moved {
		var nw proto.PaneInfo
		if err := call(c, proto.MethodAgentMove, proto.AgentMoveParams{ID: p.ID, Account: to, Reason: "back"}, &nw); err != nil {
			fmt.Printf("%s: %v\n", p.ID, err)
			failed++
			continue
		}
		fmt.Printf("%s: back on %s as %s\n", p.ID, to, nw.ID)
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d agents did not move back (one mid-turn moves when its turn ends: try again then)", failed, len(moved))
	}
	return nil
}
