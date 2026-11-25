package sessions

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// claudeFull is a Claude Code transcript with each kind of item conch
// reads, in the shapes Claude Code writes (the words made up): thinking,
// a failed command ("Exit code N" first, as Claude reports it), a read, an
// edit, a file written new, a multi-edit, a to-do list, an image, an
// unknown tool, a call that never got its result, an interruption, a
// sidechain and a meta line — and a last line cut off mid-write.
var claudeFull = strings.Join([]string{
	`{"type":"user","timestamp":"2026-11-20T10:00:00Z","isMeta":true,"message":{"content":"<command-name>/clear</command-name>"}}`,
	`{"type":"user","timestamp":"2026-11-20T10:00:01Z","message":{"role":"user","content":"Fix the failing build on arm64"}}`,
	`{"type":"assistant","timestamp":"2026-11-20T10:00:02Z","message":{"content":[{"type":"thinking","thinking":"secret plan","signature":"sig"},{"type":"text","text":"Running the tests first."},{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"go test ./...","description":"Run tests"}}]}}`,
	`{"type":"user","timestamp":"2026-11-20T10:00:03Z","message":{"content":[{"type":"tool_result","tool_use_id":"t1","is_error":true,"content":"Exit code 1\n--- FAIL: TestArm (0.00s)\n    arm_test.go:9: no cross compiler"}]}}`,
	`{"type":"assistant","timestamp":"2026-11-20T10:00:04Z","message":{"content":[{"type":"tool_use","id":"t2","name":"Read","input":{"file_path":"/w/build/arm.go"}}]}}`,
	`{"type":"user","timestamp":"2026-11-20T10:00:05Z","message":{"content":[{"type":"tool_result","tool_use_id":"t2","content":"package build\n"}]}}`,
	`{"type":"assistant","timestamp":"2026-11-20T10:00:06Z","isSidechain":true,"message":{"content":[{"type":"text","text":"a subagent's words"}]}}`,
	`{"type":"assistant","timestamp":"2026-11-20T10:00:07Z","message":{"content":[{"type":"tool_use","id":"t3","name":"Edit","input":{"file_path":"/w/build/arm.go","old_string":"a\nb\nc","new_string":"a\nB\nB2\nc"}},{"type":"tool_use","id":"t4","name":"Write","input":{"file_path":"/w/docs/ünïcode ✓.md","content":"one\ntwo\n"}}]}}`,
	`{"type":"user","timestamp":"2026-11-20T10:00:08Z","message":{"content":[{"type":"tool_result","tool_use_id":"t3","content":"The file has been updated."},{"type":"tool_result","tool_use_id":"t4","content":"File created successfully at: /w/docs/ünïcode ✓.md"}]}}`,
	`{"type":"assistant","timestamp":"2026-11-20T10:00:09Z","message":{"content":[{"type":"tool_use","id":"t5","name":"MultiEdit","input":{"file_path":"/w/build/arm.go","edits":[{"old_string":"x","new_string":"y"},{"old_string":"p","new_string":"q\nr"}]}},{"type":"tool_use","id":"t6","name":"TodoWrite","input":{"todos":[{"content":"Install the cross compiler","status":"completed","activeForm":"x"},{"content":"Run the arm tests","status":"in_progress","activeForm":"x"},{"content":"Update the docs","status":"pending","activeForm":"x"}]}}]}}`,
	`{"type":"user","timestamp":"2026-11-20T10:00:10Z","message":{"content":[{"type":"tool_result","tool_use_id":"t5","content":"ok"},{"type":"tool_result","tool_use_id":"t6","content":"Todos have been modified"},{"type":"tool_result","tool_use_id":"nobody","content":"a result for no call"}]}}`,
	`{"type":"assistant","timestamp":"2026-11-20T10:00:11Z","message":{"content":[{"type":"tool_use","id":"t7","name":"mcp__linear__get_issue","input":{"id":"ENG-1"}},{"type":"tool_use","id":"t8","name":"WebFetch","input":{"url":"https://example.com/arm"}}]}}`,
	`{"type":"user","timestamp":"2026-11-20T10:00:12Z","message":{"content":[{"type":"tool_result","tool_use_id":"t7","content":[{"type":"text","text":"{\"title\":\"arm\"}"}]}]}}`,
	`{"type":"assistant","timestamp":"2026-11-20T10:00:13Z","message":{"content":[{"type":"text","text":"Installed the compiler; the arm tests still need a run."}]}}`,
	`{"type":"user","timestamp":"2026-11-20T10:01:00Z","message":{"content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"iVBOR"}},{"type":"text","text":"Here is the CI screen"}]}}`,
	`{"type":"user","timestamp":"2026-11-20T10:01:01Z","message":{"content":[{"type":"text","text":"[Request interrupted by user]"}]}}`,
	`{"type":"assistant","timestamp":"2026-11-20T10:01:02Z","message":{"content":[{"type":"text","text":"I see the same fail`,
}, "\n")

