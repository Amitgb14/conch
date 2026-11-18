package sessions

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// importEnv puts every agent's store and conch's own under a scratch
// home, as the plan's tests require, and returns where Claude's is.
func importEnv(t *testing.T) (Env, string) {
	t.Helper()
	home := t.TempDir()
	cfg := filepath.Join(home, "claude-config")
	t.Setenv("HOME", home)
	t.Setenv("CONCH_HOME", filepath.Join(home, "conch"))
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex"))
	return Env{Home: home, Getenv: os.Getenv}, cfg
}

func msg(id, role, content string, sec int) ImportedMessage {
	return ImportedMessage{ID: id, Role: role, Content: content, Time: time.Date(2026, 7, 10, 15, 19, sec, 0, time.UTC)}
}

// conductorConversation is a Claude conversation as Conductor keeps it:
// a prompt, a tool call and its result, a subagent's own messages (a
// Task), the answer, and the rows that are not the conversation.
func conductorConversation(dir string) Imported {
	return Imported{
		Session:   Session{Agent: "claude", ID: "conductor-s1", Dir: dir, Branch: "victoria", Title: "Fix the build"},
		Repo:      "shop",
		Root:      dir,
		Workspace: dir,
		Messages: []ImportedMessage{
			msg("m1", "user", "Why does the build fail?", 1),
			msg("m2", "assistant", `{"type":"system","subtype":"init","session_id":"c1","claude_code_version":"2.1.150"}`, 2),
			msg("m3", "assistant", `{"type":"assistant","uuid":"u3","session_id":"c1","parent_tool_use_id":null,"request_id":"req_1","message":{"id":"msg_1","model":"claude-x","role":"assistant","content":[{"type":"text","text":"Let me look."},{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"make <all>"}}],"usage":{"input_tokens":3}}}`, 3),
			msg("m4", "assistant", `{"type":"user","uuid":"u4","session_id":"c1","parent_tool_use_id":null,"timestamp":"2026-07-10T15:19:04Z","tool_use_result":{"stdout":"make: *** error","stderr":""},"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"make: *** error"}]}}`, 4),
			msg("m5", "assistant", `{"type":"assistant","uuid":"u5","parent_tool_use_id":null,"message":{"id":"msg_2","role":"assistant","content":[{"type":"tool_use","id":"toolu_2","name":"Task","input":{"prompt":"Find the header"}}]}}`, 5),
			msg("m6", "assistant", `{"type":"user","uuid":"u6","parent_tool_use_id":"toolu_2","message":{"role":"user","content":[{"type":"text","text":"Find the header"}]}}`, 6),
			msg("m7", "assistant", `{"type":"assistant","uuid":"u7","parent_tool_use_id":"toolu_2","message":{"id":"msg_3","role":"assistant","content":[{"type":"tool_use","id":"toolu_3","name":"Grep","input":{"pattern":"x.h"}}]}}`, 7),
			msg("m8", "assistant", `{"type":"user","uuid":"u8","parent_tool_use_id":"toolu_2","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_3","content":"src/x.h"}]}}`, 8),
			msg("m9", "assistant", `{"type":"user","uuid":"u9","parent_tool_use_id":null,"tool_use_result":{"content":"src/x.h"},"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_2","content":"It is src/x.h"}]}}`, 9),
			msg("m10", "assistant", `{"type":"assistant","uuid":"u10","parent_tool_use_id":null,"message":{"id":"msg_4","role":"assistant","content":[{"type":"text","text":"A missing header; fixed."}]}}`, 10),
			msg("m11", "assistant", `{"type":"result","subtype":"success","total_cost_usd":0.1}`, 11),
			msg("m12", "assistant", `not json at all`, 12),
		},
	}
}

