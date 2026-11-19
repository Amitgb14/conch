package phone

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const transcriptFixture = `{"timestamp":"2026-11-13T07:09:36Z","message":{"content":[{"type":"tool_use","id":"t1","name":"Artifact","input":{"type_url":"https://claude.ai/artifact/TYPE","title":"Conch iOS"}}]}}
{"timestamp":"2026-11-13T07:09:40Z","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"Created a new Artifact at https://claude.ai/artifact/VoeJ (version 1) from the Artifact type https://claude.ai/artifact/TYPE"}]}}
{"timestamp":"2026-11-13T07:10:00Z","message":{"content":[{"type":"tool_use","id":"t2","name":"Artifact","input":{"url":"https://claude.ai/artifact/VoeJ","file_path":"/x/canvas.json"}}]}}
{"timestamp":"2026-11-13T08:00:00Z","message":{"content":[{"type":"tool_result","tool_use_id":"t2","content":[{"type":"text","text":"Updated the Artifact at https://claude.ai/artifact/VoeJ (Version 2), an Artifact of type \"Design\""}]}]}}
{"timestamp":"2026-11-13T08:05:00Z","message":{"content":[{"type":"tool_use","id":"t3","name":"Artifact","input":{"file_path":"PAGE"}}]}}
{"timestamp":"2026-11-13T08:06:00Z","message":{"content":[{"type":"tool_result","tool_use_id":"t3","content":"Published https://claude.ai/code/artifact/abc-123 as a new page"}]}}
{"timestamp":"2026-11-13T08:07:00Z","message":{"content":[{"type":"tool_use","id":"t4","name":"Artifact","input":{"action":"read","url":"https://claude.ai/artifact/OTHER"}}]}}
{"timestamp":"2026-11-13T08:08:00Z","message":{"content":[{"type":"tool_result","tool_use_id":"t4","content":"<artifact-content> https://claude.ai/artifact/OTHER"}]}}
{"timestamp":"2026-11-13T08:09:00Z","message":{"content":[{"type":"tool_result","tool_use_id":"t9","content":"Updated the Artifact at https://claude.ai/artifact/NOTOURS"}]}}
`

func TestParseArtifacts(t *testing.T) {
	page := filepath.Join(t.TempDir(), "report.html")
	if err := os.WriteFile(page, []byte("<html><head><title>Flake report</title></head></html>"), 0o600); err != nil {
		t.Fatal(err)
	}
	arts, err := parseArtifacts(strings.NewReader(strings.ReplaceAll(transcriptFixture, "PAGE", page)))
	if err != nil {
		t.Fatal(err)
	}
	if len(arts) != 2 {
		t.Fatalf("got %d artifacts, want 2 (reads and others' results are not publishes): %+v", len(arts), arts)
	}
	page0, design := arts[0], arts[1]
	if page0.URL != "https://claude.ai/code/artifact/abc-123" || page0.Title != "Flake report" || page0.Kind != "Page" {
		t.Errorf("newest = %+v, want the page titled from its <title>", page0)
	}
	if design.Title != "Conch iOS" || design.Kind != "Design" || design.Versions != 2 {
		t.Errorf("design = %+v, want its first title, its type and 2 versions", design)
	}
	if !design.Updated.After(design.Created) {
		t.Errorf("design updated %v not after created %v", design.Updated, design.Created)
	}
}

func TestFindTranscript(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "-Users-me-repo")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "s1.jsonl")
	if err := os.WriteFile(want, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := findTranscript(root, "s1"); got != want {
		t.Errorf("findTranscript = %q, want %q", got, want)
	}
	for _, bad := range []string{"../s1", "a/b", "missing"} {
		if got := findTranscript(root, bad); got != "" {
			t.Errorf("findTranscript(%q) = %q, want none", bad, got)
		}
	}
}

