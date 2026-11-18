package sessions

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestImportAgain(t *testing.T) {
	env, _ := importEnv(t)
	dir := filepath.Join(env.Home, "shop")
	os.MkdirAll(dir, 0o755)
	statePath := filepath.Join(os.Getenv("CONCH_HOME"), "imports", "conductor", "state.json")
	w := ClaudeWriter{Env: env}
	ctx := context.Background()
	now := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	im := conductorConversation(dir)

	st, err := LoadImportState(statePath)
	if err != nil || len(st) != 0 {
		t.Fatalf("no state yet: %v %v", st, err)
	}
	out := ImportResumable(ctx, im, w, st, now)
	if out.Status != ImportWritten || out.Path == "" || out.AgentSessionID == "" || out.Err != nil {
		t.Fatalf("first import: %+v", out)
	}
	rec := st[im.Session.ID]
	fi, _ := os.Stat(out.Path)
	if rec.Agent != "claude" || rec.AgentSessionID != out.AgentSessionID || rec.Path != out.Path ||
		rec.LastMessageID != "m12" || rec.Size != fi.Size() || !rec.ImportedAt.Equal(now) || rec.Dir != dir {
		t.Errorf("record: %+v", rec)
	}
	if err := st.Save(statePath); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(statePath); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("state file: %v %v", fi, err)
	}
	st, err = LoadImportState(statePath)
	if err != nil || st[im.Session.ID] != rec {
		t.Fatalf("state read back: %+v %v", st, err)
	}
	before, _ := os.ReadFile(out.Path)

	// Again, nothing new: nothing written.
	again := ImportResumable(ctx, im, w, st, now.Add(time.Hour))
	if again.Status != ImportUnchanged || again.Path != out.Path {
		t.Errorf("import twice: %+v", again)
	}
	if after, _ := os.ReadFile(out.Path); !bytes.Equal(before, after) {
		t.Error("importing twice changed the session")
	}
	if m, _ := filepath.Glob(filepath.Join(filepath.Dir(out.Path), "*.jsonl")); len(m) != 1 {
		t.Errorf("importing twice made another session: %v", m)
	}

	// A new message in Conductor: added, chained on.
	im.Messages = append(im.Messages,
		msg("m13", "user", "And the tests?", 20),
		msg("m14", "assistant", `{"type":"assistant","uuid":"u14","message":{"id":"msg_5","role":"assistant","content":[{"type":"text","text":"They pass."}]}}`, 21))
	more := ImportResumable(ctx, im, w, st, now.Add(2*time.Hour))
	if more.Status != ImportAdded || more.Added != 2 || more.Path != out.Path {
		t.Fatalf("a new message: %+v", more)
	}
	after, _ := os.ReadFile(out.Path)
	if !bytes.HasPrefix(after, before) {
		t.Fatal("adding rewrote what was there")
	}
	lines := readWritten(t, out.Path)
	n := len(lines)
	if lines[n-3].Type != "user" || string(lines[n-3].Message.Content) != `"And the tests?"` || parent(lines[n-3]) != "u10" {
		t.Errorf("the new prompt follows the last answer: %+v", lines[n-3])
	}
	if lines[n-2].UUID != "u14" || parent(lines[n-2]) != lines[n-3].UUID || lines[n-2].SessionID != out.AgentSessionID {
		t.Errorf("the new answer: %+v", lines[n-2])
	}
	if lines[n-1].Type != "ai-title" {
		t.Errorf("the title ends it again: %+v", lines[n-1])
	}
	fi, _ = os.Stat(out.Path)
	if r := st[im.Session.ID]; r.LastMessageID != "m14" || r.Size != fi.Size() || r.AgentSessionID != out.AgentSessionID {
		t.Errorf("record after adding: %+v", r)
	}
	if again := ImportResumable(ctx, im, w, st, now); again.Status != ImportUnchanged {
		t.Errorf("after adding, again: %+v", again)
	}

	// Carried on in conch: Claude Code wrote to it. Left alone.
	f, _ := os.OpenFile(out.Path, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(`{"type":"user","message":{"role":"user","content":"carry on"}}` + "\n")
	f.Close()
	carried, _ := os.ReadFile(out.Path)
	recBefore := st[im.Session.ID]
	im.Messages = append(im.Messages, msg("m15", "user", "One more", 30))
	left := ImportResumable(ctx, im, w, st, now)
	if left.Status != ImportCarriedOn || !strings.Contains(left.Why, "carried on") {
		t.Errorf("carried on: %+v", left)
	}
	if b, _ := os.ReadFile(out.Path); !bytes.Equal(b, carried) {
		t.Error("a session carried on in conch was written to")
	}
	if st[im.Session.ID] != recBefore {
		t.Error("the record of a session carried on changed")
	}

	// Removed since: never written again, never deleted.
	os.Remove(out.Path)
	gone := ImportResumable(ctx, im, w, st, now)
	if gone.Status != ImportCarriedOn || !strings.Contains(gone.Why, "removed") {
		t.Errorf("removed: %+v", gone)
	}
	if _, err := os.Stat(out.Path); err == nil {
		t.Error("a session somebody removed was written again")
	}
}

