package sessions

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The fixtures below are shaped like the agents' own files on a Mac
// (pi 1.1.0, grok 1.0.77, cursor-agent 2026.10.22, kilo 7.8.18); the
// words are made up.

func piFile(id, cwd string, msgs ...string) string {
	out := []string{
		`{"type":"session","version":3,"id":"` + id + `","timestamp":"2026-10-09T18:30:54.041Z","cwd":"` + cwd + `"}`,
		`{"type":"thinking_level_change","id":"e2","parentId":null,"timestamp":"2026-10-09T18:30:54.082Z","thinkingLevel":"off"}`,
		`{"type":"message","id":"s1","timestamp":"2026-10-09T18:32:31.426Z","message":{"role":"system","content":"","sections":{"preamble":"You are"}}}`,
	}
	return strings.Join(append(out, msgs...), "\n") + "\n"
}

func piMsg(role, text string) string {
	return `{"type":"message","id":"m","timestamp":"2026-10-09T18:33:00.000Z","message":{"role":"` + role + `","content":[{"type":"text","text":"` + text + `"},{"type":"thinking","thinking":"hidden"}]}}`
}

func TestPiSessions(t *testing.T) {
	a6NoTools(t)
	home := t.TempDir()
	dir := filepath.Join(home, ".pi", "agent", "sessions", "--w-proj--")
	write(t, filepath.Join(dir, "2026-10-09T18-30-54-041Z_p1.jsonl"), piFile("p1", "/w/proj",
		piMsg("user", "<environment>ctx</environment>"), piMsg("user", "Rename   the flag"), piMsg("assistant", "Renamed."),
		`{"type":"message","message":{"role":"assistant","content":[],"stopReason":"error"}}`, `not json`))
	// Started and left: nothing was asked.
	write(t, filepath.Join(dir, "2026-10-09T18-40-00-000Z_p2.jsonl"), piFile("p2", "/w/proj"))
	// Elsewhere.
	write(t, filepath.Join(home, ".pi", "agent", "sessions", "--x--", "a_p3.jsonl"), piFile("p3", "/x", piMsg("user", "other")))
	// No session line.
	write(t, filepath.Join(dir, "b_p4.jsonl"), piMsg("user", "orphan")+"\n")

	env := a6Env(home, nil)
	got := List(env, []string{"/w/proj"}, 0)
	if a6IDs(got) != "pi:p1" || got[0].Title != "Rename the flag" || got[0].Dir != "/w/proj" {
		t.Fatalf("%s %+v", a6IDs(got), got)
	}
	turns, err := Transcript(context.Background(), env, got[0])
	if err != nil || turnsText(turns) != "user: Rename   the flag\nassistant: Renamed." {
		t.Fatalf("%q %v", turnsText(turns), err)
	}
	trash := t.TempDir()
	if err := Delete(context.Background(), env, got[0], trash); err != nil {
		t.Fatal(err)
	}
	if moved, _ := filepath.Glob(filepath.Join(trash, "pi-*-2026-10-09T18-30-54-041Z_p1.jsonl")); len(moved) != 1 {
		t.Fatalf("not in the trash: %v", moved)
	}
	if got := List(env, []string{"/w/proj"}, 0); len(got) != 0 {
		t.Fatalf("still listed: %s", a6IDs(got))
	}
}

func grokSession(t *testing.T, home, cwd, id, summary string, history ...string) string {
	t.Helper()
	dir := filepath.Join(home, ".grok", "sessions", strings.ReplaceAll(cwd, "/", "%2F"), id)
	write(t, filepath.Join(dir, "summary.json"), summary)
	write(t, filepath.Join(dir, "chat_history.jsonl"), strings.Join(history, "\n")+"\n")
	write(t, filepath.Join(dir, "updates.jsonl"), "{}\n")
	return dir
}

var grokHistory = []string{
	`{"type": "system", "content": "You are Grok"}`,
	`{"type": "user", "content": [{"type": "text", "text": "<user_info>\nOS Version: macos\n</user_info>"}]}`,
	`{"type": "user", "content": [{"type": "text", "text": "<system-reminder>\nskills\n</system-reminder>"}], "synthetic_reason": "system_reminder"}`,
	`{"type": "user", "content": [{"type": "text", "text": "<user_query>\nreview the parser\n</user_query>"}], "prompt_index": 0}`,
	`{"type": "reasoning", "content": null, "id": "r", "summary": []}`,
	`{"type": "assistant", "content": "", "tool_calls": [{"id": "c1"}], "model_id": "grok-4.7"}`,
	`{"type": "tool_result", "content": "file text", "tool_call_id": "c1"}`,
	`{"type": "assistant", "content": "It is fine.", "model_id": "grok-4.7"}`,
	`{"type": "user", "content": [{"type": "text", "text": "plain words"}], "prompt_index": 1}`,
	`{"type": "assistant", "content": "Yes."}`,
}

