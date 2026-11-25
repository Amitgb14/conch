package sessions

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// requests is what the person asked, in order.
func requests(c Conversation) []string {
	var out []string
	for _, it := range c.Items {
		if it.Kind == KindMessage && it.Role == "user" {
			out = append(out, it.Text)
		}
	}
	return out
}

// commands is each command with its output: a command that never
// finished has none to compare, since each agent says so its own way.
func commands(c Conversation) []string {
	var out []string
	for _, it := range c.Items {
		if it.Kind == KindCommand {
			out = append(out, it.Text+" → "+it.Output)
		}
	}
	return out
}

func finished(c Conversation) []string {
	var out []string
	for _, it := range c.Items {
		if it.Kind == KindCommand {
			s := it.Text
			if it.Done && !strings.Contains(it.Output, "did not finish") && !strings.Contains(it.Output, "interrupted") {
				s += " → " + it.Output
			}
			out = append(out, s)
		}
	}
	return out
}

func readSession(t *testing.T, agent, path string) Conversation {
	t.Helper()
	c, err := ReadConversation(context.Background(), Env{}, Session{Agent: agent, Path: path})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// Codex's conversations, both shapes, written as Claude Code sessions and
// read back: every request word for word, every command with its output
// as Claude's Bash, everything else a note — never an edit of Claude's.
func TestCodexToClaudeRoundTrip(t *testing.T) {
	for name, fixture := range map[string]string{"old": codexOld, "new": codexNew} {
		t.Run(name, func(t *testing.T) {
			src := readConv(t, "codex", fixture)
			home, dir := t.TempDir(), t.TempDir()
			w := ClaudeWriter{Env: Env{Home: home}}
			wr, err := w.WriteConversation(context.Background(), src, dir)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(wr.Path, filepath.Join(home, ".claude", "projects")) || wr.ID == "" || len(wr.Files) != 1 {
				t.Fatalf("written: %+v", wr)
			}
			got := readSession(t, "claude", wr.Path)
			if err := CheckReadBack(src, got); err != nil {
				t.Fatal(err)
			}
			if strings.Join(requests(got), "|") != strings.Join(requests(src), "|") {
				t.Fatalf("requests:\n%q\n%q", requests(got), requests(src))
			}
			if strings.Join(finished(got), "|") != strings.Join(finished(src), "|") {
				t.Fatalf("commands:\n%q\n%q", finished(got), finished(src))
			}
			body, _ := os.ReadFile(wr.Path)
			for _, never := range []string{`"name":"Edit"`, `"name":"Write"`, `"name":"MultiEdit"`, "gAAAA", "hidden"} {
				if strings.Contains(string(body), never) {
					t.Fatalf("the session holds %s:\n%s", never, body)
				}
			}
			text := strings.Join(func() []string {
				var s []string
				for _, it := range got.Items {
					s = append(s, it.Text)
				}
				return s
			}(), "\n")
			if !strings.Contains(text, "conch switched this conversation from Codex to Claude Code") {
				t.Fatalf("no line saying where it came from:\n%s", text)
			}
			switch name {
			case "old":
				for _, note := range []string{"[Codex edited upload/up.go (+2 −1)]", "[Codex created upload/retry.go (1 lines)]", "[Codex deleted upload/old.go]",
					"[Codex used view_image", "The plan as it stands:", "- [x] Read the uploader", "- [~] Add the retry", "An image was attached"} {
					if !strings.Contains(text, note) {
						t.Fatalf("missing %q in:\n%s", note, text)
					}
				}
			case "new":
				for _, note := range []string{"[Codex read web/page.tsx]", "[Codex edited web/página.tsx (+1 −1)]", "finance.get_stock_price", "— it failed", "[Interrupted]", "Quota exceeded"} {
					if !strings.Contains(text, note) {
						t.Fatalf("missing %q in:\n%s", note, text)
					}
				}
			}
			// The failed command keeps its exit code through Claude's
			// "Exit code N".
			for _, it := range got.Items {
				if it.Kind == KindCommand && it.Text == "npm test" && (!it.Failed || it.Exit != 2) {
					t.Fatalf("npm test: %+v", it)
				}
			}
		})
	}
}

// A command that never got its result is answered as interrupted, as
// Claude Code answers one it was stopped in, so the session still resumes.
func TestSwitchCommandWithoutResult(t *testing.T) {
	c := Conversation{Session: Session{Agent: "codex", ID: "c9"}, Items: []Item{
		{Kind: KindMessage, Role: "user", Text: "run it"},
		{Kind: KindCommand, Role: "assistant", Text: "make long"},
	}}
	wr, err := ClaudeWriter{Env: Env{Home: t.TempDir()}}.WriteConversation(context.Background(), c, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	got := readSession(t, "claude", wr.Path)
	if cmds := commands(got); len(cmds) != 1 || !strings.Contains(cmds[0], "make long") {
		t.Fatalf("%q", cmds)
	}
	body, _ := os.ReadFile(wr.Path)
	if !strings.Contains(string(body), "[Request interrupted by user for tool use]") {
		t.Fatalf("not answered:\n%s", body)
	}
}

// A conversation that starts mid-way — its first request cut off — opens
// with a note, since Claude Code's session must start with the person.
func TestSwitchConversationWithoutItsStart(t *testing.T) {
	c := Conversation{Session: Session{Agent: "codex", ID: "c9"}, Items: []Item{
		{Kind: KindMessage, Role: "assistant", Text: "done half"},
		{Kind: KindMessage, Role: "user", Text: "now the rest"},
	}}
	wr, err := ClaudeWriter{Env: Env{Home: t.TempDir()}}.WriteConversation(context.Background(), c, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	got := readSession(t, "claude", wr.Path)
	if r := requests(got); len(r) != 2 || !strings.Contains(r[0], "its beginning was not kept") || r[1] != "now the rest" {
		t.Fatalf("%q", r)
	}
	if err := CheckReadBack(c, got); err != nil {
		t.Fatal(err)
	}
}

func TestSwitchWriteEdges(t *testing.T) {
	w := ClaudeWriter{Env: Env{Home: t.TempDir()}}
	// An empty conversation writes nothing.
	if _, err := w.WriteConversation(context.Background(), Conversation{Session: Session{Agent: "codex"}}, t.TempDir()); err == nil {
		t.Fatal("an empty conversation was written")
	}
	// A missing folder to resume in writes nothing.
	one := Conversation{Session: Session{Agent: "codex", ID: "x"}, Items: []Item{{Kind: KindMessage, Role: "user", Text: "hi"}}}
	if _, err := w.WriteConversation(context.Background(), one, filepath.Join(t.TempDir(), "gone")); err == nil {
		t.Fatal("written for a folder that is not there")
	}
	if entries, _ := os.ReadDir(filepath.Join(w.Env.Home, ".claude", "projects")); len(entries) != 0 {
		t.Fatalf("left behind: %v", entries)
	}
	// One message, in a folder with a non-ASCII name; what the person
	// typed that looks like JSON stays what they typed.
	dir := filepath.Join(t.TempDir(), "prøjekt ✓")
	os.MkdirAll(dir, 0o755)
	one.Items[0].Text = `{"type":"assistant","message":"not really"}`
	wr, err := w.WriteConversation(context.Background(), one, dir)
	if err != nil {
		t.Fatal(err)
	}
	got := readSession(t, "claude", wr.Path)
	if r := requests(got); len(r) != 1 || r[0] != one.Items[0].Text {
		t.Fatalf("%q", r)
	}
	// A cancelled context writes nothing.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := w.WriteConversation(ctx, one, dir); err == nil {
		t.Fatal("written with a cancelled context")
	}
}

// A conversation over the budget keeps its first request and its latest
// turns whole; the middle is shortened and said to be.
func TestFitConversation(t *testing.T) {
	c := Conversation{Session: Session{Agent: "codex"}}
	at := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	c.Items = append(c.Items, Item{Kind: KindMessage, Role: "user", Text: "the first request", Time: at})
	for i := 0; i < 200; i++ {
		c.Items = append(c.Items,
			Item{Kind: KindMessage, Role: "user", Text: "turn " + strings.Repeat("u", 2000)},
			Item{Kind: KindCommand, Role: "assistant", Text: "make", Output: strings.Repeat("o", 4000), Done: true},
			Item{Kind: KindMessage, Role: "assistant", Text: "answer " + strings.Repeat("a", 2000)})
	}
	c.Items = append(c.Items, Item{Kind: KindMessage, Role: "user", Text: "the latest request"},
		Item{Kind: KindMessage, Role: "assistant", Text: "the latest answer"})
	budget := 200 << 10
	fit, shortened := FitConversation(c, budget)
	if shortened == 0 {
		t.Fatal("nothing shortened")
	}
	size := 0
	for _, it := range fit.Items {
		size += itemSize(it)
	}
	if size > budget {
		t.Fatalf("%d bytes over a budget of %d", size, budget)
	}
	if fit.Items[0].Text != "the first request" || fit.Items[1].Kind != KindNote || !strings.Contains(fit.Items[1].Text, "shortened") {
		t.Fatalf("head: %+v %+v", fit.Items[0], fit.Items[1])
	}
	last := fit.Items[len(fit.Items)-2:]
	if last[0].Text != "the latest request" || last[1].Text != "the latest answer" {
		t.Fatalf("tail: %+v", last)
	}
	// The kept tail starts at a request.
	for i, it := range fit.Items {
		if it.Kind == KindCommand {
			if it.Output != strings.Repeat("o", 4000) {
				t.Fatal("a kept command was cut")
			}
			for j := i; j >= 0; j-- {
				if fit.Items[j].Kind == KindNote {
					t.Fatal("a command from the shortened middle was kept")
				}
				if fit.Items[j].Kind == KindMessage && fit.Items[j].Role == "user" && !strings.HasPrefix(fit.Items[j].Text, "the first") {
					break
				}
			}
			break
		}
	}
	// Within budget: as it was.
	small := Conversation{Items: c.Items[:4]}
	if got, n := FitConversation(small, budget); n != 0 || len(got.Items) != 4 {
		t.Fatalf("%d %d", n, len(got.Items))
	}
	// Measure says so.
	_, cr := Measure(c, budget)
	if cr.Fits || cr.Shortened != shortened || len(cr.Left) < 2 {
		t.Fatalf("%+v", cr)
	}
	// The fitted conversation still writes and reads back.
	wr, err := ClaudeWriter{Env: Env{Home: t.TempDir()}}.WriteConversation(context.Background(), fit, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckReadBack(fit, readSession(t, "claude", wr.Path)); err != nil {
		t.Fatal(err)
	}
}

// One step larger than the whole budget is kept, cut.
func TestFitConversationOneHugeStep(t *testing.T) {
	c := Conversation{Items: []Item{
		{Kind: KindMessage, Role: "user", Text: "go"},
		{Kind: KindMessage, Role: "assistant", Text: "a"},
		{Kind: KindMessage, Role: "user", Text: strings.Repeat("z", 50000)},
	}}
	fit, _ := FitConversation(c, 10000)
	if len(fit.Items) == 0 || fit.Items[0].Text != "go" || !strings.HasPrefix(fit.Items[len(fit.Items)-1].Text, "zzz") {
		t.Fatalf("%+v", fit.Items)
	}
}

// A writer whose trial reads back wrong writes nothing for real.
type badWriter struct {
	ClaudeWriter
	reads Conversation
}

func (b badWriter) ReadBack(context.Context, Written) (Conversation, error) { return b.reads, nil }

func TestTryWriteFallsBack(t *testing.T) {
	src := readConv(t, "codex", codexOld)
	real := t.TempDir()
	w := ClaudeWriter{Env: Env{Home: real}}
	bad := func(tmp string) ConversationWriter {
		return badWriter{ClaudeWriter: ClaudeWriter{Env: Env{Home: tmp}}, reads: Conversation{Items: []Item{{Kind: KindMessage, Role: "user", Text: "other"}}}}
	}
	if _, err := TryWrite(context.Background(), src, w, bad, t.TempDir()); err == nil || !strings.Contains(err.Error(), "trial") {
		t.Fatalf("%v", err)
	}
	if _, err := os.Stat(filepath.Join(real, ".claude")); !os.IsNotExist(err) {
		t.Fatal("written for real after a trial failed")
	}
	good := func(tmp string) ConversationWriter { return ClaudeWriter{Env: Env{Home: tmp}} }
	wr, err := TryWrite(context.Background(), src, w, good, t.TempDir())
	if err != nil || !strings.HasPrefix(wr.Path, real) {
		t.Fatalf("%v %+v", err, wr)
	}
}

func TestCheckReadBack(t *testing.T) {
	sent := Conversation{Items: []Item{
		{Kind: KindMessage, Role: "user", Text: "a"}, {Kind: KindCommand, Text: "ls"}, {Kind: KindMessage, Role: "assistant", Text: "b"},
	}}
	for name, got := range map[string][]Item{
		"empty":        nil,
		"no command":   {{Kind: KindMessage, Role: "user", Text: "a"}, {Kind: KindMessage, Role: "assistant", Text: "b"}},
		"changed word": {{Kind: KindMessage, Role: "user", Text: "A"}, {Kind: KindCommand, Text: "ls"}, {Kind: KindMessage, Role: "assistant", Text: "b"}},
	} {
		if CheckReadBack(sent, Conversation{Items: got}) == nil {
			t.Errorf("%s: passed", name)
		}
	}
	if err := CheckReadBack(sent, sent); err != nil {
		t.Fatal(err)
	}
}

// Undo removes what a switch wrote and nothing else: a file the agent has
// written to since holds its own work.
func TestSwitchRecordUndo(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.jsonl"), filepath.Join(dir, "b.jsonl")
	write(t, a, "one\n")
	write(t, b, "two\n")
	other := filepath.Join(dir, "other.jsonl")
	write(t, other, "not ours\n")
	rec := Record("20261124T1", "p3", Conversation{Session: Session{Agent: "codex", ID: "c1"}}, Written{Agent: "claude", ID: "s", Path: a, Files: []string{a, b}})
	recs := filepath.Join(t.TempDir(), "switches")
	if err := rec.Save(recs); err != nil {
		t.Fatal(err)
	}
	write(t, b, "two\nand the agent's own\n")
	loaded, err := LoadSwitchRecord(recs, "20261124T1")
	if err != nil || loaded.From != "codex" || loaded.To != "claude" || len(loaded.Files) != 2 {
		t.Fatalf("%v %+v", err, loaded)
	}
	removed, kept, err := loaded.Undo()
	if err != nil || len(removed) != 1 || removed[0] != a || len(kept) != 1 || kept[0] != b {
		t.Fatalf("%v removed %v kept %v", err, removed, kept)
	}
	for _, f := range []string{b, other} {
		if _, err := os.Stat(f); err != nil {
			t.Fatalf("%s went: %v", f, err)
		}
	}
	// Again: nothing left of its own to remove.
	if removed, _, err := loaded.Undo(); err != nil || len(removed) != 0 {
		t.Fatalf("%v %v", removed, err)
	}
	// A record that is not there, or not JSON.
	if _, err := LoadSwitchRecord(recs, "nope"); !os.IsNotExist(err) {
		t.Fatalf("%v", err)
	}
	write(t, filepath.Join(recs, "bad.json"), "{")
	if _, err := LoadSwitchRecord(recs, "bad"); err == nil {
		t.Fatal("a broken record loaded")
	}
	// A stamp cannot climb out of the folder.
	if safeStamp("../../x") == "../../x" {
		t.Fatal("unsafe stamp")
	}
}

// Rows are what ClaudeWriter takes: a command's call and its result side
// by side, so no other row answers the call first.
func TestClaudeRowsKeepCallsWithResults(t *testing.T) {
	src := readConv(t, "codex", codexNew)
	rows := claudeRows(src)
	for i, r := range rows {
		var m sdkMessage
		if err := json.Unmarshal([]byte(r.Content), &m); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(r.Content, `"tool_use"`) || !strings.Contains(r.Content, `"Bash"`) {
			continue
		}
		if i+1 >= len(rows) || !strings.Contains(rows[i+1].Content, `"tool_result"`) {
			t.Fatalf("call %d not followed by its result", i)
		}
	}
}