func readConv(t *testing.T, agent, content string) Conversation {
	t.Helper()
	path := filepath.Join(t.TempDir(), "s.jsonl")
	write(t, path, content)
	c, err := ReadConversation(context.Background(), Env{}, Session{Agent: agent, Path: path, Dir: "/w", ID: "s1", Title: "arm build"})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// itemsText is a conversation's items, one a line, for comparing.
func itemsText(c Conversation) string {
	var b strings.Builder
	for _, it := range c.Items {
		fmt.Fprintf(&b, "%s/%s %q", it.Kind, it.Role, it.Text)
		if it.Input != "" {
			fmt.Fprintf(&b, " in=%q", it.Input)
		}
		if it.Output != "" {
			fmt.Fprintf(&b, " out=%q", it.Output)
		}
		if it.Kind == KindEdit {
			fmt.Fprintf(&b, " +%d-%d", it.Added, it.Removed)
		}
		for _, f := range []struct {
			on   bool
			name string
		}{{it.Done, "done"}, {it.Failed, "failed"}, {it.Created, "created"}, {it.Deleted, "deleted"}} {
			if f.on {
				b.WriteString(" " + f.name)
			}
		}
		if it.Exit != 0 {
			fmt.Fprintf(&b, " exit=%d", it.Exit)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func TestClaudeConversation(t *testing.T) {
	c := readConv(t, "claude", claudeFull)
	want := `message/user "Fix the failing build on arm64"
message/assistant "Running the tests first."
command/assistant "go test ./..." out="--- FAIL: TestArm (0.00s)\n    arm_test.go:9: no cross compiler" done failed exit=1
read/assistant "/w/build/arm.go" done
edit/assistant "/w/build/arm.go" +2-1 done
edit/assistant "/w/docs/ünïcode ✓.md" +2-0 done created
edit/assistant "/w/build/arm.go" +3-2 done
tool/assistant "mcp__linear__get_issue" in="{\"id\":\"ENG-1\"}" done
tool/assistant "WebFetch" in="https://example.com/arm"
message/assistant "Installed the compiler; the arm tests still need a run."
note/user "Image attached"
message/user "Here is the CI screen"
note/user "Interrupted"
`
	if got := itemsText(c); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if c.MessagesOnly || c.Session.ID != "s1" {
		t.Fatalf("%+v", c)
	}
	if c.Plan == nil || len(c.Plan.Steps) != 3 || c.Plan.Steps[1] != (PlanStep{"Run the arm tests", "in_progress"}) || !c.Plan.Open() {
		t.Fatalf("plan: %+v", c.Plan)
	}
	if strings.Contains(itemsText(c), "secret plan") || strings.Contains(itemsText(c), "subagent") {
		t.Fatal("thinking or a sidechain was kept")
	}
	if c.Items[7].Size != len(`{"title":"arm"}`) {
		t.Fatalf("result size %d", c.Items[7].Size)
	}
	// The messages are what Transcript reads, less the interruption, which
	// is a note here.
	tr, _ := Transcript(context.Background(), Env{}, c.Session)
	if n := len(tr) - 1; n >= 0 && strings.HasPrefix(tr[n].Text, "[Request interrupted") {
		tr = tr[:n]
	}
	if turnsText(c.Turns()) != turnsText(tr) {
		t.Fatalf("Turns:\n%s\nTranscript:\n%s", turnsText(c.Turns()), turnsText(tr))
	}
}

// codexOld is a rollout as Codex wrote it before 0.158: messages as
// user_message/agent_message events (and again as response_items), tools
// as response_item function_calls with their outputs.
var codexOld = strings.Join([]string{
	`{"timestamp":"2026-08-01T09:00:00Z","type":"session_meta","payload":{"id":"c1","cwd":"/w","git":{"branch":"main"}}}`,
	`{"timestamp":"2026-08-01T09:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Add a retry to the uploader"}]}}`,
	`{"timestamp":"2026-08-01T09:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"Add a retry to the uploader","images":["data:image/png;base64,AAA"]}}`,
	`{"timestamp":"2026-08-01T09:00:02Z","type":"response_item","payload":{"type":"reasoning","summary":[],"encrypted_content":"gAAAA"}}`,
	`{"timestamp":"2026-08-01T09:00:03Z","type":"response_item","payload":{"type":"function_call","name":"update_plan","arguments":"{\"explanation\":\"Retry with backoff\",\"plan\":[{\"step\":\"Read the uploader\",\"status\":\"completed\"},{\"step\":\"Add the retry\",\"status\":\"in_progress\"}]}","call_id":"p1"}}`,
	`{"timestamp":"2026-08-01T09:00:04Z","type":"response_item","payload":{"type":"function_call_output","call_id":"p1","output":"Plan updated"}}`,
	`{"timestamp":"2026-08-01T09:00:05Z","type":"response_item","payload":{"type":"function_call","name":"shell","arguments":"{\"command\":[\"bash\",\"-lc\",\"go test ./upload\"],\"workdir\":\"/w\"}","call_id":"s1"}}`,
	`{"timestamp":"2026-08-01T09:00:06Z","type":"response_item","payload":{"type":"function_call_output","call_id":"s1","output":"{\"output\":\"FAIL upload\\n\",\"metadata\":{\"exit_code\":1,\"duration_seconds\":0.5}}"}}`,
	`{"timestamp":"2026-08-01T09:00:07Z","type":"response_item","payload":{"type":"function_call","name":"shell","arguments":"{\"command\":[\"apply_patch\",\"*** Begin Patch\\n*** Update File: upload/up.go\\n@@\\n-\\treturn err\\n+\\treturn retry(err)\\n+\\t// again\\n*** Add File: upload/retry.go\\n+package upload\\n*** Delete File: upload/old.go\\n*** End Patch\"]}","call_id":"s2"}}`,
	`{"timestamp":"2026-08-01T09:00:08Z","type":"response_item","payload":{"type":"function_call_output","call_id":"s2","output":"{\"output\":\"Success.\",\"metadata\":{\"exit_code\":0}}"}}`,
	`{"timestamp":"2026-08-01T09:00:09Z","type":"response_item","payload":{"type":"custom_tool_call","name":"apply_patch","input":"*** Begin Patch\n*** Update File: upload/up.go\n*** Move to: upload/upload.go\n@@\n-a\n+b\n*** End Patch","call_id":"s3"}}`,
	`{"timestamp":"2026-08-01T09:00:10Z","type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"s3","output":"Done"}}`,
	`{"timestamp":"2026-08-01T09:00:11Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"go test ./upload\"}","call_id":"s4"}}`,
	`{"timestamp":"2026-08-01T09:00:12Z","type":"response_item","payload":{"type":"function_call_output","call_id":"s4","output":"Chunk ID: 1\nWall time: 0.4 seconds\nProcess exited with code 0\nOutput:\nok upload\n"}}`,
	`{"timestamp":"2026-08-01T09:00:13Z","type":"response_item","payload":{"type":"local_shell_call","call_id":"s5","status":"completed","action":{"type":"exec","command":["git","commit","-m","retry it"]}}}`,
	`{"timestamp":"2026-08-01T09:00:14Z","type":"response_item","payload":{"type":"function_call","name":"view_image","arguments":"{\"path\":\"/w/shot.png\"}","call_id":"s6"}}`,
	`{"timestamp":"2026-08-01T09:00:15Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Added the retry."}]}}`,
	`{"timestamp":"2026-08-01T09:00:15Z","type":"event_msg","payload":{"type":"agent_message","message":"Added the retry."}}`,
	`{"timestamp":"2026-08-01T09:00:16Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"AgentMessage","content":[{"type":"Text","text":"Added the retry."}]}}}`,
}, "\n") + "\n"

func TestCodexConversationOlderRollout(t *testing.T) {
	c := readConv(t, "codex", codexOld)
	want := `note/user "Image attached"
message/user "Add a retry to the uploader"
command/assistant "go test ./upload" out="FAIL upload" done failed exit=1
edit/assistant "upload/up.go" +2-1 done
edit/assistant "upload/retry.go" +1-0 done created
edit/assistant "upload/old.go" +0-0 done deleted
edit/assistant "upload/upload.go" +1-1 done
command/assistant "go test ./upload" out="ok upload" done
command/assistant "git commit -m 'retry it'"
tool/assistant "view_image" in="{\"path\":\"/w/shot.png\"}"
message/assistant "Added the retry."
`
	if got := itemsText(c); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if c.Plan == nil || c.Plan.Text != "Retry with backoff" || len(c.Plan.Steps) != 2 || !c.Plan.Open() {
		t.Fatalf("plan: %+v", c.Plan)
	}
	tr, _ := Transcript(context.Background(), Env{}, c.Session)
	if turnsText(c.Turns()) != turnsText(tr) {
		t.Fatalf("Turns:\n%s\nTranscript:\n%s", turnsText(c.Turns()), turnsText(tr))
	}
}

// codexNew is a rollout as Codex 0.158–0.171 writes it, shaped after a
// real 0.171 rollout: item_completed events for messages and commands
// (each part of a command parsed, so one that only reads is reads), an
// apply_patch inside the JavaScript an "exec" call runs, an MCP tool, a
// web search, a plan limit, and an interruption.
var codexNew = strings.Join([]string{
	`{"timestamp":"2026-11-22T18:51:47Z","type":"session_meta","payload":{"id":"01a2","cwd":"/w","originator":"codex-tui","cli_version":"0.171.0"}}`,
	`{"timestamp":"2026-11-22T18:51:48Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>x</environment_context>"}]}}`,
	`{"timestamp":"2026-11-22T18:51:50Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","id":"u1","content":[{"type":"local_image","path":"/tmp/a.png"},{"type":"text","text":"Why is the page blank?"}]}}}`,
	`{"timestamp":"2026-11-22T18:51:51Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"Reasoning","id":"r1","summary_text":["hidden"],"raw_content":[]}}}`,
	`{"timestamp":"2026-11-22T18:51:52Z","type":"response_item","payload":{"type":"custom_tool_call","status":"completed","call_id":"x1","name":"exec","input":"const r=await tools.exec_command({cmd:\"sed -n '1,40p' web/page.tsx\"});text(r.output)\n"}}`,
	`{"timestamp":"2026-11-22T18:51:53Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"CommandExecution","id":"e1","command":["/bin/zsh","-lc","sed -n '1,40p' web/page.tsx"],"parsed_cmd":[{"type":"read","cmd":"sed -n '1,40p' web/page.tsx","name":"page.tsx","path":"web/page.tsx"}],"status":"completed","aggregated_output":"export default function Page() {}\n","exit_code":0}}}`,
	`{"timestamp":"2026-11-22T18:51:54Z","type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"x1","output":[{"type":"input_text","text":"Script completed\nWall time 0.1 seconds\nOutput:\n"},{"type":"input_text","text":"export default"}]}}`,
	`{"timestamp":"2026-11-22T18:51:55Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"CommandExecution","id":"e2","command":["/bin/zsh","-lc","npm test"],"parsed_cmd":[{"type":"unknown","cmd":"npm test"}],"status":"failed","aggregated_output":"1 failing\n","exit_code":2}}}`,
	`{"timestamp":"2026-11-22T18:51:56Z","type":"response_item","payload":{"type":"custom_tool_call","status":"completed","call_id":"x2","name":"exec","input":"await tools.apply_patch(\"*** Begin Patch\\n*** Update File: web/página.tsx\\n@@\\n-return null\\n+return <Main/>\\n*** End Patch\")\n"}}`,
	`{"timestamp":"2026-11-22T18:51:57Z","type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"x2","output":[{"type":"input_text","text":"Script completed\nWall time 0.1 seconds\nOutput:\n"}]}}`,
	`{"timestamp":"2026-11-22T18:51:58Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"McpToolCall","id":"m1","server":"finance","tool":"get_stock_price","arguments":{"ticker":"AAPL"},"status":"failed","error":{"message":"cancelled"}}}}`,
	`{"timestamp":"2026-11-22T18:51:59Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"Extension","kind":"web.search","id":"w1","query":"react blank page","results":null}}}`,
	`{"timestamp":"2026-11-22T18:52:00Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"AgentMessage","id":"a1","phase":"final_answer","content":[{"type":"Text","text":"Page returned null; fixed."}]}}}`,
	`{"timestamp":"2026-11-22T18:52:01Z","type":"event_msg","payload":{"type":"turn_aborted","reason":"interrupted"}}`,
	`{"timestamp":"2026-11-22T18:52:02Z","type":"event_msg","payload":{"type":"task_complete","last_agent_message":null,"error":{"message":"Quota exceeded. Check your plan.","codex_error_info":"usage_limit_exceeded"}}}`,
}, "\n") + "\n"

func TestCodexConversationItems(t *testing.T) {
	c := readConv(t, "codex", codexNew)
	want := `note/user "Image attached"
message/user "Why is the page blank?"
read/assistant "web/page.tsx" done
command/assistant "npm test" out="1 failing" done failed exit=2
edit/assistant "web/página.tsx" +1-1 done
tool/assistant "finance.get_stock_price" in="{\"ticker\":\"AAPL\"}" done failed
tool/assistant "web.search" in="react blank page" done
message/assistant "Page returned null; fixed."
note/user "Interrupted"
note/assistant "Quota exceeded. Check your plan." failed
`
	if got := itemsText(c); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if strings.Contains(itemsText(c), "hidden") {
		t.Fatal("reasoning was kept")
	}
}

func TestConversationEdgeCases(t *testing.T) {
	for _, agent := range []string{"claude", "codex"} {
		t.Run(agent, func(t *testing.T) {
			// An empty session.
			if c := readConv(t, agent, ""); len(c.Items) != 0 || c.Plan != nil || c.HasMessages() {
				t.Fatalf("empty: %+v", c)
			}
			// A missing file is an error, not an empty conversation.
			if _, err := ReadConversation(context.Background(), Env{}, Session{Agent: agent, Path: filepath.Join(t.TempDir(), "nope", "s.jsonl")}); err == nil {
				t.Fatal("missing file read")
			}
		})
	}
	// One message.
	c := readConv(t, "claude", `{"type":"user","message":{"content":"just this"}}`)
	if itemsText(c) != "message/user \"just this\"\n" || !c.HasMessages() {
		t.Fatalf("one message: %s", itemsText(c))
	}
	// An overlong line is skipped and the lines around it still read.
	huge := `{"type":"assistant","message":{"content":[{"type":"text","text":"` + strings.Repeat("x", 9<<20) + `"}]}}`
	c = readConv(t, "claude", strings.Join([]string{`{"type":"user","message":{"content":"before"}}`, huge, `{"type":"assistant","message":{"content":"after"}}`}, "\n"))
	if itemsText(c) != "message/user \"before\"\nmessage/assistant \"after\"\n" {
		t.Fatalf("overlong: %s", itemsText(c))
	}
	// Garbage between lines and a tool call whose input is not an object.
	c = readConv(t, "claude", "not json\n"+`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"z","name":"Odd","input":"text"}]}}`)
	if itemsText(c) != "tool/assistant \"Odd\"\n" {
		t.Fatalf("odd input: %s", itemsText(c))
	}
}

// Every other agent gets its messages, said to be all there is.
func TestConversationOtherAgents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	write(t, path, `{"type":"message","message":{"role":"user","content":"hello"}}`+"\n"+
		`{"type":"message","message":{"role":"assistant","content":[{"type":"text","text":"hi there"}]}}`)
	c, err := ReadConversation(context.Background(), Env{}, Session{Agent: "pi", Path: path})
	if err != nil || !c.MessagesOnly || itemsText(c) != "message/user \"hello\"\nmessage/assistant \"hi there\"\n" {
		t.Fatalf("%v %+v", err, c)
	}
	if _, err := ReadConversation(context.Background(), Env{}, Session{Agent: "some-acp-agent"}); err == nil {
		t.Fatal("an agent conch cannot read")
	}
}

func TestConversationCapsSize(t *testing.T) {
	var lines []string
	for i := 0; i < 6; i++ {
		lines = append(lines, fmt.Sprintf(`{"type":"user","message":{"content":"%d %s"}}`, i, strings.Repeat("y", 1<<20)))
	}
	c := readConv(t, "claude", strings.Join(lines, "\n"))
	if len(c.Items) != 3 || !strings.HasPrefix(c.Items[2].Text, "5 ") {
		t.Fatalf("kept %d items", len(c.Items))
	}
}

func TestLineStat(t *testing.T) {
	for _, tc := range []struct {
		old, new string
		a, r     int
	}{
		{"", "", 0, 0},
		{"", "a\nb\n", 2, 0},
		{"a\nb", "", 0, 2},
		{"a\nb\nc", "a\nx\nc", 1, 1},
		{"same", "same", 0, 0},
	} {
		if a, r := lineStat(tc.old, tc.new); a != tc.a || r != tc.r {
			t.Errorf("%q→%q: +%d -%d", tc.old, tc.new, a, r)
		}
	}
}

func TestCodexResult(t *testing.T) {
	for _, tc := range []struct {
		raw    string
		out    string
		failed bool
		exit   int
	}{
		{`"plain"`, "plain", false, 0},
		{`"{\"output\":\"x\",\"metadata\":{\"exit_code\":3}}"`, "x", true, 3},
		{`"Process exited with code 1\nOutput:\nboom"`, "boom", true, 1},
		{`[{"type":"input_text","text":"Script failed\nWall time 1s\nOutput:\n"},{"type":"input_text","text":"bad"}]`, "bad", true, 0},
		{`{"content":"c","success":false}`, "c", true, 0},
		{`42`, "", false, 0},
	} {
		out, failed, exit := codexResult([]byte(tc.raw))
		if out != tc.out || failed != tc.failed || exit != tc.exit {
			t.Errorf("%s: %q %v %d", tc.raw, out, failed, exit)
		}
	}
}

func TestShellLine(t *testing.T) {
	if got := shellLine([]string{"bash", "-lc", "ls -la"}); got != "ls -la" {
		t.Error(got)
	}
	if got := shellLine([]string{"git", "commit", "-m", "it's"}); got != `git commit -m 'it'\''s'` {
		t.Error(got)
	}
	if got := shellLine(nil); got != "" {
		t.Error(got)
	}
}

// TestHandoffGolden pins the whole document for a Claude conversation
// with each kind of item. Run with -update to rewrite it.
func TestHandoffGolden(t *testing.T) {
	old := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = old })
	c := readConv(t, "claude", claudeFull)
	c.Session = Session{Agent: "claude", ID: "s1", Dir: "/w", Branch: "fix/arm", Title: "arm build",
		Started: time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC), Updated: time.Date(2026, 11, 20, 10, 2, 0, 0, time.UTC)}
	got := Handoff(c)
	golden := filepath.Join("testdata", "handoff_claude.golden.md")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("handoff differs from %s (go test -run TestHandoffGolden -update):\n%s", golden, got)
	}
}

