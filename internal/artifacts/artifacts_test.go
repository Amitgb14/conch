package artifacts

import (
	"fmt"
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
	arts, err := Parse(strings.NewReader(strings.ReplaceAll(transcriptFixture, "PAGE", page)))
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
	if got := FindTranscript(root, "s1"); got != want {
		t.Errorf("findTranscript = %q, want %q", got, want)
	}
	for _, bad := range []string{"../s1", "a/b", "missing"} {
		if got := FindTranscript(root, bad); got != "" {
			t.Errorf("FindTranscript(%q) = %q, want none", bad, got)
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

	c := NewCache()
	arts, err := c.ReadAll([]string{a, b, filepath.Join(dir, "gone.jsonl")})
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
	if len(same.Folders) != 1 || same.Folders[0] != dir {
		t.Errorf("folders = %v, want the one folder both transcripts are in", same.Folders)
	}
	if arts[1].Dir != "/repo" {
		t.Errorf("dir = %q, want the session's cwd", arts[1].Dir)
	}
}

// TestReadAllKeepsManyTranscripts: listing every artifact reads every
// transcript, and a computer can hold more than a thousand. Each must stay
// cached between requests, or every request reads them all again.
func TestReadAllKeepsManyTranscripts(t *testing.T) {
	dir := t.TempDir()
	pub := func(url string) string {
		return `{"timestamp":"2026-11-13T07:00:00Z","message":{"content":[{"type":"tool_use","id":"x","name":"Artifact","input":{"title":"T"}}]}}
{"timestamp":"2026-11-13T07:00:01Z","message":{"content":[{"type":"tool_result","tool_use_id":"x","content":"Created a new Artifact at ` + url + `"}]}}
`
	}
	first := filepath.Join(dir, "first.jsonl")
	if err := os.WriteFile(first, []byte(pub("https://claude.ai/artifact/AAAA")), 0o600); err != nil {
		t.Fatal(err)
	}
	paths := []string{first}
	for i := range 1500 {
		p := filepath.Join(dir, fmt.Sprintf("s%d.jsonl", i))
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	c := NewCache()
	if _, err := c.ReadAll(paths); err != nil {
		t.Fatal(err)
	}
	// Change the first file behind the cache's back, same size and time:
	// only a cache that kept it still answers the old URL.
	st, _ := os.Stat(first)
	if err := os.WriteFile(first, []byte(pub("https://claude.ai/artifact/BBBB")), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(first, st.ModTime(), st.ModTime()); err != nil {
		t.Fatal(err)
	}
	arts, err := c.ReadAll(paths)
	if err != nil {
		t.Fatal(err)
	}
	if len(arts) != 1 || arts[0].URL != "https://claude.ai/artifact/AAAA" {
		t.Errorf("second listing read the transcript again: %+v", arts)
	}
}

// TestArtifactCacheFull: at its bound the cache forgets transcripts that
// are gone and keeps the ones still there.
func TestArtifactCacheFull(t *testing.T) {
	old := MaxCached
	MaxCached = 3
	t.Cleanup(func() { MaxCached = old })
	dir := t.TempDir()
	var paths []string
	for i := range 4 {
		p := filepath.Join(dir, fmt.Sprintf("s%d.jsonl", i))
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	c := NewCache()
	if _, err := c.ReadAll(paths[:3]); err != nil {
		t.Fatal(err)
	}
	os.Remove(paths[0])
	if _, err := c.ReadAll(paths[3:]); err != nil {
		t.Fatal(err)
	}
	if _, gone := c.entries[paths[0]]; gone || len(c.entries) != 3 {
		t.Errorf("entries %v: want the deleted one dropped and the rest kept", c.entries)
	}
	// Full of transcripts that all still exist: it starts again.
	p := filepath.Join(dir, "s9.jsonl")
	os.WriteFile(p, nil, 0o600)
	if _, err := c.Read(p); err != nil {
		t.Fatal(err)
	}
	if len(c.entries) != 1 {
		t.Errorf("%d entries, want the cache started again with one", len(c.entries))
	}
}
