// Command acpfake is an agent that speaks the Agent Client Protocol on
// its stdin and stdout and does nothing else, for testing conch's ACP
// client (internal/acp) without a real agent or a model.
//
// What it does is decided by what it is prompted with:
//
//	hello            three message chunks, then end_turn
//	perm             a tool call and a permission request; says what was chosen
//	slow             works until cancelled (session/cancel), then "cancelled"
//	perm-after-cancel  waits for a cancel, then asks permission anyway
//	edit NAME TEXT   writes NAME through fs/write_text_file, reads it back, shows a diff
//	read PATH        reads PATH through fs/read_text_file
//	run COMMAND      runs COMMAND through terminal/*, says its output
//	unknown          asks the client a method it does not offer
//	plan             sends a plan
//	huge             sends one update past conch's line limit, then a small one
//	anything else    "you said: ..."
//
// and how it starts by ACPFAKE_MODE, a comma-separated list: exit-init
// (dies during initialize, saying why on stderr), malformed (a line that
// is not JSON before answering initialize), noload (no loadSession),
// list (offers session/list), auth (session/new needs signing in).
// ACPFAKE_DIR keeps sessions, so they can be loaded and listed.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type msg struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcErr         `json:"error,omitempty"`
}

type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type turn struct {
	Prompt string `json:"prompt"`
	Answer string `json:"answer"`
}

type session struct {
	ID      string `json:"id"`
	Cwd     string `json:"cwd"`
	Turns   []turn `json:"turns"`
	Updated string `json:"updated"`
}

var (
	wmu     sync.Mutex
	mode    = map[string]bool{}
	dir     = os.Getenv("ACPFAKE_DIR")
	pmu     sync.Mutex
	next    int
	waiting = map[string]chan msg{}

	smu       sync.Mutex
	sessions  = map[string]*session{}
	cancelled = map[string]chan struct{}{}
)

func send(m msg) {
	m.JSONRPC = "2.0"
	b, _ := json.Marshal(m)
	wmu.Lock()
	defer wmu.Unlock()
	os.Stdout.Write(append(b, '\n'))
}

func raw(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// call asks the client and waits for its answer.
func call(method string, params any) msg {
	pmu.Lock()
	next++
	id := fmt.Sprintf("\"fake-%d\"", next)
	ch := make(chan msg, 1)
	waiting[id] = ch
	pmu.Unlock()
	send(msg{ID: json.RawMessage(id), Method: method, Params: raw(params)})
	return <-ch
}

func update(sid string, u map[string]any) {
	send(msg{Method: "session/update", Params: raw(map[string]any{"sessionId": sid, "update": u})})
}

func say(sid, text string) {
	update(sid, map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": text}})
}

func main() {
	for _, m := range strings.Split(os.Getenv("ACPFAKE_MODE"), ",") {
		mode[strings.TrimSpace(m)] = true
	}
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		var m msg
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			continue
		}
		if m.Method == "" {
			pmu.Lock()
			ch := waiting[string(m.ID)]
			delete(waiting, string(m.ID))
			pmu.Unlock()
			if ch != nil {
				ch <- m
			}
			continue
		}
		go handle(m)
	}
}

func reply(m msg, result any) { send(msg{ID: m.ID, Result: raw(result)}) }

func fail(m msg, code int, text string) { send(msg{ID: m.ID, Error: &rpcErr{Code: code, Message: text}}) }

