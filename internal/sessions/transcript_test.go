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

func turnsText(turns []Turn) string {
	var out []string
	for _, t := range turns {
		out = append(out, t.Role+": "+t.Text)
	}
	return strings.Join(out, "\n")
}

func TestClaudeTranscript(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	write(t, path, strings.Join([]string{
		`{"type":"ai-title","aiTitle":"Fix it"}`,
		`{"type":"user","timestamp":"2026-09-14T10:00:00Z","message":{"role":"user","content":"Fix the flaky test"}}`,
		`{"type":"user","isMeta":true,"message":{"role":"user","content":"meta caveat"}}`,
		`{"type":"user","message":{"role":"user","content":"<command-name>/clear</command-name>"}}`,
		`{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"hmm"}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"Looking at the test."}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{}}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"output"}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"Found a race."}]}}`,
		`{"type":"assistant","isSidechain":true,"message":{"content":[{"type":"text","text":"subagent chatter"}]}}`,
		`not json`,
		`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"Thanks,"},{"type":"image"},{"type":"text","text":"ship it"}]}}`,
		`{"type":"system","content":"compact"}`,
	}, "\n")+"\n")
	turns, err := Transcript(context.Background(), Env{}, Session{Agent: "claude", Path: path})
	if err != nil {
		t.Fatal(err)
	}
	want := "user: Fix the flaky test\nassistant: Looking at the test.\n\nFound a race.\nuser: Thanks,\n\nship it"
	if got := turnsText(turns); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if !turns[0].Time.Equal(time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("time: %v", turns[0].Time)
	}
}

func TestCodexTranscript(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	write(t, path, strings.Join([]string{
		`{"type":"session_meta","payload":{"id":"cx","cwd":"/src"}}`,
		`{"timestamp":"2026-09-14T10:00:00Z","type":"event_msg","payload":{"type":"user_message","message":"add a flag"}}`,
		`{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"duplicate"}]}}`,
		`{"type":"event_msg","payload":{"type":"token_count"}}`,
		`{"type":"event_msg","payload":{"type":"agent_message","message":"Added --verbose."}}`,
		`{"type":"event_msg","payload":{"type":"user_message","message":"  "}}`,
	}, "\n"))
	turns, err := Transcript(context.Background(), Env{}, Session{Agent: "codex", Path: path})
	if err != nil || turnsText(turns) != "user: add a flag\nassistant: Added --verbose." {
		t.Fatalf("%q %v", turnsText(turns), err)
	}
}

func TestGeminiTranscript(t *testing.T) {
	dir := t.TempDir()
	jsonFile := filepath.Join(dir, "session-1.json")
	write(t, jsonFile, `{"sessionId":"g1","messages":[
		{"type":"user","content":"explain main.go"},
		{"type":"info","content":"loading"},
		{"type":"gemini","content":[{"text":"It starts the server."}]},
		{"type":"error","content":"oops"}]}`)
	turns, err := Transcript(context.Background(), Env{}, Session{Agent: "gemini", Path: jsonFile})
	if err != nil || turnsText(turns) != "user: explain main.go\nassistant: It starts the server." {
		t.Fatalf("json: %q %v", turnsText(turns), err)
	}

	jsonl := filepath.Join(dir, "session-2.jsonl")
	write(t, jsonl, `{"sessionId":"g2","kind":"main"}`+"\n"+
		`{"type":"user","content":"hi"}`+"\n"+`garbage`+"\n"+`{"type":"model","content":"hello"}`+"\n")
	turns, err = Transcript(context.Background(), Env{}, Session{Agent: "gemini", Path: jsonl})
	if err != nil || turnsText(turns) != "user: hi\nassistant: hello" {
		t.Fatalf("jsonl: %q %v", turnsText(turns), err)
	}
}