// A call left open at the first import was answered then; when its real
// result arrives later it is not answered twice.
func TestImportAgainAfterAnOpenCall(t *testing.T) {
	env, _ := importEnv(t)
	dir := t.TempDir()
	im := Imported{Session: Session{Agent: "claude", ID: "s", Dir: dir, Title: "t"}, Messages: []ImportedMessage{
		msg("m1", "user", "go", 1),
		msg("m2", "assistant", `{"type":"assistant","uuid":"a1","message":{"id":"msg_1","role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{}}]}}`, 2),
	}}
	st := ImportState{}
	w := ClaudeWriter{Env: env}
	first := ImportResumable(context.Background(), im, w, st, time.Now())
	if first.Status != ImportWritten {
		t.Fatal(first)
	}
	im.Messages = append(im.Messages,
		msg("m3", "assistant", `{"type":"user","uuid":"r1","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"late"}]}}`, 3),
		msg("m4", "assistant", `{"type":"assistant","uuid":"a2","message":{"id":"msg_2","role":"assistant","content":[{"type":"text","text":"done"}]}}`, 4))
	more := ImportResumable(context.Background(), im, w, st, time.Now())
	if more.Status != ImportAdded {
		t.Fatalf("%+v", more)
	}
	results := 0
	var lines []writtenLine
	for _, l := range readWritten(t, first.Path) {
		_, r := toolIDs(t, l.Message.Content)
		results += len(r)
		if l.Type == "user" || l.Type == "assistant" {
			lines = append(lines, l)
		}
	}
	if results != 1 {
		t.Errorf("t1 answered %d times", results)
	}
	if last := lines[len(lines)-1]; last.UUID != "a2" || parent(last) != lines[len(lines)-2].UUID {
		t.Errorf("the answer follows the chain: %+v", last)
	}
}

func TestImportResumableRefusals(t *testing.T) {
	env, _ := importEnv(t)
	dir := t.TempDir()
	w := ClaudeWriter{Env: env}
	st := ImportState{}
	empty := ImportResumable(context.Background(), Imported{Session: Session{Agent: "claude", ID: "e", Dir: dir}}, w, st, time.Now())
	if empty.Status != ImportSkipped {
		t.Errorf("nothing said: %+v", empty)
	}
	codex := ImportResumable(context.Background(), Imported{Session: Session{Agent: "codex", ID: "c", Dir: dir},
		Messages: []ImportedMessage{msg("m1", "user", "hi", 1)}}, w, st, time.Now())
	if codex.Status != ImportFailed || codex.Err == nil {
		t.Errorf("a codex conversation to Claude's writer: %+v", codex)
	}
	if len(st) != 0 {
		t.Errorf("a refusal was recorded: %+v", st)
	}
	// A conversation whose last imported message is gone from Conductor.
	im := conductorConversation(dir)
	ok := ImportResumable(context.Background(), im, w, st, time.Now())
	if ok.Status != ImportWritten {
		t.Fatal(ok)
	}
	im.Messages = append(im.Messages[:3:3], msg("m99", "user", "new", 40))
	odd := ImportResumable(context.Background(), im, w, st, time.Now())
	if odd.Status != ImportFailed || odd.Err == nil || !strings.Contains(odd.Err.Error(), "m12") {
		t.Errorf("last imported message gone: %+v", odd)
	}
}

