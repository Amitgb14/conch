package sessions

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// codexItems is a rollout as Codex 0.158–0.171 writes it: no user_message
// or agent_message events, the conversation in item_completed events (and
// again, with the instructions conch and Codex add, in response_items).
// The shapes are a real 0.171 rollout's, the words made up.
var codexItems = strings.Join([]string{
	`{"timestamp":"2026-11-22T18:51:47.866Z","type":"session_meta","payload":{"id":"01a2","cwd":"/w","originator":"codex-tui","cli_version":"0.171.0","git":{"branch":"feat"}}}`,
	`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"# AGENTS.md instructions for /w"},{"type":"input_text","text":"<environment_context>\n  <cwd>/w</cwd>"}]}}`,
	`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Plan the   release"}]}}`,
	`{"timestamp":"2026-11-22T18:51:50.340Z","type":"event_msg","payload":{"type":"item_completed","thread_id":"01a2","turn_id":"t1","item":{"type":"UserMessage","id":"u1","client_id":"c1","content":[{"type":"text","text":"Plan the   release","text_elements":[]}]}}}`,
	`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"Reasoning","id":"r1","summary_text":["thinking"],"raw_content":[]}}}`,
	`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"AgentMessage","id":"a1","phase":"commentary","content":[{"type":"Text","text":"Reading the changelog."}]}}}`,
	`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"Extension","id":"x1","kind":"web_search","query":"q","results":[]}}}`,
	`{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"duplicate"}]}}`,
	`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"output_tokens":42}}}}`,
	`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"AgentMessage","id":"a2","phase":"final_answer","content":[{"type":"Text","text":"Three steps."}]}}}`,
	`{"timestamp":"2026-11-22T18:54:14.322Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","id":"u2","content":[{"type":"local_image","path":"/tmp/a.png"},{"type":"text","text":"and the docs"}]}}}`,
	`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","id":"u3","content":[{"type":"text","text":"<environment_context>changed</environment_context>"}]}}}`,
	`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","id":"u4","content":[]}}}`,
	`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"AgentMessage","id":"a3","content":[{"type":"Text","text":"Docs too."}]}}}`,
}, "\n") + "\n"

func TestCodexItemsAreListed(t *testing.T) {
	a6NoTools(t)
	home := t.TempDir()
	write(t, filepath.Join(home, ".codex", "sessions", "2026", "11", "22", "rollout-2026-11-22T10-51-47-01a2.jsonl"), codexItems)
	got := List(a6Env(home, nil), []string{"/w"}, 0)
	if len(got) != 1 {
		t.Fatalf("a rollout of item_completed events is not listed: %+v", got)
	}
	s := got[0]
	if s.Agent != "codex" || s.ID != "01a2" || s.Title != "Plan the release" || s.Branch != "feat" || s.Output != 42 {
		t.Fatalf("%+v", s)
	}
}

func TestCodexItemsTranscript(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	write(t, path, codexItems)
	turns, err := Transcript(context.Background(), Env{}, Session{Agent: "codex", Path: path})
	want := "user: Plan the   release\nassistant: Reading the changelog.\n\nThree steps.\nuser: and the docs\nassistant: Docs too."
	if err != nil || turnsText(turns) != want {
		t.Fatalf("got:\n%s\nwant:\n%s\n%v", turnsText(turns), want, err)
	}
	if turns[0].Time.IsZero() {
		t.Error("a turn's time is the event's")
	}
}

// A rollout an older Codex wrote with both kinds keeps the events and is
// not read twice.
func TestCodexOldEventsWinOverItems(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	write(t, path, strings.Join([]string{
		`{"type":"session_meta","payload":{"id":"o","cwd":"/w"}}`,
		`{"type":"event_msg","payload":{"type":"user_message","message":"old prompt"}}`,
		`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","content":[{"type":"text","text":"old prompt"}]}}}`,
		`{"type":"event_msg","payload":{"type":"agent_message","message":"old answer"}}`,
		`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"AgentMessage","content":[{"type":"Text","text":"old answer"}]}}}`,
	}, "\n"))
	turns, err := Transcript(context.Background(), Env{}, Session{Agent: "codex", Path: path})
	if err != nil || turnsText(turns) != "user: old prompt\nassistant: old answer" {
		t.Fatalf("%q %v", turnsText(turns), err)
	}
	s := parseCodex(path, statOf(t, path))
	if s == nil || s.Title != "old prompt" {
		t.Fatalf("%+v", s)
	}
}

// Instructions alone are not a prompt: such a rollout is still skipped.
func TestCodexItemsWithoutAPromptAreSkipped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	write(t, path, strings.Join([]string{
		`{"type":"session_meta","payload":{"id":"n","cwd":"/w"}}`,
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context/>"}]}}`,
		`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","content":[{"type":"text","text":"  "}]}}}`,
		`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","content":"not a list"}}}`,
		`{"type":"event_msg","payload":{"type":"item_completed","item":null}}`,
	}, "\n"))
	if s := parseCodex(path, statOf(t, path)); s != nil {
		t.Fatalf("%+v", s)
	}
}

func statOf(t *testing.T, path string) os.FileInfo {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return st
}
