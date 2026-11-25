package sessions

import (
	"strings"
	"testing"
)

// geminiFull is a Gemini CLI 0.66 chat in JSONL, in the shapes
// chatRecordingService.ts writes (the words made up): a header, a request,
// Gemini's thoughts and a failed command recorded on its message, a read,
// an edit, a file written under a non-ASCII path, a to-do list, an
// unknown tool, a call that never got its result, a message written again
// as it grew, a $patch filling in a result, a $rewindTo dropping a turn,
// Gemini's own info line, an error, an image — and a last line cut off.
var geminiFull = strings.Join([]string{
	`{"sessionId":"g-1","projectHash":"abc","startTime":"2026-11-20T10:00:00.000Z","lastUpdated":"2026-11-20T10:00:00.000Z"}`,
	`{"id":"m1","timestamp":"2026-11-20T10:00:01.000Z","type":"user","content":[{"text":"Fix the failing build on arm64"}]}`,
	`{"id":"m2","timestamp":"2026-11-20T10:00:02.000Z","type":"gemini","content":"Running the tests first.","thoughts":[{"subject":"Plan","description":"secret plan"}],"toolCalls":[{"id":"run_shell_command_1","name":"run_shell_command","args":{"command":"go test ./..."},"status":"executing"}]}`,
	`{"id":"m2","timestamp":"2026-11-20T10:00:02.000Z","type":"gemini","content":"Running the tests first.","thoughts":[{"subject":"Plan","description":"secret plan"}],"toolCalls":[{"id":"run_shell_command_1","name":"run_shell_command","args":{"command":"go test ./..."},"status":"error","result":[{"functionResponse":{"id":"run_shell_command_1","name":"run_shell_command","response":{"output":"Output: --- FAIL: TestArm\nExit Code: 1"}}}]}]}`,
	`{"id":"m3","timestamp":"2026-11-20T10:00:03.000Z","type":"gemini","content":"","toolCalls":[{"id":"read_file_2","name":"read_file","args":{"file_path":"/w/build/arm.go"},"status":"success","result":[{"functionResponse":{"id":"read_file_2","name":"read_file","response":{"output":"package build\n"}}}]},{"id":"replace_3","name":"replace","args":{"file_path":"/w/build/arm.go","old_string":"a\nb\nc","new_string":"a\nB\nB2\nc"},"status":"success"},{"id":"write_file_4","name":"write_file","args":{"file_path":"/w/docs/ünïcode ✓.md","content":"one\ntwo\n"},"status":"success","result":[{"functionResponse":{"id":"write_file_4","name":"write_file","response":{"output":"Successfully created and wrote to new file"}}}]}]}`,
	`{"$patch":{"updates":[{"id":"m3","toolCalls":[{"id":"replace_3","result":[{"functionResponse":{"id":"replace_3","name":"replace","response":{"output":"Successfully modified file"}}}]}]}]}}`,
	`{"id":"m4","timestamp":"2026-11-20T10:00:04.000Z","type":"gemini","content":"","toolCalls":[{"id":"write_todos_5","name":"write_todos","args":{"todos":[{"description":"Install the cross compiler","status":"completed"},{"description":"Run the arm tests","status":"in_progress"},{"description":"Dropped idea","status":"cancelled"}]},"status":"success","result":[{"functionResponse":{"id":"write_todos_5","name":"write_todos","response":{"output":"ok"}}}]},{"id":"mcp_6","name":"linear_get_issue","args":{"id":"ENG-1"},"status":"success","result":[{"functionResponse":{"id":"mcp_6","name":"linear_get_issue","response":{"error":"not found"}}}]},{"id":"web_fetch_7","name":"web_fetch","args":{"prompt":"https://example.com/arm"},"status":"executing"}]}`,
	`{"$set":{"lastUpdated":"2026-11-20T10:00:05.000Z"}}`,
	`{"id":"m5","timestamp":"2026-11-20T10:00:05.000Z","type":"user","content":[{"text":"a request taken back"}]}`,
	`{"$rewindTo":"m5"}`,
	`{"id":"m6","timestamp":"2026-11-20T10:00:06.000Z","type":"info","content":"Request cancelled."}`,
	`{"id":"m7","timestamp":"2026-11-20T10:00:07.000Z","type":"gemini","content":[{"text":"Installed the compiler; "},{"text":"the arm tests still need a run."}]}`,
	`{"id":"m8","timestamp":"2026-11-20T10:01:00.000Z","type":"user","content":[{"inlineData":{"mimeType":"image/png","data":"iVBOR"}},{"text":"Here is the CI screen"}]}`,
	`{"id":"m9","timestamp":"2026-11-20T10:01:01.000Z","type":"error","content":"Quota exceeded for the model"}`,
	`{"id":"m10","timestamp":"2026-11-20T10:01:02.000Z","type":"gemini","content":"I see the same fail`,
}, "\n")