func TestHandoffStanding(t *testing.T) {
	c := readConv(t, "codex", codexNew)
	doc := Handoff(c)
	for _, want := range []string{
		"## Where it stands",
		"### Files changed\n\n- `web/página.tsx` (+1 −1)\n",
		"### Last error\n\n- _Quota exceeded. Check your plan._\n",
		"- Read `web/page.tsx`\n",
		"- Ran `npm test` — failed, exit 2\n  ```\n  1 failing\n  ```\n",
		"- Used `finance.get_stock_price`: `{\"ticker\":\"AAPL\"}` — failed\n",
		"The messages are word for word.",
	} {
		if !strings.Contains(doc, want) {
			t.Fatalf("missing %q in:\n%s", want, doc)
		}
	}
	if strings.Contains(doc, "Open plan") {
		t.Fatal("no plan, yet one shown")
	}

	// A command that failed and later passed is not the last error; one
	// that failed and was never run again is.
	items := []Item{
		{Kind: KindCommand, Text: "make", Failed: true, Done: true, Exit: 2},
		{Kind: KindCommand, Text: "make", Done: true},
	}
	if lastError(items) != nil {
		t.Fatal("a fixed command is still the last error")
	}
	items = append(items, Item{Kind: KindCommand, Text: "make lint", Failed: true, Done: true, Output: "vet: bad"}, Item{Kind: KindCommand, Text: "ls", Done: true})
	if e := lastError(items); e == nil || e.Text != "make lint" {
		t.Fatalf("last error: %+v", e)
	}
	// An agent error it answered after is past.
	if lastError([]Item{{Kind: KindNote, Failed: true, Text: "overloaded"}, {Kind: KindMessage, Role: "assistant", Text: "back"}}) != nil {
		t.Fatal("an answered error")
	}

	// A plan all done is not open; a text plan is.
	if (&Plan{Steps: []PlanStep{{"a", "completed"}}}).Open() || !(&Plan{Text: "do it"}).Open() || (*Plan)(nil).Open() {
		t.Fatal("Plan.Open")
	}
	doc = Handoff(Conversation{Session: Session{Agent: "claude"}, Items: []Item{{Kind: KindMessage, Role: "user", Text: "go"}}, Plan: &Plan{Text: "1. read\n2. write"}})
	if !strings.Contains(doc, "### Open plan\n\n1. read\n2. write\n") {
		t.Fatalf("text plan:\n%s", doc)
	}
}

