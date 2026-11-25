package sessions

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// qwenWriteVersion is the Qwen Code QwenWriter's sessions were checked
// against (chatRecordingService.ts and sessionService.ts as shipped in
// 0.27.3), and the version its records carry.
const qwenWriteVersion = "0.27.3"

// QwenWriter writes Qwen Code sessions: <id>.jsonl in the folder Qwen
// keeps for the project, <QWEN_HOME or ~/.qwen>/projects/<the folder's
// path, every character but a letter or digit made "-">/chats, which
// `qwen --resume ID` opens with its history.
//
// A line is a record as Qwen's recorder makes one — uuid, parentUuid
// naming the line before (Qwen resumes the chain that ends at the last
// record, so a broken one loses what is above the break), sessionId,
// timestamp, type, cwd (Qwen checks that its hash is the project's), and
// a message of Gemini's shape: the person's text, the model's text with
// its function calls, and a tool_result for each call with the function's
// response. It is a plain transcript, which Qwen calls a legacy one: no
// managed journal is written beside it.
type QwenWriter struct {
	Env Env
}

// Agent is the agent whose sessions it writes.
func (QwenWriter) Agent() string { return "qwen" }

// WriteConversation writes c as a new Qwen Code session to resume in dir.
// Nothing is left behind when it fails.
func (w QwenWriter) WriteConversation(ctx context.Context, c Conversation, dir string) (Written, error) {
	if err := ctx.Err(); err != nil {
		return Written{}, err
	}
	if !c.HasMessages() {
		return Written{}, errors.New("nothing was said in it to carry over")
	}
	if dir == "" {
		return Written{}, errors.New("no folder to resume it in")
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return Written{}, fmt.Errorf("no folder to resume it in: %s is not there", dir)
	}
	id := newSessionUUID()
	chats := filepath.Join(qwenHome(w.Env), "projects", qwenDirName(dir), "chats")
	if err := os.MkdirAll(chats, 0o700); err != nil {
		return Written{}, err
	}
	path := filepath.Join(chats, id+".jsonl")
	if fileExists(path) {
		return Written{}, fmt.Errorf("a session %s is there already", id)
	}
	if err := writeWhole(path, qwenTranscriptFile(c, id, dir, time.Now())); err != nil {
		return Written{}, err
	}
	return Written{Agent: "qwen", ID: id, Path: path, Files: []string{path}}, nil
}

// ReadBack reads a session WriteConversation wrote, as conch reads any.
func (QwenWriter) ReadBack(ctx context.Context, wr Written) (Conversation, error) {
	return ReadConversation(ctx, Env{}, Session{Agent: "qwen", ID: wr.ID, Path: wr.Path})
}

// qwenTranscriptFile is c as a Qwen Code session. What Qwen says between
// two of the person's messages is one record a step, as Qwen records a
// turn: its words and the notes, then a command's call on the same
// record, the call's result a tool_result of its own.
func qwenTranscriptFile(c Conversation, id, dir string, now time.Time) []byte {
	from := AgentName(c.Session.Agent)
	var body []byte
	var parent any // the first record's parentUuid is null
	n := 0
	last := now
	if len(c.Items) > 0 && !c.Items[0].Time.IsZero() {
		last = c.Items[0].Time
	}
	add := func(typ string, t time.Time, msg map[string]any, extra map[string]any) {
		if t.IsZero() {
			t = last
		}
		last = t
		n++
		uuid := newSessionUUID()
		provenance := map[string]string{"user": "real_user", "assistant": "assistant_output", "tool_result": "tool_result"}[typ]
		r := map[string]any{"uuid": uuid, "parentUuid": parent, "sessionId": id, "timestamp": geminiStamp(t), "type": typ,
			"provenance": provenance, "cwd": dir, "version": qwenWriteVersion, "message": msg}
		if c.Session.Branch != "" {
			r["gitBranch"] = c.Session.Branch
		}
		for k, v := range extra {
			r[k] = v
		}
		body = appendJSONLine(body, r)
		parent = uuid
	}
	user := func(text string, t time.Time) {
		add("user", t, map[string]any{"role": "user", "parts": []map[string]string{{"text": text}}}, nil)
	}
	var said []string
	var saidAt time.Time
	flush := func(call map[string]any) {
		if len(said) == 0 && call == nil {
			return
		}
		var parts []map[string]any
		if len(said) > 0 {
			parts = append(parts, map[string]any{"text": strings.Join(said, "\n\n")})
		}
		if call != nil {
			parts = append(parts, map[string]any{"functionCall": call})
		}
		add("assistant", saidAt, map[string]any{"role": "model", "parts": parts}, nil)
		said = nil
	}
	say := func(text string, t time.Time) {
		if len(said) == 0 {
			saidAt = t
		}
		said = append(said, text)
	}
	items, start := crossOrder(c)
	if start {
		user(startNote(from), items[0].Time)
	}
	calls := 0
	for _, it := range items {
		switch {
		case it.Kind == KindMessage && it.Role == "user":
			flush(nil)
			user(it.Text, it.Time)
		case it.Kind == KindMessage:
			say(it.Text, it.Time)
		case it.Kind == KindCommand:
			if len(said) == 0 {
				saidAt = it.Time
			}
			calls++
			call := fmt.Sprintf("call_conch_%d", calls)
			flush(map[string]any{"id": call, "name": "run_shell_command",
				"args": map[string]any{"command": it.Text, "description": "Run by " + from, "is_background": false}})
			out, status := qwenShellOutput(it, dir)
			add("tool_result", it.Time,
				map[string]any{"role": "user", "parts": []map[string]any{{"functionResponse": map[string]any{
					"id": call, "name": "run_shell_command", "response": map[string]string{"output": out}}}}},
				map[string]any{"toolCallResult": map[string]any{"callId": call, "status": status, "resultDisplay": it.Output}})
		default:
			say(crossNote(it, from), it.Time)
		}
	}
	// The last line joins what was said last, as one turn.
	say(switchedNote(c, "qwen"), time.Time{})
	flush(nil)
	return body
}

// qwenShellOutput is a command's result as Qwen Code 0.27's shell tool
// returns it to the model (Command, Directory, Output, Error, Exit Code,
// Signal and Process Group PGID, every one said), and the call's status.
func qwenShellOutput(it Item, dir string) (out, status string) {
	if !it.Done {
		return shellCancelled, "cancelled"
	}
	o := it.Output
	if o == "" {
		o = "(empty)"
	}
	exit := 0
	status = "success"
	if it.Failed {
		exit, status = failedExit(it), "error"
	}
	return fmt.Sprintf("Command: %s\nDirectory: %s\nOutput: %s\nError: (none)\nExit Code: %d\nSignal: (none)\nProcess Group PGID: (none)",
		it.Text, dir, o, exit), status
}
