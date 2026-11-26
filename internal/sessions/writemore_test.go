package sessions

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// nativeTargets are the agents conch writes sessions for, each with a
// writer into home.
var nativeTargets = []string{"claude", "codex", "gemini", "qwen"}

// TestSwitchRoundTrips writes every agent's recorded conversation as each
// other agent conch writes for, and reads it back: every request word for
// word, every command with its output as the target's own shell tool,
// everything else a note in the target's words — never an edit of its
// own — and a last line saying where it came from.
func TestSwitchRoundTrips(t *testing.T) {
	sources := map[string]string{"claude": claudeFull, "codex": codexNew, "gemini": geminiFull, "qwen": qwenFull, "copilot": copilotFull}
	for from, fixture := range sources {
		for _, to := range nativeTargets {
			if to == from {
				continue
			}
			t.Run(from+"→"+to, func(t *testing.T) {
				src := readConv(t, from, fixture)
				home, dir := t.TempDir(), t.TempDir()
				w := WriterFor(to, Env{Home: home})
				wr, err := w.WriteConversation(context.Background(), src, dir)
				if err != nil {
					t.Fatal(err)
				}
				if wr.Agent != to || wr.ID == "" || !strings.HasPrefix(wr.Path, home) {
					t.Fatalf("written: %+v", wr)
				}
				got, err := w.ReadBack(context.Background(), wr)
				if err != nil {
					t.Fatal(err)
				}
				if err := CheckReadBack(src, got); err != nil {
					t.Fatal(err)
				}
				if strings.Join(requests(got), "|") != strings.Join(requests(src), "|") {
					t.Fatalf("requests:\n%q\n%q", requests(got), requests(src))
				}
				if a, b := strings.Join(finished(got), "|"), strings.Join(finished(src), "|"); a != b {
					t.Fatalf("commands:\n%q\n%q", a, b)
				}
				for i, it := range commandItems(got) {
					want := commandItems(src)[i]
					if it.Failed != want.Failed && want.Done {
						t.Fatalf("%s: failed %v, was %v", it.Text, it.Failed, want.Failed)
					}
				}
				var text []string
				for _, it := range got.Items {
					text = append(text, it.Text)
					if it.Kind == KindEdit || it.Kind == KindRead {
						t.Fatalf("a step %s did not take became one of its own: %+v", AgentName(to), it)
					}
				}
				all := strings.Join(text, "\n")
				if !strings.Contains(all, "conch switched this conversation from "+AgentName(from)+" to "+AgentName(to)) {
					t.Fatalf("no line saying where it came from:\n%s", all)
				}
				for _, it := range src.Items {
					if it.Kind == KindEdit && !strings.Contains(all, it.Text) {
						t.Fatalf("the edit of %s is not in a note:\n%s", it.Text, all)
					}
				}
				body, _ := os.ReadFile(wr.Path)
				for _, never := range []string{"secret plan", "gAAAA", "taken back"} {
					if bytes.Contains(body, []byte(never)) {
						t.Fatalf("the session holds %q", never)
					}
				}
			})
		}
	}
}

func commandItems(c Conversation) []Item {
	var out []Item
	for _, it := range c.Items {
		if it.Kind == KindCommand {
			out = append(out, it)
		}
	}
	return out
}

// geminiRecordsOf is a written Gemini chat's lines.
func jsonLines(t *testing.T, path string) []map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	for _, l := range bytes.Split(bytes.TrimSpace(b), []byte("\n")) {
		var m map[string]any
		if err := json.Unmarshal(l, &m); err != nil {
			t.Fatalf("a line that is not JSON: %s", l)
		}
		out = append(out, m)
	}
	return out
}