func TestOpenCodeTranscripts(t *testing.T) {
	home := t.TempDir()
	e := Env{Home: home, Getenv: func(string) string { return "" }}
	storage := filepath.Join(home, ".local", "share", "opencode", "storage")
	write(t, filepath.Join(storage, "message", "ses_x", "msg_2.json"), `{"id":"msg_2","role":"assistant","time":{"created":2000}}`)
	write(t, filepath.Join(storage, "message", "ses_x", "msg_1.json"), `{"id":"msg_1","role":"user","time":{"created":1000}}`)
	write(t, filepath.Join(storage, "message", "ses_x", "bad.json"), `{`)
	write(t, filepath.Join(storage, "part", "msg_1", "prt_1.json"), `{"type":"text","text":"rename the flag"}`)
	write(t, filepath.Join(storage, "part", "msg_1", "prt_2.json"), `{"type":"text","text":"injected","synthetic":true}`)
	write(t, filepath.Join(storage, "part", "msg_2", "prt_1.json"), `{"type":"reasoning","text":"thinking"}`)
	write(t, filepath.Join(storage, "part", "msg_2", "prt_2.json"), `{"type":"text","text":"Renamed."}`)
	turns, err := Transcript(context.Background(), e, Session{Agent: "opencode", ID: "ses_x", Path: filepath.Join(storage, "session", "p", "ses_x.json")})
	if err != nil || turnsText(turns) != "user: rename the flag\nassistant: Renamed." {
		t.Fatalf("files: %q %v", turnsText(turns), err)
	}

	bin, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("no sqlite3")
	}
	db := filepath.Join(home, "opencode.db")
	schema := `CREATE TABLE message (id text primary key, session_id text, time_created integer, time_updated integer, data text);
CREATE TABLE part (id text primary key, message_id text, session_id text, time_created integer, time_updated integer, data text);
INSERT INTO message VALUES ('m1','ses''q',1,1,'{"role":"user"}');
INSERT INTO message VALUES ('m2','ses''q',2,2,'{"role":"assistant"}');
INSERT INTO part VALUES ('p1','m1','ses''q',1,1,'{"type":"text","text":"why is it slow?"}');
INSERT INTO part VALUES ('p2','m1','ses''q',1,1,'{"type":"text","text":"file contents","synthetic":true}');
INSERT INTO part VALUES ('p3','m2','ses''q',2,2,'{"type":"step-start"}');
INSERT INTO part VALUES ('p4','m2','ses''q',3,3,'{"type":"text","text":"An N+1 query."}');
INSERT INTO part VALUES ('p5','m9','other',1,1,'{"type":"text","text":"elsewhere"}');`
	if out, err := exec.Command(bin, db, schema).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	turns, err = Transcript(context.Background(), e, Session{Agent: "opencode", ID: "ses'q", Path: db})
	if err != nil || turnsText(turns) != "user: why is it slow?\nassistant: An N+1 query." {
		t.Fatalf("sqlite: %q %v", turnsText(turns), err)
	}
}

func TestOpenCodeDBTranscriptErrors(t *testing.T) {
	ctx := context.Background()
	s := Session{Agent: "opencode", ID: "s", Path: "/nowhere/opencode.db"}
	a6NoTools(t)
	if _, err := Transcript(ctx, Env{}, s); err == nil || !strings.Contains(err.Error(), "sqlite3") {
		t.Fatalf("no sqlite3: %v", err)
	}
	a6Tool(t, "sqlite3", "exit 3\n")
	if _, err := Transcript(ctx, Env{}, s); err == nil {
		t.Fatal("failing sqlite3: no error")
	}
	a6Tool(t, "sqlite3", "echo 'not json'\n")
	if _, err := Transcript(ctx, Env{}, s); err == nil {
		t.Fatal("garbage output: no error")
	}
	a6Tool(t, "sqlite3", "exit 0\n") // no rows: empty output
	if turns, err := Transcript(ctx, Env{}, s); err != nil || len(turns) != 0 {
		t.Fatalf("empty: %v %v", turns, err)
	}
}

func TestTranscriptErrors(t *testing.T) {
	ctx := context.Background()
	if _, err := Transcript(ctx, Env{}, Session{Agent: "aider"}); err == nil {
		t.Error("unknown agent: no error")
	}
	missing := filepath.Join(t.TempDir(), "gone")
	for _, agent := range []string{"claude", "codex", "gemini"} {
		if _, err := Transcript(ctx, Env{}, Session{Agent: agent, Path: missing}); err == nil {
			t.Errorf("%s: missing file gave no error", agent)
		}
	}
	// OpenCode's file store with nothing saved is just empty.
	if turns, err := Transcript(ctx, Env{Home: t.TempDir()}, Session{Agent: "opencode", ID: "none"}); err != nil || len(turns) != 0 {
		t.Errorf("opencode empty: %v %v", turns, err)
	}
}

