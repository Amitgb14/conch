package sessions

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// SessionWriter makes a conversation imported from another app into a
// session of its agent's own, which that agent resumes as if it had
// written it. dir is the folder it resumes in: the workspace it ran in if
// that is still there, else the repository.
type SessionWriter interface {
	Agent() string
	Write(ctx context.Context, im Imported, dir string) (agentSessionID, path string, err error)
}

// SessionAppender adds what was said since an earlier import to the
// session that import wrote. after is the id of the last message that
// import wrote; added counts the messages it adds now.
type SessionAppender interface {
	Append(ctx context.Context, im Imported, dir, agentSessionID, path, after string) (added int, err error)
}

// claudeImportVersion is the Claude Code Conductor shipped (agent-binaries/),
// which wrote every conversation that does not say otherwise.
const claudeImportVersion = "2.1.201"

// ClaudeWriter writes Claude Code sessions: one JSONL file in the project
// folder Claude keeps for dir, under $CLAUDE_CONFIG_DIR/projects or
// ~/.claude/projects, which `claude --resume ID` opens with its history.
//
// Conductor kept each of the agent's stream messages (the Claude Agent
// SDK's) whole, so a line is that message with what Claude Code adds
// around it: a uuid chained to the line before by parentUuid, the session,
// the time, the folder and branch. A subagent's messages (the SDK's
// parent_tool_use_id) are sidechains with a chain of their own, as Claude
// Code keeps them out of the conversation it resumes.
type ClaudeWriter struct {
	Env Env
}

// Agent is the agent whose sessions it writes.
func (ClaudeWriter) Agent() string { return "claude" }

func (w ClaudeWriter) root() string {
	if d := w.Env.get("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	return filepath.Join(w.Env.Home, ".claude")
}

// Original is Claude Code's own session id, as Claude Code wrote it while
// another app ran it (Conductor runs Claude Code with the usual config
// folder): its file under any project folder, and the folder it ran in,
// read from its lines. Nothing for an id it has no file of, one that is
// not a plain name, or a file that names no folder.
func (w ClaudeWriter) Original(id string) (path, cwd string) {
	if id == "" || strings.ContainsAny(id, `/\*?[`) || id == "." || id == ".." {
		return "", ""
	}
	m, _ := filepath.Glob(filepath.Join(w.root(), "projects", "*", id+".jsonl"))
	for _, p := range m {
		if d := sessionCwd(p); d != "" {
			return p, d
		}
	}
	return "", ""
}

// sessionCwd is the folder a Claude Code session ran in: the first cwd
// its lines name, among the first few hundred.
func sessionCwd(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	cwd := ""
	lines(f, 300, func(b []byte) bool {
		var l struct {
			Cwd string `json:"cwd"`
		}
		if json.Unmarshal(b, &l) == nil && l.Cwd != "" {
			cwd = l.Cwd
			return false
		}
		return true
	})
	return cwd
}

// Write writes im as a new session and returns its id and file. Nothing is
// left behind when it fails: the file is made whole beside its place and
// only then moved there.
func (w ClaudeWriter) Write(ctx context.Context, im Imported, dir string) (string, string, error) {
	r, err := w.prepare(ctx, im, dir, newSessionUUID())
	if err != nil {
		return "", "", err
	}
	body, err := r.render(im.Messages)
	if err != nil {
		return "", "", err
	}
	if r.main == "" {
		return "", "", errors.New("nothing was said in it that Claude Code could resume")
	}
	body = append(body, r.title(im.Session.Title)...)
	path, err := w.place(r.cwd, r.sid)
	if err != nil {
		return "", "", err
	}
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	if err := writeWhole(path, body); err != nil {
		return "", "", err
	}
	return r.sid, path, nil
}

// Check says whether Write would write im, writing nothing.
func (w ClaudeWriter) Check(ctx context.Context, im Imported, dir string) error {
	r, err := w.prepare(ctx, im, dir, "check")
	if err != nil {
		return err
	}
	if _, err := r.render(im.Messages); err != nil {
		return err
	}
	if r.main == "" {
		return errors.New("nothing was said in it that Claude Code could resume")
	}
	return nil
}

// Append adds the messages after the one with id after to the session at
// path, which an earlier Write made from the same conversation. The lines
// already written are rebuilt from the messages up to after, so the new
// ones chain on from them; the file is put back as it was if the write
// fails part way.
func (w ClaudeWriter) Append(ctx context.Context, im Imported, dir, sid, path, after string) (int, error) {
	cut := -1
	for i, m := range im.Messages {
		if m.ID == after {
			cut = i + 1
		}
	}
	if cut < 0 {
		return 0, fmt.Errorf("the last message imported (%s) is no longer in the conversation", after)
	}
	if cut == len(im.Messages) {
		return 0, nil
	}
	r, err := w.prepare(ctx, im, dir, sid)
	if err != nil {
		return 0, err
	}
	if _, err := r.render(im.Messages[:cut]); err != nil {
		return 0, err
	}
	r.lines = 0
	body, err := r.renderMore(im.Messages[cut:])
	if err != nil {
		return 0, err
	}
	body = r.close(body)
	if r.lines == 0 {
		return 0, nil // only rows that are not the conversation
	}
	body = append(body, r.title(im.Session.Title)...)
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	st, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return 0, err
	}
	if _, err := f.Write(body); err != nil {
		f.Close()
		_ = os.Truncate(path, st.Size())
		return 0, err
	}
	if err := f.Close(); err != nil {
		_ = os.Truncate(path, st.Size())
		return 0, err
	}
	return len(im.Messages) - cut, nil
}

