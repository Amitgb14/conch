package sessions

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Claude Code's conversation written as a Codex rollout and read back:
// every request word for word, every command as Codex's exec_command with
// its output and exit code, everything else a note.
func TestClaudeToCodexRoundTrip(t *testing.T) {
	src := readConv(t, "claude", claudeFull)
	src.Session.Branch = "arm"
	home, dir := t.TempDir(), t.TempDir()
	wr, err := CodexWriter{Env: Env{Home: home}}.WriteConversation(context.Background(), src, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(wr.Path, filepath.Join(home, ".codex", "sessions")) || codexRolloutID(filepath.Base(wr.Path)) != wr.ID {
		t.Fatalf("written: %+v", wr)
	}
	// Found as `codex resume ID` would find it, and listed as Codex's.
	if CodexRollout(Env{Home: home}, wr.ID) != wr.Path {
		t.Fatal("not found by its id")
	}
	if s := parseCodex(wr.Path, mustStat(t, wr.Path)); s == nil || s.ID != wr.ID || s.Dir != dir || s.Branch != "arm" {
		t.Fatalf("listed as %+v", s)
	}
	got := readSession(t, "codex", wr.Path)
	if err := CheckReadBack(src, got); err != nil {
		t.Fatal(err)
	}
	if strings.Join(requests(got), "|") != strings.Join(requests(src), "|") {
		t.Fatalf("requests:\n%q\n%q", requests(got), requests(src))
	}
	if strings.Join(finished(got), "|") != strings.Join(finished(src), "|") {
		t.Fatalf("commands:\n%q\n%q", finished(got), finished(src))
	}
	for _, it := range got.Items {
		if it.Kind == KindCommand && (!it.Failed || it.Exit != 1) {
			t.Fatalf("the failed command: %+v", it)
		}
	}
	body, _ := os.ReadFile(wr.Path)
	for _, never := range []string{"secret plan", `"apply_patch"`, "a subagent's words"} {
		if strings.Contains(string(body), never) {
			t.Fatalf("the rollout holds %q", never)
		}
	}
	var text []string
	for _, it := range got.Items {
		text = append(text, it.Text)
	}
	all := strings.Join(text, "\n")
	for _, note := range []string{"[Claude Code read /w/build/arm.go]", "[Claude Code edited /w/build/arm.go (+2 −1)]", "[Claude Code created /w/docs/ünïcode ✓.md (2 lines)]",
		"[Claude Code used mcp__linear__get_issue", "An image was attached", "The plan as it stands:", "- [~] Run the arm tests",
		"conch switched this conversation from Claude Code to Codex"} {
		if !strings.Contains(all, note) {
			t.Fatalf("missing %q in:\n%s", note, all)
		}
	}
	// Every line is a rollout line: a timestamp, a type, a payload.
	for _, l := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		var cl struct {
			Timestamp string          `json:"timestamp"`
			Type      string          `json:"type"`
			Payload   json.RawMessage `json:"payload"`
		}
		if json.Unmarshal([]byte(l), &cl) != nil || cl.Timestamp == "" || cl.Type == "" || len(cl.Payload) == 0 {
			t.Fatalf("not a rollout line: %s", l)
		}
	}
}