const geminiFullItems = `message/user "Fix the failing build on arm64"
message/assistant "Running the tests first."
command/assistant "go test ./..." out="--- FAIL: TestArm" done failed exit=1
read/assistant "/w/build/arm.go" done
edit/assistant "/w/build/arm.go" +2-1 done
edit/assistant "/w/docs/ünïcode ✓.md" +2-0 done
tool/assistant "linear_get_issue" in="{\"id\":\"ENG-1\"}" done failed
tool/assistant "web_fetch" in="{\"prompt\":\"https://example.com/arm\"}"
message/assistant "Installed the compiler; the arm tests still need a run."
note/user "Image attached"
message/user "Here is the CI screen"
note/assistant "Error: Quota exceeded for the model" failed
`

func TestGeminiConversation(t *testing.T) {
	c := readConv(t, "gemini", geminiFull)
	if c.MessagesOnly {
		t.Fatal("Gemini CLI's tools were not read")
	}
	if got := itemsText(c); got != geminiFullItems {
		t.Fatalf("items:\n%s\nwant:\n%s", got, geminiFullItems)
	}
	if p := c.Plan; p == nil || len(p.Steps) != 2 || p.Steps[1] != (PlanStep{Text: "Run the arm tests", Status: "in_progress"}) {
		t.Fatalf("plan: %+v", c.Plan)
	}
	for _, it := range c.Items {
		if strings.Contains(it.Text, "secret plan") || strings.Contains(it.Text, "taken back") {
			t.Fatalf("kept %q", it.Text)
		}
	}
}

// An older Gemini CLI kept a chat as one JSON object, and kept function
// responses in the user message after the call.
func TestGeminiConversationOneObject(t *testing.T) {
	c := readConv(t, "gemini", `{"sessionId":"g2","messages":[
{"id":"a","type":"user","content":"List the files"},
{"id":"b","type":"gemini","content":[{"functionCall":{"id":"c1","name":"run_shell_command","args":{"command":"ls"}}}]},
{"id":"c","type":"user","content":[{"functionResponse":{"id":"c1","name":"run_shell_command","response":{"output":"Command: ls\nDirectory: (root)\nOutput: a.go\nb.go\nError: (none)\nExit Code: 0\nSignal: (none)"}}}]},
{"id":"d","type":"gemini","content":"Two files."}]}`)
	want := `message/user "List the files"
command/assistant "ls" out="a.go\nb.go" done
message/assistant "Two files."
`
	if got := itemsText(c); got != want {
		t.Fatalf("items:\n%s", got)
	}
}