func TestGrokSessions(t *testing.T) {
	a6NoTools(t)
	home := t.TempDir()
	grokSession(t, home, "/w/proj", "g1", `{
  "info": {"id": "g1", "cwd": "/w/proj"},
  "generated_title": "Parser   Review",
  "session_summary": "A review",
  "created_at": "2026-10-09T11:31:46.606560Z",
  "updated_at": "2026-10-09T11:41:48.463382Z",
  "last_active_at": "2026-10-09T11:45:00.000000Z",
  "head_branch": "master"
}`, grokHistory...)
	// No title yet: the first question is the title; no info: the folder says.
	grokSession(t, home, "/w/proj", "g2", `{"updated_at": "2026-10-09T12:00:00Z"}`, grokHistory[3:]...)
	// Nothing asked yet.
	grokSession(t, home, "/w/proj", "g3", `{"info": {"id": "g3", "cwd": "/w/proj"}, "generated_title": "x"}`, grokHistory[:3]...)
	// Not a summary.
	grokSession(t, home, "/w/proj", "g4", `not json`, grokHistory...)

	env := a6Env(home, nil)
	got := map[string]Session{}
	for _, s := range List(env, []string{"/w/proj"}, 0) {
		got[s.ID] = s
	}
	g1, g2 := got["g1"], got["g2"]
	if len(got) != 2 || g1.Title != "Parser Review" || g1.Branch != "master" || g1.Dir != "/w/proj" || g2.Title != "review the parser" || g2.Dir != "/w/proj" {
		t.Fatalf("%+v", got)
	}
	if want := time.Date(2026, 10, 9, 11, 45, 0, 0, time.UTC); !g1.Updated.Equal(want) {
		t.Errorf("updated %v, want the last activity", g1.Updated)
	}
	turns, err := Transcript(context.Background(), env, g1)
	if want := "user: review the parser\nassistant: It is fine.\nuser: plain words\nassistant: Yes."; err != nil || turnsText(turns) != want {
		t.Fatalf("got:\n%s\nwant:\n%s %v", turnsText(turns), want, err)
	}
	trash := t.TempDir()
	if err := Delete(context.Background(), env, g1, trash); err != nil {
		t.Fatal(err)
	}
	if moved, _ := filepath.Glob(filepath.Join(trash, "grok-*-g1", "chat_history.jsonl")); len(moved) != 1 {
		t.Fatalf("the folder was not moved whole: %v", moved)
	}
}

func cursorChat(t *testing.T, home, id, meta string, transcript ...string) {
	t.Helper()
	dir := filepath.Join(home, ".cursor", "chats", "0750", id)
	write(t, filepath.Join(dir, "meta.json"), meta)
	write(t, filepath.Join(dir, "store.db"), "db")
	if transcript != nil {
		write(t, filepath.Join(home, ".cursor", "projects", "w-proj", "agent-transcripts", id, id+".jsonl"), strings.Join(transcript, "\n")+"\n")
	}
}

var cursorLines = []string{
	`{"role":"user","message":{"content":[{"type":"text","text":"<timestamp>Friday, Oct 9, 2026</timestamp>\n<user_query>\nadd a   test\n</user_query>"}]}}`,
	`{"role":"assistant","message":{"content":[{"type":"text","text":"Added one."}]}}`,
	`{"type":"turn_ended","status":"success"}`,
}

