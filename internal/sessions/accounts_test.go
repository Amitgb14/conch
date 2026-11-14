package sessions

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Every account's folder is read, and each session says whose it is: the
// agent's own folder "", an account's by its name.
func TestSessionsOfEveryAccount(t *testing.T) {
	home := t.TempDir()
	work := filepath.Join(t.TempDir(), "claude-work")
	codexWork := filepath.Join(t.TempDir(), "codex-work")
	env := Env{Home: home, Getenv: func(string) string { return "" }, Accounts: []Account{
		{Agent: "claude", Name: "work", Dir: work},
		{Agent: "codex", Name: "work", Dir: codexWork},
		{Agent: "claude", Name: "gone", Dir: filepath.Join(home, "no-such-folder")},
	}}
	line := func(text string) string {
		return `{"type":"user","cwd":"/src/api","message":{"role":"user","content":"` + text + `"}}` + "\n"
	}
	write(t, filepath.Join(home, ".claude", "projects", "-src-api", "own.jsonl"), line("mine"))
	write(t, filepath.Join(work, "projects", "-src-api", "w1.jsonl"), line("theirs"))
	codexLine := func(id string) string {
		return `{"type":"session_meta","payload":{"id":"` + id + `","cwd":"/src/api","timestamp":"2026-09-02T10:00:00Z"}}
{"type":"event_msg","payload":{"type":"user_message","message":"hi"}}
`
	}
	write(t, filepath.Join(home, ".codex", "sessions", "2026", "09", "02", "rollout-a-c1.jsonl"), codexLine("c1"))
	write(t, filepath.Join(codexWork, "sessions", "2026", "09", "02", "rollout-b-c2.jsonl"), codexLine("c2"))
	now := time.Now()
	for _, p := range []string{filepath.Join(home, ".claude", "projects", "-src-api", "own.jsonl"), filepath.Join(work, "projects", "-src-api", "w1.jsonl")} {
		os.Chtimes(p, now, now)
	}

	got := map[string]string{}
	for _, s := range List(env, []string{"/src/api"}, 0) {
		got[s.Agent+":"+s.ID] = s.Account
	}
	want := map[string]string{"claude:own": "", "claude:w1": "work", "codex:c1": "", "codex:c2": "work"}
	if len(got) != len(want) {
		t.Fatalf("sessions %v, want %v", got, want)
	}
	for k, v := range want {
		if a, ok := got[k]; !ok || a != v {
			t.Errorf("%s: account %q (listed %v), want %q", k, a, ok, v)
		}
	}

	// An account whose folder is the agent's own is not read twice.
	env.Accounts = []Account{{Agent: "claude", Name: "same", Dir: filepath.Join(home, ".claude")}}
	n := 0
	for _, s := range List(env, []string{"/src/api"}, 0) {
		if s.Agent == "claude" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("the agent's own folder named as an account listed %d sessions", n)
	}
}
