package server_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
)

// agentUpdate waits for a pane.updated about id whose agent satisfies ok.
func agentUpdate(t *testing.T, c *client.Client, id string, ok func(*proto.AgentStatus) bool) {
	t.Helper()
	waitEvent(t, c, func(m proto.Message) bool {
		var p proto.PaneInfo
		return m.Event == proto.EventPaneUpdated && json.Unmarshal(m.Data, &p) == nil &&
			p.ID == id && p.Agent != nil && ok(p.Agent)
	})
}

// The whole round over the wire: the turn a prompt names is the one the
// events carry when its work ends, and a blocked agent's refusal arrives as
// its own code.
func TestAgentPromptOverTheSocket(t *testing.T) {
	c, dir := startServer(t)
	if miss := c.MissingCapabilities([]string{proto.CapAgentPrompt}); len(miss) > 0 {
		t.Fatalf("not announced: %v", miss)
	}
	err := c.Call(t.Context(), proto.MethodAgentPrompt, proto.AgentPromptParams{ID: "p9", Text: "hi"}, nil)
	var perr *proto.Error
	if !errors.As(err, &perr) || perr.Code != proto.ErrNotFound {
		t.Fatalf("missing pane: %v", err)
	}

	var info proto.PaneInfo
	if err := c.Call(t.Context(), proto.MethodPaneCreate, proto.PaneCreateParams{Agent: "claude",
		Command: []string{"/bin/sh", "-c", "stty -echo; exec cat"}, Cwd: dir, Cols: 80, Rows: 10}, &info); err != nil {
		t.Fatal(err)
	}
	agentUpdate(t, c, info.ID, func(a *proto.AgentStatus) bool { return a.Name == "claude" })
	report := func(event string) {
		t.Helper()
		if err := c.Call(t.Context(), proto.MethodAgentReport, proto.AgentReportParams{ID: info.ID, Agent: "claude", Event: event}, nil); err != nil {
			t.Fatal(err)
		}
	}

	var res proto.AgentPromptResult
	if err := c.Call(t.Context(), proto.MethodAgentPrompt, proto.AgentPromptParams{ID: info.ID, Text: "run the tests"}, &res); err != nil {
		t.Fatal(err)
	}
	if res != (proto.AgentPromptResult{ID: info.ID, Agent: "claude", Turn: 1}) {
		t.Fatalf("result %+v", res)
	}
	report("UserPromptSubmit")
	agentUpdate(t, c, info.ID, func(a *proto.AgentStatus) bool { return a.State == proto.AgentWorking && a.Turn == 1 })
	report("Stop")
	agentUpdate(t, c, info.ID, func(a *proto.AgentStatus) bool { return a.State == proto.AgentDone && a.Turn == 1 })

	report("PermissionRequest")
	agentUpdate(t, c, info.ID, func(a *proto.AgentStatus) bool { return a.State == proto.AgentBlocked })
	err = c.Call(t.Context(), proto.MethodAgentPrompt, proto.AgentPromptParams{ID: info.ID, Text: "go on"}, nil)
	if !errors.As(err, &perr) || perr.Code != proto.ErrAgentBlocked {
		t.Fatalf("blocked: %v", err)
	}
}
