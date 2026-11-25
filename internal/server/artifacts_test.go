package server

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/artifacts"
	"github.com/Amitgb14/conch/internal/proto"
)

// publishLines is a transcript's record of one Artifact tool call and the
// reply that names what it published.
func publishLines(id, at, cwd, title, reply string) string {
	return `{"timestamp":"` + at + `","cwd":"` + cwd + `","message":{"content":[{"type":"tool_use","id":"` + id + `","name":"Artifact","input":{"title":"` + title + `"}}]}}
{"timestamp":"` + at + `","cwd":"` + cwd + `","message":{"content":[{"type":"tool_result","tool_use_id":"` + id + `","content":"` + reply + `"}]}}
`
}

func appendFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(text); err != nil {
		t.Fatal(err)
	}
}

func urls(arts []proto.Artifact) []string {
	var out []string
	for _, a := range arts {
		out = append(out, a.URL)
	}
	return out
}

// artifact.list reads Claude Code's transcripts: a pane's agent's own, or
// every one on the machine with the panes and project each belongs to.
func TestArtifactList(t *testing.T) {
	s, home, work := shareFixture(t)
	// Each test reads its own scratch home; a shared cache would hold on
	// to another test's files by path.
	old := artifacts.Shared
	artifacts.Shared = artifacts.NewCache()
	t.Cleanup(func() { artifacts.Shared = old })

	proj, err := s.projects.add(work, false)
	if err != nil {
		t.Fatal(err)
	}
	e := agentPane(t, s, "p1", "claude", work, "exec sleep 30")
	t.Cleanup(func() { e.p.Close() })

	// Nothing to read yet: empty lists, never null.
	got, perr := s.artifactList(proto.ArtifactListParams{})
	if perr != nil || got.Artifacts == nil || len(got.Artifacts) != 0 {
		t.Fatalf("no artifacts: %+v %v", got, perr)
	}
	// An agent that has not named its conversation says so.
	got, perr = s.artifactList(proto.ArtifactListParams{Pane: "p1"})
	if perr != nil || len(got.Artifacts) != 0 || !strings.Contains(got.Note, "has not said") {
		t.Fatalf("no session yet: %+v %v", got, perr)
	}

	mine := filepath.Join(home, ".claude", "projects", claudeProjectName(work), "c1.jsonl")
	appendFile(t, mine, publishLines("a1", "2026-11-24T10:00:00Z", work, "Mock", `Created a new Artifact at https://claude.ai/artifact/MINE (version 1)`)+
		publishLines("a2", "2026-11-24T11:00:00Z", work, "Mock", `Updated the Artifact at https://claude.ai/artifact/MINE (Version 2), an Artifact of type \"Design\"`))
	// Another session in the same folder: a resumed agent's earlier one.
	appendFile(t, filepath.Join(filepath.Dir(mine), "c0.jsonl"),
		publishLines("b1", "2026-11-23T09:00:00Z", work, "Notes", `Created a new Artifact at https://claude.ai/artifact/EARLIER`))
	// Somebody else's, elsewhere, in no project.
	appendFile(t, filepath.Join(home, ".claude", "projects", "-elsewhere", "z.jsonl"),
		publishLines("c1", "2026-11-24T12:00:00Z", "/elsewhere", "Theirs", `Created a new Artifact at https://claude.ai/artifact/THEIRS`)+
			"not json\n")

	s.report(e, proto.AgentReportParams{ID: "p1", Agent: "claude", Event: "PostToolUse", SessionID: "c1"})
	got, perr = s.artifactList(proto.ArtifactListParams{Pane: "p1"})
	if perr != nil {
		t.Fatal(perr)
	}
	if want := []string{"https://claude.ai/artifact/MINE", "https://claude.ai/artifact/EARLIER"}; !slices.Equal(urls(got.Artifacts), want) {
		t.Fatalf("the pane's: %v, want %v", urls(got.Artifacts), want)
	}
	m := got.Artifacts[0]
	if m.Versions != 2 || m.Kind != "Design" || m.Title != "Mock" || !slices.Equal(m.Panes, []string{"p1"}) || m.Project != proj.info.ID {
		t.Errorf("mine = %+v, want 2 versions, its type, the pane and project %s", m, proj.info.ID)
	}

	all, perr := s.artifactList(proto.ArtifactListParams{})
	if perr != nil {
		t.Fatal(perr)
	}
	if want := []string{"https://claude.ai/artifact/THEIRS", "https://claude.ai/artifact/MINE", "https://claude.ai/artifact/EARLIER"}; !slices.Equal(urls(all.Artifacts), want) {
		t.Fatalf("every one: %v, want %v", urls(all.Artifacts), want)
	}
	if a := all.Artifacts[0]; len(a.Panes) != 0 || a.Project != "" {
		t.Errorf("theirs = %+v, want no pane and no project", a)
	}
	if a := all.Artifacts[1]; !slices.Equal(a.Panes, []string{"p1"}) || a.Project != proj.info.ID {
		t.Errorf("mine in the whole list = %+v, want pane p1 and the project", a)
	}

	// Through the protocol, as the app asks.
	res, perr := s.dispatch(&client{}, proto.Message{Method: proto.MethodArtifactList, Params: proto.Marshal(proto.ArtifactListParams{Pane: "p1"})})
	if perr != nil {
		t.Fatal(perr)
	}
	if l, ok := res.(proto.ArtifactList); !ok || len(l.Artifacts) != 2 {
		t.Fatalf("dispatch %#v", res)
	}

	// Not Claude Code, no agent, no pane.
	codex := agentPane(t, s, "p2", "codex", work, "exec sleep 30")
	t.Cleanup(func() { codex.p.Close() })
	if got, perr := s.artifactList(proto.ArtifactListParams{Pane: "p2"}); perr != nil || len(got.Artifacts) != 0 || !strings.Contains(got.Note, "only Claude Code") {
		t.Errorf("codex: %+v %v", got, perr)
	}
	plain := agentPane(t, s, "p3", "", work, "exec sleep 30")
	t.Cleanup(func() { plain.p.Close() })
	if got, perr := s.artifactList(proto.ArtifactListParams{Pane: "p3"}); perr != nil || !strings.Contains(got.Note, "no agent") {
		t.Errorf("a shell: %+v %v", got, perr)
	}
	if _, perr := s.artifactList(proto.ArtifactListParams{Pane: "nope"}); perr == nil || perr.Code != proto.ErrNotFound {
		t.Errorf("an unknown pane: %v", perr)
	}
	if !slices.Contains(proto.Capabilities, proto.CapArtifactList) || !slowMethods[proto.MethodArtifactList] || !tokenReads[proto.MethodArtifactList] {
		t.Error("artifact.list is not announced, run off the read loop, or open to read tokens")
	}
}

