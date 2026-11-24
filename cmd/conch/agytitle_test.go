package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// What agy 1.5.16 gave its title command in a real session, its account
// fields left out.
const agyTitleJSON = `{
  "cwd": "/w/proj", "session_id": "3dde1bcf-073d-4944-8387-a0d6386f32c0",
  "conversation_id": "3dde1bcf-073d-4944-8387-a0d6386f32c0",
  "model": {"id": "Gemini 4 Hydrogen (High)", "display_name": "Gemini 4 Hydrogen (High)", "effort": "high"},
  "context_window": {"total_input_tokens": 17967, "total_output_tokens": 123, "context_window_size": 1048576,
    "used_percentage": 1.7134666442871094, "remaining_percentage": 98.28653335571289,
    "current_usage": {"input_tokens": 2239, "output_tokens": 24, "cache_creation_input_tokens": 0, "cache_read_input_tokens": 10316}},
  "product": "antigravity",
  "quota": {
    "3p-5h": {"remaining_fraction": 1, "reset_time": "1970-01-01T00:00:00Z", "reset_in_seconds": 0},
    "gemini-weekly": {"remaining_fraction": 0.9816392, "reset_time": "2026-12-01T19:30:28Z", "reset_in_seconds": 602915}
  },
  "agent_state": "idle", "plan_tier": "Antigravity Starter Quota", "terminal_width": 80
}`

func TestAgyTitleParams(t *testing.T) {
	now := time.Date(2026, 11, 24, 19, 30, 0, 0, time.UTC)
	p := agyTitleParams("p7", []byte(agyTitleJSON), now)
	if p.ID != "p7" || p.Agent != "antigravity" || p.Event != "StatusLine" || p.SessionID != "3dde1bcf-073d-4944-8387-a0d6386f32c0" {
		t.Fatalf("%+v", p)
	}
	// The context in use is the share agy gives of its window.
	if p.ContextSize != 1048576 || p.ContextUsed != 17967 {
		t.Errorf("context %d of %d", p.ContextUsed, p.ContextSize)
	}
	// Its quota, as used rather than left; a bucket that has never reset
	// (the 1970 one) says nothing and is left out.
	if p.Limits == nil || len(p.Limits.Windows) != 1 {
		t.Fatalf("limits %+v", p.Limits)
	}
	w := p.Limits.Windows[0]
	if w.Key != "gemini-weekly" || w.UsedPct < 1.83 || w.UsedPct > 1.84 || !w.ResetsAt.Equal(time.Date(2026, 12, 1, 19, 30, 28, 0, time.UTC)) {
		t.Errorf("window %+v", w)
	}
	// Before a conversation starts, nothing to name and no window yet.
	p = agyTitleParams("p7", []byte(`{"conversation_id": "", "context_window": {"context_window_size": 0}}`), now)
	if p.SessionID != "" || p.ContextSize != 0 || p.Limits != nil {
		t.Errorf("before: %+v", p)
	}
	if p := agyTitleParams("p7", []byte("not json"), now); p.Event != "StatusLine" || p.SessionID != "" {
		t.Errorf("not json: %+v", p)
	}
}

// The person's own title command still sets the title: it is given the
// same input and what it prints is the title. Without one conch prints
// nothing, and conch's own never runs itself.
func TestAgyUserTitle(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	settings := filepath.Join(home, ".gemini", "antigravity-cli", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	runUserAgyTitle([]byte(agyTitleJSON), &out)
	if out.Len() != 0 {
		t.Fatalf("no settings: %q", out.String())
	}
	for body, want := range map[string]string{
		`{"title": {"command": "printf 'T:'; grep -o '\"agent_state\": \"[a-z]*\"'"}}`: `T:"agent_state": "idle"`,
		`{"title": {"command": "echo mine", "enabled": false}}`:                        "",
		`{"title": true}`: "",
		`{"title": {"command": "/x/conch report agy-title"}}`: "",
		`not json`: "",
		`{"trustedWorkspaces": ["/w"], "title": {"type": "command", "command": "echo two; echo l"}}`: "two\nl",
	} {
		if err := os.WriteFile(settings, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		out.Reset()
		runUserAgyTitle([]byte(agyTitleJSON), &out)
		if strings.TrimSpace(out.String()) != want {
			t.Errorf("%s: printed %q, want %q", body, out.String(), want)
		}
	}
}