// Markdown inside commands and output cannot break out of its code.
func TestHandoffFencesCode(t *testing.T) {
	c := Conversation{Session: Session{Agent: "codex"}, Items: []Item{
		{Kind: KindMessage, Role: "user", Text: "go"},
		{Kind: KindCommand, Role: "assistant", Text: "echo `date`", Done: true, Output: "```\n## not a heading"},
	}}
	doc := Handoff(c)
	if !strings.Contains(doc, "- Ran ``echo `date```") && !strings.Contains(doc, "- Ran `` echo `date` ``") {
		t.Fatalf("inline code:\n%s", doc)
	}
	if !strings.Contains(doc, "  ````\n  ```\n  ## not a heading\n  ````\n") {
		t.Fatalf("fence:\n%s", doc)
	}
}

// A command's long output keeps its end; a turn of very many steps is cut
// and counted.
func TestHandoffCutsSteps(t *testing.T) {
	out := "first line\n" + strings.Repeat("middle\n", 2000) + "the real error"
	items := []Item{{Kind: KindMessage, Role: "user", Text: "go"},
		{Kind: KindCommand, Role: "assistant", Text: "make", Done: true, Failed: true, Output: out}}
	for i := 0; i < 500; i++ {
		items = append(items, Item{Kind: KindRead, Role: "assistant", Text: fmt.Sprintf("/w/f%03d.go", i), Done: true})
	}
	doc := Handoff(Conversation{Session: Session{Agent: "claude"}, Items: items})
	if !strings.Contains(doc, "the real error") || strings.Contains(doc, "first line") || !strings.Contains(doc, "lines cut …") {
		t.Fatalf("output not cut to its end:\n%s", doc)
	}
	if !strings.Contains(doc, "files read_") || strings.Contains(doc, "f499.go") {
		t.Fatalf("steps not cut:\n%s", doc[len(doc)-400:])
	}
}

