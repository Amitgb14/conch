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
