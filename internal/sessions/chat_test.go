package sessions

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeLines(t *testing.T, path string, ls ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(ls, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestClaudeChatKeepsToolsInOrder(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c1.jsonl")
	writeLines(t, p,
		`{"type":"user","timestamp":"2026-11-11T10:00:00Z","message":{"content":"Add a workbench"}}`,
		`{"type":"assistant","timestamp":"2026-11-11T10:00:05Z","message":{"content":[{"type":"text","text":"Reading first."},{"type":"tool_use","name":"Read","input":{"file_path":"/w/FilesView.swift"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","content":"412 lines"}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"swift   test\n --filter Workbench","description":"run"}}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"Done."}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"All green."}]}}`,
		`{"type":"assistant","isSidechain":true,"message":{"content":[{"type":"text","text":"a subagent"}]}}`,
		`{"type":"user","isMeta":true,"message":{"content":"meta"}}`,
		`{"type":"user","message":{"content":"<command-name>/clear</command-name>"}}`,
		`not json`,
		`{"type":"assistant","message":{"content":"plain string answer"}}`,
	)
	got, err := Chat(context.Background(), Env{}, Session{Agent: "claude", Path: p})
	if err != nil {
		t.Fatal(err)
	}
	want := []ChatTurn{
		{Role: "user", Text: "Add a workbench"},
		{Role: "assistant", Text: "Reading first."},
		{Role: "tool", Text: "Read", Detail: "/w/FilesView.swift"},
		{Role: "tool", Text: "Bash", Detail: "swift test --filter Workbench"},
		{Role: "assistant", Text: "Done.\n\nAll green.\n\nplain string answer"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d turns: %+v", len(got), got)
	}
	for i := range want {
		g := got[i]
		if g.Role != want[i].Role || g.Text != want[i].Text || g.Detail != want[i].Detail {
			t.Fatalf("turn %d: got %+v want %+v", i, g, want[i])
		}
	}
	if got[0].Time.IsZero() {
		t.Fatal("the user's turn lost its time")
	}
}

func TestChatOtherAgentsAreTheirTranscript(t *testing.T) {
	p := filepath.Join(t.TempDir(), "rollout.jsonl")
	writeLines(t, p,
		`{"type":"event_msg","payload":{"type":"user_message","message":"tidy the README"}}`,
		`{"type":"event_msg","payload":{"type":"agent_message","message":"Tidied."}}`,
	)
	got, err := Chat(context.Background(), Env{}, Session{Agent: "codex", Path: p})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Role != "user" || got[1].Role != "assistant" || got[1].Text != "Tidied." {
		t.Fatalf("codex chat %+v", got)
	}
	if _, err := Chat(context.Background(), Env{}, Session{Agent: "devin"}); err == nil {
		t.Fatal("an agent conch can't read gave a chat")
	}
	if _, err := Chat(context.Background(), Env{}, Session{Agent: "claude", Path: filepath.Join(t.TempDir(), "gone.jsonl")}); err == nil {
		t.Fatal("a missing transcript gave a chat")
	}
}

func TestToolDetail(t *testing.T) {
	cases := map[string]string{
		`{"file_path":"/a/b.go","old_string":"x"}`: "/a/b.go",
		`{"pattern":"TODO","path":"internal"}`:     "TODO",
		`{"command":"   "}`:                        "",
		`{"other":1}`:                              "",
		`not json`:                                 "",
		`{"url":"https://example.com"}`:            "https://example.com",
	}
	for in, want := range cases {
		if got := toolDetail([]byte(in)); got != want {
			t.Errorf("%s: got %q want %q", in, got, want)
		}
	}
	long := `{"command":"` + strings.Repeat("é", 300) + `"}`
	got := toolDetail([]byte(long))
	if !strings.HasSuffix(got, "…") || len(got) > maxToolDetail+len("…") || !strings.HasPrefix(got, "é") {
		t.Fatalf("long detail %q (%d bytes)", got, len(got))
	}
}

func TestCapChat(t *testing.T) {
	var turns []ChatTurn
	for i := 0; i < maxChatTurns+10; i++ {
		turns = append(turns, ChatTurn{Role: "assistant", Text: "x"})
	}
	if got := capChat(turns); len(got) != maxChatTurns {
		t.Fatalf("kept %d", len(got))
	}
	big := []ChatTurn{{Role: "user", Text: strings.Repeat("a", maxTranscriptBytes)}, {Role: "assistant", Text: "last"}}
	if got := capChat(big); len(got) != 1 || got[0].Text != "last" {
		t.Fatalf("byte cap kept %d", len(got))
	}
	if got := capChat(nil); len(got) != 0 {
		t.Fatal("nil grew")
	}
}