// A conversation far over the limit keeps the first request and the
// latest turns whole, shortens what it can between, and leaves out the
// oldest of those.
func TestHandoffOverLimit(t *testing.T) {
	items := []Item{{Kind: KindMessage, Role: "user", Text: "FIRST REQUEST"}}
	for i := 0; i < 3000; i++ {
		items = append(items,
			Item{Kind: KindMessage, Role: "assistant", Text: fmt.Sprintf("answer %d %s", i, strings.Repeat("é", 3000))},
			Item{Kind: KindCommand, Role: "assistant", Text: "make", Done: true},
			Item{Kind: KindMessage, Role: "user", Text: fmt.Sprintf("question %d", i)})
	}
	items = append(items, Item{Kind: KindMessage, Role: "assistant", Text: "LAST ANSWER"})
	doc := Handoff(Conversation{Session: Session{Agent: "claude"}, Items: items})
	if len(doc) > maxHandoffBytes {
		t.Fatalf("%d bytes", len(doc))
	}
	first, gap, shortened := strings.Index(doc, "FIRST REQUEST"), strings.Index(doc, "earlier turns left out"), strings.Index(doc, "turns are shortened")
	if first < 0 || gap < first || shortened < gap || !strings.HasSuffix(doc, "LAST ANSWER\n") {
		t.Fatalf("order: first %d gap %d shortened %d", first, gap, shortened)
	}
	// The latest turns are whole: the last long answer is all there.
	if !strings.Contains(doc, "answer 2999 "+strings.Repeat("é", 3000)+"\n") {
		t.Fatal("latest turn not whole")
	}
	// The shortened ones say what they did in counts, and are cut on a
	// character.
	if !strings.Contains(doc, "_1 command run_") || !strings.Contains(doc, "_… cut …_") || strings.ContainsRune(doc, '�') {
		t.Fatal("middle not shortened")
	}
}
