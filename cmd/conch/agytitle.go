package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
)

// conch report agy-title is the title command conch starts Antigravity CLI
// with (its --settings). agy runs it whenever its state changes, and every
// few seconds besides, with its session as JSON on stdin — the one place it
// names its conversation, beside its context window and its quota — and
// sets the terminal's title from what it prints. So it reports those to
// the server and then runs the person's own title command, if they have
// one, so their title is as it was. Like every report it prints nothing
// of its own, always exits 0 and gives up quickly.
func runAgyTitle() {
	data, _ := io.ReadAll(io.LimitReader(os.Stdin, 4<<20))
	done := make(chan struct{})
	go func() {
		defer close(done)
		paneID, sock := os.Getenv("CONCH_PANE_ID"), os.Getenv("CONCH_SOCKET")
		if paneID == "" || sock == "" {
			return
		}
		c, err := client.Dial(sock, "conch-status")
		if err != nil {
			return
		}
		defer c.Close()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = c.Call(ctx, proto.MethodAgentReport, agyTitleParams(paneID, data, time.Now()), nil)
	}()
	runUserAgyTitle(data, os.Stdout)
	select {
	case <-done:
	case <-time.After(time.Second):
	}
}

// agyTitleParams turns what agy gave its title command into a report.
func agyTitleParams(paneID string, data []byte, now time.Time) proto.AgentReportParams {
	var in struct {
		ConversationID string `json:"conversation_id"`
		ContextWindow  struct {
			Size    int     `json:"context_window_size"`
			UsedPct float64 `json:"used_percentage"`
		} `json:"context_window"`
		Quota map[string]struct {
			Remaining  float64 `json:"remaining_fraction"`
			ResetTime  string  `json:"reset_time"`
			ResetInSec float64 `json:"reset_in_seconds"`
		} `json:"quota"`
	}
	_ = json.Unmarshal(data, &in)
	p := proto.AgentReportParams{ID: paneID, Agent: "antigravity", Event: "StatusLine", SessionID: in.ConversationID}
	if in.ContextWindow.Size > 0 {
		p.ContextSize = in.ContextWindow.Size
		p.ContextUsed = int(math.Round(in.ContextWindow.UsedPct * float64(in.ContextWindow.Size) / 100))
	}
	keys := make([]string, 0, len(in.Quota))
	for k := range in.Quota {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var windows []proto.NamedWindow
	for _, k := range keys {
		q := in.Quota[k]
		at, err := time.Parse(time.RFC3339, q.ResetTime)
		// A bucket that has never been drawn on says 1970 and nothing
		// left to reset: no window to speak of.
		if err != nil || at.Unix() <= 0 || q.ResetInSec <= 0 {
			continue
		}
		windows = append(windows, proto.NamedWindow{Key: k, UsedPct: math.Max(0, (1-q.Remaining)*100), ResetsAt: at})
	}
	if len(windows) > 0 {
		p.Limits = &proto.PlanLimits{Agent: "antigravity", At: now, Windows: windows}
	}
	return p
}

// runUserAgyTitle runs the title command in the person's own agy
// settings, if there is one, with the same input, and passes on what it
// prints: conch's --settings took its place for this session.
func runUserAgyTitle(data []byte, out io.Writer) {
	home, _ := os.UserHomeDir()
	b, err := os.ReadFile(filepath.Join(home, ".gemini", "antigravity-cli", "settings.json"))
	if err != nil {
		return
	}
	var s struct {
		Title json.RawMessage `json:"title"`
	}
	if json.Unmarshal(b, &s) != nil || len(s.Title) == 0 {
		return
	}
	var t struct {
		Command string `json:"command"`
		Enabled *bool  `json:"enabled"`
	}
	// agy reads a boolean as {"enabled": …} with no command: nothing runs.
	if json.Unmarshal(s.Title, &t) != nil || (t.Enabled != nil && !*t.Enabled) {
		return
	}
	cmd := strings.TrimSpace(t.Command)
	if cmd == "" || strings.Contains(cmd, "report agy-title") {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, "sh", "-c", cmd)
	c.Stdin, c.Stdout, c.Stderr = bytes.NewReader(data), out, io.Discard
	_ = c.Run()
}
