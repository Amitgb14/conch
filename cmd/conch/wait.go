package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// waitTimeout is what a `conch wait` that ran out of time returns, so main
// can exit 124 like timeout(1) and a script can tell it from a real error.
type waitTimeout struct{ after time.Duration }

func (e waitTimeout) Error() string { return "timed out after " + e.after.String() }

// waitStates maps the words people type to the states the server reports.
// "waiting" is what the TUI calls a blocked agent, so both are accepted.
var waitStates = map[string]string{
	"done":    "done",
	"waiting": "blocked",
	"blocked": "blocked",
	"working": "working",
	"idle":    "idle",
}

// runWait blocks until a pane's agent reaches one of the states asked for,
// so a shell can chain work without polling:
//
//	conch wait p3 && conch send p4 "review what p3 changed"
func runWait(args []string) error {
	fs := flag.NewFlagSet("wait", flag.ContinueOnError)
	state := fs.String("state", "done", "states to wait for, comma separated: done, waiting, working, idle")
	timeout := fs.Duration("timeout", 0, "give up after this long (0: wait forever)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: conch wait [-state done] [-timeout 30m] ID")
	}
	want, err := wantedStates(*state)
	if err != nil {
		return err
	}
	id := fs.Arg(0)

	c, err := connect(false)
	if err != nil {
		return errors.New("server is not running")
	}
	defer c.Close()

	// Connected before the pane is read, so an update between the two can
	// only be queued for the loop below, never missed.
	var list proto.PaneList
	if err := call(c, proto.MethodPaneList, nil, &list); err != nil {
		return err
	}
	info, ok := paneByID(list.Panes, id)
	if !ok {
		return fmt.Errorf("no pane %q", id)
	}
	if s := agentState(info); want[s] {
		return printWaited(info, s)
	}
	// A pane that ended before the wait began sends no event to end it.
	if info.State == proto.PaneExited {
		return endedErr(info, *state)
	}
	if info.Agent == nil {
		fmt.Fprintf(os.Stderr, "waiting for an agent to start in %s…\n", id)
	}

	var late <-chan time.Time
	if *timeout > 0 {
		t := time.NewTimer(*timeout)
		defer t.Stop()
		late = t.C
	}
	for {
		select {
		case msg, ok := <-c.Events:
			if !ok {
				if err := c.Err(); err != nil {
					return err
				}
				return errors.New("the server closed the connection")
			}
			var p proto.PaneInfo
			if msg.Data == nil || json.Unmarshal(msg.Data, &p) != nil || p.ID != id {
				continue
			}
			switch msg.Event {
			case proto.EventPaneUpdated:
				if s := agentState(p); want[s] {
					return printWaited(p, s)
				}
			case proto.EventPaneExited, proto.EventPaneClosed:
				return endedErr(p, *state)
			}
		case <-late:
			return waitTimeout{after: *timeout}
		}
	}
}

// wantedStates turns "done,waiting" into the states to stop on.
func wantedStates(list string) (map[string]bool, error) {
	want := map[string]bool{}
	for _, name := range strings.Split(list, ",") {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			continue
		}
		s, ok := waitStates[name]
		if !ok {
			return nil, fmt.Errorf("unknown state %q; use %s", name, strings.Join(waitStateNames(), ", "))
		}
		want[s] = true
	}
	if len(want) == 0 {
		return nil, errors.New("-state needs at least one state")
	}
	return want, nil
}

func waitStateNames() []string {
	names := make([]string, 0, len(waitStates))
	for n := range waitStates {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func paneByID(panes []proto.PaneInfo, id string) (proto.PaneInfo, bool) {
	for _, p := range panes {
		if p.ID == id {
			return p, true
		}
	}
	return proto.PaneInfo{}, false
}

// agentState is the pane's agent state, or "" when no agent runs in it.
func agentState(p proto.PaneInfo) string {
	if p.Agent == nil {
		return ""
	}
	return p.Agent.State
}

func endedErr(p proto.PaneInfo, state string) error {
	return fmt.Errorf("pane %s ended (exit %d) before it was %s", p.ID, p.ExitCode, state)
}

func printWaited(p proto.PaneInfo, state string) error {
	name := "agent"
	if p.Agent != nil && p.Agent.Name != "" {
		name = p.Agent.Name
	}
	fmt.Printf("%s %s %s\n", p.ID, name, state)
	return nil
}
