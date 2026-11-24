package server_test

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// Antigravity CLI names its conversation only in what it gives its title
// command, which conch reports as a status line: that is what lets its
// chat be read. A status line from Claude, which says so through its
// hooks, leaves the session alone as before.
func TestAStatusLineNamesAntigravitysConversation(t *testing.T) {
	// conch starts agy with --settings, which a bare sh refuses and exits
	// on — sometimes before the pane was seen as an agent. The stand-in
	// takes any arguments and stays.
	bin := filepath.Join(t.TempDir(), "agy")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexec cat\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	c, _ := startServerWithAgents(t, map[string]string{
		"antigravity": "agent = \"antigravity\"\nlabel = \"Antigravity CLI\"\nprocess_names = [\"cat\"]\n\n[run]\nbinary = " + strconv.Quote(bin) + "\n",
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var info proto.PaneInfo
	if err := c.Call(ctx, proto.MethodPaneCreate, proto.PaneCreateParams{Agent: "antigravity", Cols: 80, Rows: 24, NoProject: true}, &info); err != nil {
		t.Fatal(err)
	}
	session := func() (string, bool) {
		var list proto.PaneList
		if err := c.Call(ctx, proto.MethodPaneList, nil, &list); err != nil {
			t.Fatal(err)
		}
		for _, p := range list.Panes {
			if p.ID == info.ID && p.Agent != nil && p.Agent.Name == "antigravity" {
				return p.Agent.SessionID, true
			}
		}
		return "", false
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		if _, ok := session(); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the pane was never seen as Antigravity CLI")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := c.Call(ctx, proto.MethodAgentReport, proto.AgentReportParams{ID: info.ID, Agent: "antigravity", Event: "StatusLine",
		SessionID: "3dde1bcf-073d-4944-8387-a0d6386f32c0", ContextUsed: 17967, ContextSize: 1048576}, nil); err != nil {
		t.Fatal(err)
	}
	for {
		if id, _ := session(); id == "3dde1bcf-073d-4944-8387-a0d6386f32c0" {
			break
		}
		if time.Now().After(deadline.Add(10 * time.Second)) {
			t.Fatal("the conversation it named was not taken")
		}
		time.Sleep(100 * time.Millisecond)
	}
}
