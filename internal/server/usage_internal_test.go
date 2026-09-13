package server

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFindSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")
	dir := "/src/api"
	write := func(name, id string, age time.Duration) {
		p := filepath.Join(home, ".codex", "sessions", "2026", "09", "13", name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(`{"type":"session_meta","payload":{"id":"`+id+`","cwd":"`+dir+`"}}
{"type":"event_msg","payload":{"type":"user_message","message":"hi"}}
`), 0o644)
		when := time.Now().Add(-age)
		os.Chtimes(p, when, when)
	}
	write("rollout-a.jsonl", "old", 3*time.Hour)
	write("rollout-b.jsonl", "mine", time.Minute)

	started := time.Now().Add(-10 * time.Minute)
	s := findSession("codex", dir, started)
	if s == nil || s.ID != "mine" || filepath.Base(s.Path) != "rollout-b.jsonl" {
		t.Fatalf("found %+v", s)
	}
	if findSession("codex", dir, time.Now().Add(time.Hour)) != nil {
		t.Fatal("a session older than the pane must not be picked")
	}
	if findSession("opencode", dir, started) != nil {
		t.Fatal("wrong agent")
	}
}
