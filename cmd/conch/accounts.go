package main

import (
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
)

// An agent's accounts (internal/accounts), kept by the server of the
// machine the agents run on — which is why these go through it, -m and
// all, rather than writing a file here.

const accountUsage = "usage: conch account list | add AGENT NAME [-dir DIR] [-key-env VAR] | remove AGENT NAME | pin [PATH] NAME | unpin [PATH] | failover PERCENT|off|default [-agent AGENT] | suggest [AGENT] | back [ACCOUNT]"

// agentAccountCommands are the subcommands of conch account that are about
// agent accounts; conch account on its own is Conch Cloud's.
var agentAccountCommands = map[string]bool{"list": true, "ls": true, "add": true, "remove": true, "rm": true, "pin": true, "unpin": true, "failover": true, "suggest": true, "back": true}

func runAgentAccount(args []string) error {
	if len(args) == 0 || !agentAccountCommands[args[0]] {
		return errors.New(accountUsage)
	}
	sub, rest := args[0], args[1:]
	if sub == "suggest" {
		return accountSuggest(rest)
	}
	if sub == "back" {
		return accountBack(rest)
	}
	var params any
	var method string
	perAgent, keyed := false, false
	switch sub {
	case "list", "ls":
		if len(rest) > 0 {
			return errors.New(accountUsage)
		}
		method = proto.MethodAccountList
	case "add":
		fs := flag.NewFlagSet("account add", flag.ContinueOnError)
		dir := fs.String("dir", "", "the account's folder (default: one of conch's own)")
		keyEnv := fs.String("key-env", "", "antigravity: the variable holding this account's Gemini API key (its name, not the key)")
		words, err := parseInterspersed(fs, rest)
		if err != nil {
			return err
		}
		if len(words) != 2 {
			return errors.New("usage: conch account add AGENT NAME [-dir DIR] [-key-env VAR]")
		}
		method, params = proto.MethodAccountAdd, proto.AccountAddParams{Agent: words[0], Name: words[1], Dir: *dir, KeyEnv: *keyEnv}
		keyed = *keyEnv != ""
	case "remove", "rm":
		if len(rest) != 2 {
			return errors.New("usage: conch account remove AGENT NAME")
		}
		method, params = proto.MethodAccountRemove, proto.AccountRef{Agent: rest[0], Name: rest[1]}
	case "pin", "unpin":
		var path, name string
		switch {
		case sub == "pin" && len(rest) == 1:
			name = rest[0]
		case sub == "pin" && len(rest) == 2:
			path, name = rest[0], rest[1]
		case sub == "unpin" && len(rest) <= 1:
			if len(rest) == 1 {
				path = rest[0]
			}
		default:
			return errors.New("usage: conch account pin [PATH] NAME | unpin [PATH]")
		}
		var err error
		if path, err = accountPath(path); err != nil {
			return err
		}
		method, params = proto.MethodAccountPin, proto.AccountPinParams{Path: path, Account: name}
	case "failover":
		fs := flag.NewFlagSet("account failover", flag.ContinueOnError)
		agent := fs.String("agent", "", "set this agent's own line (claude, codex, antigravity); default: every agent's")
		words, err := parseInterspersed(fs, rest)
		if err != nil {
			return err
		}
		if len(words) != 1 {
			return errors.New("usage: conch account failover PERCENT|off|default [-agent AGENT]")
		}
		p := proto.AccountFailoverParams{Agent: *agent}
		switch v := strings.TrimSuffix(words[0], "%"); v {
		case "off":
		case "default":
			// The agent's own line taken away: it follows every agent's.
			if *agent == "" {
				return errors.New("default is for an agent's own line: conch account failover default -agent claude")
			}
			p.Shared = true
		default:
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 || n > 100 {
				return fmt.Errorf("failover takes a percentage from 0 to 100, off, or default with -agent: %q", words[0])
			}
			p.At = n
		}
		method, params = proto.MethodAccountFailover, p
		perAgent = *agent != ""
	}

	c, err := connect(true)
	if err != nil {
		return err
	}
	defer c.Close()
	if err := needAccounts(c); err != nil {
		return err
	}
	// An older server would take an agent's line as every agent's.
	if perAgent && len(c.MissingCapabilities([]string{proto.CapAccountFailoverAgent})) > 0 {
		return errors.New("the conch server there keeps one failover line for every agent; reload it with `conch server reload` to give one agent its own")
	}
	if keyed && len(c.MissingCapabilities([]string{proto.CapAccountKeys})) > 0 {
		return errors.New("the conch server there has no accounts made of API keys; reload it with `conch server reload`")
	}
	switch method {
	case proto.MethodAccountList:
		var l proto.AccountList
		if err := call(c, method, nil, &l); err != nil {
			return err
		}
		printAccounts(l, time.Now())
	case proto.MethodAccountAdd:
		var a proto.AccountInfo
		if err := call(c, method, params, &a); err != nil {
			return err
		}
		fmt.Printf("%s account %s in %s\n", a.Agent, a.Name, a.Dir)
		if a.KeyEnv != "" {
			fmt.Printf("It uses the Gemini API key in $%s, which your login shell sets: conch keeps no key.\n", a.KeyEnv)
		} else {
			fmt.Printf("Sign in once: conch new -agent %s -account %s, and log in there.\n", a.Agent, a.Name)
		}
	default:
		if err := call(c, method, params, nil); err != nil {
			return err
		}
	}
	return nil
}