func TestCapTurnsKeepsLatest(t *testing.T) {
	big := strings.Repeat("x", 1500<<10)
	turns := []Turn{{Role: "user", Text: "first"}, {Role: "assistant", Text: big}, {Role: "user", Text: big}, {Role: "assistant", Text: big}, {Role: "user", Text: "last"}}
	got := capTurns(turns)
	if len(got) != 3 || got[len(got)-1].Text != "last" {
		t.Fatalf("kept %d turns", len(got))
	}
	if small := capTurns(turns[:1]); len(small) != 1 {
		t.Fatal("small transcript cut")
	}
	if capTurns(nil) != nil {
		t.Fatal("nil")
	}
}

func TestMatch(t *testing.T) {
	dir := t.TempDir()
	claudePath := filepath.Join(dir, "a.jsonl")
	write(t, claudePath, `{"type":"user","message":{"content":"The Deploy script fails on arm64"}}`+"\n"+
		`{"type":"assistant","message":{"content":[{"type":"text","text":"The cross compiler is missing. Install gcc-aarch64."}]}}`+"\n")
	codexPath := filepath.Join(dir, "b.jsonl")
	write(t, codexPath, `{"type":"event_msg","payload":{"type":"user_message","message":"tidy the README"}}`+"\n")
	list := []Session{
		{Agent: "claude", ID: "c1", Title: "Deploy fails", Branch: "fix/arm", Dir: "/src/api", Path: claudePath},
		{Agent: "codex", ID: "x1", Title: "Docs", Dir: "/src/api", Path: codexPath},
		{Agent: "gemini", ID: "g1", Title: "Broken", Path: filepath.Join(dir, "missing.json")},
	}
	ctx := context.Background()
	check := func(query string, want map[string]string) {
		t.Helper()
		got := Match(ctx, Env{}, list, query)
		if len(got) != len(want) {
			t.Fatalf("%q: %v, want %v", query, got, want)
		}
		for k, v := range want {
			if g, ok := got[k]; !ok || (v != "*" && g != v) {
				t.Fatalf("%q: %s = %q (present %v), want %q", query, k, g, ok, v)
			}
		}
	}
	check("", map[string]string{})
	check("   ", map[string]string{})
	check("deploy", map[string]string{"claude|c1": ""})  // title
	check("FIX/ARM", map[string]string{"claude|c1": ""}) // branch, any case
	check("codex", map[string]string{"codex|x1": ""})    // agent
	check("x1", map[string]string{"codex|x1": ""})       // ID
	check("api", map[string]string{})                    // not the directory
	check("readme", map[string]string{"codex|x1": "tidy the README"})
	check("compiler aarch64", map[string]string{"claude|c1": "*"})
	check("compiler readme", map[string]string{}) // every word, one session
	check("nothing-like-this", map[string]string{})

	got := Match(ctx, Env{}, list, "compiler")
	if s := got["claude|c1"]; !strings.Contains(s, "cross compiler is missing") {
		t.Fatalf("snippet %q", s)
	}

	// A changed file is read again.
	write(t, codexPath, `{"type":"event_msg","payload":{"type":"user_message","message":"now about kubernetes"}}`+"\n")
	later := time.Now().Add(time.Second)
	os.Chtimes(codexPath, later, later)
	check("kubernetes", map[string]string{"codex|x1": "now about kubernetes"})
	check("readme", map[string]string{})

	// A cancelled search stops.
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if got := Match(cctx, Env{}, list, "deploy"); len(got) != 0 {
		t.Fatalf("cancelled: %v", got)
	}
}

