package main

import (
	"encoding/json"
	"os"
	"path/filepath"
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
