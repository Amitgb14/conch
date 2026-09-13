package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
)

func runAgent(args []string) error {
	switch {
	case len(args) == 1 && args[0] == "status":
		return agentStatus()
	case len(args) == 2 && args[0] == "install":
		return agentInstall(args[1])
	case len(args) >= 1 && args[0] == "setup":
		return agentSetup(args[1:])
	case len(args) != 2 || args[0] != "explain":
		return errors.New("usage: conch agent explain ID | status | install NAME | setup [-agent NAME] [-copy] [DIR]")
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

// runReport forwards an agent event to the server owning this pane. It
// runs inside the agent's hook or plugin machinery, so it prints nothing
// (some agents read hook output as instructions), always exits 0, and gives
// up quickly. Outside a conch pane it does nothing.
//
//	conch report claude-hook | gemini-hook   (hook JSON on stdin)
//	conch report opencode EVENT              (from conch's OpenCode plugin)
func runReport(args []string) {
	paneID, sock := os.Getenv("CONCH_PANE_ID"), os.Getenv("CONCH_SOCKET")
	if paneID == "" || sock == "" || len(args) == 0 {
		return
	}
	params := proto.AgentReportParams{ID: paneID}
	switch {
	case args[0] == "opencode" && len(args) >= 2:
		params.Agent, params.Event = "opencode", args[1]
	case args[0] == "claude-hook" || args[0] == "gemini-hook":
		var in struct {
			Event            string `json:"hook_event_name"`
			SessionID        string `json:"session_id"`
			NotificationType string `json:"notification_type"`
			Message          string `json:"message"`
			TranscriptPath   string `json:"transcript_path"`
		}
		data, _ := io.ReadAll(io.LimitReader(os.Stdin, 4<<20))
		if json.Unmarshal(data, &in) != nil || in.Event == "" {
			return
		}
		params = proto.AgentReportParams{
			ID: paneID, Agent: strings.TrimSuffix(args[0], "-hook"), Event: in.Event,
			NotificationType: in.NotificationType, Message: in.Message, SessionID: in.SessionID,
		}
		if params.Agent == "claude" { // Claude's transcript format is the one usage reads
			params.TranscriptPath = in.TranscriptPath
		}
	default:
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
		_ = c.Call(ctx, proto.MethodAgentReport, params, nil)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
	}
}

func agentSetup(args []string) error {
	fs := flag.NewFlagSet("agent setup", flag.ContinueOnError)
	agent := fs.String("agent", "", "only this agent (claude, codex, gemini, opencode)")
	copyFiles := fs.Bool("copy", false, "copy missing local files from the main checkout first")
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	dir := "."
	if fs.NArg() > 0 {
		dir = fs.Arg(0)
	}
	if machineFlag == "" {
		if abs, err := filepath.Abs(dir); err == nil {
			dir = abs
		}
	}
	c, err := connect(true)
	if err != nil {
		return err
	}
	defer c.Close()
	params := proto.AgentSetupParams{Dir: dir, Agent: *agent}
	var res proto.AgentSetupResult
	if err := call(c, proto.MethodAgentSetup, params, &res); err != nil {
		return err
	}
	if *copyFiles {
		if res.Main == "" {
			return fmt.Errorf("%s is not a linked worktree of a project", res.Dir)
		}
		var cr proto.WorktreeFilesResult
		if err := call(c, proto.MethodWorktreeFiles, proto.WorktreeFilesParams{ProjectID: res.ProjectID, Path: res.Worktree}, &cr); err != nil {
			return err
		}
		for _, f := range cr.Copied {
			fmt.Printf("copied %s\n", f)
		}
		res = proto.AgentSetupResult{}
		if err := call(c, proto.MethodAgentSetup, params, &res); err != nil {
			return err
		}
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(res)
	}
	printSetup(os.Stdout, res)
	return nil
}

func printSetup(w io.Writer, res proto.AgentSetupResult) {
	fmt.Fprintf(w, "%s\n", res.Dir)
	if res.Main != "" {
		fmt.Fprintf(w, "worktree of %s\n", res.Main)
		if len(res.LocalFiles) > 0 {
			fmt.Fprintln(w, "\nLocal files (from the main checkout)")
			for _, f := range res.LocalFiles {
				fmt.Fprintf(w, "  %-8s %s\n", f.State, f.Path)
			}
		}
	}
	for _, a := range res.Agents {
		fmt.Fprintf(w, "\n== %s ==\n", a.Label)
		for _, n := range a.Notes {
			fmt.Fprintf(w, "  ! %s\n", n)
		}
		for _, g := range a.Groups {
			fmt.Fprintf(w, "  %s\n", g.Title)
			for _, it := range g.Items {
				mark := " "
				if it.Missing {
					mark = "✗"
				}
				line := fmt.Sprintf("   %s %-28s %-9s", mark, it.Name, it.Scope)
				if it.Detail != "" {
					line += " " + it.Detail
				}
				if it.Missing {
					line += " (only in the main checkout)"
				}
				fmt.Fprintln(w, strings.TrimRight(line, " "))
			}
		}
	}
}
