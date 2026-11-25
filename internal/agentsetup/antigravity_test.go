package agentsetup

import (
	"path/filepath"
	"strings"
	"testing"
)

// Antigravity CLI's places, as antigravity.google/docs (skills, rules,
// mcp, plugins) give them for the CLI, laid out in a scratch home and
// checkout.
func TestAntigravity(t *testing.T) {
	env, repo, sub := fixture(t)
	g := filepath.Join(env.Home, ".gemini")
	put(t, filepath.Join(g, "GEMINI.md"), "")
	put(t, filepath.Join(g, "config", "AGENTS.md"), "see @CLAUDE.md") // @path names a file, it does not pull it in
	put(t, filepath.Join(g, "config", "rules", "style.md"), "---\ntrigger: always_on\n---\n")
	put(t, filepath.Join(g, "config", "rules", "deep", "skipped.md"), "") // only the folder's own files
	put(t, filepath.Join(g, "config", "skills", "deploy", "SKILL.md"), "---\nname: deploy\n---\n")
	put(t, filepath.Join(g, "antigravity-cli", "skills", "cli-only", "SKILL.md"), "")
	put(t, filepath.Join(g, "skills", "old", "SKILL.md"), "")
	put(t, filepath.Join(g, "config", "mcp_config.json"), `{"mcpServers": {"fs": {"command": "x", "env": {"TOKEN": "secret"}}, "docs": {"serverUrl": "https://secret.example/mcp", "disabled": true}}}`)
	put(t, filepath.Join(g, "config", "plugins", "firebase", "skills", "fb", "SKILL.md"), "")
	put(t, filepath.Join(g, "config", "plugins", "firebase", "mcp_config.json"), `{"mcpServers": {"fb": {"url": "u"}}}`)
	// agy's own bundled skills are its business, not the person's.
	put(t, filepath.Join(g, "antigravity-cli", "builtin", "skills", "guide", "SKILL.md"), "")

	put(t, filepath.Join(repo, "AGENTS.md"), "")
	put(t, filepath.Join(repo, ".agents", "GEMINI.md"), "")
	put(t, filepath.Join(repo, ".agents", "rules", "go.md"), "")
	put(t, filepath.Join(repo, ".agent", "rules", "legacy.md"), "")
	put(t, filepath.Join(sub, "GEMINI.md"), "")
	put(t, filepath.Join(repo, ".agents", "skills", "test", "SKILL.md"), "")
	put(t, filepath.Join(sub, ".agents", "skills", "svc", "SKILL.md"), "")
	put(t, filepath.Join(repo, ".agents", "mcp_config.json"), `{"mcpServers": {"db": {"command": "y"}}}`)
	put(t, filepath.Join(sub, ".agents", "mcp_config.json"), `{"mcpServers": {"near": {"command": "z"}}}`)
	put(t, filepath.Join(sub, ".agents", "plugins", "team", "skills", "lint", "SKILL.md"), "")

	a, ok := Inspect(env, "antigravity", sub)
	if !ok {
		t.Fatal("antigravity unknown")
	}
	if a.Label != "Antigravity CLI" {
		t.Errorf("label %q", a.Label)
	}
	if got := names(a, GroupInstructions); got != "GEMINI.md(user) AGENTS.md(user) style.md(user) AGENTS.md(project) GEMINI.md(project) go.md(project) legacy.md(project) GEMINI.md(project)" {
		t.Errorf("instructions: %s", got)
	}
	if got := names(a, GroupSkills); got != "deploy(user) cli-only(user) old(user) test(project) svc(project) fb(plugin) lint(plugin)" {
		t.Errorf("skills: %s", got)
	}
	// The workspace is where agy starts, so a folder's own file is read
	// and one above it is not.
	if got := names(a, GroupMCP); got != "docs(user) fs(user) near(project) fb(plugin)" {
		t.Errorf("mcp: %s", got)
	}
	for _, g := range a.Groups {
		for _, it := range g.Items {
			if strings.Contains(it.Detail, "secret") {
				t.Errorf("a secret in %+v", it)
			}
			if g.Title == GroupMCP && it.Name == "docs" && it.Detail != "http · disabled" {
				t.Errorf("docs: %q", it.Detail)
			}
		}
	}
	if got := names(a, GroupPlugins); got != "firebase(plugin) team(plugin)" {
		t.Errorf("plugins: %s", got)
	}

	// Nothing set up: an empty report, not a failure.
	empty, _, _ := fixture(t)
	if a, ok := Inspect(empty, "antigravity", t.TempDir()); !ok || len(a.Groups) != 0 {
		t.Errorf("empty: %v %+v", ok, a.Groups)
	}
	// A config that is not JSON is skipped, the rest still read.
	put(t, filepath.Join(g, "config", "mcp_config.json"), "{not json")
	if a, _ := Inspect(env, "antigravity", sub); names(a, GroupMCP) != "near(project) fb(plugin)" {
		t.Errorf("bad config: %s", names(a, GroupMCP))
	}
}

// Sync to Antigravity CLI: instructions as a copy (its "@path" names a
// file without reading it in), skills where it reads them, and servers
// left alone with the reason said, since how it names a variable is not
// documented.
func TestSyncToAntigravity(t *testing.T) {
	root := syncRepo(t)
	res, err := Sync(root, "claude", []string{"antigravity"}, true)
	if err != nil {
		t.Fatal(err)
	}
	got := read(t, filepath.Join(root, "AGENTS.md"))
	if !strings.Contains(got, "Run the tests.") || strings.Contains(got, "@CLAUDE.md") {
		t.Fatalf("AGENTS.md is not a copy:\n%s", got)
	}
	if !isFile(filepath.Join(root, ".agents", "skills", "review", "SKILL.md")) {
		t.Fatalf("the skill did not reach it:\n%s", changeLines(res))
	}
	for _, name := range []string{"gh", "docs"} {
		if c := find(t, res, "antigravity", SyncMCP, name); c.Action != ActionSkip || !strings.Contains(c.Detail, "Antigravity CLI") {
			t.Errorf("%s: %+v", name, c)
		}
	}
	if isFile(filepath.Join(root, ".agents", "mcp_config.json")) {
		t.Error("wrote a server file for it")
	}

	// From it: its AGENTS.md and skills reach Claude.
	other := t.TempDir()
	write(t, filepath.Join(other, ".git", "HEAD"), "ref: refs/heads/master\n")
	write(t, filepath.Join(other, "AGENTS.md"), "# Ours\n")
	write(t, filepath.Join(other, ".agents", "skills", "ship", "SKILL.md"), "---\nname: ship\n---\n")
	if _, err := Sync(other, "antigravity", []string{"claude"}, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(read(t, filepath.Join(other, "CLAUDE.md")), "AGENTS.md") || !isFile(filepath.Join(other, ".claude", "skills", "ship", "SKILL.md")) {
		t.Fatal("antigravity's setup did not reach Claude")
	}
}

func TestSyncUserToAntigravity(t *testing.T) {
	env, _, _ := fixture(t)
	write(t, filepath.Join(env.Home, ".claude", "CLAUDE.md"), "# Mine\n")
	if _, err := SyncUser(env, "claude", []string{"antigravity"}, true); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(env.Home, ".gemini", "config", "AGENTS.md")); !strings.Contains(got, "# Mine") {
		t.Fatalf("~/.gemini/config/AGENTS.md:\n%s", got)
	}
}