func mustStat(t *testing.T, p string) os.FileInfo {
	t.Helper()
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestCodexWriterEdges(t *testing.T) {
	home := t.TempDir()
	w := CodexWriter{Env: Env{Home: home, Getenv: func(k string) string {
		if k == "CODEX_HOME" {
			return filepath.Join(home, "elsewhere")
		}
		return ""
	}}}
	one := Conversation{Session: Session{Agent: "claude", ID: "x"}, Items: []Item{{Kind: KindMessage, Role: "user", Text: "only this"}}}
	if _, err := w.WriteConversation(context.Background(), Conversation{}, t.TempDir()); err == nil {
		t.Fatal("an empty conversation was written")
	}
	if _, err := w.WriteConversation(context.Background(), one, filepath.Join(t.TempDir(), "gone")); err == nil {
		t.Fatal("written for a missing folder")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := w.WriteConversation(ctx, one, t.TempDir()); err == nil {
		t.Fatal("written with a cancelled context")
	}
	wr, err := w.WriteConversation(context.Background(), one, t.TempDir())
	if err != nil || !strings.HasPrefix(wr.Path, filepath.Join(home, "elsewhere", "sessions")) {
		t.Fatalf("CODEX_HOME not followed: %v %+v", err, wr)
	}
	if r := requests(readSession(t, "codex", wr.Path)); len(r) != 1 || r[0] != "only this" {
		t.Fatalf("%q", r)
	}
	// A command with no result is said to have been cut off.
	c := Conversation{Session: Session{Agent: "claude"}, Items: []Item{{Kind: KindMessage, Role: "user", Text: "go"}, {Kind: KindCommand, Text: "sleep 99"}}}
	wr, err = w.WriteConversation(context.Background(), c, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	got := readSession(t, "codex", wr.Path)
	if cmds := commands(got); len(cmds) != 1 || !strings.Contains(cmds[0], "did not finish") {
		t.Fatalf("%q", cmds)
	}
}

// fakeCodex puts a codex on a scratch PATH that answers app-server as
// Codex does — or, with old set, as a Codex without the import — and on
// the import writes the rollout and the ledger row it was handed through
// the environment, logging what it was sent. Its PATH is the scratch
// folder alone, so it names the tools it uses in full.
func fakeCodex(t *testing.T, old bool) (bin, log string) {
	t.Helper()
	dir := t.TempDir()
	log = filepath.Join(dir, "sent.log")
	importReply := `{"id":2,"result":{"importId":"i1"}}`
	done := `/bin/mkdir -p "${FAKE_ROLLOUT%/*}"; /bin/cp "$FAKE_ROLLOUT_SRC" "$FAKE_ROLLOUT"; printf '%s' "$FAKE_LEDGER" > "$HOME/.codex/external_agent_session_imports.json"
      echo '{"method":"externalAgentConfig/import/progress","params":{"importId":"i1"}}'
      echo "{\"method\":\"externalAgentConfig/import/completed\",\"params\":{\"importId\":\"i1\",\"itemTypeResults\":[{\"itemType\":\"SESSIONS\",\"successes\":[{\"target\":\"$FAKE_TARGET\"}],\"failures\":[]}]}}"`
	if old {
		importReply = `{"id":2,"error":{"code":-32601,"message":"method not found"}}`
		done = ""
	}
	script := `#!/bin/sh
[ "$1" = "app-server" ] || exit 2
while IFS= read -r line; do
  printf '%s\n' "$line" >> "` + log + `"
  case "$line" in
    *'"method":"initialize"'*) echo '{"method":"some/notification","params":{}}'; echo '{"id":1,"result":{"userAgent":"fake"}}' ;;
    *'externalAgentConfig/import'*) echo '` + importReply + `'
      ` + done + ` ;;
  esac
done
`
	bin = filepath.Join(dir, "bin")
	os.MkdirAll(bin, 0o755)
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, log
}

// importFixture is a Claude Code session where Codex imports from, and
// the environment a fake codex finds it in.
func importFixture(t *testing.T, bin string) (Env, string) {
	t.Helper()
	home := t.TempDir()
	src := filepath.Join(home, ".claude", "projects", "-w", "c1.jsonl")
	write(t, src, claudeFull)
	codexHome := filepath.Join(home, ".codex")
	os.MkdirAll(codexHome, 0o700)
	env := Env{Home: home, Getenv: func(k string) string {
		if k == "PATH" {
			return bin
		}
		return ""
	}}
	return env, src
}

// Codex imports a Claude Code session itself, driven as /codex:transfer
// drives it; the thread is the one its ledger names for that file.
func TestCodexImporter(t *testing.T) {
	bin, log := fakeCodex(t, false)
	env, src := importFixture(t, bin)
	real, _ := filepath.EvalSymlinks(src)
	// What Codex's importer writes: messages, the tools flattened.
	rolloutSrc := filepath.Join(t.TempDir(), "imported.jsonl")
	write(t, rolloutSrc, strings.Join([]string{
		`{"timestamp":"2026-11-20T10:00:01Z","type":"session_meta","payload":{"id":"t-1","cwd":"/w"}}`,
		`{"timestamp":"2026-11-20T10:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"Fix the failing build on arm64"}}`,
		`{"timestamp":"2026-11-20T10:00:02Z","type":"event_msg","payload":{"type":"agent_message","message":"Running the tests first.\n<external_agent_tool_call>Bash go test</external_agent_tool_call>"}}`,
		`{"timestamp":"2026-11-20T10:01:00Z","type":"event_msg","payload":{"type":"user_message","message":"Here is the CI screen"}}`,
		`{"timestamp":"2026-11-20T10:01:01Z","type":"event_msg","payload":{"type":"agent_message","message":"Imported from Claude Code"}}`,
	}, "\n")+"\n")
	rollout := filepath.Join(env.Home, ".codex", "sessions", "2026", "11", "24", "rollout-2026-11-24T10-00-00-t-1.jsonl")
	ledger, _ := json.Marshal(map[string]any{"records": []map[string]any{
		{"source_path": real, "content_sha256": "stale", "imported_thread_id": "t-0"},
		{"source_path": real, "content_sha256": fileHash(src), "imported_thread_id": "t-1", "imported_at": 1},
	}})
	t.Setenv("FAKE_ROLLOUT_SRC", rolloutSrc)
	t.Setenv("FAKE_ROLLOUT", rollout)
	t.Setenv("FAKE_LEDGER", string(ledger))
	t.Setenv("FAKE_TARGET", "")

	wr, err := CodexImporter{Env: env, Wait: 20 * time.Second}.Import(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	if wr.ID != "t-1" || wr.Path != rollout || len(wr.Files) != 1 {
		t.Fatalf("%+v", wr)
	}
	sent, _ := os.ReadFile(log)
	lines := strings.Split(strings.TrimSpace(string(sent)), "\n")
	if len(lines) != 3 || !strings.Contains(lines[0], `"method":"initialize"`) || !strings.Contains(lines[1], `"method":"initialized"`) {
		t.Fatalf("sent:\n%s", sent)
	}
	if strings.Contains(string(sent), "jsonrpc") {
		t.Fatal("Codex's app-server takes no jsonrpc field")
	}
	var req struct {
		Method string `json:"method"`
		Params struct {
			MigrationItems []struct {
				ItemType string `json:"itemType"`
				Details  struct {
					Sessions []struct {
						Path string `json:"path"`
					} `json:"sessions"`
				} `json:"details"`
			} `json:"migrationItems"`
		} `json:"params"`
	}
	if err := json.Unmarshal([]byte(lines[2]), &req); err != nil || req.Method != "externalAgentConfig/import" ||
		req.Params.MigrationItems[0].ItemType != "SESSIONS" || req.Params.MigrationItems[0].Details.Sessions[0].Path != real {
		t.Fatalf("%v %s", err, lines[2])
	}

	// Through SwitchWrite: the import reads back with every request, and
	// is what was written.
	conv := readConv(t, "claude", claudeFull)
	conv.Session.Path = src
	os.Remove(rollout)
	wr, via, err := SwitchWrite(context.Background(), conv, conv, "codex", env, t.TempDir())
	if err != nil || via != ViaImporter || wr.ID != "t-1" {
		t.Fatalf("%v %s %+v", err, via, wr)
	}
}

// A Codex too old to import, one not installed, and a session Codex does
// not look in all fall back to the rollout conch writes.
func TestCodexImporterFallsBack(t *testing.T) {
	conv := readConv(t, "claude", claudeFull)

	bin, _ := fakeCodex(t, true)
	env, src := importFixture(t, bin)
	if _, err := (CodexImporter{Env: env, Wait: 20 * time.Second}).Import(context.Background(), src); !errors.Is(err, ErrNoImporter) {
		t.Fatalf("old codex: %v", err)
	}
	conv.Session.Path = src
	wr, via, err := SwitchWrite(context.Background(), conv, conv, "codex", env, t.TempDir())
	if err != nil || via != ViaWriter || !strings.HasPrefix(wr.Path, filepath.Join(env.Home, ".codex")) {
		t.Fatalf("%v %s %+v", err, via, wr)
	}

	// No codex at all.
	none := Env{Home: env.Home, Getenv: func(string) string { return t.TempDir() }}
	if _, err := (CodexImporter{Env: none}).Import(context.Background(), src); !errors.Is(err, ErrNoImporter) {
		t.Fatalf("no codex: %v", err)
	}
	// Another account's folder: Codex reads only ~/.claude/projects.
	other := filepath.Join(t.TempDir(), "acct", "projects", "-w", "c2.jsonl")
	write(t, other, claudeFull)
	if _, err := (CodexImporter{Env: env}).Import(context.Background(), other); !errors.Is(err, ErrNoImporter) {
		t.Fatalf("outside: %v", err)
	}
	// Not a Claude conversation: conch writes it.
	codexConv := readConv(t, "codex", codexOld)
	if _, via, err := SwitchWrite(context.Background(), codexConv, codexConv, "claude", Env{Home: t.TempDir()}, t.TempDir()); err != nil || via != ViaWriter {
		t.Fatalf("%v %s", err, via)
	}
	// An agent conch writes no sessions for.
	if _, _, err := SwitchWrite(context.Background(), codexConv, codexConv, "gemini", Env{Home: t.TempDir()}, t.TempDir()); err == nil {
		t.Fatal("wrote gemini")
	}
}

// An import that never completes gives up at its wait.
func TestCodexImporterTimesOut(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "codex"), []byte("#!/bin/sh\nexec sleep 30\n"), 0o755)
	env, src := importFixture(t, dir)
	start := time.Now()
	_, err := CodexImporter{Env: env, Wait: 300 * time.Millisecond}.Import(context.Background(), src)
	if err == nil || time.Since(start) > 10*time.Second {
		t.Fatalf("%v after %v", err, time.Since(start))
	}
}
