package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const sampleStatus = `{"session_id":"s1","cwd":"/src/api","workspace":{"project_dir":"%s"},
 "context_window":{"total_input_tokens":45210,"context_window_size":200000,"used_percentage":22.6},
 "rate_limits":{"five_hour":{"used_percentage":41.7,"resets_at":1789340400},"seven_day":{"used_percentage":18,"resets_at":1789700000}}}`

func TestStatusParams(t *testing.T) {
	var in statusInput
	if err := json.Unmarshal([]byte(sampleStatus), &in); err != nil {
		t.Fatal(err)
	}
	p := statusParams("p1", in, time.Unix(1, 0))
	if p.Event != "StatusLine" || p.ContextUsed != 45210 || p.ContextSize != 200000 || p.Limits == nil {
		t.Fatalf("%+v", p)
	}
	if p.Limits.FiveHour.UsedPct != 41.7 || !p.Limits.FiveHour.ResetsAt.Equal(time.Unix(1789340400, 0)) || p.Limits.Week.UsedPct != 18 || p.Limits.Spend != nil {
		t.Fatalf("limits %+v %+v", p.Limits.FiveHour, p.Limits.Week)
	}
	var none statusInput
	json.Unmarshal([]byte(`{"session_id":"s2"}`), &none)
	if statusParams("p1", none, time.Now()).Limits != nil {
		t.Fatal("no rate_limits (API key users) must report no limits")
	}
}

func TestUserStatusLine(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	project := t.TempDir()
	put := func(path, cmd string) {
		os.MkdirAll(filepath.Dir(path), 0o755)
		b, _ := json.Marshal(map[string]any{"statusLine": map[string]any{"type": "command", "command": cmd}})
		os.WriteFile(path, b, 0o644)
	}
	in := statusInput{Cwd: project}
	in.Workspace.ProjectDir = project
	if userStatusLine(in) != "" {
		t.Fatal("no settings, no status line")
	}
	put(filepath.Join(home, ".claude", "settings.json"), "~/bin/mystatus")
	if got := userStatusLine(in); got != "~/bin/mystatus" {
		t.Fatalf("user: %q", got)
	}
	put(filepath.Join(project, ".claude", "settings.json"), "project-status")
	if got := userStatusLine(in); got != "project-status" {
		t.Fatalf("project wins: %q", got)
	}
	put(filepath.Join(project, ".claude", "settings.local.json"), "/x/conch report claude-status")
	if got := userStatusLine(in); got != "project-status" {
		t.Fatalf("conch's own command must be skipped: %q", got)
	}
}

// TestStatusParamsCarriesEveryWindow: an account with a per-model
// allowance reports more windows than the three conch grew up with, and
// they are passed on under the agent's own names rather than dropped.
func TestStatusParamsCarriesEveryWindow(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	in := statusInput{}
	in.RateLimits = map[string]*struct {
		UsedPercentage float64 `json:"used_percentage"`
		ResetsAt       float64 `json:"resets_at"`
	}{
		"five_hour":       {UsedPercentage: 9, ResetsAt: float64(now.Add(time.Hour).Unix())},
		"seven_day":       {UsedPercentage: 38},
		"seven_day_fable": {UsedPercentage: 0},
		"spend_limit":     {UsedPercentage: 3},
	}
	p := statusParams("p1", in, now)
	if p.Limits == nil {
		t.Fatal("no limits reported")
	}
	// The three conch has always sent are still there, for older servers.
	if p.Limits.FiveHour == nil || p.Limits.Week == nil || p.Limits.Spend == nil {
		t.Fatalf("the known windows went missing: %+v", p.Limits)
	}
	// And every one the agent named, in a settled order.
	var keys []string
	for _, w := range p.Limits.Windows {
		keys = append(keys, w.Key)
	}
	if got := strings.Join(keys, ","); got != "five_hour,seven_day,seven_day_fable,spend_limit" {
		t.Fatalf("named windows: %q", got)
	}
	for _, w := range p.Limits.Windows {
		if w.Key == "five_hour" && !w.ResetsAt.Equal(now.Add(time.Hour)) {
			t.Fatalf("reset time lost: %v", w.ResetsAt)
		}
		if w.Key == "seven_day" && !w.ResetsAt.IsZero() {
			t.Fatalf("a window with no reset time invented one: %v", w.ResetsAt)
		}
	}
	// No rate limits at all: nothing is reported rather than an empty one.
	if p := statusParams("p1", statusInput{}, now); p.Limits != nil {
		t.Fatalf("limits out of nothing: %+v", p.Limits)
	}
}
