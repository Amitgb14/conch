package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/Amitgb14/conch/internal/buildinfo"
	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/remote"
	"github.com/Amitgb14/conch/internal/report"
)

// `conch bug` is what somebody hitting a problem can hand over without
// writing anything down: the facts about their conch, and with -open a
// GitHub issue already filled in with them. (`conch report` is taken, and
// not by people: it is the channel agents' hooks shout down.)
//
// It never sends anything by itself. -open ends in a browser with every
// word visible, and the person presses submit — which is also why the
// report carries facts and never contents (internal/report).

const bugUsage = "usage: conch bug [-open] [-log] [WHAT HAPPENED...]"

func runBug(args []string) error {
	fs := flag.NewFlagSet("bug", flag.ContinueOnError)
	open := fs.Bool("open", false, "open a GitHub issue with it, already filled in")
	showLog := fs.Bool("log", false, "say where the server's log is, and what is in it")
	title := fs.String("title", "", "the issue's title, with -open")
	if err := fs.Parse(args); err != nil {
		return err
	}
	facts := gatherReport()
	facts.What = strings.Join(fs.Args(), " ")

	fmt.Print(facts.Text())
	if *showLog {
		fmt.Printf("\nserver log: %s\n", config.ServerLogPath())
		fmt.Println("It holds project paths, branch names and pane titles, so read it before you attach it.")
	}
	if !*open {
		if !*showLog {
			fmt.Println("\nconch bug -open files an issue with this in it; -log says where the server's log is.")
		}
		return nil
	}
	headline := strings.TrimSpace(*title)
	if headline == "" {
		headline = facts.What
	}
	link := facts.IssueURL(headline)
	if err := openInBrowser(link); err != nil {
		// A terminal with no desktop behind it — over ssh, in a container.
		// The link is the report either way.
		fmt.Printf("\n%s\n", link)
		return fmt.Errorf("could not open a browser (%v); the link is above", err)
	}
	fmt.Println("\nopening an issue in your browser — nothing is sent until you submit it")
	return nil
}

// gatherReport collects what can be had without asking anyone anything: the
// binary's own facts, and the server's when one answers. A conch that will
// not start is the report that matters most, so nothing here fails.
func gatherReport() report.Facts {
	f := report.Facts{
		Version:  proto.Version,
		Build:    buildinfo.Build(),
		Platform: runtime.GOOS + "/" + runtime.GOARCH,
		Terminal: report.Terminal{
			Program: os.Getenv("TERM_PROGRAM"),
			Term:    os.Getenv("TERM"),
			Tmux:    os.Getenv("TMUX") != "",
			SSH:     os.Getenv("SSH_CONNECTION") != "",
		},
	}
	if cfg, err := config.Load(); err == nil {
		f.Terminal.Icons = cfg.UI.Icons
	}
	f.Machines = savedMachineCount()
	c, err := connect(false)
	if err != nil {
		return f // no server: said as such, and the rest still stands
	}
	defer c.Close()
	f.MachinesOnline = 1 // this one answered; the others are not dialled for a report
	f.Server = report.Server{
		Version: c.Server.Version, Build: c.Server.Build, Platform: c.Server.Platform,
		PID: c.Server.PID, Started: c.Server.Started, LoadedAt: c.Server.LoadedAt,
		Missing: c.MissingCapabilities(proto.Capabilities),
	}
	var list proto.PaneList
	if err := call(c, proto.MethodPaneList, nil, &list); err == nil {
		f.Agents = map[string]int{}
		for _, p := range list.Panes {
			if p.State != proto.PaneRunning {
				continue
			}
			f.Server.Panes++
			if p.Agent != nil && p.Agent.Name != "" {
				f.Agents[p.Agent.Name]++
			}
		}
	}
	return f
}

// savedMachineCount is how many machines are in the catalog — a number,
// never their names or addresses, which are the person's own.
func savedMachineCount() int {
	ms, err := remote.Machines()
	if err != nil {
		return 0
	}
	n := 0
	for _, m := range ms {
		if m.Enabled {
			n++
		}
	}
	return n
}
