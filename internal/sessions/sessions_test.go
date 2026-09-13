package sessions

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestStores(t *testing.T) {
	home := t.TempDir()
	env := Env{Home: home, Getenv: func(string) string { return "" }}
	repo := "/src/api"
	wt := "/src/api.worktrees/fix-login"

	// Claude: a session in the repo (with an AI title in the tail), one in a
	// subfolder, one in a sibling folder sharing the prefix, and one where
	// nothing was said.
	cdir := filepath.Join(home, ".claude", "projects")
	write(t, filepath.Join(cdir, "-src-api", "s1.jsonl"),
		`{"type":"user","cwd":"/src/api","gitBranch":"main","timestamp":"2026-09-01T10:00:00Z","isMeta":true,"message":{"role":"user","content":"<command-name>/clear</command-name>"}}
{"type":"user","cwd":"/src/api","gitBranch":"main","timestamp":"2026-09-01T10:00:01Z","message":{"role":"user","content":[{"type":"text","text":"Fix the   flaky\nlogin test"}]}}
{"type":"assistant","cwd":"/src/api","message":{"content":[{"type":"text","text":"ok"}]}}
{"type":"ai-title","aiTitle":"Flaky login test","sessionId":"s1"}
`)
	write(t, filepath.Join(cdir, "-src-api-web", "s2.jsonl"),
		`{"type":"user","cwd":"/src/api/web","gitBranch":"main","message":{"role":"user","content":"Style the header"}}`+"\n")
	write(t, filepath.Join(cdir, "-src-api-v2", "s3.jsonl"),
		`{"type":"user","cwd":"/src/api-v2","message":{"role":"user","content":"not this project"}}`+"\n")
	write(t, filepath.Join(cdir, "-src-api", "empty.jsonl"), `{"type":"mode","mode":"normal"}`+"\n")

	// Codex, in the worktree.
	write(t, filepath.Join(home, ".codex", "sessions", "2026", "09", "02", "rollout-2026-09-02T10-00-00-c1.jsonl"),
		`{"type":"session_meta","payload":{"id":"c1","cwd":"/src/api.worktrees/fix-login","timestamp":"2026-09-02T10:00:00Z","git":{"branch":"fix-login"}}}
{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>…"}]}}
{"type":"event_msg","payload":{"type":"user_message","message":"Add rate limiting"}}
`)

	// Gemini, keyed through projects.json.
	write(t, filepath.Join(home, ".gemini", "projects.json"), `{"projects":{"/src/api":"api"}}`)
	write(t, filepath.Join(home, ".gemini", "tmp", "api", "chats", "session-2026-09-03-g1.json"),
		`{"sessionId":"g1","startTime":"2026-09-03T10:00:00Z","lastUpdated":"2026-09-03T11:00:00Z","messages":[{"type":"user","content":[{"text":"Explain the auth flow"}]}]}`)

	// OpenCode, the older file store.
	write(t, filepath.Join(home, ".local", "share", "opencode", "storage", "session", "p1", "ses_o1.json"),
		`{"id":"ses_o1","directory":"/src/api","title":"Refactor config","time":{"created":1756900000000,"updated":1756990000000}}`)
	write(t, filepath.Join(home, ".local", "share", "opencode", "storage", "session", "p1", "ses_child.json"),
		`{"id":"ses_child","parentID":"ses_o1","directory":"/src/api","title":"subtask","time":{"created":1,"updated":1}}`)

	now := time.Now()
	for _, p := range []string{"-src-api/s1.jsonl", "-src-api-web/s2.jsonl", "-src-api-v2/s3.jsonl"} {
		os.Chtimes(filepath.Join(cdir, p), now, now)
	}

	got := map[string]Session{}
	for _, s := range List(env, []string{repo, wt}, 0) {
		got[s.Agent+":"+s.ID] = s
	}
	if s := got["claude:s1"]; s.Title != "Flaky login test" || s.Dir != repo || s.Branch != "main" {
		t.Errorf("claude s1: %+v", s)
	}
	if s := got["claude:s2"]; s.Dir != "/src/api/web" || s.Title != "Style the header" {
		t.Errorf("claude session in a subfolder: %+v", s)
	}
	if _, ok := got["claude:s3"]; ok {
		t.Error("a sibling folder sharing the prefix was listed")
	}
	if _, ok := got["claude:empty"]; ok {
		t.Error("a session with nothing said was listed")
	}
	if s := got["codex:c1"]; s.Dir != wt || s.Title != "Add rate limiting" || s.Branch != "fix-login" {
		t.Errorf("codex: %+v", s)
	}
	if s := got["gemini:g1"]; s.Dir != repo || s.Title != "Explain the auth flow" {
		t.Errorf("gemini: %+v", s)
	}
	{
		if s := got["opencode:ses_o1"]; s.Title != "Refactor config" {
			t.Errorf("opencode: %+v", s)
		}
	}
	if _, ok := got["opencode:ses_child"]; ok {
		t.Error("an opencode subtask session was listed")
	}
	if len(got) != 5 {
		t.Errorf("got %d sessions: %v", len(got), got)
	}
}

