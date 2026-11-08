package main

import (
	"context"
	"encoding/json"
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
	model := fs.Bool("model", false, "ask the model even when conch can plan it alone")
	pane := fs.String("pane", "", "the pane the request is about when it says \"this\" or \"here\"")
	suggest := fs.Bool("suggest", false, "print what is worth asking now, as JSON; the arguments are earlier requests, oldest first")
	if err := fs.Parse(args); err != nil {
		return err
	}
	request := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if request == "" && !*suggest {
		return errors.New(`usage: conch ask [-y | -n] [-model] [-pane ID] "start two agents on api to fix the flaky tests"`)
	}
	cfg, cerr := config.Load()
	if cerr != nil { // a config with a typo in it is not worth refusing over
		fmt.Fprintln(os.Stderr, "conch: settings not read ("+cerr.Error()+"); using the defaults")
	}
	var provider brain.Provider
	if !*suggest { // suggesting asks no model, so it works with none set up
		p, err := brain.New(cfg.Brain)
		if err != nil {
			return err
		}
		if err := p.Check(); err != nil {
			return err
		}
		provider = p
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
	here := ""
	if *pane != "" {
		w.Selected = fmt.Sprintf("pane %s on machine %s", *pane, id)
		for _, p := range panes.Panes {
			if p.ID == *pane && p.Agent != nil {
				here = p.DisplayName()
			}
		}
	}
	if *suggest {
		return printSuggestions(w, cfg, here, fs.Args())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	plan, fast := brain.Plan{}, false
	if !*model && !cfg.Brain.NoFastPath {
		plan, fast = brain.FastPlan(w, request)
	}
	if fast {
		fmt.Fprintln(os.Stderr, "planned without the model (-model asks it)")
	} else {
		fmt.Fprintf(os.Stderr, "thinking (%s)…\n", provider.Name())
		if plan, err = brain.MakePlan(ctx, provider, w, request); err != nil {
			return err
		}
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

// askSuggestions is what `conch ask -suggest` prints: what the Mac app's
// Ask popover draws under its request, so it offers what the TUI's
// window does without a second copy of the rules in Swift.
type askSuggestions struct {
	Context     string             `json:"context"`
	Provider    string             `json:"provider"`
	Fast        bool               `json:"fast"` // requests conch knows are answered without the model
	Counts      brain.Counts       `json:"counts"`
	Summary     string             `json:"summary"`
	Suggestions []brain.Suggestion `json:"suggestions"`
}

func printSuggestions(w brain.World, cfg config.Config, here string, history []string) error {
	out := askSuggestions{
		Context:     w.Selected,
		Provider:    cfg.Brain.Provider,
		Fast:        !cfg.Brain.NoFastPath,
		Counts:      brain.CountAgents(w),
		Suggestions: brain.Suggest(w, here, false, !cfg.Brain.NoFastPath, history),
	}
	out.Summary = out.Counts.String()
	if out.Suggestions == nil {
		out.Suggestions = []brain.Suggestion{}
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