type writtenLine struct {
	Type          string          `json:"type"`
	ParentUUID    *string         `json:"parentUuid"`
	UUID          string          `json:"uuid"`
	SessionID     string          `json:"sessionId"`
	Timestamp     string          `json:"timestamp"`
	Cwd           string          `json:"cwd"`
	GitBranch     string          `json:"gitBranch"`
	IsSidechain   bool            `json:"isSidechain"`
	UserType      string          `json:"userType"`
	Version       string          `json:"version"`
	Entrypoint    string          `json:"entrypoint"`
	RequestID     string          `json:"requestId"`
	AITitle       string          `json:"aiTitle"`
	ToolUseResult json.RawMessage `json:"toolUseResult"`
	Message       struct {
		ID      string          `json:"id"`
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

func readWritten(t *testing.T, path string) []writtenLine {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []writtenLine
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		var l writtenLine
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			t.Fatalf("a line that is not JSON: %v\n%s", err, sc.Bytes())
		}
		out = append(out, l)
	}
	if !bytes.HasSuffix(b, []byte("\n")) {
		t.Error("the file does not end with a newline")
	}
	return out
}

func parent(l writtenLine) string {
	if l.ParentUUID == nil {
		return "<null>"
	}
	return *l.ParentUUID
}

// toolIDs lists a message's tool_use ids and the ids its tool_results
// answer.
func toolIDs(t *testing.T, content json.RawMessage) (uses, results []string) {
	t.Helper()
	var blocks []struct {
		Type      string `json:"type"`
		ID        string `json:"id"`
		ToolUseID string `json:"tool_use_id"`
	}
	if len(content) == 0 || content[0] != '[' {
		return nil, nil
	}
	if err := json.Unmarshal(content, &blocks); err != nil {
		t.Fatal(err)
	}
	for _, b := range blocks {
		switch b.Type {
		case "tool_use":
			uses = append(uses, b.ID)
		case "tool_result":
			results = append(results, b.ToolUseID)
		}
	}
	return uses, results
}

func TestClaudeWriter(t *testing.T) {
	env, cfg := importEnv(t)
	dir := filepath.Join(env.Home, "src", "shop")
	os.MkdirAll(dir, 0o755)
	im := conductorConversation(dir)
	id, path, err := ClaudeWriter{Env: env}.Write(context.Background(), im, dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(cfg, "projects", claudeDirName(dir), id+".jsonl"); path != want {
		t.Errorf("path = %s, want %s", path, want)
	}
	if len(id) != 36 || strings.Count(id, "-") != 4 {
		t.Errorf("session id %q is not a uuid", id)
	}
	if st, err := os.Stat(path); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("the session file: %v %v", st, err)
	}
	if m, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".*")); len(m) > 0 {
		t.Errorf("writing left a temporary file: %v", m)
	}

	lines := readWritten(t, path)
	// prompt, call, result, Task call, 3 subagent lines, Task result,
	// answer, title: system, result and the unreadable row are left out.
	if len(lines) != 10 {
		for _, l := range lines {
			t.Logf("%s %s", l.Type, l.Message.Content)
		}
		t.Fatalf("%d lines, want 10", len(lines))
	}
	first := lines[0]
	if first.Type != "user" || string(first.Message.Content) != `"Why does the build fail?"` || first.Message.Role != "user" || first.ParentUUID != nil {
		t.Errorf("the prompt: %+v %s", first, first.Message.Content)
	}
	for i, l := range lines[:9] {
		if l.SessionID != id || l.Cwd != dir || l.GitBranch != "victoria" || l.UserType != "external" ||
			l.Version != "2.1.150" || l.Entrypoint != "conch-import" || l.UUID == "" {
			t.Errorf("line %d lacks a field: %+v", i, l)
		}
		if ts, err := time.Parse(time.RFC3339, l.Timestamp); err != nil || ts.Second() == 0 {
			t.Errorf("line %d's timestamp %q", i, l.Timestamp)
		}
	}
	if lines[0].Timestamp != "2026-07-10T15:19:01.000Z" {
		t.Errorf("the row's time is kept: %s", lines[0].Timestamp)
	}
	// The SDK's own uuids are kept; the prompt, which has none, gets one.
	for i, want := range map[int]string{1: "u3", 2: "u4", 3: "u5", 4: "u6", 5: "u7", 6: "u8", 7: "u9", 8: "u10"} {
		if lines[i].UUID != want {
			t.Errorf("line %d uuid = %s, want %s", i, lines[i].UUID, want)
		}
	}
	// The main chain runs prompt → call → result → Task → its result →
	// answer, past the subagent; the subagent's lines chain among
	// themselves, from nothing, as sidechains.
	for i, want := range map[int]string{1: first.UUID, 2: "u3", 3: "u4", 4: "<null>", 5: "u6", 6: "u7", 7: "u5", 8: "u9"} {
		if got := parent(lines[i]); got != want {
			t.Errorf("line %d (%s) parentUuid = %s, want %s", i, lines[i].UUID, got, want)
		}
	}
	for i, l := range lines[:9] {
		if side := i >= 4 && i <= 6; l.IsSidechain != side {
			t.Errorf("line %d isSidechain = %v, want %v", i, l.IsSidechain, side)
		}
	}
	if lines[1].RequestID != "req_1" || lines[1].Message.ID != "msg_1" || !strings.Contains(string(lines[1].Message.Content), `"make <all>"`) {
		t.Errorf("the agent's message, whole: %+v %s", lines[1], lines[1].Message.Content)
	}
	if string(lines[2].ToolUseResult) != `{"stdout":"make: *** error","stderr":""}` {
		t.Errorf("toolUseResult = %s", lines[2].ToolUseResult)
	}
	if last := lines[9]; last.Type != "ai-title" || last.AITitle != "Fix the build" || last.SessionID != id {
		t.Errorf("the last line names it: %+v", last)
	}

	// Every result answers a call made before it in its own chain.
	called := map[bool]map[string]bool{false: {}, true: {}}
	for _, l := range lines {
		uses, results := toolIDs(t, l.Message.Content)
		for _, u := range uses {
			called[l.IsSidechain][u] = true
		}
		for _, r := range results {
			if !called[l.IsSidechain][r] {
				t.Errorf("a result for %s, which was not called before it in its chain", r)
			}
			delete(called[l.IsSidechain], r)
		}
	}
	for side, left := range called {
		if len(left) > 0 {
			t.Errorf("calls with no result (sidechain %v): %v", side, left)
		}
	}
}