// After the Artifact tool runs, clients are told what was published —
// once, and also when the transcript is written only after the hook.
func TestArtifactPublished(t *testing.T) {
	s, home, work := shareFixture(t)
	old, oldRetries := artifacts.Shared, artifactRetries
	artifacts.Shared = artifacts.NewCache()
	artifactRetries = []time.Duration{0, 50 * time.Millisecond, 100 * time.Millisecond}
	t.Cleanup(func() { artifacts.Shared, artifactRetries = old, oldRetries })

	e := agentPane(t, s, "p1", "claude", work, "exec sleep 30")
	t.Cleanup(func() { e.p.Close() })
	path := filepath.Join(home, ".claude", "projects", claudeProjectName(work), "c1.jsonl")
	now := func() string { return time.Now().UTC().Format(time.RFC3339) }

	// Published an hour ago: nothing new to say when nothing was known.
	appendFile(t, path, publishLines("o", time.Now().Add(-time.Hour).UTC().Format(time.RFC3339), work, "Old", `Created a new Artifact at https://claude.ai/artifact/OLD`))
	if told := s.artifactPublished("p1", "c1"); len(told) != 0 {
		t.Fatalf("an old artifact was announced: %+v", told)
	}
	// A new one.
	appendFile(t, path, publishLines("n", now(), work, "New", `Created a new Artifact at https://claude.ai/artifact/NEW`))
	told := s.artifactPublished("p1", "c1")
	if len(told) != 1 || told[0].Pane != "p1" || told[0].Agent != "claude" || told[0].Artifact.URL != "https://claude.ai/artifact/NEW" || told[0].Artifact.Title != "New" {
		t.Fatalf("new: %+v", told)
	}
	// Its next version, written to the transcript only after the hook.
	go func() {
		time.Sleep(20 * time.Millisecond)
		appendFile(t, path, publishLines("n2", now(), work, "New", `Updated the Artifact at https://claude.ai/artifact/NEW (Version 2)`))
	}()
	told = s.artifactPublished("p1", "c1")
	if len(told) != 1 || told[0].Artifact.Versions != 2 {
		t.Fatalf("second version: %+v", told)
	}
	// Nothing changed: nothing said.
	if told := s.artifactPublished("p1", "c1"); len(told) != 0 {
		t.Fatalf("said again: %+v", told)
	}
	// A pane that is gone, a session with no transcript.
	if told := s.artifactPublished("nope", "c1"); len(told) != 0 {
		t.Fatalf("unknown pane: %+v", told)
	}
	if told := s.artifactPublished("p1", "missing"); len(told) != 0 {
		t.Fatalf("missing transcript: %+v", told)
	}
	s.published.forget("p1")
	s.published.mu.Lock()
	_, kept := s.published.panes["p1"]
	s.published.mu.Unlock()
	if kept {
		t.Error("a closed pane's record was kept")
	}
}

func TestProjectOfDir(t *testing.T) {
	projects := []proto.ProjectInfo{
		{ID: "a", Path: "/w/app", Worktrees: []proto.WorktreeInfo{{Path: "/w/app"}, {Path: "/w/app.worktrees/fix"}}},
		{ID: "b", Path: "/w/app/vendor/lib"},
	}
	for dir, want := range map[string]string{
		"/w/app":                   "a",
		"/w/app/src":               "a",
		"/w/app.worktrees/fix/x":   "a",
		"/w/app/vendor/lib/y":      "b", // the deeper project
		"/w/application":           "",  // a prefix, not a folder inside
		"/elsewhere":               "",
		"":                         "",
		"/w/app.worktrees/fix/../": "",
	} {
		if got := projectOfDir(projects, dir); got != want {
			t.Errorf("projectOfDir(%q) = %q, want %q", dir, got, want)
		}
	}
}
