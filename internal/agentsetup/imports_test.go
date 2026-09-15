package agentsetup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/proto"
)

func details(a proto.AgentSetup, group string) string {
	var out []string
	for _, g := range a.Groups {
		if g.Title == group {
			for _, it := range g.Items {
				s := it.Name + "(" + it.Scope + ")"
				if it.Detail != "" {
					s += "<" + it.Detail + ">"
				}
				out = append(out, s)
			}
		}
	}
	return strings.Join(out, " ")
}

func TestClaudeImports(t *testing.T) {
	env, repo, _ := fixture(t)
	put(t, filepath.Join(env.Home, ".claude/CLAUDE.md"), "Personal rules. See @~/notes/style.md for style.\n")
	put(t, filepath.Join(env.Home, "notes/style.md"), "Be brief.\n")
	put(t, filepath.Join(repo, "CLAUDE.md"), strings.Join([]string{
		"@AGENTS.md",
		"Docs: @docs/guide.md, and the API in @docs/api\\ notes.md.",
		"Write to someone@example.com or ping @alice.", // not imports
		"Use `@docs/ignored.md` literally.",            // code span
		"```",
		"@docs/in-block.md", // code block
		"```",
		"@missing/file.md",                  // doesn't exist
		"@" + filepath.Join(repo, "abs.md"), // absolute
		"@CLAUDE.md",                        // itself: a cycle
	}, "\n"))
	put(t, filepath.Join(repo, "AGENTS.md"), "Shared rules.\n@docs/deep1.md\n")
	put(t, filepath.Join(repo, "docs/guide.md"), "guide")
	put(t, filepath.Join(repo, "docs/api notes.md"), "api")
	put(t, filepath.Join(repo, "docs/ignored.md"), "no")
	put(t, filepath.Join(repo, "docs/in-block.md"), "no")
	put(t, filepath.Join(repo, "abs.md"), "abs")
	// A chain deeper than five imports stops; paths are relative to each file.
	put(t, filepath.Join(repo, "docs/deep1.md"), "@deep2.md")
	put(t, filepath.Join(repo, "docs/deep2.md"), "@deep3.md")
	put(t, filepath.Join(repo, "docs/deep3.md"), "@deep4.md")
	put(t, filepath.Join(repo, "docs/deep4.md"), "@deep5.md")
	put(t, filepath.Join(repo, "docs/deep5.md"), "@deep6.md @../AGENTS.md") // AGENTS.md again: already listed
	put(t, filepath.Join(repo, "docs/deep6.md"), "too deep")

	a, _ := Inspect(env, "claude", repo)
	got := details(a, GroupInstructions)
	want := "CLAUDE.md(user) style.md(user)<imported by CLAUDE.md> " +
		"CLAUDE.md(project) AGENTS.md(project)<imported by CLAUDE.md> " +
		"deep1.md(project)<imported by AGENTS.md> deep2.md(project)<imported by deep1.md> deep3.md(project)<imported by deep2.md> " +
		"deep4.md(project)<imported by deep3.md> " +
		"guide.md(project)<imported by CLAUDE.md> api notes.md(project)<imported by CLAUDE.md> abs.md(project)<imported by CLAUDE.md>"
	if got != want {
		t.Fatalf("instructions:\n got %s\nwant %s", got, want)
	}
	for _, not := range []string{"ignored.md", "in-block.md", "deep6.md", "example.com", "alice", "missing"} {
		if strings.Contains(got, not) {
			t.Errorf("listed %s", not)
		}
	}
}

func TestGeminiImports(t *testing.T) {
	env, repo, _ := fixture(t)
	put(t, filepath.Join(repo, "GEMINI.md"), "@./context/arch.md\n")
	put(t, filepath.Join(repo, "context/arch.md"), "arch")
	a, _ := Inspect(env, "gemini", repo)
	if got := details(a, GroupInstructions); got != "GEMINI.md(project) arch.md(project)<imported by GEMINI.md>" {
		t.Fatalf("gemini: %s", got)
	}
}

func TestImportsOfEdgeCases(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "m.md")
	put(t, p, "~~~\n@a.md\n~~~\n@b.md! (@c.md) @d.md;\n\t@e.md\n@\n")
	if got := strings.Join(importsOf(p), ","); got != "b.md,d.md,e.md" {
		t.Fatalf("refs: %s", got) // "(@c.md)" isn't after white space
	}
	if importsOf(filepath.Join(dir, "none.md")) != nil {
		t.Fatal("missing file")
	}
	big := filepath.Join(dir, "big.md")
	os.WriteFile(big, []byte("@x.md "+strings.Repeat("a", 1<<20)), 0o644)
	if importsOf(big) != nil {
		t.Fatal("huge file read")
	}
	if resolveImport("", p, "~/x.md") != "" || resolveImport("/h", p, "~") != "/h" || resolveImport("/h", p, "/abs.md") != "/abs.md" ||
		resolveImport("/h", p, "../x.md") != filepath.Join(filepath.Dir(dir), "x.md") {
		t.Fatal("resolveImport")
	}
	if stripCodeSpans("a `@b` c") != "a      c" {
		t.Fatalf("strip: %q", stripCodeSpans("a `@b` c"))
	}
}