// accountPath is the folder a pin is for: the one named, or the current
// one, made absolute here when the server is this computer's.
func accountPath(path string) (string, error) {
	if onRemoteMachine() {
		if path == "" {
			return "", errors.New("on another machine, name the folder to pin: conch -m M account pin PATH NAME")
		}
		return path, remoteDir(path)
	}
	if path == "" {
		path, _ = os.Getwd()
	}
	return filepath.Abs(path)
}

// needAccounts refuses, with what to do, a server that has no accounts.
func needAccounts(c *client.Client) error {
	if len(c.MissingCapabilities([]string{proto.CapAccounts})) > 0 {
		return errors.New("the conch server there predates agent accounts; reload it with `conch server reload`")
	}
	return nil
}

func printAccounts(l proto.AccountList, now time.Time) {
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	last := ""
	for _, a := range l.Accounts {
		if a.Agent != last {
			fmt.Fprintf(tw, "%s\n", a.Agent)
			last = a.Agent
		}
		name, dir := a.Name, a.Dir
		if a.Default {
			name, dir = "default", "(its own)"
		}
		if a.KeyEnv != "" {
			dir += " · key in $" + a.KeyEnv
		}
		running := ""
		if a.Agents > 0 {
			running = fmt.Sprintf("%d agent", a.Agents)
			if a.Agents > 1 {
				running += "s"
			}
		}
		fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n", name, dir, limitsText(a.Limits, now), running)
	}
	tw.Flush()
	if len(l.Pins) > 0 {
		fmt.Println("Pinned")
		for _, p := range l.Pins {
			fmt.Printf("  %s → %s\n", p.Path, p.Account)
		}
	}
	if l.FailoverAt > 0 {
		fmt.Printf("Failover at %d%%: a new agent whose account is past it starts on the next one\n", l.FailoverAt)
	} else {
		fmt.Println("Failover off: conch account failover 90 moves new agents off an account at 90%")
	}
	// An agent with a line of its own, in the order the agents are listed.
	for _, agent := range l.Agents {
		at, ok := l.FailoverFor[agent]
		if !ok {
			continue
		}
		if at > 0 {
			fmt.Printf("  %s's own: at %d%%\n", agent, at)
		} else {
			fmt.Printf("  %s's own: off\n", agent)
		}
	}
}

// limitsText is an account's plan windows as the status bar says them.
func limitsText(l *proto.PlanLimits, now time.Time) string {
	if l == nil {
		return ""
	}
	var parts []string
	for _, w := range []struct {
		name string
		win  *proto.LimitWindow
	}{{"5h", l.FiveHour}, {"7d", l.Week}, {"spend", l.Spend}} {
		if w.win == nil || (!w.win.ResetsAt.IsZero() && now.After(w.win.ResetsAt)) {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s %d%%", w.name, int(math.Round(w.win.UsedPct))))
	}
	return strings.Join(parts, " · ")
}
