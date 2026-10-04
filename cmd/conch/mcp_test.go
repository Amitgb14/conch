package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
)

// a4MCPReply is one JSON-RPC reply, as a test reads it.
type a4MCPReply struct {
	ID     json.RawMessage `json:"id"`
	Result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError    bool            `json:"isError"`
		Structured json.RawMessage `json:"structuredContent"`
		// initialize and tools/list, read from the same reply.
		ProtocolVersion string          `json:"protocolVersion"`
		Capabilities    json.RawMessage `json:"capabilities"`
		ServerInfo      struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
		Instructions string    `json:"instructions"`
		Tools        []mcpTool `json:"tools"`
	} `json:"result"`
	Error *mcpError `json:"error"`
}

// text is what a model would read off the reply.
func (r a4MCPReply) text() string {
	var b []string
	for _, c := range r.Result.Content {
		b = append(b, c.Text)
	}
	return strings.Join(b, "\n")
}

// a4MCP serves the lines as one session and returns the replies in order,
// what went to stderr, and whether serving itself failed.
func a4MCP(t *testing.T, lines ...string) ([]a4MCPReply, string, error) {
	t.Helper()
	var out, errOut strings.Builder
	s := &mcpSession{w: &out, err: &errOut, dial: func() (*client.Client, error) { return connect(false) }}
	defer s.close()
	err := s.serve(strings.NewReader(strings.Join(lines, "\n") + "\n"))
	var replies []a4MCPReply
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line == "" {
			continue
		}
		// Stdout is the protocol: every line of it must be a JSON-RPC
		// message with the version in it, or a client stops reading.
		var envelope struct {
			JSONRPC string `json:"jsonrpc"`
		}
		if json.Unmarshal([]byte(line), &envelope) != nil || envelope.JSONRPC != "2.0" {
			t.Fatalf("stdout line is not a JSON-RPC message: %q", line)
		}
		var r a4MCPReply
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("reply %q: %v", line, err)
		}
		replies = append(replies, r)
	}
	return replies, errOut.String(), err
}

// a4MCPOne serves one tools/call and returns its reply.
func a4MCPOne(t *testing.T, tool string, args map[string]any) a4MCPReply {
	t.Helper()
	replies, _, err := a4MCP(t, a4MCPCall(1, tool, args))
	if err != nil {
		t.Fatalf("serve: %v", err)
	}
	if len(replies) != 1 {
		t.Fatalf("%s: %d replies, want 1: %+v", tool, len(replies), replies)
	}
	return replies[0]
}

func a4MCPCall(id int, tool string, args map[string]any) string {
	if args == nil {
		args = map[string]any{}
	}
	b, _ := json.Marshal(map[string]any{"name": tool, "arguments": args})
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":%s}`, id, b)
}

// TestA4MCPHandshake: the first thing any client does. The version conch
// answers with is its own, whatever was asked for, which is what the spec
// says to do; a notification is answered with nothing at all.
func TestA4MCPHandshake(t *testing.T) {
	a4Env(t)
	replies, stderr, err := a4MCP(t,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"1999-01-01","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"ping"}`,
	)
	if err != nil {
		t.Fatalf("serve: %v", err)
	}
	if stderr != "" {
		t.Errorf("stderr: %q", stderr)
	}
	if len(replies) != 2 {
		t.Fatalf("%d replies, want 2 (the notification gets none): %+v", len(replies), replies)
	}
	init := replies[0].Result
	if init.ProtocolVersion != mcpProtocol {
		t.Errorf("protocolVersion %q, want %q", init.ProtocolVersion, mcpProtocol)
	}
	if init.ServerInfo.Name != "conch" || init.ServerInfo.Version != proto.Version {
		t.Errorf("serverInfo %+v, want conch %s", init.ServerInfo, proto.Version)
	}
	if !strings.Contains(string(init.Capabilities), "tools") {
		t.Errorf("capabilities %s say nothing about tools", init.Capabilities)
	}
	// The instructions are what a model reads before it calls anything:
	// they must say the person sees the panes, and that scope is a thing.
	for _, want := range []string{"usage", "tree", "started"} {
		if !strings.Contains(init.Instructions, want) {
			t.Errorf("instructions do not mention %q: %q", want, init.Instructions)
		}
	}
	if string(replies[1].ID) != "2" || replies[1].Error != nil {
		t.Errorf("ping: %+v", replies[1])
	}
}

