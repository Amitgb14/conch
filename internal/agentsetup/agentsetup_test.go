package agentsetup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/proto"
)

func put(t *testing.T, path, content string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fixture(t *testing.T) (Env, string, string) {
	t.Helper()
	base, _ := filepath.EvalSymlinks(t.TempDir())
	home := filepath.Join(base, "home")
	repo := filepath.Join(base, "src", "api")
	os.MkdirAll(filepath.Join(repo, ".git"), 0o755)
	sub := filepath.Join(repo, "svc")
	os.MkdirAll(sub, 0o755)
	env := Env{Home: home, Getenv: func(string) string { return "" }, GOOS: "linux"}
	return env, repo, sub
}

// names renders a group as "name(scope)" entries.
func names(a proto.AgentSetup, group string) string {
	var out []string
	for _, g := range a.Groups {
		if g.Title == group {
			for _, it := range g.Items {
				s := it.Name + "(" + it.Scope + ")"
				if it.Missing {
					s += "!"
				}
				out = append(out, s)
			}
		}
	}
	return strings.Join(out, " ")
}

func TestCodex(t *testing.T) {
	env, repo, sub := fixture(t)
	put(t, filepath.Join(env.Home, ".codex/AGENTS.md"), "user")
	put(t, filepath.Join(env.Home, ".codex/config.toml"), `
[mcp_servers.docs]
url = "https://secret.example/mcp"
[projects."`+repo+`"]
trust_level = "trusted"
`)
	put(t, filepath.Join(env.Home, ".codex/skills/.system/imagegen/SKILL.md"), "---\nname: imagegen\n---\n")
	put(t, filepath.Join(env.Home, ".codex/prompts/fix.md"), "")
	put(t, filepath.Join(repo, "AGENTS.md"), "")
	put(t, filepath.Join(sub, "AGENTS.override.md"), "")
	put(t, filepath.Join(sub, "AGENTS.md"), "")
	put(t, filepath.Join(repo, ".agents/skills/deploy/SKILL.md"), "no frontmatter")

	a, ok := Inspect(env, "codex", sub)
	if !ok {
		t.Fatal("codex unknown")
	}
	if got := names(a, GroupInstructions); got != "AGENTS.md(user) AGENTS.md(project) AGENTS.override.md(project)" {
		t.Fatalf("instructions: %s", got)
	}
	if got := names(a, GroupSkills); got != "deploy(project) imagegen(system)" {
		t.Fatalf("skills: %s", got)
	}
	if got := names(a, GroupCommands); got != "/prompts:fix(user)" {
		t.Fatalf("commands: %s", got)
	}
	mcp := a.Groups[len(a.Groups)-2]
	if names(a, GroupMCP) != "docs(user)" || strings.Contains(mcp.Items[0].Detail, "secret") || mcp.Items[0].Detail != "http" {
		t.Fatalf("mcp: %+v", mcp)
	}
	if len(a.Notes) != 0 {
		t.Fatalf("trusted repo got notes: %v", a.Notes)
	}
	if a, _ := Inspect(env, "codex", filepath.Dir(repo)); len(a.Notes) != 1 {
		t.Fatalf("untrusted folder should get a note: %v", a.Notes)
	}
}

func TestGeminiAndOpenCode(t *testing.T) {
	env, repo, _ := fixture(t)
	put(t, filepath.Join(env.Home, ".gemini/settings.json"), `{
  // comments are allowed
  "context": {"fileName": ["AGENTS.md", "GEMINI.md"]},
  "mcpServers": {"fs": {"command": "x",},},
}`)
	put(t, filepath.Join(repo, "AGENTS.md"), "")
	put(t, filepath.Join(env.Home, ".gemini/commands/git/commit.toml"), "")
	put(t, filepath.Join(env.Home, ".gemini/extensions/ext1/gemini-extension.json"), `{"name":"cloud","version":"1.2","mcpServers":{"cloud":{"httpUrl":"u"}}}`)
	put(t, filepath.Join(env.Home, ".gemini/extensions/ext1/GEMINI.md"), "")
	g, _ := Inspect(env, "gemini", repo)
	if got := names(g, GroupInstructions); got != "AGENTS.md(project) GEMINI.md(extension)" {
		t.Fatalf("gemini instructions: %s", got)
	}
	if names(g, GroupCommands) != "/git:commit(user)" || names(g, GroupMCP) != "fs(user) cloud(extension)" || names(g, GroupPlugins) != "cloud(extension)" {
		t.Fatalf("gemini: %+v", g.Groups)
	}

	put(t, filepath.Join(env.Home, ".claude/CLAUDE.md"), "")
	put(t, filepath.Join(env.Home, ".agents/skills/shared/SKILL.md"), "---\nname: shared\n---\n")
	os.MkdirAll(filepath.Join(env.Home, ".claude/skills"), 0o755)
	os.Symlink(filepath.Join(env.Home, ".agents/skills/shared"), filepath.Join(env.Home, ".claude/skills/shared"))
	put(t, filepath.Join(repo, "opencode.jsonc"), `{"mcp": {"db": {"type": "local", "enabled": false}}, "instructions": ["docs/*.md"]}`)
	o, _ := Inspect(env, "opencode", repo)
	if got := names(o, GroupInstructions); got != "CLAUDE.md(user) AGENTS.md(project) docs/*.md(project)" {
		t.Fatalf("opencode instructions: %s", got)
	}
	if got := names(o, GroupSkills); got != "shared(user)" {
		t.Fatalf("a skill linked into two folders should show once: %s", got)
	}
	if names(o, GroupMCP) != "db(project)" || !strings.Contains(o.Groups[2].Items[0].Detail, "disabled") {
		t.Fatalf("opencode mcp: %+v", o.Groups)
	}
}

func TestClaudeAndMarkMissing(t *testing.T) {
	env, repo, _ := fixture(t)
	put(t, filepath.Join(env.Home, ".claude/settings.json"), `{"enabledPlugins": {"lsp@official": true}}`)
	put(t, filepath.Join(env.Home, ".claude/plugins/installed_plugins.json"),
		`{"plugins": {"lsp@official": [{"installPath": "`+filepath.Join(env.Home, "plugin")+`"}]}}`)
	put(t, filepath.Join(env.Home, "plugin/commands/check.md"), "")
	put(t, filepath.Join(repo, ".mcp.json"), `{"mcpServers": {"web": {"type": "http"}}}`)
	put(t, filepath.Join(repo, ".claude/commands/ship.md"), "")
	put(t, filepath.Join(env.Home, ".claude.json"), `{"projects": {"`+repo+`": {"hasTrustDialogAccepted": true, "enabledMcpjsonServers": ["web"]}}}`)

	main, _ := Inspect(env, "claude", repo)
	if got := names(main, GroupCommands); got != "/ship(project) /check(plugin)" {
		t.Fatalf("commands: %s", got)
	}
	if names(main, GroupPlugins) != "lsp@official(user)" || len(main.Notes) != 0 {
		t.Fatalf("plugins/notes: %+v %v", main.Groups, main.Notes)
	}

	// A worktree without the untracked .mcp.json and command.
	wt := filepath.Join(filepath.Dir(repo), "api.worktrees", "feat")
	put(t, filepath.Join(wt, ".git"), "gitdir: x")
	here, _ := Inspect(env, "claude", wt)
	if len(here.Notes) != 1 {
		t.Fatalf("worktree should need trust: %v", here.Notes)
	}
	MarkMissing(&here, main)
	if got := names(here, GroupMCP); got != "web(project)!" {
		t.Fatalf("mcp: %s", got)
	}
	if got := names(here, GroupCommands); got != "/check(plugin) /ship(project)!" {
		t.Fatalf("commands: %s", got)
	}
}

func TestStripJSONC(t *testing.T) {
	in := `{"a": "x // not a comment", /* c */ "b": [1, 2,], }`
	if got := string(stripJSONC([]byte(in))); !strings.Contains(got, `"x // not a comment"`) || strings.Contains(got, ",]") {
		t.Fatalf("%s", got)
	}
}