func TestImportStateFiles(t *testing.T) {
	dir := t.TempDir()
	// What an older conch wrote: fewer fields, and one this one does not know.
	old := filepath.Join(dir, "old.json")
	os.WriteFile(old, []byte(`{"s1":{"agent":"claude","agentSessionID":"x","path":"/p","lastMessageID":"m3","retired":true}}`), 0o600)
	st, err := LoadImportState(old)
	if err != nil || st["s1"].AgentSessionID != "x" || st["s1"].LastMessageID != "m3" || st["s1"].Size != 0 || st["s1"].Dir != "" {
		t.Errorf("an older state: %+v %v", st, err)
	}
	if st, err := LoadImportState(filepath.Join(dir, "none.json")); err != nil || st == nil || len(st) != 0 {
		t.Errorf("no state file: %v %v", st, err)
	}
	for _, bad := range []string{"{not json", `["a list"]`} {
		p := filepath.Join(dir, "bad.json")
		os.WriteFile(p, []byte(bad), 0o600)
		// Read wrongly, it would be saved over and everything imported again.
		if _, err := LoadImportState(p); err == nil {
			t.Errorf("a broken state %q loaded", bad)
		}
	}
	empty := filepath.Join(dir, "empty.json")
	os.WriteFile(empty, nil, 0o600)
	if st, err := LoadImportState(empty); err != nil || len(st) != 0 {
		t.Errorf("an empty state file: %v %v", st, err)
	}
	deep := filepath.Join(dir, "a", "b", "state.json")
	if err := (ImportState{"s": {Agent: "claude"}}).Save(deep); err != nil {
		t.Fatal(err)
	}
	if m, _ := filepath.Glob(filepath.Join(dir, "a", "b", ".*")); len(m) > 0 {
		t.Errorf("saving left %v", m)
	}
}

// ImportPlan says what an import would do and writes nothing.
func TestImportPlan(t *testing.T) {
	env, cfg := importEnv(t)
	dir := t.TempDir()
	im := conductorConversation(dir)
	st := ImportState{}
	if p := ImportPlan(im, st); p.Status != ImportWritten || p.Path != "" {
		t.Errorf("not imported: %+v", p)
	}
	if p := ImportPlan(Imported{}, st); p.Status != ImportSkipped {
		t.Errorf("nothing said: %+v", p)
	}
	if m, _ := filepath.Glob(filepath.Join(cfg, "projects", "*", "*")); len(m) > 0 {
		t.Errorf("planning wrote %v", m)
	}
	out := ImportResumable(context.Background(), im, ClaudeWriter{Env: env}, st, time.Now())
	if p := ImportPlan(im, st); p.Status != ImportUnchanged || p.Path != out.Path {
		t.Errorf("imported: %+v", p)
	}
	im.Messages = append(im.Messages, msg("m13", "user", "more", 30))
	before, _ := os.ReadFile(out.Path)
	if p := ImportPlan(im, st); p.Status != ImportAdded || p.Added != 1 {
		t.Errorf("one new: %+v", p)
	}
	if after, _ := os.ReadFile(out.Path); !bytes.Equal(before, after) {
		t.Error("planning wrote to the session")
	}
}