// TestA4MCPToolList: the tools the roadmap asks for, each with a schema a
// client can read, in a fixed order — a model that saw them once should see
// the same list next time.
func TestA4MCPToolList(t *testing.T) {
	a4Env(t)
	replies, _, err := a4MCP(t, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if err != nil {
		t.Fatalf("serve: %v", err)
	}
	var names []string
	for _, tool := range replies[0].Result.Tools {
		names = append(names, tool.Name)
		if tool.Description == "" {
			t.Errorf("%s has no description", tool.Name)
		}
		if tool.Title == "" {
			t.Errorf("%s has no title", tool.Name)
		}
		b, _ := json.Marshal(tool.Schema)
		var sch struct {
			Type       string                     `json:"type"`
			Properties map[string]json.RawMessage `json:"properties"`
			Required   []string                   `json:"required"`
		}
		if json.Unmarshal(b, &sch) != nil || sch.Type != "object" {
			t.Errorf("%s: schema is not an object: %s", tool.Name, b)
		}
		for _, req := range sch.Required {
			if _, ok := sch.Properties[req]; !ok {
				t.Errorf("%s requires %q, which its schema does not describe", tool.Name, req)
			}
		}
	}
	want := []string{"list", "read", "start", "prompt", "wait", "task", "rename"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("tools %v, want %v", names, want)
	}
}

// TestA4MCPToolsReachTheSameMethods: every tool is the CLI's work through
// another door, so each must call the method the command calls, with the
// same payload. A tool that quietly did something else would be a second
// conch.
func TestA4MCPToolsReachTheSameMethods(t *testing.T) {
	a4Env(t)
	srv := startA4Server(t, config.SocketPath())
	pane := proto.PaneInfo{ID: "p4", Name: "reviewer", State: "running", Cwd: "/w", Branch: "feat",
		ProjectID: "pr1", Agent: &proto.AgentStatus{Name: "codex", State: "working"}}
	srv.setHandle(func(msg proto.Message, conn *proto.Conn) (any, *proto.Error) {
		switch msg.Method {
		case proto.MethodPaneList:
			return proto.PaneList{Panes: []proto.PaneInfo{pane, {ID: "p5", Name: "zsh", State: "exited"}}}, nil
		case proto.MethodPaneRead:
			var ref proto.PaneRef
			_ = json.Unmarshal(msg.Params, &ref)
			if ref.ID == "p5" {
				return proto.PaneReadResult{Lines: []string{"", "", ""}}, nil // nothing on it
			}
			// As a real screen comes back: rows the program never filled.
			return proto.PaneReadResult{Lines: []string{"one", "", "two", "three", "", "   ", ""}}, nil
		case proto.MethodPaneCreate, proto.MethodTaskCreate, proto.MethodPaneRename:
			return pane, nil
		case proto.MethodProjectAdd:
			return proto.ProjectInfo{ID: "pr1", Name: "conch", Git: true}, nil
		case proto.MethodAgentPrompt:
			return proto.AgentPromptResult{ID: "p4", Agent: "codex", Turn: 7}, nil
		}
		return nil, nil
	})

	// list: every pane, with what each is doing and its folder.
	r := a4MCPOne(t, "list", nil)
	if r.Result.IsError || !strings.Contains(r.text(), "p4  reviewer  codex:working  /w") {
		t.Errorf("list: %q", r.text())
	}
	if !strings.Contains(r.text(), "(exited)") {
		t.Errorf("list says nothing about the ended pane: %q", r.text())
	}
	var listed struct {
		Panes []map[string]any `json:"panes"`
	}
	if json.Unmarshal(r.Result.Structured, &listed) != nil || len(listed.Panes) != 2 {
		t.Fatalf("list structured: %s", r.Result.Structured)
	}
	if listed.Panes[0]["agent_state"] != "working" || listed.Panes[0]["branch"] != "feat" {
		t.Errorf("list structured[0]: %v", listed.Panes[0])
	}

	// read: the screen without the blank rows under it — those are tokens
	// a model pays for, and they would make tail the bottom of an empty
	// screen rather than the last thing said. A blank row between lines is
	// the layout and stays.
	if r = a4MCPOne(t, "read", map[string]any{"pane": "p4"}); r.text() != "one\n\ntwo\nthree" {
		t.Errorf("read: %q", r.text())
	}
	if r = a4MCPOne(t, "read", map[string]any{"pane": "p4", "tail": 2}); r.text() != "two\nthree" {
		t.Errorf("read tail 2: %q", r.text())
	}
	// A tail longer than the screen is the screen, not an error.
	if r = a4MCPOne(t, "read", map[string]any{"pane": "p4", "tail": 99}); r.text() != "one\n\ntwo\nthree" {
		t.Errorf("read tail 99: %q", r.text())
	}
	// A screen with nothing on it at all says so: an empty answer is one a
	// model has to guess at, and a pane that just started is the usual
	// reason for one.
	r = a4MCPOne(t, "read", map[string]any{"pane": "p5", "tail": 5})
	if r.Result.IsError || !strings.Contains(r.text(), "nothing on p5's screen yet") {
		t.Errorf("read a blank screen: %q", r.text())
	}
	var read struct {
		Lines []string `json:"lines"`
	}
	if json.Unmarshal(r.Result.Structured, &read) != nil || len(read.Lines) != 0 {
		t.Errorf("a blank screen has lines: %s", r.Result.Structured)
	}
	// A pane is found by its name as well as its id, as every command does.
	if r = a4MCPOne(t, "read", map[string]any{"pane": "reviewer"}); r.Result.IsError {
		t.Errorf("read by name: %q", r.text())
	}

	// start: pane.create with the agent, the folder and the first message.
	r = a4MCPOne(t, "start", map[string]any{"agent": "codex", "cwd": "/w", "name": "reviewer",
		"prompt": "look at the diff", "args": "--model o3"})
	if r.Result.IsError || !strings.Contains(r.text(), "p4: codex started in /w") {
		t.Errorf("start: %q", r.text())
	}
	// conch does not see an agent the instant its process starts, so a
	// prompt in the same breath is refused: both tools that start one say
	// to wait first. Found by driving a real `conch mcp` from inside a
	// pane, where task was followed straight by prompt.
	if !strings.Contains(r.text(), "Wait for it") {
		t.Errorf("start does not say to wait: %q", r.text())
	}
	var create proto.PaneCreateParams
	if srv.params(t, proto.MethodPaneCreate, &create) {
		if create.Agent != "codex" || create.Cwd != "/w" || create.Name != "reviewer" ||
			create.Prompt != "look at the diff" || create.AgentArgs != "--model o3" ||
			create.Cols != 120 || create.Rows != 40 {
			t.Errorf("pane.create params %+v", create)
		}
	}

	// prompt without waiting: agent.prompt, and the turn that answers it.
	r = a4MCPOne(t, "prompt", map[string]any{"pane": "reviewer", "text": "check finding 3"})
	if r.Result.IsError || !strings.Contains(r.text(), "p4: sent to codex") {
		t.Errorf("prompt: %q", r.text())
	}
	var sent proto.AgentPromptParams
	if srv.params(t, proto.MethodAgentPrompt, &sent); sent.ID != "p4" || sent.Text != "check finding 3" {
		t.Errorf("agent.prompt params %+v", sent)
	}

	// task: the project is resolved from the folder, then task.create.
	r = a4MCPOne(t, "task", map[string]any{"prompt": "write the tests", "cwd": "/w",
		"branch": "tests", "base": "main", "agent": "claude", "name": "tester"})
	if r.Result.IsError || !strings.Contains(r.text(), "started on branch feat") {
		t.Errorf("task: %q", r.text())
	}
	if !strings.Contains(r.text(), "Wait for it") {
		t.Errorf("task does not say to wait: %q", r.text())
	}
	var task proto.TaskCreateParams
	if srv.params(t, proto.MethodTaskCreate, &task) {
		if task.ProjectID != "pr1" || task.Prompt != "write the tests" || task.Branch != "tests" ||
			task.Base != "main" || task.Agent != "claude" || task.Name != "tester" {
			t.Errorf("task.create params %+v", task)
		}
	}

	// rename: pane.rename, and the name it ended up with.
	if r = a4MCPOne(t, "rename", map[string]any{"pane": "p4", "name": "reviewer"}); r.Result.IsError {
		t.Errorf("rename: %q", r.text())
	}
	var renamed proto.PaneRenameParams
	if srv.params(t, proto.MethodPaneRename, &renamed); renamed.ID != "p4" || renamed.Name != "reviewer" {
		t.Errorf("pane.rename params %+v", renamed)
	}
}

// TestA4MCPTaskWithoutAGitRepository: `conch task` in a folder that is no
// repository starts the agent there instead, and so must the tool — the
// same words through two doors, or they are two different conches.
func TestA4MCPTaskWithoutAGitRepository(t *testing.T) {
	a4Env(t)
	srv := startA4Server(t, config.SocketPath())
	srv.setHandle(func(msg proto.Message, conn *proto.Conn) (any, *proto.Error) {
		switch msg.Method {
		case proto.MethodProjectAdd:
			return proto.ProjectInfo{ID: "pr2", Name: "notes", Git: false}, nil
		case proto.MethodPaneCreate:
			return proto.PaneInfo{ID: "p8", State: "running", Cwd: "/notes"}, nil
		}
		return nil, nil
	})
	r := a4MCPOne(t, "task", map[string]any{"prompt": "tidy these notes", "cwd": "/notes"})
	if r.Result.IsError || !strings.Contains(r.text(), "not a git repository") {
		t.Fatalf("task in a plain folder: %q", r.text())
	}
	if !strings.Contains(r.text(), "Wait for it") {
		t.Errorf("task in a plain folder does not say to wait: %q", r.text())
	}
	for _, m := range srv.methods() {
		if m == proto.MethodTaskCreate {
			t.Error("task.create was called for a folder with no repository")
		}
	}
	var create proto.PaneCreateParams
	if srv.params(t, proto.MethodPaneCreate, &create); create.Prompt != "tidy these notes" {
		t.Errorf("pane.create params %+v", create)
	}
}

// TestA4MCPPromptRefusedWhileWaiting: a message onto an open question is
// refused by the server, and the tool has to hand the agent something it
// can act on — the question is on the screen, so say to read it.
func TestA4MCPPromptRefusedWhileWaiting(t *testing.T) {
	a4Env(t)
	srv := startA4Server(t, config.SocketPath())
	srv.setHandle(func(msg proto.Message, conn *proto.Conn) (any, *proto.Error) {
		switch msg.Method {
		case proto.MethodPaneList:
			return proto.PaneList{Panes: []proto.PaneInfo{a4Pane("p1", "blocked")}}, nil
		case proto.MethodAgentPrompt:
			return nil, &proto.Error{Code: proto.ErrAgentBlocked, Message: "the claude agent in p1 is waiting on a question"}
		}
		return nil, nil
	})
	r := a4MCPOne(t, "prompt", map[string]any{"pane": "p1", "text": "carry on"})
	if !r.Result.IsError {
		t.Fatalf("a refused prompt is not an error: %q", r.text())
	}
	if !strings.Contains(r.text(), "waiting on a question") || !strings.Contains(r.text(), "read") {
		t.Errorf("the refusal does not say what to do: %q", r.text())
	}
	// It is a tool result, not a transport failure: a JSON-RPC error would
	// be swallowed by the client instead of reaching the model.
	if r.Error != nil {
		t.Errorf("refusal came back as a protocol error: %+v", r.Error)
	}
}

// TestA4MCPPromptWaitsForTheTurn: with wait, the tool returns only once the
// work the message started has ended — the same wait `conch agent prompt
// -wait` does, off the same events.
func TestA4MCPPromptWaitsForTheTurn(t *testing.T) {
	a4Env(t)
	srv := startA4Server(t, config.SocketPath())
	done := a4Pane("p1", "done")
	done.Agent.Turn = 7
	srv.setHandle(func(msg proto.Message, conn *proto.Conn) (any, *proto.Error) {
		switch msg.Method {
		case proto.MethodPaneList:
			return proto.PaneList{Panes: []proto.PaneInfo{a4Pane("p1", "working")}}, nil
		case proto.MethodAgentPrompt:
			go func() {
				working := a4Pane("p1", "working")
				working.Agent.Turn = 7
				conn.Write(a4Updated(working)) // the turn, still going
				conn.Write(a4Updated(done))
			}()
			return proto.AgentPromptResult{ID: "p1", Agent: "claude", Turn: 7}, nil
		}
		return nil, nil
	})
	r := a4MCPOne(t, "prompt", map[string]any{"pane": "p1", "text": "go", "wait": true, "timeout_seconds": 10})
	if r.Result.IsError || !strings.Contains(r.text(), "p1: claude is done") {
		t.Fatalf("prompt -wait: %q", r.text())
	}
	var facts map[string]any
	if json.Unmarshal(r.Result.Structured, &facts) != nil || facts["waited"] != true || facts["agent_state"] != "done" {
		t.Errorf("structured %s", r.Result.Structured)
	}
}

// TestA4MCPWait: the states it stops on, the pane it reports, and what it
// says when the time runs out — an agent that gets "timed out" can decide
// to wait again, where a bare failure tells it nothing.
func TestA4MCPWait(t *testing.T) {
	a4Env(t)
	a4WaitServer(t, []proto.PaneInfo{a4Pane("p1", "working"), a4Pane("p2", "done")})

	r := a4MCPOne(t, "wait", map[string]any{"pane": "p2"})
	if r.Result.IsError || !strings.Contains(r.text(), "p2: claude is done") {
		t.Fatalf("wait on a pane already done: %q", r.text())
	}
	// Idle is not done, so this one waits — and says so when it gives up.
	r = a4MCPOne(t, "wait", map[string]any{"pane": "p2", "state": []string{"idle"}, "timeout_seconds": 0.15})
	if !r.Result.IsError || !strings.Contains(r.text(), "timed out") {
		t.Fatalf("wait that runs out: %+v %q", r.Result.IsError, r.text())
	}
	// A state nobody has is worth saying, with the ones that exist.
	r = a4MCPOne(t, "wait", map[string]any{"pane": "p2", "state": []string{"thinking"}})
	if !r.Result.IsError || !strings.Contains(r.text(), "unknown state") {
		t.Fatalf("unknown state: %q", r.text())
	}
	// A pane that is not there, and one that is not named at all.
	if r = a4MCPOne(t, "wait", map[string]any{"pane": "p99"}); !r.Result.IsError {
		t.Error("a missing pane is not an error")
	}
	if r = a4MCPOne(t, "wait", map[string]any{}); !r.Result.IsError || !strings.Contains(r.text(), "which pane") {
		t.Errorf("no pane given: %q", r.text())
	}
}

// TestA4MCPWaitEndsWhenThePaneDoes: a helper that exits before it finishes
// must end the wait, not hold the session until the timeout.
func TestA4MCPWaitEndsWhenThePaneDoes(t *testing.T) {
	a4Env(t)
	ended := a4Pane("p1", "working")
	ended.State = proto.PaneExited
	ended.ExitCode = 1
	a4WaitServer(t, []proto.PaneInfo{a4Pane("p1", "working")},
		proto.Message{Event: proto.EventPaneExited, Data: proto.Marshal(ended)})
	r := a4MCPOne(t, "wait", map[string]any{"pane": "p1", "timeout_seconds": 10})
	if !r.Result.IsError || !strings.Contains(r.text(), "ended") {
		t.Fatalf("a pane that exits: %q", r.text())
	}
}

// TestA4MCPBadInput: everything a client can get wrong. A bad line must not
// end the session — an agent's tools going away over one malformed message
// would be worse than the message.
func TestA4MCPBadInput(t *testing.T) {
	a4Env(t)
	a4WaitServer(t, []proto.PaneInfo{a4Pane("p1", "done")})
	replies, _, err := a4MCP(t,
		`not json at all`,
		`{"jsonrpc":"2.0","id":2,"method":"frobnicate"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"nope"}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{}}`,
		a4MCPCall(5, "read", map[string]any{"pane": "p1", "nonsense": true}),
		a4MCPCall(6, "prompt", map[string]any{"pane": "p1"}),
		a4MCPCall(7, "wait", map[string]any{"pane": "p1", "timeout_seconds": -1}),
		a4MCPCall(8, "read", map[string]any{"pane": "p1", "tail": -2}),
		``, // an empty line is skipped, not answered
		`{"jsonrpc":"2.0","id":9,"method":"tools/list"}`,
	)
	if err != nil {
		t.Fatalf("serve: %v", err)
	}
	if len(replies) != 9 {
		t.Fatalf("%d replies, want 9: %+v", len(replies), replies)
	}
	// The parse error has no id to answer: JSON-RPC says null.
	if replies[0].Error == nil || replies[0].Error.Code != mcpParseError {
		t.Errorf("bad JSON: %+v", replies[0])
	}
	codes := map[string]int{"2": mcpMethodNotFound, "3": mcpInvalidParams, "4": mcpInvalidParams}
	for _, r := range replies[1:4] {
		want, ok := codes[string(r.ID)]
		if !ok {
			continue
		}
		if r.Error == nil || r.Error.Code != want {
			t.Errorf("id %s: %+v, want code %d", r.ID, r.Error, want)
		}
	}
	// Everything a tool itself refuses is a result the model reads.
	for i, want := range map[int]string{4: "cannot be read", 5: "say what to send", 6: "negative", 7: "negative"} {
		r := replies[i]
		if !r.Result.IsError || !strings.Contains(r.text(), want) {
			t.Errorf("reply %d (%s): %q, want %q", i, r.ID, r.text(), want)
		}
	}
	// And the session is still answering afterwards.
	if len(replies[8].Result.Tools) == 0 {
		t.Error("the session stopped answering after the bad lines")
	}
}

// TestA4MCPWithoutAServer: nothing to connect to is a tool result, not a
// crash and not a protocol error — the agent can start the server itself,
// or tell the person.
func TestA4MCPWithoutAServer(t *testing.T) {
	a4Env(t) // CONCH_SOCKET points at a socket nothing listens on
	r := a4MCPOne(t, "list", nil)
	if !r.Result.IsError || !strings.Contains(r.text(), "not answering") {
		t.Fatalf("no server: %+v %q", r.Result.IsError, r.text())
	}
	if r.Error != nil {
		t.Errorf("no server came back as a protocol error: %+v", r.Error)
	}
}

// TestA4MCPOlderServerWithoutPrompts: a server from before `agent prompt`
// cannot be prompted, and the tool says what to do instead of failing in
// the middle of a call.
func TestA4MCPOlderServerWithoutPrompts(t *testing.T) {
	a4Env(t)
	srv := startA4Server(t, config.SocketPath())
	srv.setHello(func(n int) proto.HelloResult {
		h := currentHello(n)
		var keep []string
		for _, c := range h.Capabilities {
			if c != proto.CapAgentPrompt {
				keep = append(keep, c)
			}
		}
		h.Capabilities = keep
		return h
	})
	r := a4MCPOne(t, "prompt", map[string]any{"pane": "p1", "text": "hello"})
	if !r.Result.IsError || !strings.Contains(r.text(), "older build") {
		t.Fatalf("older server: %q", r.text())
	}
	// It never reached the pane: nothing was sent anywhere.
	for _, m := range srv.methods() {
		if m == proto.MethodAgentPrompt {
			t.Error("agent.prompt was called on a server without the capability")
		}
	}
}

// TestA4MCPKeepsOneConnection: the session dials once and keeps it, and
// replaces one that dropped — an MCP server an agent starts outlives a
// `conch server reload`.
func TestA4MCPKeepsOneConnection(t *testing.T) {
	a4Env(t)
	srv := startA4Server(t, config.SocketPath())
	srv.setHandle(func(msg proto.Message, conn *proto.Conn) (any, *proto.Error) {
		if msg.Method == proto.MethodPaneList {
			return proto.PaneList{Panes: []proto.PaneInfo{a4Pane("p1", "done")}}, nil
		}
		return nil, nil
	})
	var out, errOut strings.Builder
	s := &mcpSession{w: &out, err: &errOut, dial: func() (*client.Client, error) { return connect(false) }}
	defer s.close()
	first, err := s.conch()
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.conch()
	if err != nil || again != first {
		t.Fatalf("a second tool dialled again: %v", err)
	}
	// The server drops it; the next tool gets a fresh connection.
	first.Close()
	deadline := time.Now().Add(2 * time.Second)
	for first.Err() == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	third, err := s.conch()
	if err != nil {
		t.Fatalf("after the connection dropped: %v", err)
	}
	if third == first {
		t.Fatal("a dropped connection was handed back")
	}
}

// TestA4MCPOverlongMessage: a line past the limit cannot be answered — the
// id was in the part conch could not hold — so it says so on stderr and
// ends, rather than growing to whatever a client sends.
func TestA4MCPOverlongMessage(t *testing.T) {
	a4Env(t)
	long := `{"jsonrpc":"2.0","id":1,"method":"ping","params":"` + strings.Repeat("x", mcpMaxLine) + `"}`
	replies, stderr, err := a4MCP(t, long, `{"jsonrpc":"2.0","id":2,"method":"ping"}`)
	if err == nil {
		t.Fatal("an overlong message was accepted")
	}
	if len(replies) != 0 {
		t.Errorf("replies to an overlong message: %+v", replies)
	}
	if !strings.Contains(stderr, "over") {
		t.Errorf("stderr says nothing useful: %q", stderr)
	}
}

// TestA4MCPUsage: `conch mcp` takes no arguments, and one typed by mistake
// must not be read as a tool call.
func TestA4MCPUsage(t *testing.T) {
	a4Env(t)
	if err := runMCP([]string{"serve"}); err == nil {
		t.Fatal("conch mcp serve was accepted")
	}
}

// TestA4MCPEmptyLists: a client that asks for resources or prompts must be
// told there are none, not met with "no such method" — some clients ask
// before they call a tool and treat an error as a server that is broken.
func TestA4MCPEmptyLists(t *testing.T) {
	a4Env(t)
	replies, _, err := a4MCP(t,
		`{"jsonrpc":"2.0","id":1,"method":"resources/list"}`,
		`{"jsonrpc":"2.0","id":2,"method":"prompts/list"}`,
	)
	if err != nil {
		t.Fatalf("serve: %v", err)
	}
	for _, r := range replies {
		if r.Error != nil {
			t.Errorf("id %s: %+v", r.ID, r.Error)
		}
	}
}

// TestA4MCPDefaults: a tool called with nothing but what it requires falls
// back to the same choices the commands make — the person's default agent
// and the folder the agent is working in.
func TestA4MCPDefaults(t *testing.T) {
	dir := a4Env(t)
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("[agents]\ndefault = \"codex\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := startA4Server(t, config.SocketPath())
	srv.setHandle(func(msg proto.Message, conn *proto.Conn) (any, *proto.Error) {
		switch msg.Method {
		case proto.MethodPaneList:
			return proto.PaneList{Panes: []proto.PaneInfo{a4Pane("p1", "idle")}}, nil
		case proto.MethodPaneCreate, proto.MethodPaneRename:
			return proto.PaneInfo{ID: "p2", State: "running", Cwd: "/w"}, nil
		}
		return nil, nil
	})
	here, _ := os.Getwd()

	if r := a4MCPOne(t, "start", map[string]any{}); r.Result.IsError {
		t.Fatalf("start with no arguments: %q", r.text())
	}
	var create proto.PaneCreateParams
	if srv.params(t, proto.MethodPaneCreate, &create) {
		if create.Agent != "codex" {
			t.Errorf("start chose %q, not the configured default", create.Agent)
		}
		if create.Cwd != here {
			t.Errorf("start chose %q, not the working directory %q", create.Cwd, here)
		}
	}

	// An empty name is how a pane gets its own back, as `conch rename ID`
	// with no name does; it is not a missing argument.
	if r := a4MCPOne(t, "rename", map[string]any{"pane": "p1", "name": ""}); r.Result.IsError {
		t.Fatalf("rename to nothing: %q", r.text())
	}
	var renamed proto.PaneRenameParams
	if srv.params(t, proto.MethodPaneRename, &renamed); renamed.ID != "p1" || renamed.Name != "" {
		t.Errorf("pane.rename params %+v", renamed)
	}
}

// TestA4MCPServerRefusals: what the conch server says no to — a pane
// another agent owns, an agent it does not know — reaches the model as the
// server's own words, so it can act on them instead of guessing.
func TestA4MCPServerRefusals(t *testing.T) {
	a4Env(t)
	srv := startA4Server(t, config.SocketPath())
	srv.setHandle(func(msg proto.Message, conn *proto.Conn) (any, *proto.Error) {
		switch msg.Method {
		case proto.MethodPaneList:
			return proto.PaneList{Panes: []proto.PaneInfo{a4Pane("p1", "idle")}}, nil
		case proto.MethodPaneCreate:
			return nil, &proto.Error{Code: proto.ErrBadRequest, Message: `unknown agent "nosuchagent"`}
		case proto.MethodPaneRename:
			return nil, &proto.Error{Code: proto.ErrOutOfScope, Message: "the claude agent in p9 may not rename p1"}
		case proto.MethodProjectAdd:
			return nil, &proto.Error{Code: proto.ErrBadRequest, Message: "/no/such/folder: no such folder"}
		}
		return nil, nil
	})
	r := a4MCPOne(t, "start", map[string]any{"agent": "nosuchagent"})
	if !r.Result.IsError || !strings.Contains(r.text(), "nosuchagent") {
		t.Errorf("unknown agent: %q", r.text())
	}
	r = a4MCPOne(t, "rename", map[string]any{"pane": "p1", "name": "mine"})
	if !r.Result.IsError || !strings.Contains(r.text(), "may not rename") {
		t.Errorf("out of scope: %q", r.text())
	}
	// A folder that is no project at all: task says so rather than
	// starting an agent somewhere nobody asked for.
	r = a4MCPOne(t, "task", map[string]any{"prompt": "go", "cwd": "/no/such/folder"})
	if !r.Result.IsError || !strings.Contains(r.text(), "no such folder") {
		t.Errorf("task in a folder that is not there: %q", r.text())
	}
}

// TestA4MCPListSaysWhatIsInsideAnAgent: Claude's Agent tool runs agents
// inside the pane's own process, with no pane of their own, and the tree
// lists them under it (internal/tui/subagents_test.go). A tool that left
// them out would tell an agent less than the person can see.
func TestA4MCPListSaysWhatIsInsideAnAgent(t *testing.T) {
	a4Env(t)
	srv := startA4Server(t, config.SocketPath())
	busy := a4Pane("p1", "working")
	busy.Agent.Subagents = []proto.Subagent{
		{ID: "s1", Type: "Explore", Description: "find where panes are named"},
		{ID: "s2", Type: "general-purpose"},
	}
	srv.setHandle(func(msg proto.Message, conn *proto.Conn) (any, *proto.Error) {
		if msg.Method == proto.MethodPaneList {
			return proto.PaneList{Panes: []proto.PaneInfo{busy, a4Pane("p2", "idle")}}, nil
		}
		return nil, nil
	})
	r := a4MCPOne(t, "list", nil)
	if !strings.Contains(r.text(), "+2 subagents inside it") {
		t.Errorf("list says nothing about the subagents: %q", r.text())
	}
	// The pane with none says nothing about them, rather than "+0".
	if strings.Contains(r.text(), "+0") {
		t.Errorf("list counts subagents nobody has: %q", r.text())
	}
	var listed struct {
		Panes []struct {
			Pane      string `json:"pane"`
			Subagents []struct {
				ID   string `json:"id"`
				Type string `json:"type"`
				Task string `json:"task"`
			} `json:"subagents"`
		} `json:"panes"`
	}
	if err := json.Unmarshal(r.Result.Structured, &listed); err != nil {
		t.Fatalf("structured: %v", err)
	}
	subs := listed.Panes[0].Subagents
	if len(subs) != 2 || subs[0].Type != "Explore" || subs[0].Task != "find where panes are named" {
		t.Errorf("subagents %+v", subs)
	}
	// One Claude gave no task is listed by what it is, not dropped.
	if subs[1].ID != "s2" || subs[1].Type != "general-purpose" || subs[1].Task != "" {
		t.Errorf("a subagent with no task: %+v", subs[1])
	}
	if len(listed.Panes[1].Subagents) != 0 {
		t.Errorf("a pane with no subagents has %+v", listed.Panes[1].Subagents)
	}
}
