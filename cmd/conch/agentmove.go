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
	words, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(words) != 1 {
		return errors.New("usage: conch agent move ID [-account NAME] [-continue]")
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
	if err := call(c, proto.MethodAgentMove, proto.AgentMoveParams{ID: id, Account: *account, Continue: *cont}, &nw); err != nil {
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