func (w ClaudeWriter) prepare(ctx context.Context, im Imported, dir, sid string) (*claudeRender, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if im.Session.Agent != "claude" {
		return nil, fmt.Errorf("a %s conversation is not Claude Code's to resume", AgentName(im.Session.Agent))
	}
	if dir == "" {
		dir = im.Session.Dir
	}
	if dir == "" {
		return nil, errors.New("no folder to resume it in: its workspace and repository are both gone")
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("no folder to resume it in: %s is not there", dir)
	}
	return &claudeRender{
		sid: sid, cwd: dir, branch: im.Session.Branch, conv: im.Session.ID,
		version: claudeVersionIn(im.Messages),
		side:    map[string]string{}, pending: map[string][]string{}, pendingMsg: map[string]string{},
		called: map[string]string{}, answered: map[string]bool{}, seen: map[string]bool{},
	}, nil
}

// place is the file for session sid in dir's project folder, made if need
// be. A path Claude Code names in full is named so here; a longer one it
// cuts at 200 and ends with a hash of its own, so the folder Claude made
// for it is used when there is one, and otherwise one ending in a hash of
// conch's, which the lister finds by the same cut.
func (w ClaudeWriter) place(cwd, sid string) (string, error) {
	folder := w.folder(cwd)
	if err := os.MkdirAll(folder, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(folder, sid+".jsonl"), nil
}

// Filed says whether the session at path is in the project folder Claude
// Code reads for cwd: the only one `claude --resume` run there looks in.
func (w ClaudeWriter) Filed(path, cwd string) bool {
	return filepath.Dir(path) == w.folder(cwd)
}

// Move puts the session at path into cwd's project folder, unchanged —
// one carried on keeps everything — so that `claude --resume` run in cwd
// finds it: a session written for a worktree since removed is otherwise
// found nowhere. A file already there by that name is never written over.
func (w ClaudeWriter) Move(path, cwd string) (string, error) {
	to, err := w.place(cwd, strings.TrimSuffix(filepath.Base(path), ".jsonl"))
	if err != nil {
		return "", err
	}
	if _, err := os.Lstat(to); err == nil {
		return "", fmt.Errorf("%s is there already", to)
	}
	if err := os.Rename(path, to); err != nil {
		return "", err
	}
	return to, nil
}

// Folder is cwd's project folder, made or not.
func (w ClaudeWriter) Folder(cwd string) string { return w.folder(cwd) }

func (w ClaudeWriter) folder(cwd string) string {
	projects := filepath.Join(w.root(), "projects")
	name := claudeDirName(cwd)
	if len(name) > claudeDirMax {
		cut := name[:claudeDirMax] + "-"
		name = ""
		if entries, err := os.ReadDir(projects); err == nil {
			for _, e := range entries {
				if e.IsDir() && strings.HasPrefix(e.Name(), cut) {
					name = e.Name()
					break
				}
			}
		}
		if name == "" {
			sum := sha256.Sum256([]byte(cwd))
			name = cut + hex.EncodeToString(sum[:4])
		}
	}
	return filepath.Join(projects, name)
}

// writeWhole writes body to path through a file beside it, so path is
// either not there or whole.
func writeWhole(path string, body []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// claudeVersionIn is the Claude Code version the conversation's own init
// message names, else the one Conductor shipped.
func claudeVersionIn(msgs []ImportedMessage) string {
	for _, m := range msgs {
		if !strings.Contains(m.Content, "claude_code_version") {
			continue
		}
		var init struct {
			Type    string `json:"type"`
			Version string `json:"claude_code_version"`
		}
		if json.Unmarshal([]byte(m.Content), &init) == nil && init.Type == "system" && init.Version != "" {
			return init.Version
		}
	}
	return claudeImportVersion
}

// claudeSessionLine is one line of a Claude Code session, its fields in the order
// Claude Code writes them.
type claudeSessionLine struct {
	ParentUUID    *string         `json:"parentUuid"`
	IsSidechain   bool            `json:"isSidechain"`
	Type          string          `json:"type"`
	Message       json.RawMessage `json:"message"`
	ToolUseResult json.RawMessage `json:"toolUseResult,omitempty"`
	RequestID     string          `json:"requestId,omitempty"`
	UUID          string          `json:"uuid"`
	Timestamp     string          `json:"timestamp"`
	UserType      string          `json:"userType"`
	Entrypoint    string          `json:"entrypoint"`
	Cwd           string          `json:"cwd"`
	SessionID     string          `json:"sessionId"`
	Version       string          `json:"version"`
	GitBranch     string          `json:"gitBranch"`
}

// sdkMessage is the part of a Claude Agent SDK stream message a session
// line is made from.
type sdkMessage struct {
	Type          string          `json:"type"`
	UUID          string          `json:"uuid"`
	ParentToolUse *string         `json:"parent_tool_use_id"`
	RequestID     string          `json:"request_id"`
	ToolUseResult json.RawMessage `json:"tool_use_result"`
	Message       json.RawMessage `json:"message"`
}

type contentBlock struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	ToolUseID string `json:"tool_use_id"`
}

// claudeRender turns a conversation's rows into session lines, keeping
// the chains: the conversation's (key "") and each subagent's (keyed by
// the tool call that started it).
type claudeRender struct {
	sid, cwd, branch, version, conv string
	main                            string            // the conversation's last uuid
	side                            map[string]string // a subagent's last uuid
	// pending are a chain's tool calls not yet answered, made by the
	// message pendingMsg names; called says in which chain each call was
	// made, answered which have their result.
	pending    map[string][]string
	pendingMsg map[string]string
	called     map[string]string
	answered   map[string]bool
	seen       map[string]bool
	last       time.Time
	lines      int // lines written since this was last set to 0
}

func (r *claudeRender) render(msgs []ImportedMessage) ([]byte, error) {
	body, err := r.renderMore(msgs)
	if err != nil {
		return nil, err
	}
	return r.close(body), nil
}

// close ends every chain. A call still open when the conversation ends
// is answered, as Claude Code answers one it was stopped in, or the next
// prompt would follow a call with no result and the API would refuse the
// conversation.
func (r *claudeRender) close(body []byte) []byte {
	chains := make([]string, 0, len(r.pending))
	for chain := range r.pending {
		chains = append(chains, chain)
	}
	sort.Strings(chains) // the conversation's own first, then the same order every time
	for _, chain := range chains {
		body = r.answerPending(body, chain)
	}
	return body
}

// renderMore renders msgs without closing what they leave open, which
// Append needs to close once the new messages are in.
func (r *claudeRender) renderMore(msgs []ImportedMessage) ([]byte, error) {
	var body []byte
	for _, m := range callsFirst(msgs) {
		if !m.Time.IsZero() {
			r.last = m.Time
		}
		var err error
		if body, err = r.add(body, m); err != nil {
			return nil, fmt.Errorf("message %s: %w", m.ID, err)
		}
	}
	return body, nil
}

func (r *claudeRender) add(body []byte, m ImportedMessage) ([]byte, error) {
	trimmed := strings.TrimSpace(m.Content)
	var s sdkMessage
	isJSON := strings.HasPrefix(trimmed, "{") && json.Unmarshal([]byte(trimmed), &s) == nil
	if m.Role == "user" && (!isJSON || s.Type == "") {
		// What was typed, as it was typed.
		text, _ := json.Marshal(m.Content)
		msg, _ := json.Marshal(map[string]json.RawMessage{"role": json.RawMessage(`"user"`), "content": text})
		body = r.answerPending(body, "")
		return r.line(body, m, "", "user", r.uuidFor(m, ""), msg, nil, ""), nil
	}
	if !isJSON {
		return body, nil // a row nobody can read: it was never the conversation
	}
	chain := ""
	if s.ParentToolUse != nil {
		chain = *s.ParentToolUse
	}
	switch s.Type {
	case "assistant":
		var msg struct {
			ID      string          `json:"id"`
			Content json.RawMessage `json:"content"`
		}
		if json.Unmarshal(s.Message, &msg) != nil {
			return body, nil
		}
		if r.pendingMsg[chain] != msg.ID || msg.ID == "" {
			// Calls a message made are answered before another message;
			// one message's calls arrive a line each.
			body = r.answerPending(body, chain)
		}
		if r.chainEmpty(chain) && chain == "" {
			return nil, errors.New("it begins with Claude's reply, not a prompt")
		}
		for _, b := range blocks(msg.Content) {
			if b.Type == "tool_use" && b.ID != "" {
				r.pending[chain] = append(r.pending[chain], b.ID)
				r.pendingMsg[chain] = msg.ID
				r.called[b.ID] = chain
			}
		}
		return r.line(body, m, chain, "assistant", r.uuidFor(m, s.UUID), s.Message, nil, s.RequestID), nil
	case "user":
		var msg map[string]json.RawMessage
		if json.Unmarshal(s.Message, &msg) != nil {
			return body, nil
		}
		content := msg["content"]
		var parts []json.RawMessage
		if len(content) > 0 && content[0] == '[' && json.Unmarshal(content, &parts) == nil {
			kept := parts[:0]
			results := 0
			for _, p := range parts {
				var b contentBlock
				_ = json.Unmarshal(p, &b)
				if b.Type == "tool_result" {
					where, ok := r.called[b.ToolUseID]
					if !ok || where != chain {
						return nil, fmt.Errorf("a tool's result (%s) answers no call made before it", b.ToolUseID)
					}
					if r.answered[b.ToolUseID] {
						continue // answered already, when the call was left open
					}
					r.answered[b.ToolUseID] = true
					results++
				}
				kept = append(kept, p)
			}
			if len(kept) == 0 {
				return body, nil
			}
			if len(kept) != len(parts) {
				msg["content"], _ = json.Marshal(kept)
				s.Message, _ = json.Marshal(msg)
			}
			if results == 0 {
				body = r.answerPending(body, chain) // a prompt, not results
			} else {
				r.dropAnswered(chain)
			}
		} else {
			body = r.answerPending(body, chain)
		}
		return r.line(body, m, chain, "user", r.uuidFor(m, s.UUID), s.Message, s.ToolUseResult, ""), nil
	}
	// The stream's other messages — the system's, the result, progress —
	// are not the conversation.
	return body, nil
}

// callsFirst puts each tool result after the call it answers. Conductor
// stores the stream as it arrives, and a result can be stored a few
// milliseconds before its call (seen in a real database: the result's
// row, then the call's 4ms later). A row of results whose call comes later
// moves to just after the last of those calls; everything else keeps its
// place. A result whose call never comes stays where it is, and is
// refused there as before.
func callsFirst(msgs []ImportedMessage) []ImportedMessage {
	calledAt := map[string]int{} // a tool_use id → the row that made it
	answers := make([][]string, len(msgs))
	for i, m := range msgs {
		var s sdkMessage
		if json.Unmarshal([]byte(strings.TrimSpace(m.Content)), &s) != nil {
			continue
		}
		var msg struct {
			Content json.RawMessage `json:"content"`
		}
		if json.Unmarshal(s.Message, &msg) != nil {
			continue
		}
		for _, b := range blocks(msg.Content) {
			switch {
			case s.Type == "assistant" && b.Type == "tool_use" && b.ID != "":
				if _, ok := calledAt[b.ID]; !ok {
					calledAt[b.ID] = i
				}
			case s.Type == "user" && b.Type == "tool_result":
				answers[i] = append(answers[i], b.ToolUseID)
			}
		}
	}
	after := map[int][]int{} // a call's row → the early results to put after it
	moved := map[int]bool{}
	for i, ids := range answers {
		last := -1
		for _, id := range ids {
			if at, ok := calledAt[id]; ok && at > i && at > last {
				last = at
			}
		}
		if last >= 0 {
			after[last] = append(after[last], i)
			moved[i] = true
		}
	}
	if len(moved) == 0 {
		return msgs
	}
	out := make([]ImportedMessage, 0, len(msgs))
	for i, m := range msgs {
		if !moved[i] {
			out = append(out, m)
		}
		for _, j := range after[i] {
			out = append(out, msgs[j])
		}
	}
	return out
}

func blocks(content json.RawMessage) []contentBlock {
	var bs []contentBlock
	if len(content) > 0 && content[0] == '[' {
		_ = json.Unmarshal(content, &bs)
	}
	return bs
}

func (r *claudeRender) chainEmpty(chain string) bool {
	if chain == "" {
		return r.main == ""
	}
	return r.side[chain] == ""
}

func (r *claudeRender) dropAnswered(chain string) {
	open := r.pending[chain][:0]
	for _, id := range r.pending[chain] {
		if !r.answered[id] {
			open = append(open, id)
		}
	}
	r.pending[chain] = open
	if len(open) == 0 {
		delete(r.pending, chain)
		delete(r.pendingMsg, chain)
	}
}

// answerPending writes a result for each of chain's calls that has none:
// the call never returned before the conversation went on, or ended.
func (r *claudeRender) answerPending(body []byte, chain string) []byte {
	r.dropAnswered(chain)
	ids := r.pending[chain]
	if len(ids) == 0 {
		return body
	}
	parts := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		r.answered[id] = true
		parts = append(parts, map[string]any{"type": "tool_result", "tool_use_id": id, "is_error": true,
			"content": "[Request interrupted by user for tool use]"})
	}
	delete(r.pending, chain)
	delete(r.pendingMsg, chain)
	msg, _ := json.Marshal(map[string]any{"role": "user", "content": parts})
	uuid := uuidFrom(r.conv, "interrupted", strings.Join(ids, ","))
	return r.line(body, ImportedMessage{Time: r.last}, chain, "user", uuid, msg, nil, "")
}