// qwenFull is a Qwen Code 0.27 session (chatRecordingService.ts' records,
// the words made up): a request, a thought and a failed command, a read
// and an edit, a to-do list, an unknown tool's error, a call with no
// result, a message from another agent (a subtype), a turn rewound and
// taken another way — both kept in the file, only the second the
// conversation — a title record beside the chain, and a cut-off line.
var qwenFull = strings.Join([]string{
	`{"uuid":"u1","parentUuid":null,"sessionId":"q-1","timestamp":"2026-11-20T10:00:01.000Z","type":"user","cwd":"/w","message":{"role":"user","parts":[{"text":"Fix the failing build on arm64"}]}}`,
	`{"uuid":"u2","parentUuid":"u1","sessionId":"q-1","timestamp":"2026-11-20T10:00:02.000Z","type":"assistant","cwd":"/w","message":{"role":"model","parts":[{"text":"secret plan","thought":true},{"text":"Running the tests first."},{"functionCall":{"id":"call_1","name":"run_shell_command","args":{"command":"go test ./..."}}}]}}`,
	`{"uuid":"u3","parentUuid":"u2","sessionId":"q-1","timestamp":"2026-11-20T10:00:03.000Z","type":"tool_result","cwd":"/w","message":{"role":"user","parts":[{"functionResponse":{"id":"call_1","name":"run_shell_command","response":{"output":"Command: go test ./...\nDirectory: (root)\nOutput: --- FAIL: TestArm\nError: (none)\nExit Code: 1\nSignal: (none)\nProcess Group PGID: 4242"}}}]},"toolCallResult":{"callId":"call_1","status":"success"}}`,
	`{"uuid":"u4","parentUuid":"u3","sessionId":"q-1","timestamp":"2026-11-20T10:00:04.000Z","type":"assistant","cwd":"/w","message":{"role":"model","parts":[{"functionCall":{"id":"call_2","name":"read_file","args":{"absolute_path":"/w/build/arm.go"}}},{"functionCall":{"id":"call_3","name":"edit","args":{"file_path":"/w/build/arm.go","old_string":"a\nb","new_string":"a\nB"}}},{"functionCall":{"id":"call_4","name":"todo_write","args":{"todos":[{"id":"1","content":"Run the arm tests","status":"pending"}]}}}]}}`,
	`{"uuid":"u5","parentUuid":"u4","sessionId":"q-1","timestamp":"2026-11-20T10:00:05.000Z","type":"tool_result","cwd":"/w","message":{"role":"user","parts":[{"functionResponse":{"id":"call_2","name":"read_file","response":{"output":"package build"}}}]}}`,
	`{"uuid":"u6","parentUuid":"u5","sessionId":"q-1","timestamp":"2026-11-20T10:00:06.000Z","type":"tool_result","cwd":"/w","message":{"role":"user","parts":[{"functionResponse":{"id":"call_3","name":"edit","response":{"output":"ok"}}}]}}`,
	`{"uuid":"u7","parentUuid":"u6","sessionId":"q-1","timestamp":"2026-11-20T10:00:07.000Z","type":"user","cwd":"/w","message":{"role":"user","parts":[{"text":"a request taken back"}]}}`,
	`{"uuid":"u8","parentUuid":"u6","sessionId":"q-1","timestamp":"2026-11-20T10:00:08.000Z","type":"user","subtype":"agent_message","cwd":"/w","message":{"role":"user","parts":[{"text":"a message from another agent"}]}}`,
	`{"uuid":"u9","parentUuid":"u8","sessionId":"q-1","timestamp":"2026-11-20T10:00:09.000Z","type":"assistant","cwd":"/w","message":{"role":"model","parts":[{"functionCall":{"id":"call_5","name":"mcp_linear_get_issue","args":{"id":"ENG-1"}}},{"functionCall":{"id":"call_6","name":"web_fetch","args":{"url":"https://example.com"}}}]}}`,
	`{"uuid":"u10","parentUuid":"u9","sessionId":"q-1","timestamp":"2026-11-20T10:00:10.000Z","type":"tool_result","cwd":"/w","message":{"role":"user","parts":[{"functionResponse":{"id":"call_5","name":"mcp_linear_get_issue","response":{"error":"not found"}}}]},"toolCallResult":{"callId":"call_5","status":"error"}}`,
	`{"uuid":"u11","parentUuid":"u10","sessionId":"q-1","timestamp":"2026-11-20T10:00:11.000Z","type":"assistant","cwd":"/w","message":{"role":"model","parts":[{"text":"The ticket is gone; the arm tests still need a run."}]}}`,
	`{"uuid":"u12","parentUuid":null,"sessionId":"q-1","timestamp":"2026-11-20T10:00:12.000Z","type":"system","subtype":"custom_title","cwd":"/w","systemPayload":{"customTitle":"arm build"}}`,
	`{"uuid":"u13","parentUuid":"u11","sessionId":"q-1","timestamp":"2026-11-20T10:00:13.000Z","type":"user","cwd":"/w","message":{"role":"user","parts":[{"text":"Here is`,
}, "\n")