func TestOpenCodeSQLite(t *testing.T) {
	bin, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("no sqlite3")
	}
	home := t.TempDir()
	db := filepath.Join(home, ".local", "share", "opencode", "opencode.db")
	os.MkdirAll(filepath.Dir(db), 0o755)
	schema := `CREATE TABLE session (id text primary key, project_id text, parent_id text, slug text, directory text, title text, version text, time_created integer, time_updated integer, time_archived integer);
INSERT INTO session VALUES ('ses_a','p',NULL,'s','/src/it''s','Quote dir','1',1000,2000,NULL);
INSERT INTO session VALUES ('ses_b','p',NULL,'s','/src/it''s/sub','Sub dir','1',1000,3000,NULL);
INSERT INTO session VALUES ('ses_c','p',NULL,'s','/src/it''s2','Sibling','1',1000,3000,NULL);
INSERT INTO session VALUES ('ses_d','p',NULL,'s','/src/it''s','Archived','1',1000,3000,5);`
	if out, err := exec.Command(bin, db, schema).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	ss := List(Env{Home: home, Getenv: func(string) string { return "" }}, []string{"/src/it's"}, 0)
	var ids []string
	for _, s := range ss {
		ids = append(ids, s.ID)
	}
	if strings.Join(ids, ",") != "ses_b,ses_a" {
		t.Fatalf("sqlite sessions: %v", ids)
	}
}

func TestDelete(t *testing.T) {
	home := t.TempDir()
	env := Env{Home: home, Getenv: func(string) string { return "" }}
	dir := filepath.Join(home, ".claude", "projects", "-src-api")
	write(t, filepath.Join(dir, "s1.jsonl"), `{"type":"user","cwd":"/src/api","message":{"role":"user","content":"hi"}}`+"\n")
	write(t, filepath.Join(dir, "s1", "subagents", "a.jsonl"), "{}\n")
	write(t, filepath.Join(dir, "s2.jsonl"), `{"type":"user","cwd":"/src/api","message":{"role":"user","content":"keep"}}`+"\n")

	list := List(env, []string{"/src/api"}, 0)
	var s1 Session
	for _, s := range list {
		if s.ID == "s1" {
			s1 = s
		}
	}
	trash := filepath.Join(home, "trash")
	if err := Delete(context.Background(), env, s1, trash); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "s1.jsonl")); !os.IsNotExist(err) {
		t.Fatal("session file still there")
	}
	if _, err := os.Stat(filepath.Join(dir, "s1")); !os.IsNotExist(err) {
		t.Fatal("session directory still there")
	}
	moved, _ := filepath.Glob(filepath.Join(trash, "claude-*-s1*"))
	if len(moved) != 2 {
		t.Fatalf("trash has %v", moved)
	}
	if left := List(env, []string{"/src/api"}, 0); len(left) != 1 || left[0].ID != "s2" {
		t.Fatalf("remaining: %+v", left)
	}
}