// A Gemini CLI session is found as Gemini CLI 0.66 finds one for
// --resume ID: a session-*-<first 8 of the id>.jsonl in the project's
// chats, whose header names the id and the hash of the project's folder;
// every tool call has its result, so Gemini's history has a response for
// each call.
func TestGeminiWriterFile(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(t.TempDir(), "My Projéct")
	os.MkdirAll(dir, 0o755)
	src := readConv(t, "claude", claudeFull)
	w := GeminiWriter{Env: Env{Home: home}}
	wr, err := w.WriteConversation(context.Background(), src, dir)
	if err != nil {
		t.Fatal(err)
	}
	chats := filepath.Join(home, ".gemini", "tmp", "my-proj-ct", "chats")
	if filepath.Dir(wr.Path) != chats || !strings.HasPrefix(filepath.Base(wr.Path), "session-") || !strings.HasSuffix(wr.Path, "-"+wr.ID[:8]+".jsonl") {
		t.Fatalf("path %s", wr.Path)
	}
	marker := filepath.Join(home, ".gemini", "tmp", "my-proj-ct", ".project_root")
	if b, _ := os.ReadFile(marker); string(b) != dir {
		t.Fatalf("marker: %q", b)
	}
	if len(wr.Files) != 2 || wr.Files[1] != marker {
		t.Fatalf("files: %v", wr.Files)
	}
	recs := jsonLines(t, wr.Path)
	sum := sha256.Sum256([]byte(dir))
	if recs[0]["sessionId"] != wr.ID || recs[0]["projectHash"] != hex.EncodeToString(sum[:]) || recs[0]["messages"] != nil {
		t.Fatalf("header: %v", recs[0])
	}
	if recs[1]["type"] != "user" {
		t.Fatalf("does not open with the person: %v", recs[1])
	}
	// Gemini's turns: two of its messages in a row only where the first
	// made calls, whose responses come between them.
	for i := 2; i < len(recs); i++ {
		if recs[i]["type"] == "gemini" && recs[i-1]["type"] == "gemini" && recs[i-1]["toolCalls"] == nil {
			t.Fatalf("two of Gemini's messages in a row:\n%v\n%v", recs[i-1], recs[i])
		}
	}
	ids := map[any]bool{}
	for _, r := range recs[1:] {
		if ids[r["id"]] || r["id"] == "" {
			t.Fatalf("an id twice or none: %v", r)
		}
		ids[r["id"]] = true
		calls, _ := r["toolCalls"].([]any)
		for _, c := range calls {
			m := c.(map[string]any)
			if m["name"] != "run_shell_command" || m["result"] == nil {
				t.Fatalf("a call: %v", m)
			}
		}
	}
	// conch lists it: Gemini CLI has not registered the project yet, and
	// the marker says whose folder it is.
	found := false
	for _, s := range List(Env{Home: home}, []string{dir}, 0) {
		if s.Agent == "gemini" && s.ID == wr.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("conch does not list the session it wrote")
	}
	// A second session in the same project uses the same folder and
	// writes no marker.
	wr2, err := w.WriteConversation(context.Background(), src, dir)
	if err != nil || filepath.Dir(wr2.Path) != chats || len(wr2.Files) != 1 {
		t.Fatalf("%v %+v", err, wr2)
	}
}