const qwenFullItems = `message/user "Fix the failing build on arm64"
message/assistant "Running the tests first."
command/assistant "go test ./..." out="--- FAIL: TestArm" done failed exit=1
read/assistant "/w/build/arm.go" done
edit/assistant "/w/build/arm.go" +1-1 done
tool/assistant "mcp_linear_get_issue" in="{\"id\":\"ENG-1\"}" done failed
tool/assistant "web_fetch" in="{\"url\":\"https://example.com\"}"
message/assistant "The ticket is gone; the arm tests still need a run."
`

func TestQwenConversation(t *testing.T) {
	c := readConv(t, "qwen", qwenFull)
	if c.MessagesOnly {
		t.Fatal("Qwen Code's tools were not read")
	}
	if got := itemsText(c); got != qwenFullItems {
		t.Fatalf("items:\n%s\nwant:\n%s", got, qwenFullItems)
	}
	if p := c.Plan; p == nil || len(p.Steps) != 1 || p.Steps[0].Text != "Run the arm tests" {
		t.Fatalf("plan: %+v", c.Plan)
	}
}

// Records with no ids, from an older Qwen Code, are read in the file's
// order; a chain with a gap is read from the gap, as Qwen resumes it.
func TestQwenChain(t *testing.T) {
	old := readConv(t, "qwen", `{"type":"user","message":{"role":"user","parts":[{"text":"one"}]}}
{"type":"assistant","message":{"role":"model","parts":[{"text":"two"}]}}`)
	if got := itemsText(old); got != "message/user \"one\"\nmessage/assistant \"two\"\n" {
		t.Fatalf("no ids:\n%s", got)
	}
	gap := readConv(t, "qwen", `{"uuid":"a","parentUuid":null,"type":"user","message":{"role":"user","parts":[{"text":"lost"}]}}
{"uuid":"c","parentUuid":"b","type":"user","message":{"role":"user","parts":[{"text":"after the gap"}]}}
{"uuid":"d","parentUuid":"c","type":"assistant","message":{"role":"model","parts":[{"text":"kept"}]}}`)
	if got := itemsText(gap); got != "message/user \"after the gap\"\nmessage/assistant \"kept\"\n" {
		t.Fatalf("gap:\n%s", got)
	}
	loop := readConv(t, "qwen", `{"uuid":"a","parentUuid":"b","type":"user","message":{"role":"user","parts":[{"text":"x"}]}}
{"uuid":"b","parentUuid":"a","type":"assistant","message":{"role":"model","parts":[{"text":"y"}]}}`)
	if len(loop.Items) != 2 {
		t.Fatalf("loop: %s", itemsText(loop))
	}
}