// Read back by conch's own reader of Claude's sessions, and listed for
// the folder it resumes in.
func TestClaudeWriterReadsBack(t *testing.T) {
	env, _ := importEnv(t)
	dir := filepath.Join(env.Home, "src", "shop")
	os.MkdirAll(dir, 0o755)
	id, path, err := ClaudeWriter{Env: env}.Write(context.Background(), conductorConversation(dir), dir)
	if err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(path)
	s := parseClaude(path, st)
	if s == nil || s.ID != id || s.Dir != dir || s.Branch != "victoria" || s.Title != "Fix the build" {
		t.Fatalf("parseClaude: %+v", s)
	}
	if !s.Started.Equal(time.Date(2026, 7, 10, 15, 19, 1, 0, time.UTC)) {
		t.Errorf("started = %v", s.Started)
	}
	turns, err := claudeTranscript(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 2 || turns[0].Text != "Why does the build fail?" || turns[1].Text != "Let me look.\n\nA missing header; fixed." {
		t.Errorf("transcript: %+v", turns)
	}
	found := false
	for _, s := range List(env, []string{dir}, 0) {
		found = found || s.Agent == "claude" && s.ID == id
	}
	if !found {
		t.Errorf("the written session is not in sessions.List for %s", dir)
	}
}

// Read from a Conductor database, written, listed: the whole way, with
// a workspace that is gone, so it resumes in the repository.
func TestClaudeWriterFromConductor(t *testing.T) {
	env, _ := importEnv(t)
	db := filepath.Join(env.Home, "conductor.db")
	root := filepath.Join(env.Home, "my shop.v2")
	os.MkdirAll(root, 0o755)
	conductorFixture(t, db, filepath.Join(env.Home, "gone", "victoria"), root, false)
	all, err := ReadConductor(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	im := all[0]
	if im.Session.Dir != root {
		t.Fatalf("the workspace is gone, so the repository: %q", im.Session.Dir)
	}
	id, path, err := ClaudeWriter{Env: env}.Write(context.Background(), im, im.Session.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(path, "-my-shop-v2") {
		t.Errorf("path %s is not under the repository's project folder", path)
	}
	lines := readWritten(t, path)
	if lines[0].Cwd != root {
		t.Errorf("cwd = %s, want the repository", lines[0].Cwd)
	}
	if lines[1].Version != "2.1.201" {
		t.Errorf("no init message says the version: %s, want Conductor's 2.1.201", lines[1].Version)
	}
	listed := false
	for _, s := range List(env, []string{root}, 0) {
		listed = listed || s.ID == id
	}
	if !listed {
		t.Error("not listed for the repository")
	}
}

func TestClaudeWriterInterrupted(t *testing.T) {
	env, _ := importEnv(t)
	dir := t.TempDir()
	im := Imported{Session: Session{Agent: "claude", ID: "s", Dir: dir, Title: "t"}, Messages: []ImportedMessage{
		msg("m1", "user", "go", 1),
		msg("m2", "assistant", `{"type":"assistant","uuid":"a1","message":{"id":"msg_1","role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{}}]}}`, 2),
		// A second call in the same message, as parallel calls arrive.
		msg("m3", "assistant", `{"type":"assistant","uuid":"a2","message":{"id":"msg_1","role":"assistant","content":[{"type":"tool_use","id":"t2","name":"Bash","input":{}}]}}`, 3),
		msg("m4", "assistant", `{"type":"user","uuid":"r2","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t2","content":"ok"}]}}`, 4),
		// Stopped before t1 returned, and asked something else.
		msg("m5", "user", "never mind", 5),
		msg("m6", "assistant", `{"type":"assistant","uuid":"a3","message":{"id":"msg_2","role":"assistant","content":[{"type":"tool_use","id":"t3","name":"Bash","input":{}}]}}`, 6),
	}}
	_, path, err := ClaudeWriter{Env: env}.Write(context.Background(), im, dir)
	if err != nil {
		t.Fatal(err)
	}
	lines := readWritten(t, path)
	var kinds []string
	for _, l := range lines {
		uses, results := toolIDs(t, l.Message.Content)
		kinds = append(kinds, l.Type+":"+strings.Join(uses, ",")+"/"+strings.Join(results, ","))
	}
	// t1 is answered before the next prompt, t3 at the end, each as
	// Claude Code answers a call it was stopped in.
	want := "user:/ assistant:t1/ assistant:t2/ user:/t2 user:/t1 user:/ assistant:t3/ user:/t3 ai-title:/"
	if got := strings.Join(kinds, " "); got != want {
		t.Errorf("lines:\n got %s\nwant %s", got, want)
	}
	if !strings.Contains(string(lines[4].Message.Content), `"is_error":true`) {
		t.Errorf("the answer to a call that never returned is an error: %s", lines[4].Message.Content)
	}
	for i := 1; i < len(lines)-1; i++ {
		if parent(lines[i]) != lines[i-1].UUID {
			t.Errorf("line %d does not follow line %d", i, i-1)
		}
	}
}

func TestClaudeWriterRefuses(t *testing.T) {
	env, cfg := importEnv(t)
	dir := t.TempDir()
	good := conductorConversation(dir)
	homeless := good
	homeless.Session.Dir = ""
	cases := map[string]struct {
		im   Imported
		dir  string
		want string
	}{
		"another agent":     {Imported{Session: Session{Agent: "codex", Dir: dir}, Messages: good.Messages}, dir, "Codex"},
		"no folder":         {homeless, "", "no folder"},
		"a folder not here": {good, filepath.Join(dir, "gone"), "gone"},
		"nothing said":      {Imported{Session: Session{Agent: "claude", Dir: dir}}, dir, "nothing"},
		"only noise": {Imported{Session: Session{Agent: "claude", Dir: dir}, Messages: []ImportedMessage{
			msg("m1", "assistant", `{"type":"system","subtype":"init"}`, 1)}}, dir, "nothing"},
		"the agent first": {Imported{Session: Session{Agent: "claude", Dir: dir}, Messages: good.Messages[2:]}, dir, "prompt"},
		"a result with no call": {Imported{Session: Session{Agent: "claude", Dir: dir}, Messages: []ImportedMessage{
			msg("m1", "user", "hi", 1),
			msg("m2", "assistant", `{"type":"user","uuid":"x","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"nope","content":""}]}}`, 2)}}, dir, "nope"},
	}
	for name, c := range cases {
		if err := (ClaudeWriter{Env: env}).Check(context.Background(), c.im, c.dir); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: Check said %v; want an error naming %q", name, err, c.want)
		}
		_, path, err := ClaudeWriter{Env: env}.Write(context.Background(), c.im, c.dir)
		if err == nil || !strings.Contains(err.Error(), c.want) || path != "" {
			t.Errorf("%s: %q, %v; want an error naming %q", name, path, err, c.want)
		}
	}
	if err := (ClaudeWriter{Env: env}).Check(context.Background(), good, dir); err != nil {
		t.Errorf("Check refused a good conversation: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := (ClaudeWriter{Env: env}).Write(ctx, good, dir); err == nil {
		t.Error("a cancelled write went ahead")
	}
	// Never a half-written session: nothing at all was left.
	if m, _ := filepath.Glob(filepath.Join(cfg, "projects", "*", "*")); len(m) > 0 {
		t.Errorf("a refused write left %v", m)
	}
}

func TestClaudeWriterDefaultsAndOddRows(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	env := Env{Home: home, Getenv: func(string) string { return "" }}
	dir := t.TempDir()
	im := Imported{Session: Session{Agent: "claude", ID: "s", Dir: dir}, Messages: []ImportedMessage{
		// A prompt kept as a stream message rather than as text.
		msg("m1", "user", `{"type":"user","message":{"role":"user","content":"kept as a message"}}`, 1),
		msg("m2", "assistant", `{"type":"assistant","uuid":"same","message":{"id":"a","role":"assistant","content":[{"type":"text","text":"one"}]}}`, 2),
		msg("m3", "user", "again", 3),
		// The same uuid twice would make a loop of the chain.
		msg("m4", "assistant", `{"type":"assistant","uuid":"same","message":{"id":"b","role":"assistant","content":[{"type":"text","text":"two"}]}}`, 0),
	}}
	im.Messages[3].Time = time.Time{}
	_, path, err := ClaudeWriter{Env: env}.Write(context.Background(), im, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(path, filepath.Join(home, ".claude", "projects")+string(filepath.Separator)) {
		t.Errorf("without CLAUDE_CONFIG_DIR it goes under ~/.claude: %s", path)
	}
	lines := readWritten(t, path)
	if len(lines) != 4 {
		t.Fatalf("lines = %d, want 4 and no title for an untitled conversation", len(lines))
	}
	if string(lines[0].Message.Content) != `"kept as a message"` || lines[0].Cwd != dir {
		t.Errorf("first: %+v %s", lines[0], lines[0].Message.Content)
	}
	if lines[3].UUID == "same" || lines[3].UUID == "" || parent(lines[3]) != lines[2].UUID {
		t.Errorf("a uuid seen before gets a new one: %+v", lines[3])
	}
	if lines[3].Timestamp != lines[2].Timestamp {
		t.Errorf("a row with no time takes the one before's: %s vs %s", lines[3].Timestamp, lines[2].Timestamp)
	}
}

func TestClaudeProjectDir(t *testing.T) {
	for in, want := range map[string]string{
		"/Users/me/my_proj.v2":   "-Users-me-my-proj-v2",
		"/Users/me/my shop":      "-Users-me-my-shop",
		"/Users/me/café":         "-Users-me-caf-", // one UTF-16 unit, one dash
		"/Users/me/日本":           "-Users-me---",   // as JavaScript's replace counts
		"/Users/me/a🐚b":          "-Users-me-a--b", // outside the BMP: two units
		"/private/tmp/x.y/z-w_v": "-private-tmp-x-y-z-w-v",
	} {
		if got := claudeDirName(in); got != want {
			t.Errorf("claudeDirName(%q) = %q, want %q", in, got, want)
		}
	}
}

// A path longer than Claude Code names in full: the writer uses the
// folder Claude made for it when there is one, else one of its own that
// the lister still finds.
func TestClaudeWriterLongPath(t *testing.T) {
	env, cfg := importEnv(t)
	dir := filepath.Join(t.TempDir(), strings.Repeat("deep", 30), strings.Repeat("er", 20))
	os.MkdirAll(dir, 0o755)
	if len(claudeDirName(dir)) <= 200 {
		t.Fatal("the path is not long enough to test")
	}
	im := conductorConversation(dir)
	id, path, err := ClaudeWriter{Env: env}.Write(context.Background(), im, dir)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(filepath.Base(filepath.Dir(path))); n > 210 {
		t.Errorf("project folder name of %d bytes", n)
	}
	listed := false
	for _, s := range List(env, []string{dir}, 0) {
		listed = listed || s.ID == id
	}
	if !listed {
		t.Error("a long path's session is not listed")
	}
	// Claude's own folder for it, with its hash: used rather than a second.
	own := filepath.Join(cfg, "projects", claudeDirName(dir)[:200]+"-abc123")
	os.MkdirAll(own, 0o700)
	os.RemoveAll(filepath.Dir(path))
	_, path, err = ClaudeWriter{Env: env}.Write(context.Background(), im, dir)
	if err != nil || filepath.Dir(path) != own {
		t.Errorf("wrote %s (%v), want into %s", path, err, own)
	}
}

// Conductor stores the stream as it arrives, and a tool's result can be
// stored a few milliseconds before the call it answers — seen in a real
// database: the result row, then the call 4ms later. That is one
// conversation, not a broken one, so the result is written after its
// call and the conversation resumes. A result whose call never comes is
// still refused (TestClaudeWriterRefuses).
func TestClaudeWriterResultStoredBeforeItsCall(t *testing.T) {
	env, _ := importEnv(t)
	dir := t.TempDir()
	call := `{"type":"assistant","uuid":"u3","message":{"id":"msg_1","role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"Write","input":{"file_path":"a.go"}}]}}`
	result := `{"type":"user","uuid":"u4","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"File has not been read yet."}]}}`
	im := Imported{
		Session: Session{Agent: "claude", ID: "s-order", Dir: dir, Title: "Out of order"},
		Messages: []ImportedMessage{
			msg("m1", "user", "Write a.go", 1),
			msg("m2", "assistant", result, 2), // stored first
			msg("m3", "assistant", call, 2),
			msg("m4", "assistant", `{"type":"assistant","uuid":"u5","message":{"id":"msg_2","role":"assistant","content":[{"type":"text","text":"Read it first, then wrote it."}]}}`, 3),
			msg("m5", "user", "Thanks", 4),
		},
	}
	w := ClaudeWriter{Env: env}
	if err := w.Check(context.Background(), im, dir); err != nil {
		t.Fatalf("Check: %v", err)
	}
	_, path, err := w.Write(context.Background(), im, dir)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	var order []string
	for _, l := range readWritten(t, path) {
		uses, results := toolIDs(t, l.Message.Content)
		for _, u := range uses {
			order = append(order, "call "+u)
		}
		for _, r := range results {
			order = append(order, "result "+r)
		}
	}
	if strings.Join(order, ", ") != "call toolu_1, result toolu_1" {
		t.Errorf("tool lines in the session: %v", order)
	}
}

// Original finds the session Claude Code kept itself in any project
// folder, and the folder it ran in from its lines; nothing for an id it
// has no file of, a name that is a pattern or a path, or a file that
// names no folder.
func TestClaudeOriginal(t *testing.T) {
	env, cfg := importEnv(t)
	w := ClaudeWriter{Env: env}
	proj := filepath.Join(cfg, "projects", "-w-dakar")
	os.MkdirAll(proj, 0o700)
	want := filepath.Join(proj, "dbb0ec46.jsonl")
	os.WriteFile(want, []byte("{\"type\":\"queue-operation\"}\nnot json\n{\"type\":\"user\",\"cwd\":\"/w/dakar\"}\n"), 0o600)
	os.WriteFile(filepath.Join(proj, "nocwd.jsonl"), []byte("{\"type\":\"ai-title\"}\n"), 0o600)
	if p, cwd := w.Original("dbb0ec46"); p != want || cwd != "/w/dakar" {
		t.Errorf("Original = %q %q", p, cwd)
	}
	for _, id := range []string{"", "nope", "nocwd", "*", "dbb0ec4?", "../projects/-w-dakar/dbb0ec46", "..", "."} {
		if p, cwd := w.Original(id); p != "" || cwd != "" {
			t.Errorf("Original(%q) = %q %q", id, p, cwd)
		}
	}
	// No config folder at all.
	if p, _ := (ClaudeWriter{Env: Env{Home: t.TempDir(), Getenv: func(string) string { return "" }}}).Original("x"); p != "" {
		t.Errorf("no folder: %q", p)
	}
}