// The project's folder is Gemini CLI's: from its registry, else the one
// whose marker names the folder, else a new one beside another project's
// of the same name.
func TestGeminiWriterProjectFolder(t *testing.T) {
	src := readConv(t, "codex", codexNew)
	dir := filepath.Join(t.TempDir(), "app")
	os.MkdirAll(dir, 0o755)

	t.Run("registry", func(t *testing.T) {
		home := t.TempDir()
		write(t, filepath.Join(home, ".gemini", "projects.json"), fmt.Sprintf(`{"projects":{%q:"app-7"}}`, dir))
		wr, err := GeminiWriter{Env: Env{Home: home}}.WriteConversation(context.Background(), src, dir)
		if err != nil || !strings.Contains(wr.Path, filepath.Join("tmp", "app-7", "chats")) || len(wr.Files) != 1 {
			t.Fatalf("%v %+v", err, wr)
		}
	})
	t.Run("GEMINI_CLI_HOME", func(t *testing.T) {
		home, other := t.TempDir(), t.TempDir()
		env := Env{Home: home, Getenv: func(k string) string {
			if k == "GEMINI_CLI_HOME" {
				return other
			}
			return ""
		}}
		wr, err := GeminiWriter{Env: env}.WriteConversation(context.Background(), src, dir)
		if err != nil || !strings.HasPrefix(wr.Path, filepath.Join(other, ".gemini", "tmp", "app")) {
			t.Fatalf("%v %+v", err, wr)
		}
	})
	t.Run("marker", func(t *testing.T) {
		home := t.TempDir()
		write(t, filepath.Join(home, ".gemini", "tmp", "elsewhere", ".project_root"), dir)
		wr, err := GeminiWriter{Env: Env{Home: home}}.WriteConversation(context.Background(), src, dir)
		if err != nil || !strings.Contains(wr.Path, filepath.Join("tmp", "elsewhere", "chats")) || len(wr.Files) != 1 {
			t.Fatalf("%v %+v", err, wr)
		}
	})
	t.Run("taken", func(t *testing.T) {
		home := t.TempDir()
		write(t, filepath.Join(home, ".gemini", "tmp", "app", ".project_root"), "/another/app")
		write(t, filepath.Join(home, ".gemini", "projects.json"), `{"projects":{"/third/app":"app-1"}}`)
		wr, err := GeminiWriter{Env: Env{Home: home}}.WriteConversation(context.Background(), src, dir)
		if err != nil || !strings.Contains(wr.Path, filepath.Join("tmp", "app-2", "chats")) {
			t.Fatalf("%v %+v", err, wr)
		}
		if b, _ := os.ReadFile(filepath.Join(home, ".gemini", "tmp", "app", ".project_root")); string(b) != "/another/app" {
			t.Fatal("another project's marker changed")
		}
	})
	t.Run("registry entry owned by another", func(t *testing.T) {
		home := t.TempDir()
		write(t, filepath.Join(home, ".gemini", "projects.json"), fmt.Sprintf(`{"projects":{%q:"app"}}`, dir))
		write(t, filepath.Join(home, ".gemini", "tmp", "app", ".project_root"), "/another/app")
		wr, err := GeminiWriter{Env: Env{Home: home}}.WriteConversation(context.Background(), src, dir)
		if err != nil || !strings.Contains(wr.Path, filepath.Join("tmp", "app-1", "chats")) {
			t.Fatalf("%v %+v", err, wr)
		}
	})
}