// copilotFull is a Copilot CLI events.jsonl (session-events.d.ts' events,
// the words made up): a typed request, a hidden prompt, reasoning, a
// failed command, a view, an edit, a file created, report_intent, an
// unknown tool, a call with no result, a subagent's events, an error, an
// interruption, an image — and a cut-off line.
var copilotFull = strings.Join([]string{
	`{"id":"e0","parentId":null,"timestamp":"2026-11-20T10:00:00.000Z","type":"session.start","data":{"sessionId":"c-1","context":{"cwd":"/w"}}}`,
	`{"id":"e1","parentId":"e0","timestamp":"2026-11-20T10:00:01.000Z","type":"user.message","data":{"content":"Fix the failing build on arm64"}}`,
	`{"id":"e2","parentId":"e1","timestamp":"2026-11-20T10:00:01.500Z","type":"user.message","data":{"content":"skill text","source":"skill"}}`,
	`{"id":"e3","parentId":"e2","timestamp":"2026-11-20T10:00:02.000Z","type":"assistant.reasoning","data":{"content":"secret plan"}}`,
	`{"id":"e4","parentId":"e3","timestamp":"2026-11-20T10:00:02.100Z","type":"assistant.message","data":{"messageId":"a1","content":"Running the tests first.","encryptedContent":"gAAAA","toolRequests":[{"toolCallId":"t1","name":"bash"}]}}`,
	`{"id":"e5","parentId":"e4","timestamp":"2026-11-20T10:00:02.200Z","type":"tool.execution_start","data":{"toolCallId":"t0","toolName":"report_intent","arguments":{"intent":"Testing"}}}`,
	`{"id":"e6","parentId":"e5","timestamp":"2026-11-20T10:00:02.300Z","type":"tool.execution_start","data":{"toolCallId":"t1","toolName":"bash","arguments":{"command":"go test ./...","description":"Run tests"}}}`,
	`{"id":"e7","parentId":"e6","timestamp":"2026-11-20T10:00:03.000Z","type":"tool.execution_complete","data":{"toolCallId":"t1","success":false,"result":{"content":"--- FAIL: TestArm"}}}`,
	`{"id":"e8","parentId":"e7","timestamp":"2026-11-20T10:00:04.000Z","type":"tool.execution_start","data":{"toolCallId":"t2","toolName":"view","arguments":{"path":"/w/build/arm.go"}}}`,
	`{"id":"e9","parentId":"e8","timestamp":"2026-11-20T10:00:04.100Z","type":"tool.execution_complete","data":{"toolCallId":"t2","success":true,"result":{"content":"package build"}}}`,
	`{"id":"e10","parentId":"e9","timestamp":"2026-11-20T10:00:05.000Z","type":"tool.execution_start","data":{"toolCallId":"t3","toolName":"edit","arguments":{"path":"/w/build/arm.go","old_str":"a","new_str":"b\nc"}}}`,
	`{"id":"e11","parentId":"e10","timestamp":"2026-11-20T10:00:05.100Z","type":"tool.execution_complete","data":{"toolCallId":"t3","success":true,"result":{"content":"ok"}}}`,
	`{"id":"e12","parentId":"e11","timestamp":"2026-11-20T10:00:06.000Z","type":"tool.execution_start","data":{"toolCallId":"t4","toolName":"create","arguments":{"path":"/w/docs/ünïcode ✓.md","file_text":"one\ntwo\n"}}}`,
	`{"id":"e13","parentId":"e12","timestamp":"2026-11-20T10:00:06.100Z","type":"tool.execution_complete","data":{"toolCallId":"t4","success":true,"result":{"content":"Created"}}}`,
	`{"id":"e14","parentId":"e13","timestamp":"2026-11-20T10:00:07.000Z","type":"tool.execution_start","data":{"toolCallId":"t5","toolName":"task","arguments":{"prompt":"look around"}}}`,
	`{"id":"e15","parentId":"e14","timestamp":"2026-11-20T10:00:07.100Z","type":"assistant.message","data":{"messageId":"s1","content":"a subagent's words","parentToolCallId":"t5"}}`,
	`{"id":"e16","parentId":"e15","timestamp":"2026-11-20T10:00:07.200Z","type":"tool.execution_start","data":{"toolCallId":"s2","toolName":"bash","arguments":{"command":"ls"},"parentToolCallId":"t5"}}`,
	`{"id":"e17","parentId":"e16","timestamp":"2026-11-20T10:00:08.000Z","type":"tool.execution_complete","data":{"toolCallId":"t5","success":false,"error":{"message":"agent failed"}}}`,
	`{"id":"e18","parentId":"e17","timestamp":"2026-11-20T10:00:09.000Z","type":"assistant.message","data":{"messageId":"a2","content":"Installed the compiler; the arm tests still need a run."}}`,
	`{"id":"e19","parentId":"e18","timestamp":"2026-11-20T10:01:00.000Z","type":"user.message","data":{"content":"Here is the CI screen","attachments":[{"type":"image","path":"/tmp/ci.png"}]}}`,
	`{"id":"e20","parentId":"e19","timestamp":"2026-11-20T10:01:00.500Z","type":"tool.execution_start","data":{"toolCallId":"t6","toolName":"github-mcp-server-get_issue","arguments":{"number":7}}}`,
	`{"id":"e21","parentId":"e20","timestamp":"2026-11-20T10:01:01.000Z","type":"abort","data":{"reason":"user"}}`,
	`{"id":"e22","parentId":"e21","timestamp":"2026-11-20T10:01:02.000Z","type":"session.error","data":{"errorType":"quota","message":"Quota exceeded"}}`,
	`{"id":"e23","parentId":"e22","timestamp":"2026-11-20T10:01:03.000Z","type":"assistant.message","data":{"content":"I see the same fa`,
}, "\n")

