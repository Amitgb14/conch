package sessions

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// geminiJSONL is a chat as Gemini CLI writes it in JSONL: a header line
// (written again when the session is saved anew), then {"$set": …} lines
// that replace fields — the whole message list among them — and message
// lines of their own, a message written again under its id as it grows.
// The shapes are a real Gemini CLI file's, the words made up.
var geminiJSONL = strings.Join([]string{
	`{"sessionId":"gs","projectHash":"56d6","startTime":"2026-11-20T10:00:00.000Z","lastUpdated":"2026-11-20T10:00:00.000Z","kind":"main"}`,
	`{"$set":{"messages":[{"id":"m0","timestamp":"2026-11-20T10:00:00.000Z","type":"user","content":[{"text":"<session_context>\nThis is the Gemini CLI.\n</session_context>"}]}],"lastUpdated":"2026-11-20T10:00:00.000Z"}}`,
	`{"sessionId":"gs","projectHash":"56d6","startTime":"2026-11-20T10:00:05.000Z","lastUpdated":"2026-11-20T10:00:05.000Z","kind":"main"}`,
	`{"$set":{"messages":[{"id":"m0","timestamp":"2026-11-20T10:00:05.000Z","type":"user","content":[{"text":"<session_context>\nThis is the Gemini CLI.\n</session_context>"}]},{"id":"m1","timestamp":"2026-11-20T10:00:06.000Z","type":"user","content":[{"text":"Explain   the parser"}]}],"lastUpdated":"2026-11-20T10:00:06.000Z"}}`,
	`garbage`,
	`{"id":"m2","timestamp":"2026-11-20T10:00:07.000Z","type":"gemini","content":"It reads"}`,
	`{"id":"m2","timestamp":"2026-11-20T10:00:08.000Z","type":"gemini","content":"It reads lines."}`,
	`{"id":"m3","timestamp":"2026-11-20T10:00:09.000Z","type":"info","content":"tool ran"}`,
	`{"id":"m4","timestamp":"2026-11-20T10:01:00.000Z","type":"user","content":"and the header?"}`,
	`{"$set":{"lastUpdated":"2026-11-20T10:01:30.000Z"}}`,
	`{"id":"m5","timestamp":"2026-11-20T10:01:30.000Z","type":"gemini","content":[{"text":"First line."}]}`,
}, "\n") + "\n"

func TestGeminiJSONLIsListed(t *testing.T) {
	a6NoTools(t)
	home := t.TempDir()
	dir := "/w/proj"
	chats := filepath.Join(home, ".gemini", "tmp", "proj", "chats")
	write(t, filepath.Join(home, ".gemini", "projects.json"), `{"projects":{"/w/proj":"proj"}}`)
	write(t, filepath.Join(chats, "session-2026-11-20T10-00-gs.jsonl"), geminiJSONL)
	got := List(a6Env(home, nil), []string{dir}, 0)
	if len(got) != 1 || got[0].ID != "gs" || got[0].Title != "Explain the parser" {
		t.Fatalf("a JSONL chat without a summary is not listed by its first prompt: %+v", got)
	}
	if want := "2026-11-20T10:01:30Z"; got[0].Updated.UTC().Format("2006-01-02T15:04:05Z") != want {
		t.Errorf("updated %v, want the last $set's %s", got[0].Updated, want)
	}
}

func TestGeminiJSONLTranscript(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session-gs.jsonl")
	write(t, path, geminiJSONL)
	turns, err := Transcript(context.Background(), Env{}, Session{Agent: "gemini", Path: path})
	want := "user: Explain   the parser\nassistant: It reads lines.\nuser: and the header?\nassistant: First line."
	if err != nil || turnsText(turns) != want {
		t.Fatalf("got:\n%s\nwant:\n%s\n%v", turnsText(turns), want, err)
	}
}

// A JSONL chat holding only the context Gemini adds is not a session
// anybody started, and a summary still wins over the first prompt.
func TestGeminiJSONLEdges(t *testing.T) {
	dir := t.TempDir()
	only := filepath.Join(dir, "session-a.jsonl")
	write(t, only, strings.Join(strings.Split(geminiJSONL, "\n")[:3], "\n"))
	st, _ := os.Stat(only)
	if s := parseGemini(only, st); s != nil {
		t.Fatalf("context alone: %+v", s)
	}
	sum := filepath.Join(dir, "session-b.jsonl")
	write(t, sum, geminiJSONL+`{"$set":{"summary":"Parser walkthrough"}}`+"\n")
	st, _ = os.Stat(sum)
	if s := parseGemini(sum, st); s == nil || s.Title != "Parser walkthrough" {
		t.Fatalf("summary: %+v", s)
	}
	empty := filepath.Join(dir, "session-c.jsonl")
	write(t, empty, "")
	st, _ = os.Stat(empty)
	if s := parseGemini(empty, st); s != nil {
		t.Fatalf("empty: %+v", s)
	}
}