func handle(m msg) {
	var p map[string]any
	_ = json.Unmarshal(m.Params, &p)
	str := func(k string) string { s, _ := p[k].(string); return s }
	switch m.Method {
	case "initialize":
		if mode["exit-init"] {
			fmt.Fprintln(os.Stderr, "acpfake: no API key set")
			os.Exit(3)
		}
		if mode["malformed"] {
			wmu.Lock()
			os.Stdout.Write([]byte("this is not json\n{\"jsonrpc\":\"2.0\"}\n"))
			wmu.Unlock()
		}
		caps := map[string]any{"loadSession": !mode["noload"]}
		if mode["list"] {
			caps["sessionCapabilities"] = map[string]any{"list": map[string]any{}}
		}
		res := map[string]any{"protocolVersion": 1, "agentCapabilities": caps,
			"agentInfo": map[string]any{"name": "acpfake", "version": "0.1"}}
		if mode["auth"] {
			res["authMethods"] = []any{map[string]any{"id": "key", "name": "an API key"}}
		}
		reply(m, res)
	case "session/new":
		if mode["auth"] {
			fail(m, -32000, "Authentication required")
			return
		}
		smu.Lock()
		s := &session{ID: fmt.Sprintf("s-%d-%d", os.Getpid(), len(sessions)+1), Cwd: str("cwd")}
		sessions[s.ID] = s
		smu.Unlock()
		save(s)
		reply(m, map[string]any{"sessionId": s.ID})
	case "session/load":
		s := load(str("sessionId"))
		if s == nil {
			fail(m, -32602, "no such session")
			return
		}
		smu.Lock()
		sessions[s.ID] = s
		smu.Unlock()
		for _, t := range s.Turns {
			update(s.ID, map[string]any{"sessionUpdate": "user_message_chunk", "content": map[string]any{"type": "text", "text": t.Prompt}})
			say(s.ID, t.Answer)
		}
		reply(m, map[string]any{})
	case "session/list":
		reply(m, map[string]any{"sessions": list(str("cwd"))})
	case "session/cancel":
		smu.Lock()
		if ch := cancelled[str("sessionId")]; ch != nil {
			close(ch)
			delete(cancelled, str("sessionId"))
		}
		smu.Unlock()
	case "session/prompt":
		sid := str("sessionId")
		var pp struct {
			Prompt []struct {
				Text string `json:"text"`
			} `json:"prompt"`
		}
		_ = json.Unmarshal(m.Params, &pp)
		text := ""
		for _, b := range pp.Prompt {
			text += b.Text
		}
		cancel := make(chan struct{})
		smu.Lock()
		cancelled[sid] = cancel
		smu.Unlock()
		answer, stop := prompt(sid, text, cancel)
		smu.Lock()
		if s := sessions[sid]; s != nil {
			s.Turns = append(s.Turns, turn{Prompt: text, Answer: answer})
			defer save(s)
		}
		delete(cancelled, sid)
		smu.Unlock()
		reply(m, map[string]any{"stopReason": stop})
	default:
		if len(m.ID) > 0 {
			fail(m, -32601, "method not found")
		}
	}
}