const copilotFullItems = `message/user "Fix the failing build on arm64"
message/assistant "Running the tests first."
command/assistant "go test ./..." out="--- FAIL: TestArm" done failed
read/assistant "/w/build/arm.go" done
edit/assistant "/w/build/arm.go" +2-1 done
edit/assistant "/w/docs/ünïcode ✓.md" +2-0 done created
tool/assistant "task" in="{\"prompt\":\"look around\"}" done failed
message/assistant "Installed the compiler; the arm tests still need a run."
note/user "Image attached"
message/user "Here is the CI screen"
tool/assistant "github-mcp-server-get_issue" in="{\"number\":7}"
note/user "Interrupted"
note/assistant "Error: Quota exceeded" failed
`

func TestCopilotConversation(t *testing.T) {
	c := readConv(t, "copilot", copilotFull)
	if c.MessagesOnly {
		t.Fatal("Copilot CLI's tools were not read")
	}
	if got := itemsText(c); got != copilotFullItems {
		t.Fatalf("items:\n%s\nwant:\n%s", got, copilotFullItems)
	}
}

func TestShellResult(t *testing.T) {
	for _, tc := range []struct {
		in, out string
		exit    int
	}{
		{"Output: hi", "hi", 0},
		{"Output: (empty)", "", 0},
		{"Output: a\nb\nExit Code: 2", "a\nb", 2},
		{"Output: a\nError: spawn failed\nExit Code: 127\nSignal: (none)", "a", 127},
		{"Command: ls\nDirectory: (root)\nOutput: x\nError: (none)\nExit Code: 0\nSignal: (none)\nProcess Group PGID: 1", "x", 0},
		{"Command: ls\nDirectory: (root)\nOutput: x\nError: (none)\nExit Code: (none)\nSignal: 15\nProcess Group PGID: (none)", "x", 0},
		{"Output: the line Exit Code: 3 inside\nmore", "the line Exit Code: 3 inside\nmore", 0},
		{shellCancelled, shellCancelled, 0},
		{"plain stdout", "plain stdout", 0},
		{"", "", 0},
	} {
		if out, exit := shellResult(tc.in); out != tc.out || exit != tc.exit {
			t.Errorf("%q: %q %d", tc.in, out, exit)
		}
	}
}

// Empty and broken files read as empty conversations, never an error,
// as the other readers do; a missing one is an error.
func TestMoreConversationEdges(t *testing.T) {
	for _, agent := range []string{"gemini", "qwen", "copilot"} {
		for _, body := range []string{"", "\n\n", "not json", `{"type":"user"`} {
			if c := readConv(t, agent, body); len(c.Items) != 0 {
				t.Errorf("%s %q: %s", agent, body, itemsText(c))
			}
		}
		if _, err := ReadConversation(t.Context(), Env{}, Session{Agent: agent, Path: "/nowhere/s.jsonl"}); err == nil {
			t.Errorf("%s: no error for a missing file", agent)
		}
	}
	// One message.
	if c := readConv(t, "gemini", `{"sessionId":"g","projectHash":"h"}`+"\n"+`{"id":"a","type":"user","content":"hi"}`); itemsText(c) != "message/user \"hi\"\n" {
		t.Fatalf("one message: %s", itemsText(c))
	}
}
