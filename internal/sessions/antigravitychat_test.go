package sessions

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// agy's steps as agy 1.5.16 wrote them for a real task (file tools, then
// a command): a tool call sits on the model's step with each argument a
// JSON string of its own, and its result is the GENERIC step after it,
// behind agy's own timing lines.
var agyChatSteps = []string{
	`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-11-24T20:30:30Z","content":"<USER_REQUEST>\nRename hello to goodbye in notes.txt\n</USER_REQUEST>\n<ADDITIONAL_METADATA>\nThe current local time is: x.\n</ADDITIONAL_METADATA>"}`,
	`{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-11-24T20:30:31Z","model":"m","thinking":"hidden","content":"On it.","tool_calls":[{"name":"write_file","args":{"Content":"\"hello from agy\\n\"","Path":"\"/w/proj/notes.txt\""}}]}`,
	`{"step_index":2,"source":"MODEL","type":"GENERIC","status":"DONE","created_at":"2026-11-24T20:30:32Z","content":"Created At: 2026-11-24T12:30:30-08:00\nCompleted At: 2026-11-24T12:30:31-08:00\nCreated At: 2026-11-24T12:30:30-08:00\nCompleted At: 2026-11-24T12:30:31-08:00\nCreated file."}`,
	`{"step_index":3,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-11-24T20:30:33Z","model":"m","tool_calls":[{"name":"edit_file","args":{"Path":"\"/w/proj/notes.txt\"","Replacement":"\"goodbye\"","Target":"\"hello\""}},{"name":"read_file","args":{"Path":"\"/w/proj/notes.txt\""}}]}`,
	`{"step_index":4,"source":"MODEL","type":"GENERIC","status":"DONE","created_at":"2026-11-24T20:30:34Z","content":"Created At: x\nCompleted At: y\nMade 1 replacement in /w/proj/notes.txt"}`,
	`{"step_index":5,"source":"SYSTEM","type":"SYSTEM_MESSAGE","status":"DONE","created_at":"2026-11-24T20:30:35Z","content":"<SYSTEM_MESSAGE>work efficiently</SYSTEM_MESSAGE>"}`,
	`{"step_index":6,"source":"MODEL","type":"GENERIC","status":"ERROR","created_at":"2026-11-24T20:30:36Z","content":"Created At: x\nfile is locked"}`,
	`not json`,
	`{"step_index":7,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-11-24T20:30:37Z","model":"m","tool_calls":[{"name":"run_command","args":{"CommandLine":"\"ls -la\"","WaitMsBeforeAsync":"\"5000\"","toolSummary":"\"List directory entries\""}},{"name":"grep_search","args":{"Query":"\"goodbye\"","SearchPath":"\"/w/proj\"","IsRegex":"false"}}]}`,
	`{"step_index":8,"source":"MODEL","type":"GENERIC","status":"DONE","created_at":"2026-11-24T20:30:38Z","content":"Created At: x\nCompleted At: y\nCommand exited with code 0.\n\nCommand output:\ntotal 0"}`,
	`{"step_index":9,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-11-24T20:30:39Z","model":"m","content":"Done."}`,
}

func agyChatSession(t *testing.T, steps ...string) (Env, Session, string) {
	t.Helper()
	home := t.TempDir()
	f := filepath.Join(home, ".gemini", "antigravity-cli", "brain", "c1", ".system_generated", "logs", "transcript.jsonl")
	write(t, f, strings.Join(steps, "\n")+"\n")
	db := filepath.Join(home, ".gemini", "antigravity-cli", "conversation_summaries.db")
	write(t, db, "")
	return Env{Home: home}, Session{Agent: "antigravity", ID: "c1", Dir: "/w/proj", Path: db}, f
}

func TestAntigravityChatKeepsToolsAndResults(t *testing.T) {
	env, s, f := agyChatSession(t, agyChatSteps...)
	up, err := ChatSince(context.Background(), env, s, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, tn := range up.Turns {
		got = append(got, tn.Role+":"+tn.Text+"|"+tn.Detail+"|"+tn.InputKind)
	}
	want := []string{
		"user:Rename hello to goodbye in notes.txt||",
		"assistant:On it.||",
		"tool:write_file|/w/proj/notes.txt|code",
		"tool:edit_file|/w/proj/notes.txt|diff",
		"tool:read_file|/w/proj/notes.txt|",
		"tool:run_command|List directory entries|code",
		"tool:grep_search|goodbye  in /w/proj|",
		"assistant:Done.||",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("turns:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	byName := map[string]ChatTurn{}
	for _, tn := range up.Turns {
		byName[tn.Text] = tn
	}
	if w := byName["write_file"]; w.Input != "hello from agy\n" || w.Output != "Created file." || !w.Done || w.Error {
		t.Errorf("write: %+v", w)
	}
	if e := byName["edit_file"]; e.Input != diffLines("hello", "goodbye") || e.Output != "Made 1 replacement in /w/proj/notes.txt" || !e.Done {
		t.Errorf("edit: %+v", e)
	}
	// Two calls in one step: their results come in the order they were made.
	if r := byName["read_file"]; r.Output != "file is locked" || !r.Error || !r.Done {
		t.Errorf("read: %+v", r)
	}
	if c := byName["run_command"]; c.Input != "ls -la" || !strings.HasPrefix(c.Output, "Command exited with code 0.") {
		t.Errorf("command: %+v", c)
	}
	// A call whose result has not come yet is still running.
	if g := byName["grep_search"]; g.Done || g.Output != "" {
		t.Errorf("grep: %+v", g)
	}
	if up.Turns[0].Time.IsZero() {
		t.Error("turns keep their time")
	}

	// Unchanged: nothing sent. Its result arrives: only that is.
	same, _ := ChatSince(context.Background(), env, s, up.Epoch, up.Ver)
	if same.Reset || len(same.Turns) != 0 {
		t.Fatalf("unchanged: %+v", same)
	}
	more := `{"step_index":10,"source":"MODEL","type":"GENERIC","status":"DONE","created_at":"2026-11-24T20:30:40Z","content":"Created At: x\nnotes.txt:1: goodbye from agy"}` + "\n"
	fh, err := os.OpenFile(f, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	fh.WriteString(more)
	fh.Close()
	later := time.Now().Add(2 * time.Second)
	os.Chtimes(f, later, later) // a write in the same instant still reads as changed
	next, err := ChatSince(context.Background(), env, s, up.Epoch, up.Ver)
	if err != nil {
		t.Fatal(err)
	}
	var grep *ChatTurn
	for i := range next.Turns {
		if next.Turns[i].Text == "grep_search" {
			grep = &next.Turns[i]
		}
	}
	if grep == nil || !grep.Done || grep.Output != "notes.txt:1: goodbye from agy" {
		t.Fatalf("the result did not arrive: %+v", next)
	}
}

// A conversation agy has named but not written yet is an error to read,
// as any missing transcript is; the server shows an empty chat for it.
func TestAntigravityChatWithoutATranscript(t *testing.T) {
	env, s, f := agyChatSession(t)
	os.Remove(f)
	if _, err := Chat(context.Background(), env, s); err == nil {
		t.Fatal("a missing transcript gave a chat")
	}
}
