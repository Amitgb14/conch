package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
)

// statusInput is the part of Claude Code's status line input conch uses.
type statusInput struct {
	SessionID string `json:"session_id"`
	Cwd       string `json:"cwd"`
	Workspace struct {
		ProjectDir string `json:"project_dir"`
	} `json:"workspace"`
	ContextWindow struct {
		TotalInput int `json:"total_input_tokens"`
		Size       int `json:"context_window_size"`
	} `json:"context_window"`
	RateLimits map[string]*struct {
		UsedPercentage float64 `json:"used_percentage"`
		ResetsAt       float64 `json:"resets_at"`
	} `json:"rate_limits"`
}

// runClaudeStatus is conch's Claude status line: it reports plan limits and
// the context window to the server, and prints the user's own status line.
func runClaudeStatus() {
	data, _ := io.ReadAll(io.LimitReader(os.Stdin, 4<<20))
	var in statusInput
	_ = json.Unmarshal(data, &in)

	done := make(chan struct{})
	go func() {
		defer close(done)
		reportStatus(in)
	}()
	if cmd := userStatusLine(in); cmd != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		c := exec.CommandContext(ctx, "sh", "-c", cmd)
		c.Stdin, c.Stdout, c.Stderr = bytes.NewReader(data), os.Stdout, io.Discard
		_ = c.Run()
		cancel()
	}
	select {
	case <-done:
	case <-time.After(time.Second):
	}
}

func reportStatus(in statusInput) {
	paneID, sock := os.Getenv("CONCH_PANE_ID"), os.Getenv("CONCH_SOCKET")
	if paneID == "" || sock == "" {
		return
	}
	params := statusParams(paneID, in, time.Now())
	c, err := client.Dial(sock, "conch-status")
	if err != nil {
		return
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = c.Call(ctx, proto.MethodAgentReport, params, nil)
}

// statusParams turns status line input into a report.
func statusParams(paneID string, in statusInput, now time.Time) proto.AgentReportParams {
	params := proto.AgentReportParams{ID: paneID, Agent: "claude", Event: "StatusLine", SessionID: in.SessionID,
		ContextUsed: in.ContextWindow.TotalInput, ContextSize: in.ContextWindow.Size}
	if len(in.RateLimits) > 0 {
		l := &proto.PlanLimits{Agent: "claude", At: now}
		window := func(key string) *proto.LimitWindow {
			w := in.RateLimits[key]
			if w == nil {
				return nil
			}
			lw := &proto.LimitWindow{UsedPct: w.UsedPercentage}
			if w.ResetsAt > 0 {
				lw.ResetsAt = time.Unix(int64(w.ResetsAt), 0)
			}
			return lw
		}
		l.FiveHour, l.Week, l.Spend = window("five_hour"), window("seven_day"), window("spend_limit")
		params.Limits = l
	}
	return params
}

// userStatusLine finds the status line the user configured, which conch's
// own setting overrides: project local, project, then user settings.
func userStatusLine(in statusInput) string {
	project := in.Workspace.ProjectDir
	if project == "" {
		project = in.Cwd
	}
	userDir := os.Getenv("CLAUDE_CONFIG_DIR")
	if userDir == "" {
		home, _ := os.UserHomeDir()
		userDir = filepath.Join(home, ".claude")
	}
	var files []string
	if project != "" {
		files = append(files, filepath.Join(project, ".claude", "settings.local.json"), filepath.Join(project, ".claude", "settings.json"))
	}
	files = append(files, filepath.Join(userDir, "settings.json"))
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var s struct {
			StatusLine *struct {
				Type    string `json:"type"`
				Command string `json:"command"`
			} `json:"statusLine"`
		}
		if json.Unmarshal(b, &s) != nil || s.StatusLine == nil || s.StatusLine.Type != "command" {
			continue
		}
		if cmd := strings.TrimSpace(s.StatusLine.Command); cmd != "" && !strings.Contains(cmd, "report claude-status") {
			return cmd
		}
	}
	return ""
}
