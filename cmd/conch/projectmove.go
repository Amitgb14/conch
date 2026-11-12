package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/cloud"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/relocate"
	"github.com/Amitgb14/conch/internal/remote"
)

// connectMoveTarget reaches the machine a project moves to; tests point it
// at a server of their own.
var connectMoveTarget = connectMachine

// wakeMoveTarget readies the machine a project moves to: a Conch Cloud
// machine asleep is woken and waited for. A connection nobody asked for
// never wakes one (remote.CloudAsleepError) — its hours are the
// person's — but moving a project there is asking for it.
var wakeMoveTarget = func(ref string) error {
	m, ok := remote.LookupMachine(ref)
	if !ok {
		return nil
	}
	id, ok := remote.ParseCloudTarget(m.Target)
	if !ok {
		return nil
	}
	c, err := cloud.Open()
	if err != nil {
		return fmt.Errorf("%s is a Conch Cloud machine: %w", m.Label, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), cloudMakeWait)
	defer cancel()
	_, err = c.WaitRunning(ctx, id, cloudSay)
	return err
}

const projectMoveUsage = "usage: conch project move [-plan] [-into DIR] [-y] [-json] PROJECT -to MACHINE"

// projectMove moves a whole project to another machine — the TUI's T on a
// project, and what Conch.app's Move to another machine… runs: the
// repository (cloned there from its origin, or the project there that has
// it), every task worktree with its commits and uncommitted work, every
// branch holding commits its origin hasn't, and the agents with their
// conversations. -plan only says what would move; -json says each step as
// a line of JSON, for a window to draw.
func projectMove(c *client.Client, args []string) error {
	fs := flag.NewFlagSet("project move", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	to := fs.String("to", "", "the machine it goes to")
	into := fs.String("into", "", "where to clone it there (default: where it is here, under the home folder)")
	planOnly := fs.Bool("plan", false, "only say what would move; change nothing")
	yes := fs.Bool("y", false, "don't ask first")
	asJSON := fs.Bool("json", false, "say the plan, each step and the result as lines of JSON")
	cols := fs.Int("cols", 120, "the width agents there start at")
	rows := fs.Int("rows", 36, "the height agents there start at")
	// The project may come before or after the flags.
	var rest []string
	for len(args) > 0 {
		if err := fs.Parse(args); err != nil {
			return fmt.Errorf("%v\n%s", err, projectMoveUsage)
		}
		args = fs.Args()
		if len(args) > 0 {
			rest = append(rest, args[0])
			args = args[1:]
		}
	}
	if len(rest) != 1 || *to == "" {
		return errors.New(projectMoveUsage)
	}
	if *asJSON && !*yes && !*planOnly {
		return errors.New("-json says no questions: add -y to move, or -plan to only see what would move")
	}
	fromLabel := "local"
	if onRemoteMachine() {
		fromLabel = remote.FindMachine(machineFlag).Label
	}
	if *to == machineFlag || (*to == "local" && !onRemoteMachine()) {
		return fmt.Errorf("%s is already on %s", rest[0], fromLabel)
	}
	if err := wakeMoveTarget(*to); err != nil {
		if *asJSON {
			json.NewEncoder(os.Stdout).Encode(map[string]string{"error": err.Error()})
		}
		return err
	}
	dst, err := connectMoveTarget(*to)
	if err != nil {
		return err
	}
	defer dst.Close()
	toLabel := remote.FindMachine(*to).Label
	if toLabel == "" {
		toLabel = *to
	}

	out := json.NewEncoder(os.Stdout)
	fail := func(err error) error {
		if *asJSON {
			out.Encode(map[string]string{"error": err.Error()})
		}
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	plan, err := relocate.NewPlan(ctx, c, dst, rest[0], fromLabel, toLabel, *into)
	cancel()
	if err != nil {
		return fail(err)
	}
	if *asJSON {
		out.Encode(map[string]any{"plan": plan, "steps": plan.Steps()})
	} else {
		fmt.Print(planText(plan))
	}
	if *planOnly {
		return nil
	}
	if !*yes && !confirm(fmt.Sprintf("Move %s to %s?", plan.Name, toLabel), false) {
		return errors.New("not moved")
	}

	emit := func(e relocate.Event) {
		if *asJSON {
			out.Encode(map[string]relocate.Event{"event": e})
			return
		}
		switch e.State {
		case "start":
			fmt.Printf("[%d/%d] %s %s…\n", e.Step, e.Steps, e.Kind, e.Name)
		case "ok":
			fmt.Printf("  ✓ %s\n", e.Detail)
		case "fail":
			fmt.Printf("  ✗ %s\n", e.Detail)
		}
	}
	res, err := relocate.Run(context.Background(), plan, c, dst, relocate.Options{Cols: *cols, Rows: *rows}, emit)
	if err != nil {
		return fail(err)
	}
	if *asJSON {
		out.Encode(map[string]relocate.Result{"result": res})
	} else {
		fmt.Println(resultText(plan, res))
	}
	if len(res.Failed) > 0 {
		return stayedError(len(res.Failed), fromLabel)
	}
	return nil
}

// stayedError is what a move that left something behind exits with.
func stayedError(n int, from string) error {
	return fmt.Errorf("%s stayed on %s", relocate.Count(n, "thing", "things"), from)
}

// planText says what a move would do, and what stays, before it starts.
func planText(p relocate.Plan) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Move %s (%s) to %s\n", p.Name, p.Path, p.To)
	if p.Target != "" {
		fmt.Fprintf(&b, "  into     %s, which %s already has (%s)\n", p.TargetName, p.To, p.TargetPath)
	} else if p.Bundle {
		fmt.Fprintf(&b, "  copy     the repository (no origin: every branch and tag) into %s there\n", p.CloneTo)
	} else {
		fmt.Fprintf(&b, "  clone    %s into %s there\n", relocate.RemoteName(p.Remote), p.CloneTo)
	}
	for _, w := range p.Worktrees {
		if w.There != "" {
			fmt.Fprintf(&b, "  worktree %s is there already (%s): %s %s in it\n", w.Branch, w.There, paneNames(w.Agents), continueVerb(len(w.Agents)))
			continue
		}
		fmt.Fprintf(&b, "  worktree %s%s%s\n", w.Branch, worktreeWhat(w.Info), agentsText(w.Agents))
	}
	for _, br := range p.Branches {
		why := "only here"
		switch {
		case br.Gone:
			why = "its upstream is gone"
		case br.Ahead > 0:
			why = relocate.Count(br.Ahead, "commit", "commits") + " ahead of " + br.Upstream
		}
		fmt.Fprintf(&b, "  branch   %s (%s; given a worktree here, then moved)\n", br.Name, why)
	}
	if len(p.MainAgents) > 0 {
		fmt.Fprintf(&b, "  agents   %s %s in the project's checkout there\n", paneNames(p.MainAgents), continueVerb(len(p.MainAgents)))
	}
	if len(p.Already) > 0 {
		fmt.Fprintf(&b, "on %s already: %s\n", p.To, strings.Join(p.Already, ", "))
	}
	var stays []string
	if p.MainDirty > 0 {
		stays = append(stays, relocate.Count(p.MainDirty, "uncommitted file", "uncommitted files")+" in the main checkout")
	}
	if p.Terminals > 0 {
		stays = append(stays, relocate.Count(p.Terminals, "terminal", "terminals"))
	}
	for _, s := range p.Skipped {
		stays = append(stays, "worktree "+s)
	}
	if len(stays) > 0 {
		fmt.Fprintf(&b, "stays here: %s\n", strings.Join(stays, "; "))
	}
	for _, a := range p.Agents() {
		if a.Agent != nil && a.Agent.State == proto.AgentWorking {
			fmt.Fprintf(&b, "%s is working: it is stopped mid-task, and what it changes meanwhile stays here\n", a.DisplayName())
		}
	}
	b.WriteString("Everything here is kept; remove the worktrees later with conch worktree clean.\n")
	return b.String()
}

func worktreeWhat(i proto.WorktreeMoveInfo) string {
	var parts []string
	if i.Staged > 0 {
		parts = append(parts, fmt.Sprintf("%d staged", i.Staged))
	}
	if i.Unstaged > 0 {
		parts = append(parts, fmt.Sprintf("%d unstaged", i.Unstaged))
	}
	if i.Other > 0 {
		parts = append(parts, fmt.Sprintf("%d untracked", i.Other))
	}
	if len(parts) == 0 {
		return ""
	}
	return " · " + strings.Join(parts, " · ")
}

func agentsText(agents []proto.PaneInfo) string {
	if len(agents) == 0 {
		return ""
	}
	return " · with " + paneNames(agents)
}

func paneNames(panes []proto.PaneInfo) string {
	var out []string
	for _, p := range panes {
		out = append(out, p.DisplayName())
	}
	return strings.Join(out, ", ")
}

// continueVerb agrees with the agents named before it: claude continues,
// claude, codex continue.
func continueVerb(n int) string {
	if n == 1 {
		return "continues"
	}
	return "continue"
}

// resultText is the line said at the end.
func resultText(p relocate.Plan, r relocate.Result) string {
	s := fmt.Sprintf("moved %s to %s (%s)", p.Name, p.To, r.TargetPath)
	if n := len(r.Moved); n > 0 {
		s += " · " + relocate.Count(n, "branch", "branches")
	}
	if n := len(r.Started); n > 0 {
		s += " · " + relocate.Count(n, "agent continues", "agents continue") + " there"
	}
	if len(r.Failed) > 0 {
		s += "\nstayed here: " + strings.Join(r.Failed, "; ")
	}
	return s
}
