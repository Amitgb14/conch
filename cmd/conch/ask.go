package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Amitgb14/conch/internal/brain"
	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
)

// runAsk sends a request to the brain, shows its plan and runs it once
// confirmed.
func runAsk(args []string) error {
	fs := flag.NewFlagSet("ask", flag.ContinueOnError)
	yes := fs.Bool("y", false, "run the plan without asking")
	dry := fs.Bool("n", false, "show the plan only")
	if err := fs.Parse(args); err != nil {
		return err
	}
	request := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if request == "" {
		return errors.New(`usage: conch ask [-y | -n] "start two agents on api to fix the flaky tests"`)
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	provider, err := brain.New(cfg.Brain)
	if err != nil {
		return err
	}
	if err := provider.Check(); err != nil {
		return err
	}

	c, err := connect(true)
	if err != nil {
		return err
	}
	defer c.Close()
	var agents proto.AgentStatusResult
	var projects proto.ProjectList
	var panes proto.PaneList
	for _, step := range []struct {
		method string
		out    any
	}{{proto.MethodAgentStatus, &agents}, {proto.MethodProjectList, &projects}, {proto.MethodPaneList, &panes}} {
		if err := call(c, step.method, nil, step.out); err != nil {
			return err
		}
	}
	id, label := "local", "this computer"
	if machineFlag != "" && machineFlag != "local" {
		id, label = machineFlag, machineFlag
	}
	w := brain.World{
		DefaultAgent: firstNonEmptyStr(cfg.Agents.Default, "claude"),
		Machines:     []brain.Machine{brain.MachineFrom(id, label, true, agents.Agents, projects.Projects, panes.Panes, nil)},
	}
	if wd, err := os.Getwd(); err == nil && id == "local" {
		w.Selected = "working directory " + wd
	}

	fmt.Fprintf(os.Stderr, "thinking (%s)…\n", provider.Name())
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	plan, err := brain.MakePlan(ctx, provider, w, request)
	if err != nil {
		return err
	}
	if plan.Reply != "" {
		fmt.Println(plan.Reply)
	}
	var ok []brain.Action
	for _, a := range plan.Actions {
		if err := w.Validate(&a); err != nil {
			fmt.Printf("  ✗ %s — skipped: %v\n", w.Describe(a), err)
			continue
		}
		ok = append(ok, a)
		fmt.Printf("  %d. %s\n", len(ok), w.Describe(a))
	}
	if len(ok) == 0 || *dry {
		return nil
	}
	if !*yes && !confirm(fmt.Sprintf("Run %d action(s)? [y/N] ", len(ok)), false) {
		return nil
	}
	for i, a := range ok {
		res, err := brain.Execute(ctx, c, a, 0, 0)
		switch {
		case err != nil:
			fmt.Printf("  %d. failed: %v\n", i+1, err)
		case res.Pane != nil:
			fmt.Printf("  %d. started %s in %s\n", i+1, res.Pane.ID, res.Pane.Cwd)
		default:
			fmt.Printf("  %d. done\n", i+1)
		}
	}
	return nil
}

func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