func prompt(sid, text string, cancel chan struct{}) (string, string) {
	word, rest, _ := strings.Cut(text, " ")
	switch word {
	case "hello":
		for _, c := range []string{"Hello", ", ", "world"} {
			say(sid, c)
		}
		return "Hello, world", "end_turn"
	case "perm":
		title := "Write notes.txt"
		update(sid, map[string]any{"sessionUpdate": "tool_call", "toolCallId": "t1", "title": title, "kind": "edit", "status": "pending"})
		r := call("session/request_permission", map[string]any{"sessionId": sid,
			"toolCall": map[string]any{"toolCallId": "t1", "title": title},
			"options": []any{
				map[string]any{"optionId": "allow", "name": "Allow once", "kind": "allow_once"},
				map[string]any{"optionId": "always", "name": "Allow always", "kind": "allow_always"},
				map[string]any{"optionId": "reject", "name": "Reject", "kind": "reject_once"},
			}})
		chose := outcome(r)
		if chose == "cancelled" {
			return "chose:cancelled", "cancelled"
		}
		update(sid, map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "t1", "status": "completed"})
		say(sid, "chose:"+chose)
		return "chose:" + chose, "end_turn"
	case "slow":
		say(sid, "working")
		select {
		case <-cancel:
			return "stopped", "cancelled"
		case <-time.After(20 * time.Second):
			return "slept", "end_turn"
		}
	case "perm-after-cancel":
		<-cancel
		r := call("session/request_permission", map[string]any{"sessionId": sid,
			"toolCall": map[string]any{"toolCallId": "t2", "title": "late"},
			"options":  []any{map[string]any{"optionId": "allow", "name": "Allow", "kind": "allow_once"}}})
		say(sid, "late:"+outcome(r))
		writeMark("late-" + outcome(r))
		return "late", "cancelled"
	case "edit":
		name, body, _ := strings.Cut(rest, " ")
		smu.Lock()
		cwd := sessions[sid].Cwd
		smu.Unlock()
		path := filepath.Join(cwd, name)
		r := call("fs/write_text_file", map[string]any{"sessionId": sid, "path": path, "content": body + "\n"})
		if r.Error != nil {
			say(sid, "write failed: "+r.Error.Message)
			return "failed", "end_turn"
		}
		r = call("fs/read_text_file", map[string]any{"sessionId": sid, "path": path})
		var got struct {
			Content string `json:"content"`
		}
		_ = json.Unmarshal(r.Result, &got)
		update(sid, map[string]any{"sessionUpdate": "tool_call", "toolCallId": "e1", "title": "Edit " + name, "kind": "edit", "status": "completed",
			"content": []any{map[string]any{"type": "diff", "path": path, "oldText": nil, "newText": got.Content}}})
		say(sid, "read back: "+strings.TrimSpace(got.Content))
		return "edited", "end_turn"
	case "read":
		r := call("fs/read_text_file", map[string]any{"sessionId": sid, "path": rest})
		if r.Error != nil {
			say(sid, "read failed: "+r.Error.Message)
			return "failed", "end_turn"
		}
		var got struct {
			Content string `json:"content"`
		}
		_ = json.Unmarshal(r.Result, &got)
		say(sid, "read: "+got.Content)
		return "read", "end_turn"
	case "run":
		r := call("terminal/create", map[string]any{"sessionId": sid, "command": "/bin/sh", "args": []string{"-c", rest}})
		if r.Error != nil {
			say(sid, "run failed: "+r.Error.Message)
			return "failed", "end_turn"
		}
		var t struct {
			TerminalID string `json:"terminalId"`
		}
		_ = json.Unmarshal(r.Result, &t)
		w := call("terminal/wait_for_exit", map[string]any{"sessionId": sid, "terminalId": t.TerminalID})
		o := call("terminal/output", map[string]any{"sessionId": sid, "terminalId": t.TerminalID})
		call("terminal/release", map[string]any{"sessionId": sid, "terminalId": t.TerminalID})
		var out struct {
			Output string `json:"output"`
		}
		_ = json.Unmarshal(o.Result, &out)
		var ex struct {
			ExitCode *int `json:"exitCode"`
		}
		_ = json.Unmarshal(w.Result, &ex)
		code := -1
		if ex.ExitCode != nil {
			code = *ex.ExitCode
		}
		say(sid, fmt.Sprintf("output: %s exit: %d", strings.TrimSpace(out.Output), code))
		return "ran", "end_turn"
	case "unknown":
		r := call("_fake/teleport", map[string]any{"sessionId": sid})
		if r.Error != nil {
			say(sid, fmt.Sprintf("refused: %d", r.Error.Code))
		} else {
			say(sid, "accepted?")
		}
		return "unknown", "end_turn"
	case "plan":
		update(sid, map[string]any{"sessionUpdate": "plan", "entries": []any{
			map[string]any{"content": "read the code", "priority": "high", "status": "completed"},
			map[string]any{"content": "write the fix", "priority": "high", "status": "in_progress"},
			map[string]any{"content": "test it", "priority": "medium", "status": "pending"},
		}})
		return "plan", "end_turn"
	case "huge":
		say(sid, strings.Repeat("x", 40<<20))
		say(sid, "after the huge one")
		return "huge", "end_turn"
	}
	say(sid, "you said: "+text)
	return "you said: " + text, "end_turn"
}

func outcome(r msg) string {
	var res struct {
		Outcome struct {
			Outcome  string `json:"outcome"`
			OptionID string `json:"optionId"`
		} `json:"outcome"`
	}
	_ = json.Unmarshal(r.Result, &res)
	if res.Outcome.Outcome == "selected" {
		return res.Outcome.OptionID
	}
	return res.Outcome.Outcome
}

// writeMark leaves a file a test can wait for, when there is a folder.
func writeMark(name string) {
	if dir != "" {
		_ = os.WriteFile(filepath.Join(dir, "mark-"+name), nil, 0o644)
	}
}

func save(s *session) {
	if dir == "" {
		return
	}
	s.Updated = time.Now().UTC().Format(time.RFC3339)
	b, _ := json.Marshal(s)
	_ = os.WriteFile(filepath.Join(dir, s.ID+".json"), b, 0o644)
}

func load(id string) *session {
	if dir == "" || strings.ContainsAny(id, "/\\") {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(dir, id+".json"))
	if err != nil {
		return nil
	}
	var s session
	if json.Unmarshal(b, &s) != nil {
		return nil
	}
	return &s
}

func list(cwd string) []any {
	out := []any{}
	if dir == "" {
		return out
	}
	files, _ := filepath.Glob(filepath.Join(dir, "s-*.json"))
	sort.Strings(files)
	for _, f := range files {
		s := load(strings.TrimSuffix(filepath.Base(f), ".json"))
		if s == nil || cwd != "" && s.Cwd != cwd {
			continue
		}
		title := ""
		if len(s.Turns) > 0 {
			title = s.Turns[0].Prompt
		}
		out = append(out, map[string]any{"sessionId": s.ID, "cwd": s.Cwd, "title": title, "updatedAt": s.Updated})
	}
	return out
}
