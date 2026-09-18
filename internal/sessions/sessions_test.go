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

func TestOpenCodeSQLiteCost(t *testing.T) {
	bin, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("no sqlite3")
	}
	home := t.TempDir()
	db := filepath.Join(home, ".local", "share", "opencode", "opencode.db")
	os.MkdirAll(filepath.Dir(db), 0o755)
	// A newer store, with what each session used.
	schema := `CREATE TABLE session (id text primary key, parent_id text, directory text, title text, time_created integer, time_updated integer, time_archived integer, cost real, tokens_output integer);
INSERT INTO session VALUES ('ses_a',NULL,'/src/api','Add login',1000,2000,NULL,0.42,1234);
INSERT INTO session VALUES ('ses_b',NULL,'/src/api','No usage yet',1000,1500,NULL,0,0);`
	if out, err := exec.Command(bin, db, schema).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	ss := List(Env{Home: home, Getenv: func(string) string { return "" }}, []string{"/src/api"}, 0)
	if len(ss) != 2 {
		t.Fatalf("sessions: %+v", ss)
	}
	if ss[0].Cost != 0.42 || ss[0].Output != 1234 {
		t.Fatalf("with usage: %+v", ss[0])
	}
	if ss[1].Cost != 0 || ss[1].Output != 0 {
		t.Fatalf("without usage: %+v", ss[1])
	}
}

// TestSessionUsage covers what each store says a session used: Claude's
// running cost, Codex's tokens, and stores that say nothing.
func TestSessionUsage(t *testing.T) {
	home := t.TempDir()
	env := Env{Home: home, Getenv: func(string) string { return "" }}
	cdir := filepath.Join(home, ".claude", "projects", "-src-api")
	write(t, filepath.Join(cdir, "paid.jsonl"),
		`{"type":"user","cwd":"/src/api","message":{"role":"user","content":"Fix the login test"}}
{"type":"cost-state","totalCostUSD":0.12}
{"type":"assistant","cwd":"/src/api","message":{"content":[{"type":"text","text":"ok"}]}}
{"type":"cost-state","totalCostUSD":0.37}
`)
	write(t, filepath.Join(cdir, "free.jsonl"),
		`{"type":"user","cwd":"/src/api","message":{"role":"user","content":"Nothing costed yet"}}`+"\n")
	// Codex: the last token_count carries the total.
	write(t, filepath.Join(home, ".codex", "sessions", "2026", "09", "02", "rollout-2026-09-02T10-00-00-c9.jsonl"),
		`{"type":"session_meta","payload":{"id":"c9","cwd":"/src/api","timestamp":"2026-09-02T10:00:00Z"}}
{"type":"event_msg","payload":{"type":"user_message","message":"Add rate limiting"}}
{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":900,"output_tokens":120}}}}
{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":4000,"output_tokens":830}}}}
`)
	// Gemini records neither.
	write(t, filepath.Join(home, ".gemini", "projects.json"), `{"projects":{"/src/api":"api"}}`)
	write(t, filepath.Join(home, ".gemini", "tmp", "api", "chats", "session-2026-09-03-g9.json"),
		`{"sessionId":"g9","startTime":"2026-09-03T10:00:00Z","lastUpdated":"2026-09-03T11:00:00Z","messages":[{"type":"user","content":[{"text":"Explain the auth flow"}]}]}`)

	got := map[string]Session{}
	for _, s := range List(env, []string{"/src/api"}, 0) {
		got[s.ID] = s
	}
	if s := got["paid"]; s.Cost != 0.37 || s.Output != 0 {
		t.Errorf("claude with a cost: %+v", s)
	}
	if s := got["free"]; s.Cost != 0 {
		t.Errorf("claude without one: %+v", s)
	}
	if s := got["c9"]; s.Output != 830 || s.Cost != 0 {
		t.Errorf("codex: %+v", s)
	}
	if s := got["g9"]; s.Cost != 0 || s.Output != 0 {
		t.Errorf("gemini: %+v", s)
	}
}

// TestClaudeCostInTheTail covers a long session, where the cost lines are
// past the first lines read.
func TestClaudeCostInTheTail(t *testing.T) {
	home := t.TempDir()
	env := Env{Home: home, Getenv: func(string) string { return "" }}
	body := `{"type":"user","cwd":"/src/api","message":{"role":"user","content":"Long one"}}` + "\n"
	filler := `{"type":"assistant","cwd":"/src/api","message":{"content":[{"type":"text","text":"` + strings.Repeat("x", 900) + `"}]}}` + "\n"
	for len(body) < 400*1024 {
		body += filler
	}
	body += `{"type":"cost-state","totalCostUSD":2.5}` + "\n"
	write(t, filepath.Join(home, ".claude", "projects", "-src-api", "long.jsonl"), body)
	ss := List(env, []string{"/src/api"}, 0)
	if len(ss) != 1 || ss[0].Cost != 2.5 {
		t.Fatalf("tail cost: %+v", ss)
	}
}

// TestCostFarFromTheEnd covers what Claude Code actually writes: a handful
// of cost lines in a large transcript, none of them near the end.
func TestCostFarFromTheEnd(t *testing.T) {
	home := t.TempDir()
	env := Env{Home: home, Getenv: func(string) string { return "" }}
	path := filepath.Join(home, ".claude", "projects", "-src-api", "big.jsonl")
	body := `{"type":"user","cwd":"/src/api","message":{"role":"user","content":"A long conversation"}}` + "\n"
	body += `{"type":"cost-state","totalCostUSD":12.5}` + "\n"
	body += `{"type":"cost-state","totalCostUSD":48.25}` + "\n"
	filler := `{"type":"assistant","cwd":"/src/api","message":{"content":[{"type":"text","text":"` + strings.Repeat("y", 4000) + `"}]}}` + "\n"
	for len(body) < 700*1024 { // the cost lines end up far outside any tail
		body += filler
	}
	write(t, path, body)
	ss := List(env, []string{"/src/api"}, 0)
	if len(ss) != 1 || ss[0].Cost != 48.25 {
		t.Fatalf("cost far from the end: %+v", ss)
	}

	// Appending more only reads the new bytes, and picks up a newer total.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(filler + `{"type":"cost-state","totalCostUSD":60}` + "\n")
	f.Close()
	if ss := List(env, []string{"/src/api"}, 0); ss[0].Cost != 60 {
		t.Fatalf("after appending: %+v", ss)
	}

	// A file replaced by a shorter one is read again from the start.
	write(t, path, `{"type":"user","cwd":"/src/api","message":{"role":"user","content":"Fresh start"}}`+"\n"+
		`{"type":"cost-state","totalCostUSD":0.75}`+"\n")
	if ss := List(env, []string{"/src/api"}, 0); ss[0].Cost != 0.75 {
		t.Fatalf("after replacing: %+v", ss)
	}
}
