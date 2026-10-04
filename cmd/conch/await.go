package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
)

// Waiting for an agent, for the commands that do it and for the MCP tools
// (mcp.go), which want the pane and the state back rather than a line on
// stdout. Both kinds of wait listen on the events of a connection made
// *before* the thing they wait for was asked for, so an update that lands
// in between is queued rather than missed — that ordering is the whole
// reason these take a client instead of making one.

// awaitState waits until the agent in a pane reaches one of want, the pane
// ends, or the time runs out. note, when given, is told once that nothing
// is running there yet. It returns the pane as it was when it got there and
// the state that ended the wait.
func awaitState(c *client.Client, id string, want map[string]bool, label string, timeout time.Duration, note func(string)) (proto.PaneInfo, string, error) {
	var list proto.PaneList
	if err := call(c, proto.MethodPaneList, nil, &list); err != nil {
		return proto.PaneInfo{}, "", err
	}
	info, ok := paneByID(list.Panes, id)
	if !ok {
		return proto.PaneInfo{}, "", fmt.Errorf("no pane %q", id)
	}
	if s := agentState(info); want[s] {
		return info, s, nil
	}
	// A pane that ended before the wait began sends no event to end it.
	if info.State == proto.PaneExited {
		return info, "", endedErr(info, label)
	}
	if info.Agent == nil && note != nil {
		note(fmt.Sprintf("waiting for an agent to start in %s…", id))
	}
	return awaitEvents(c, timeout, func(p proto.PaneInfo, event string) (string, bool, error) {
		switch event {
		case proto.EventPaneUpdated:
			if s := agentState(p); want[s] {
				return s, true, nil
			}
		case proto.EventPaneExited, proto.EventPaneClosed:
			return "", true, endedErr(p, label)
		}
		return "", false, nil
	}, id)
}

// awaitTurn waits for the work that answers a prompt to end: the agent's
// turn reaching res.Turn in one of the states wanted. A question it asks
// ends the wait whatever the turn — none was open when the message went in.
func awaitTurn(c *client.Client, res proto.AgentPromptResult, want map[string]bool, label string, timeout time.Duration) (proto.PaneInfo, string, error) {
	return awaitEvents(c, timeout, func(p proto.PaneInfo, event string) (string, bool, error) {
		switch event {
		case proto.EventPaneUpdated:
			a := p.Agent
			if a == nil || a.Name != res.Agent {
				return "", true, fmt.Errorf("the %s agent in %s is gone before it was %s", res.Agent, res.ID, label)
			}
			if want[a.State] && (a.Turn >= res.Turn || a.State == proto.AgentBlocked) {
				return a.State, true, nil
			}
		case proto.EventPaneExited, proto.EventPaneClosed:
			return "", true, endedErr(p, label)
		}
		return "", false, nil
	}, res.ID)
}

// awaitEvents reads the connection's events until done says the wait is
// over, or timeout passes (0: no limit). Events about other panes, and
// anything that is not a pane, are skipped.
func awaitEvents(c *client.Client, timeout time.Duration, done func(p proto.PaneInfo, event string) (string, bool, error), id string) (proto.PaneInfo, string, error) {
	var late <-chan time.Time
	if timeout > 0 {
		t := time.NewTimer(timeout)
		defer t.Stop()
		late = t.C
	}
	for {
		select {
		case msg, ok := <-c.Events:
			if !ok {
				if err := c.Err(); err != nil {
					return proto.PaneInfo{}, "", err
				}
				return proto.PaneInfo{}, "", errors.New("the server closed the connection")
			}
			var p proto.PaneInfo
			if msg.Data == nil || json.Unmarshal(msg.Data, &p) != nil || p.ID != id {
				continue
			}
			state, over, err := done(p, msg.Event)
			if over {
				return p, state, err
			}
		case <-late:
			return proto.PaneInfo{}, "", waitTimeout{after: timeout}
		}
	}
}