func TestReadAllMergesSessions(t *testing.T) {
	dir := t.TempDir()
	line := func(id, ts, cwd, title, url string) string {
		return `{"timestamp":"` + ts + `","cwd":"` + cwd + `","message":{"content":[{"type":"tool_use","id":"` + id + `","name":"Artifact","input":{"title":"` + title + `"}}]}}
{"timestamp":"` + ts + `","cwd":"` + cwd + `","message":{"content":[{"type":"tool_result","tool_use_id":"` + id + `","content":"Updated the Artifact at ` + url + `"}]}}
`
	}
	a := filepath.Join(dir, "a.jsonl")
	b := filepath.Join(dir, "b.jsonl")
	os.WriteFile(a, []byte(line("x", "2026-11-13T07:00:00Z", "/repo", "Old", "https://claude.ai/artifact/SAME")+
		line("y", "2026-11-13T07:30:00Z", "/repo", "Only A", "https://claude.ai/artifact/ONLYA")), 0o600)
	os.WriteFile(b, []byte(line("z", "2026-11-14T07:00:00Z", "/repo2", "New", "https://claude.ai/artifact/SAME")), 0o600)

	c := &artifactCache{entries: map[string]artifactEntry{}}
	arts, err := c.readAll([]string{a, b, filepath.Join(dir, "gone.jsonl")})
	if err != nil {
		t.Fatal(err)
	}
	if len(arts) != 2 {
		t.Fatalf("got %d artifacts, want 2: %+v", len(arts), arts)
	}
	same := arts[0]
	if same.URL != "https://claude.ai/artifact/SAME" || same.Versions != 2 || same.Title != "New" || same.Dir != "/repo2" {
		t.Errorf("merged = %+v, want both sessions' versions and the newest title and dir", same)
	}
	if !same.Created.Before(same.Updated) {
		t.Errorf("merged created %v should be the earlier session's", same.Created)
	}
	if arts[1].Dir != "/repo" {
		t.Errorf("dir = %q, want the session's cwd", arts[1].Dir)
	}
}

// TestArtifactsEndpoint is the route as the phone calls it: with no pane,
// every artifact any Claude Code session on this computer published; with
// a pane that runs no Claude Code, none.
func TestArtifactsEndpoint(t *testing.T) {
	f := newFixture(t)
	p := f.pair(PermView)
	home, _ := os.UserHomeDir()

	// Nothing published yet, and no ~/.claude at all: an empty list, not null.
	_, raw := p.do("GET", "/api/artifacts", nil)
	var empty ArtifactList
	if status := p.get("/api/artifacts", &empty); status != 200 || empty.Artifacts == nil || len(empty.Artifacts) != 0 {
		t.Fatalf("no transcripts: %d %s", status, raw)
	}

	for dir, body := range map[string]string{
		"-repo-a": strings.ReplaceAll(transcriptFixture, "PAGE", "/nowhere/report.html"),
		"-repo-b": `{"timestamp":"2026-11-14T09:00:00Z","cwd":"/repo-b","message":{"content":[{"type":"tool_use","id":"b1","name":"Artifact","input":{"title":"Later"}}]}}
{"timestamp":"2026-11-14T09:00:01Z","cwd":"/repo-b","message":{"content":[{"type":"tool_result","tool_use_id":"b1","content":"Created a new Artifact at https://claude.ai/artifact/LATER"}]}}
not json at all
`,
	} {
		d := filepath.Join(home, ".claude", "projects", dir)
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "s.jsonl"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var all ArtifactList
	if status := p.get("/api/artifacts", &all); status != 200 {
		t.Fatalf("all: %d", status)
	}
	var urls []string
	for _, a := range all.Artifacts {
		urls = append(urls, a.URL)
	}
	want := []string{"https://claude.ai/artifact/LATER", "https://claude.ai/code/artifact/abc-123", "https://claude.ai/artifact/VoeJ"}
	if strings.Join(urls, " ") != strings.Join(want, " ") || all.Pane != "" {
		t.Errorf("every session's artifacts, newest first: pane %q %v, want %v", all.Pane, urls, want)
	}
	if all.Artifacts[0].Dir != "/repo-b" || all.Artifacts[0].Title != "Later" {
		t.Errorf("newest = %+v, want its title and the directory it was made in", all.Artifacts[0])
	}

	// A shell is no Claude Code: nothing, and its pane named in full.
	sh := f.pane("", "exec sleep 30")
	var none ArtifactList
	if status := p.get("/api/artifacts?pane="+sh, &none); status != 200 || none.Pane != "local:"+sh || len(none.Artifacts) != 0 {
		t.Errorf("a shell's pane: %d %+v", status, none)
	}
	if status, b := p.do("GET", "/api/artifacts?pane=nope", nil); status < 400 {
		t.Errorf("an unknown pane: %d %s", status, b)
	}
}