func TestCursorSessions(t *testing.T) {
	a6NoTools(t)
	home := t.TempDir()
	cursorChat(t, home, "c1", `{"schemaVersion":1,"createdAtMs":1791546146493,"hasConversation":true,"title":"Hello   There","updatedAtMs":1791546165632,"cwd":"/w/proj"}`, cursorLines...)
	// No title: the first question.
	cursorChat(t, home, "c2", `{"hasConversation":true,"updatedAtMs":1791546165632,"cwd":"/w/proj"}`, cursorLines...)
	// Opened and left.
	cursorChat(t, home, "c3", `{"hasConversation":false,"cwd":"/w/proj"}`)
	// Said something, but neither a title nor a transcript to name it by.
	cursorChat(t, home, "c4", `{"hasConversation":true,"cwd":"/w/proj"}`)
	cursorChat(t, home, "c5", `{"hasConversation":true,"title":"elsewhere","cwd":"/x"}`)

	env := a6Env(home, nil)
	got := map[string]Session{}
	for _, s := range List(env, []string{"/w/proj"}, 0) {
		got[s.ID] = s
	}
	c1 := got["c1"]
	if len(got) != 2 || c1.Title != "Hello There" || got["c2"].Title != "add a test" || !c1.Updated.Equal(time.UnixMilli(1791546165632)) {
		t.Fatalf("%+v", got)
	}
	turns, err := Transcript(context.Background(), env, c1)
	if err != nil || turnsText(turns) != "user: add a   test\nassistant: Added one." {
		t.Fatalf("%q %v", turnsText(turns), err)
	}
	if _, err := Transcript(context.Background(), env, Session{Agent: "cursor", Path: filepath.Join(home, ".cursor", "chats", "0750", "c4", "meta.json")}); err == nil {
		t.Error("a chat with no transcript says so")
	}
	trash := t.TempDir()
	if err := Delete(context.Background(), env, c1, trash); err != nil {
		t.Fatal(err)
	}
	chat, _ := filepath.Glob(filepath.Join(trash, "cursor-*-c1", "store.db"))
	tr, _ := filepath.Glob(filepath.Join(trash, "cursor-*-c1-2", "c1.jsonl"))
	if len(chat) != 1 || len(tr) != 1 {
		t.Fatalf("the chat and its transcript go to the trash: %v %v", chat, tr)
	}
	if _, err := os.Stat(filepath.Join(home, ".cursor", "projects", "w-proj", "agent-transcripts", "c2")); err != nil {
		t.Error("another chat's transcript was touched")
	}
}

func TestKiloSessions(t *testing.T) {
	home := t.TempDir()
	db := filepath.Join(home, ".local", "share", "kilo", "kilo.db")
	write(t, db, "")
	log := filepath.Join(t.TempDir(), "calls")
	tools := a6Tool(t, "sqlite3", `echo "$*" >> `+log+`
echo '[{"id":"ses_k1","directory":"/w/proj","title":"Kilo   task","time_created":1000,"time_updated":2000}]'
`)
	write(t, filepath.Join(tools, "kilo"), "#!/bin/sh\npwd > "+log+".kilo; echo \"$*\" >> "+log+".kilo\n")
	if err := os.Chmod(filepath.Join(tools, "kilo"), 0o755); err != nil {
		t.Fatal(err)
	}
	env := a6Env(home, nil)
	got := List(env, []string{"/w/proj"}, 0)
	if a6IDs(got) != "kilo:ses_k1" || got[0].Title != "Kilo task" || got[0].Path != db {
		t.Fatalf("%s %+v", a6IDs(got), got)
	}
	if b, _ := os.ReadFile(log); !strings.Contains(string(b), db) {
		t.Errorf("not read from kilo.db: %s", b)
	}
	got[0].Dir = t.TempDir()
	if err := Delete(context.Background(), env, got[0], t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(log + ".kilo"); !strings.HasSuffix(strings.TrimSpace(string(b)), "session delete ses_k1") {
		t.Fatalf("kilo call: %q", b)
	}
	if _, err := os.Stat(db); err != nil {
		t.Fatal("the database must not be moved")
	}
	// Without kilo, it says what is needed.
	a6NoTools(t)
	if err := Delete(context.Background(), env, got[0], t.TempDir()); err == nil || !strings.Contains(err.Error(), "kilo command") {
		t.Fatalf("no kilo: %v", err)
	}
}

func TestReadsIsTheStoreList(t *testing.T) {
	for _, a := range []string{"claude", "codex", "gemini", "opencode", "devin", "copilot", "cline", "qwen", "goose", "kilo", "pi", "grok", "cursor"} {
		if !Reads(a) {
			t.Errorf("%s: sessions are read", a)
		}
	}
	for _, a := range []string{"aider", "amp", "droid", "kiro", "", "Claude"} {
		if Reads(a) {
			t.Errorf("%q: sessions are not read", a)
		}
	}
}

func TestUserQuery(t *testing.T) {
	for in, want := range map[string]string{
		"plain": "plain",
		"<timestamp>t</timestamp>\n<user_query>\n hi \n</user_query>": "hi",
		"<user_query>unclosed":      "unclosed",
		"<user_query></user_query>": "",
	} {
		if got := userQuery(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}
