package sessions

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeLines(t *testing.T, path string, ls ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(ls, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func appendText(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(s); err != nil {
		t.Fatal(err)
	}
}

var claudeFixture = []string{
	`{"type":"user","timestamp":"2026-11-11T10:00:00Z","message":{"content":"Add a workbench"}}`,
	`{"type":"assistant","timestamp":"2026-11-11T10:00:05Z","message":{"content":[{"type":"text","text":"Reading first."},{"type":"tool_use","id":"t1","name":"Read","input":{"file_path":"/w/FilesView.swift"}}]}}`,
	`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"412 lines"}]}}`,
	`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t2","name":"Bash","input":{"command":"swift   test\n --filter Workbench"}}]}}`,
	`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t2","is_error":true,"content":[{"type":"text","text":"1 failure"}]}]}}`,
	`{"type":"assistant","message":{"content":[{"type":"text","text":"Done."}]}}`,
	`{"type":"assistant","message":{"content":[{"type":"text","text":"All green."}]}}`,
	`{"type":"assistant","isSidechain":true,"message":{"content":[{"type":"text","text":"a subagent"}]}}`,
	`{"type":"user","isMeta":true,"message":{"content":"meta"}}`,
	`{"type":"user","message":{"content":"<command-name>/clear</command-name>"}}`,
	`not json`,
	`{"type":"assistant","message":{"content":"plain string answer"}}`,
	`{"type":"user","message":{"content":[{"type":"text","text":"[Request interrupted by user]"}]}}`,
}

func TestClaudeChatKeepsToolsAndResults(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c1.jsonl")
	writeLines(t, p, claudeFixture...)
	got, err := Chat(context.Background(), Env{}, Session{Agent: "claude", ID: "c1", Path: p})
	if err != nil {
		t.Fatal(err)
	}
	want := []ChatTurn{
		{Role: "user", Text: "Add a workbench"},
		{Role: "assistant", Text: "Reading first."},
		{Role: "tool", Text: "Read", Detail: "/w/FilesView.swift", Output: "412 lines", Done: true},
		{Role: "tool", Text: "Bash", Detail: "swift test --filter Workbench", Input: "swift   test\n --filter Workbench", InputKind: "code", Output: "1 failure", Error: true, Done: true},
		{Role: "assistant", Text: "Done.\n\nAll green.\n\nplain string answer"},
		{Role: "note", Text: "Interrupted"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d turns: %+v", len(got), got)
	}
	for i, w := range want {
		g := got[i]
		if g.Seq != i || g.Role != w.Role || g.Text != w.Text || g.Detail != w.Detail || g.Input != w.Input || g.InputKind != w.InputKind ||
			g.Output != w.Output || g.Error != w.Error || g.Done != w.Done {
			t.Fatalf("turn %d: got %+v want %+v", i, g, w)
		}
	}
	if got[0].Time.IsZero() {
		t.Fatal("the user's turn lost its time")
	}
}

// A chat polled while the agent works is sent only what changed: a new
// turn, a tool's result arriving for a turn it already has, text joined to
// the last answer — and a line still being written waits for its end.
func TestChatSinceIsIncremental(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c2.jsonl")
	writeLines(t, p, claudeFixture[:2]...)
	s := Session{Agent: "claude", ID: "c2", Path: p}
	ctx := context.Background()
	first, err := ChatSince(ctx, Env{}, s, "", 0)
	if err != nil || !first.Reset || len(first.Turns) != 3 || first.Epoch == "" || first.Ver == 0 {
		t.Fatalf("first %+v %v", first, err)
	}
	// Nothing new: nothing sent.
	same, _ := ChatSince(ctx, Env{}, s, first.Epoch, first.Ver)
	if same.Reset || len(same.Turns) != 0 || same.Ver != first.Ver {
		t.Fatalf("unchanged %+v", same)
	}
	// The Read's result, and half a line.
	appendText(t, p, claudeFixture[2]+"\n"+`{"type":"assistant","message":{"content":[{"type":"te`)
	up, _ := ChatSince(ctx, Env{}, s, first.Epoch, first.Ver)
	if up.Reset || len(up.Turns) != 1 || up.Turns[0].Seq != 2 || up.Turns[0].Output != "412 lines" || !up.Turns[0].Done {
		t.Fatalf("result %+v", up)
	}
	// The line finished: text after a tool is a turn of its own.
	appendText(t, p, `xt","text":"Looks fine."}]}}`+"\n")
	up2, _ := ChatSince(ctx, Env{}, s, up.Epoch, up.Ver)
	if len(up2.Turns) != 1 || up2.Turns[0].Text != "Looks fine." || up2.Turns[0].Seq != 3 {
		t.Fatalf("finished line %+v", up2)
	}
	appendText(t, p, `{"type":"assistant","message":{"content":[{"type":"text","text":"More."}]}}`+"\n")
	up3, _ := ChatSince(ctx, Env{}, s, up2.Epoch, up2.Ver)
	if len(up3.Turns) != 1 || up3.Turns[0].Seq != 3 || up3.Turns[0].Text != "Looks fine.\n\nMore." {
		t.Fatalf("joined text %+v", up3)
	}
	// Another log's version, or one from the future: start again.
	if r, _ := ChatSince(ctx, Env{}, s, "other", 3); !r.Reset || len(r.Turns) != 4 {
		t.Fatalf("other epoch %+v", r)
	}
	if r, _ := ChatSince(ctx, Env{}, s, up3.Epoch, up3.Ver+10); !r.Reset {
		t.Fatalf("future version %+v", r)
	}
	// Rewritten shorter: a new log.
	writeLines(t, p, claudeFixture[0])
	r, _ := ChatSince(ctx, Env{}, s, up3.Epoch, up3.Ver)
	if !r.Reset || r.Epoch == up3.Epoch || len(r.Turns) != 1 {
		t.Fatalf("rewritten %+v", r)
	}
}

func TestChatSendsTheNewestTurns(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c3.jsonl")
	var ls []string
	for i := 0; i < maxChatTurns+50; i++ {
		ls = append(ls, fmt.Sprintf(`{"type":"user","message":{"content":"ask %d"}}`, i))
	}
	writeLines(t, p, ls...)
	got, err := Chat(context.Background(), Env{}, Session{Agent: "claude", ID: "c3", Path: p})
	if err != nil || len(got) != maxChatTurns || got[0].Seq != 50 || got[len(got)-1].Text != fmt.Sprintf("ask %d", maxChatTurns+49) {
		t.Fatalf("kept %d: %v", len(got), err)
	}
}

func TestChatOtherAgentsAreTheirTranscript(t *testing.T) {
	p := filepath.Join(t.TempDir(), "rollout.jsonl")
	writeLines(t, p,
		`{"type":"event_msg","payload":{"type":"user_message","message":"tidy the README"}}`,
		`{"type":"event_msg","payload":{"type":"agent_message","message":"Tidied."}}`,
	)
	s := Session{Agent: "codex", ID: "x", Path: p}
	up, err := ChatSince(context.Background(), Env{}, s, "", 0)
	if err != nil || !up.Reset || len(up.Turns) != 2 || up.Turns[0].Role != "user" || up.Turns[1].Text != "Tidied." {
		t.Fatalf("codex chat %+v %v", up, err)
	}
	// Unchanged: nothing sent, not read again.
	same, _ := ChatSince(context.Background(), Env{}, s, up.Epoch, up.Ver)
	if same.Reset || len(same.Turns) != 0 {
		t.Fatalf("unchanged %+v", same)
	}
	if _, err := Chat(context.Background(), Env{}, Session{Agent: "devin", ID: "d"}); err == nil {
		t.Fatal("an agent conch can't read gave a chat")
	}
	if _, err := Chat(context.Background(), Env{}, Session{Agent: "claude", ID: "gone", Path: filepath.Join(t.TempDir(), "gone.jsonl")}); err == nil {
		t.Fatal("a missing transcript gave a chat")
	}
}

func TestToolInput(t *testing.T) {
	d, in, k := toolInput("Edit", []byte(`{"file_path":"/a.go","old_string":"x := 1\ny","new_string":"x := 2"}`))
	if d != "/a.go" || k != "diff" || in != "- x := 1\n- y\n+ x := 2" {
		t.Fatalf("edit %q %q %q", d, in, k)
	}
	_, in, k = toolInput("MultiEdit", []byte(`{"file_path":"/a.go","edits":[{"old_string":"a","new_string":"b"},{"old_string":"","new_string":"c"}]}`))
	if k != "diff" || in != "- a\n+ b\n⋯\n+ c" {
		t.Fatalf("multiedit %q", in)
	}
	d, in, k = toolInput("Bash", []byte(`{"command":"make gate","description":"Run   the suite"}`))
	if d != "Run the suite" || in != "make gate" || k != "code" {
		t.Fatalf("bash %q %q %q", d, in, k)
	}
	d, in, _ = toolInput("TodoWrite", []byte(`{"todos":[{"content":"a","status":"completed"},{"content":"b","status":"in_progress"},{"content":"c","status":"pending"}]}`))
	if d != "3 to-dos" || in != "☑ a\n◐ b\n☐ c" {
		t.Fatalf("todos %q %q", d, in)
	}
	d, in, _ = toolInput("Read", []byte(`{"file_path":"/x"}`))
	if d != "/x" || in != "" {
		t.Fatalf("read %q %q", d, in)
	}
	d, _, _ = toolInput("Grep", []byte(`{"pattern":"TODO","path":"internal"}`))
	if d != "TODO  in internal" {
		t.Fatalf("grep %q", d)
	}
	d, in, _ = toolInput("Task", []byte(`{"description":"Review","prompt":"Look at it"}`))
	if d != "Review" || in != "Look at it" {
		t.Fatalf("task %q %q", d, in)
	}
	_, in, k = toolInput("mcp__jira__search", []byte(`{"jql":"project = X"}`))
	if k != "code" || !strings.Contains(in, `"jql": "project = X"`) {
		t.Fatalf("mcp %q", in)
	}
	if d, in, k := toolInput("Bash", []byte(`not json`)); d != "" || in != "" || k != "" {
		t.Fatal("bad input gave something")
	}
	_, in, _ = toolInput("Write", []byte(`{"file_path":"/big","content":"`+strings.Repeat("line\\n", 5000)+`"}`))
	if len(in) > maxToolInput+100 || !strings.Contains(in, "lines not shown") {
		t.Fatalf("big write kept %d bytes", len(in))
	}
}

func TestResultTextAndClips(t *testing.T) {
	if got := resultText([]byte(`"ok\n"`)); got != "ok" {
		t.Fatalf("string %q", got)
	}
	if got := resultText([]byte(`[{"type":"text","text":"a"},{"type":"image"},{"type":"other"},{"type":"text","text":"b"}]`)); got != "a\n[image]\nb" {
		t.Fatalf("parts %q", got)
	}
	if got := resultText([]byte(`{}`)); got != "" {
		t.Fatalf("object %q", got)
	}
	if clip("abc", 5) != "abc" || clip("abcdef", 3) != "abc…" || clip("ééé", 3) != "é…" {
		t.Fatal("clip")
	}
	long := strings.Repeat("x\n", 10000)
	c := clipMiddle(long, 1000)
	if len(c) > 1100 || !strings.HasPrefix(c, "x\n") || !strings.HasSuffix(c, "x\n") || !strings.Contains(c, "lines not shown") {
		t.Fatalf("clipMiddle %d", len(c))
	}
	if clipMiddle("short", 100) != "short" {
		t.Fatal("short clipped")
	}
}

func TestToolDetail(t *testing.T) {
	cases := map[string]string{
		`{"file_path":"/a/b.go","old_string":"x"}`: "/a/b.go",
		`{"pattern":"TODO","path":"internal"}`:     "TODO",
		`{"command":"   "}`:                        "",
		`{"other":1}`:                              "",
		`not json`:                                 "",
		`{"url":"https://example.com"}`:            "https://example.com",
	}
	for in, want := range cases {
		if got := toolDetail([]byte(in)); got != want {
			t.Errorf("%s: got %q want %q", in, got, want)
		}
	}
	got := toolDetail([]byte(`{"command":"` + strings.Repeat("é", 300) + `"}`))
	if !strings.HasSuffix(got, "…") || len(got) > maxToolDetail+len("…") {
		t.Fatalf("long detail %q", got)
	}
}

func TestChatLogsAreBounded(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < maxChatLogs+5; i++ {
		p := filepath.Join(dir, fmt.Sprintf("b%d.jsonl", i))
		writeLines(t, p, claudeFixture[0])
		if _, err := Chat(context.Background(), Env{}, Session{Agent: "claude", ID: fmt.Sprintf("bound%d", i), Path: p}); err != nil {
			t.Fatal(err)
		}
	}
	chatMu.Lock()
	n := len(chatLogs)
	chatMu.Unlock()
	if n > maxChatLogs {
		t.Fatalf("%d logs kept", n)
	}
}

// Past the turns kept in memory, a tool call still meets its result.
func TestChatToolResultsPastTheWindow(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c4.jsonl")
	var ls []string
	for i := 0; i < keepChatTurns+30; i++ {
		ls = append(ls,
			fmt.Sprintf(`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"u%d","name":"Bash","input":{"command":"echo %d"}}]}}`, i, i),
			fmt.Sprintf(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"u%d","content":"%d"}]}}`, i, i))
	}
	writeLines(t, p, ls...)
	got, err := Chat(context.Background(), Env{}, Session{Agent: "claude", ID: "c4", Path: p})
	if err != nil || len(got) != maxChatTurns {
		t.Fatalf("%d turns %v", len(got), err)
	}
	for _, x := range got {
		if !x.Done || x.Output != strings.TrimPrefix(x.Input, "echo ") {
			t.Fatalf("turn %d: %+v", x.Seq, x)
		}
	}
	l := logFor(Session{Agent: "claude", ID: "c4", Path: p})
	if len(l.tools) != 0 {
		t.Fatalf("%d calls left waiting", len(l.tools))
	}
}
