package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/amitghadge/conch/internal/client"
	"github.com/amitghadge/conch/internal/proto"
)

func runAgent(args []string) error {
	switch {
	case len(args) == 1 && args[0] == "status":
		return agentStatus()
	case len(args) == 2 && args[0] == "install":
		return agentInstall(args[1])
	case len(args) != 2 || args[0] != "explain":
		return errors.New("usage: conch agent explain ID | status | install claude")
	}
	c, err := connect(false)
	if err != nil {
		return err
	}
	defer c.Close()
	var out json.RawMessage
	if err := call(c, proto.MethodAgentExplain, proto.PaneRef{ID: args[1]}, &out); err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func agentStatus() error {
	c, err := connect(true)
	if err != nil {
		return err
	}
	defer c.Close()
	var res proto.AgentStatusResult
	if err := call(c, proto.MethodAgentStatus, nil, &res); err != nil {
		return err
	}
	for _, a := range res.Agents {
		if a.Installed {
			fmt.Printf("%s  installed  %s  %s\n", a.Name, a.Version, a.Path)
		} else {
			fmt.Printf("%s  not installed  (conch agent install %s)\n", a.Name, a.Name)
		}
	}
	return nil
}

// agentInstall runs an agent's installer in a pane and waits for it,
// showing the pane's screen when it ends.
func agentInstall(agent string) error {
	c, err := connect(true)
	if err != nil {
		return err
	}
	defer c.Close()
	var info proto.PaneInfo
	if err := call(c, proto.MethodAgentInstall, proto.AgentInstallParams{Agent: agent, Cols: 120, Rows: 40}, &info); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "installing %s in pane %s…\n", agent, info.ID)
	for msg := range c.Events {
		var p proto.PaneInfo
		if msg.Event != proto.EventPaneExited || json.Unmarshal(msg.Data, &p) != nil || p.ID != info.ID {
			continue
		}
		var screen proto.PaneReadResult
		_ = call(c, proto.MethodPaneRead, proto.PaneRef{ID: p.ID}, &screen)
		end := len(screen.Lines)
		for end > 0 && strings.TrimSpace(screen.Lines[end-1]) == "" {
			end--
		}
		fmt.Println(strings.Join(screen.Lines[:end], "\n"))
		_ = call(c, proto.MethodPaneClose, proto.PaneRef{ID: p.ID}, nil)
		if p.ExitCode != 0 {
			return fmt.Errorf("installing %s failed (exit %d)", agent, p.ExitCode)
		}
		return nil
	}
	return c.Err()
}

// runReport forwards an agent hook event to the server owning this pane.
// It runs inside the agent's hook machinery, so it prints nothing (Claude
// Code treats some hooks' stdout as instructions), always exits 0, and
// gives up quickly. Outside a conch pane it does nothing.
func runReport(args []string) {
	if len(args) != 1 || args[0] != "claude-hook" {
		return
	}
	paneID, sock := os.Getenv("CONCH_PANE_ID"), os.Getenv("CONCH_SOCKET")
	if paneID == "" || sock == "" {
		return
	}
	var in struct {
		Event            string `json:"hook_event_name"`
		SessionID        string `json:"session_id"`
		NotificationType string `json:"notification_type"`
		Message          string `json:"message"`
	}
	data, _ := io.ReadAll(io.LimitReader(os.Stdin, 4<<20))
	if json.Unmarshal(data, &in) != nil || in.Event == "" {
		return
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		c, err := client.Dial(sock, "conch-hook")
		if err != nil {
			return
		}
		defer c.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = c.Call(ctx, proto.MethodAgentReport, proto.AgentReportParams{
			ID: paneID, Agent: "claude", Event: in.Event,
			NotificationType: in.NotificationType, Message: in.Message, SessionID: in.SessionID,
		}, nil)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
	}
}