func TestGeminiSlug(t *testing.T) {
	for in, want := range map[string]string{"app": "app", "My Projéct": "my-proj-ct", "--x__y--": "x-y", "": "project", "✓": "project", "A1": "a1"} {
		if got := geminiSlug(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

// A Qwen Code session is one chain: each record names the one before,
// the first none, in the folder Qwen keeps for the project, its cwd the
// project's folder; every call is answered by a tool_result.
func TestQwenWriterFile(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(t.TempDir(), "my app ✓")
	os.MkdirAll(dir, 0o755)
	src := readConv(t, "claude", claudeFull)
	wr, err := QwenWriter{Env: Env{Home: home}}.WriteConversation(context.Background(), src, dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".qwen", "projects", qwenDirName(dir), "chats", wr.ID+".jsonl"); wr.Path != want || len(wr.Files) != 1 {
		t.Fatalf("written %+v, want %s", wr, want)
	}
	recs := jsonLines(t, wr.Path)
	var parent any
	answered := map[string]bool{}
	var calls []string
	for i, r := range recs {
		if r["parentUuid"] != parent || r["sessionId"] != wr.ID || r["cwd"] != dir || r["version"] != qwenWriteVersion {
			t.Fatalf("record %d: %v", i, r)
		}
		parent = r["uuid"]
		msg := r["message"].(map[string]any)
		for _, p := range msg["parts"].([]any) {
			pm := p.(map[string]any)
			if fc, ok := pm["functionCall"].(map[string]any); ok {
				calls = append(calls, fc["id"].(string))
			}
			if fr, ok := pm["functionResponse"].(map[string]any); ok {
				answered[fr["id"].(string)] = true
			}
		}
	}
	for i := 1; i < len(recs); i++ {
		if recs[i]["type"] == "assistant" && recs[i-1]["type"] == "assistant" {
			t.Fatalf("two assistant records in a row:\n%v\n%v", recs[i-1], recs[i])
		}
	}
	if recs[0]["type"] != "user" || len(calls) == 0 {
		t.Fatalf("first %v, %d calls", recs[0], len(calls))
	}
	for _, c := range calls {
		if !answered[c] {
			t.Fatalf("call %s unanswered", c)
		}
	}
	found := false
	for _, s := range List(Env{Home: home}, []string{dir}, 0) {
		found = found || s.Agent == "qwen" && s.ID == wr.ID
	}
	if !found {
		t.Fatal("conch does not list the session it wrote")
	}
	// QWEN_HOME moves it.
	other := t.TempDir()
	wr, err = QwenWriter{Env: Env{Home: home, Getenv: func(k string) string {
		return map[string]string{"QWEN_HOME": other}[k]
	}}}.WriteConversation(context.Background(), src, dir)
	if err != nil || !strings.HasPrefix(wr.Path, filepath.Join(other, "projects")) {
		t.Fatalf("%v %+v", err, wr)
	}
}

// A command that never finished is answered as cancelled, in the target's
// own words, so its history has a response for every call.
func TestMoreWritersCommandWithoutResult(t *testing.T) {
	c := Conversation{Session: Session{Agent: "codex", ID: "c9"}, Items: []Item{
		{Kind: KindMessage, Role: "user", Text: "run it"},
		{Kind: KindCommand, Role: "assistant", Text: "sleep 100"},
	}}
	for _, to := range []string{"gemini", "qwen"} {
		w := WriterFor(to, Env{Home: t.TempDir()})
		wr, err := w.WriteConversation(context.Background(), c, t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		got, _ := w.ReadBack(context.Background(), wr)
		cmds := commandItems(got)
		if len(cmds) != 1 || !cmds[0].Done || cmds[0].Output != shellCancelled {
			t.Fatalf("%s: %+v", to, cmds)
		}
	}
}

// What every writer refuses, and what it leaves behind then: nothing.
func TestMoreWritersEdges(t *testing.T) {
	for _, to := range []string{"gemini", "qwen"} {
		t.Run(to, func(t *testing.T) {
			home := t.TempDir()
			w := WriterFor(to, Env{Home: home})
			ctx := context.Background()
			one := Conversation{Session: Session{Agent: "claude"}, Items: []Item{{Kind: KindMessage, Role: "user", Text: "just this"}}}
			if _, err := w.WriteConversation(ctx, Conversation{}, t.TempDir()); err == nil {
				t.Fatal("an empty conversation")
			}
			if _, err := w.WriteConversation(ctx, one, ""); err == nil {
				t.Fatal("no folder")
			}
			if _, err := w.WriteConversation(ctx, one, filepath.Join(t.TempDir(), "gone")); err == nil {
				t.Fatal("a missing folder")
			}
			cancelled, cancel := context.WithCancel(ctx)
			cancel()
			if _, err := w.WriteConversation(cancelled, one, t.TempDir()); err == nil {
				t.Fatal("a cancelled context")
			}
			if left := filesUnder(t, home); len(left) != 0 {
				t.Fatalf("left behind: %v", left)
			}
			// One message: the person's, then where it came from.
			wr, err := w.WriteConversation(ctx, one, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			got, _ := w.ReadBack(ctx, wr)
			if err := CheckReadBack(one, got); err != nil {
				t.Fatal(err)
			}
			// It opens with the agent: a note opens it instead.
			late := Conversation{Session: Session{Agent: "codex"}, Items: []Item{{Kind: KindMessage, Role: "assistant", Text: "half way"},
				{Kind: KindMessage, Role: "user", Text: "go on"}}}
			wr, err = w.WriteConversation(ctx, late, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			got, _ = w.ReadBack(ctx, wr)
			if r := requests(got); len(r) != 2 || !strings.Contains(r[0], "its beginning was not kept") || r[1] != "go on" {
				t.Fatalf("%q", r)
			}
		})
	}
	// A home that cannot be written in: an error, which the switch turns
	// into the handoff.
	ro := t.TempDir()
	os.Chmod(ro, 0o500)
	t.Cleanup(func() { os.Chmod(ro, 0o700) })
	one := Conversation{Session: Session{Agent: "claude"}, Items: []Item{{Kind: KindMessage, Role: "user", Text: "x"}}}
	for _, to := range []string{"gemini", "qwen"} {
		if _, _, err := SwitchWrite(context.Background(), one, one, to, Env{Home: ro}, t.TempDir()); err == nil && os.Geteuid() != 0 {
			t.Fatalf("%s wrote into a read-only home", to)
		}
	}
}

func filesUnder(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			out = append(out, p)
		}
		return nil
	})
	return out
}

// A switch to Gemini CLI writes through a trial in a scratch home first,
// then for real; undo removes what it wrote — the session and the marker
// it claimed the folder with — and nothing of Gemini's own.
func TestSwitchToGeminiUndo(t *testing.T) {
	home, dir := t.TempDir(), t.TempDir()
	write(t, filepath.Join(home, ".gemini", "settings.json"), "{}")
	src := readConv(t, "qwen", qwenFull)
	fit, _ := Measure(src, SwitchBudget)
	wr, via, err := SwitchWrite(context.Background(), src, fit, "gemini", Env{Home: home}, dir)
	if err != nil || via != ViaWriter || len(wr.Files) != 2 {
		t.Fatalf("%v %s %+v", err, via, wr)
	}
	rec := Record("s1", "p1", src, wr)
	// Gemini resumed it and wrote to it: the session holds its work now
	// and stays; the marker is as conch wrote it and goes.
	f, _ := os.OpenFile(wr.Path, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(`{"$set":{"lastUpdated":"2026-11-21T00:00:00.000Z"}}` + "\n")
	f.Close()
	removed, kept, err := rec.Undo()
	if err != nil || len(removed) != 1 || removed[0] != wr.Files[1] || len(kept) != 1 || kept[0] != wr.Path {
		t.Fatalf("%v removed %v kept %v", err, removed, kept)
	}
	if _, err := os.Stat(filepath.Join(home, ".gemini", "settings.json")); err != nil {
		t.Fatal("undo removed Gemini's own file")
	}
}

// A long conversation, fitted to the budget, crosses whole at both ends.
func TestMoreWritersLargeConversation(t *testing.T) {
	c := Conversation{Session: Session{Agent: "copilot", ID: "big"}}
	c.Items = append(c.Items, Item{Kind: KindMessage, Role: "user", Text: "the first request"})
	for i := 0; i < 3000; i++ {
		c.Items = append(c.Items,
			Item{Kind: KindCommand, Role: "assistant", Text: fmt.Sprintf("step %d", i), Output: strings.Repeat("o", 400), Done: true},
			Item{Kind: KindMessage, Role: "assistant", Text: fmt.Sprintf("did %d", i)})
	}
	c.Items = append(c.Items, Item{Kind: KindMessage, Role: "user", Text: "the last request"})
	fit, cr := Measure(c, SwitchBudget)
	if cr.Fits || cr.Shortened == 0 {
		t.Fatalf("%+v", cr)
	}
	for _, to := range []string{"gemini", "qwen"} {
		w := WriterFor(to, Env{Home: t.TempDir()})
		wr, err := TryWrite(context.Background(), fit, w, func(tmp string) ConversationWriter { return WriterFor(to, Env{Home: tmp}) }, t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		got, _ := w.ReadBack(context.Background(), wr)
		r := requests(got)
		if len(r) != 2 || r[0] != "the first request" || r[1] != "the last request" {
			t.Fatalf("%s: %q", to, r)
		}
		if st, _ := os.Stat(wr.Path); st.Size() > 2*SwitchBudget {
			t.Fatalf("%s: %d bytes", to, st.Size())
		}
	}
}