func TestSnippet(t *testing.T) {
	text := strings.Repeat("a", 100) + " needle " + strings.Repeat("b", 100)
	s := snippet(text, 101, 6)
	if !strings.HasPrefix(s, "…") || !strings.HasSuffix(s, "…") || !strings.Contains(s, "needle") {
		t.Fatalf("middle: %q", s)
	}
	if s := snippet("short needle", 6, 6); s != "short needle" {
		t.Fatalf("short: %q", s)
	}
	if s := snippet("line one\n\nline   two", 0, 4); s != "line one line two" {
		t.Fatalf("whitespace: %q", s)
	}
	// Multi-byte text is never split inside a character.
	uni := strings.Repeat("é", 60) + "needle" + strings.Repeat("ü", 60)
	s = snippet(uni, 120, 6)
	if !strings.Contains(s, "needle") || strings.ContainsRune(s, '�') {
		t.Fatalf("unicode: %q", s)
	}
	for _, at := range []int{-5, len(uni) + 10} { // offsets out of range are clamped
		if s := snippet(uni, at, 3); strings.ContainsRune(s, '�') {
			t.Fatalf("clamped %d: %q", at, s)
		}
	}
}

func TestHandoff(t *testing.T) {
	s := Session{Agent: "claude", ID: "abc", Dir: "/src/api", Branch: "fix/arm", Title: "Deploy\nfails",
		Started: time.Date(2026, 9, 14, 9, 0, 0, 0, time.Local), Updated: time.Date(2026, 9, 14, 11, 30, 0, 0, time.Local)}
	turns := []Turn{
		{Role: "user", Text: "Why does deploy fail?", Time: time.Date(2026, 9, 14, 9, 1, 0, 0, time.Local)},
		{Role: "assistant", Text: "The cross compiler is missing."},
	}
	doc := Handoff(s, turns)
	for _, want := range []string{
		"# Handoff: Deploy fails\n",
		"Conversation from a Claude Code session (`abc`) in `/src/api` on branch `fix/arm`, 2026-09-14 09:00 – 2026-09-14 11:30.",
		"tool calls, command output and file edits are left out",
		"## User · 2026-09-14 09:01:00\n\nWhy does deploy fail?\n",
		"## Claude Code\n\nThe cross compiler is missing.\n",
	} {
		if !strings.Contains(doc, want) {
			t.Fatalf("missing %q in:\n%s", want, doc)
		}
	}

	if doc := Handoff(Session{Agent: "aider", Dir: "/x"}, nil); !strings.Contains(doc, "# Handoff: untitled session") ||
		!strings.Contains(doc, "a aider session in `/x`.") || !strings.Contains(doc, "_The session has no messages._") {
		t.Fatalf("empty:\n%s", doc)
	}

	// A long message is cut; a long conversation keeps the first request and
	// the latest messages.
	long := strings.Repeat("é", maxHandoffTurn+10)
	if doc := Handoff(s, []Turn{{Role: "user", Text: long}}); !strings.Contains(doc, "_… message cut …_") || strings.Count(doc, "é") != maxHandoffTurn {
		t.Fatal("long message not cut at a character")
	}
	var many []Turn
	many = append(many, Turn{Role: "user", Text: "FIRST REQUEST"})
	for i := 0; i < 200; i++ {
		many = append(many, Turn{Role: "assistant", Text: strings.Repeat("z", 7000)})
	}
	many = append(many, Turn{Role: "user", Text: "LAST MESSAGE"})
	doc = Handoff(s, many)
	if len(doc) > maxHandoffBytes || !strings.Contains(doc, "FIRST REQUEST") || !strings.HasSuffix(doc, "LAST MESSAGE\n") ||
		!strings.Contains(doc, "earlier messages left out") {
		t.Fatalf("long conversation: %d bytes", len(doc))
	}
	if strings.Index(doc, "FIRST REQUEST") > strings.Index(doc, "earlier messages left out") {
		t.Fatal("first request not first")
	}
}

func TestAgentNameAndKey(t *testing.T) {
	if AgentName("codex") != "Codex" || AgentName("opencode") != "OpenCode" || AgentName("new") != "new" {
		t.Fatal("AgentName")
	}
	if Key(Session{Agent: "claude", ID: "1"}) != "claude|1" {
		t.Fatal("Key")
	}
}