func (r *claudeRender) uuidFor(m ImportedMessage, sdk string) string {
	if sdk != "" && !r.seen[sdk] {
		return sdk
	}
	// The same every time for the same row, so importing again rebuilds
	// the chain it wrote before.
	return uuidFrom(r.conv, m.ID, sdk)
}

func (r *claudeRender) line(body []byte, m ImportedMessage, chain, typ, uuid string, msg, result json.RawMessage, requestID string) []byte {
	for r.seen[uuid] {
		uuid = uuidFrom(r.conv, uuid)
	}
	r.seen[uuid] = true
	var parent *string
	if chain == "" {
		if r.main != "" {
			p := r.main
			parent = &p
		}
		r.main = uuid
	} else {
		if p := r.side[chain]; p != "" {
			parent = &p
		}
		r.side[chain] = uuid
	}
	at := m.Time
	if at.IsZero() {
		at = r.last
	}
	if at.IsZero() {
		at = time.Now()
	}
	if len(result) == 0 || string(result) == "null" {
		result = nil
	}
	l := claudeSessionLine{ParentUUID: parent, IsSidechain: chain != "", Type: typ, Message: msg, ToolUseResult: result,
		RequestID: requestID, UUID: uuid, Timestamp: at.UTC().Format("2006-01-02T15:04:05.000Z"), UserType: "external",
		Entrypoint: "conch-import", Cwd: r.cwd, SessionID: r.sid, Version: r.version, GitBranch: r.branch}
	r.lines++
	return appendJSONLine(body, l)
}

func (r *claudeRender) title(t string) []byte {
	if strings.TrimSpace(t) == "" {
		return nil
	}
	return appendJSONLine(nil, map[string]string{"type": "ai-title", "aiTitle": t, "sessionId": r.sid})
}

// appendJSONLine adds v as one line of JSON, leaving <, > and & as they
// are, as Claude Code writes them.
func appendJSONLine(body []byte, v any) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v) // every value here marshals
	return append(body, b.Bytes()...)
}

// uuidFrom is a uuid made from parts, the same for the same parts.
func uuidFrom(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	b := sum[:16]
	b[6] = b[6]&0x0f | 0x50
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// newSessionUUID is a random (version 4) uuid, as Claude Code names a
// session.
func newSessionUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
