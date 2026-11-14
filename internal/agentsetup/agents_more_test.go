package agentsetup

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// The agents supported after the first five: what each one's setup view
// shows, from the files its own source says it reads.

func TestCopilot(t *testing.T) {
	env, repo, sub := fixture(t)
	ch := filepath.Join(env.Home, ".copilot")
	put(t, filepath.Join(ch, "copilot-instructions.md"), "mine")
	put(t, filepath.Join(ch, "instructions", "go.instructions.md"), "")
	put(t, filepath.Join(ch, "instructions", "notes.md"), "") // not an .instructions.md
	put(t, filepath.Join(ch, "skills", "tide", "SKILL.md"), "---\nname: tide\n---\n")
	put(t, filepath.Join(ch, "mcp-config.json"), `{"mcpServers":{"notion":{"type":"http","url":"https://secret.example/mcp"}}}`)
	put(t, filepath.Join(repo, "AGENTS.md"), "")
	put(t, filepath.Join(repo, "CLAUDE.md"), "")
	put(t, filepath.Join(repo, ".github", "copilot-instructions.md"), "")
	put(t, filepath.Join(repo, ".github", "instructions", "api.instructions.md"), "")
	put(t, filepath.Join(sub, "AGENTS.md"), "")
	put(t, filepath.Join(repo, ".github", "skills", "ship", "SKILL.md"), "---\nname: ship\n---\n")
	put(t, filepath.Join(repo, ".claude", "skills", "review", "SKILL.md"), "---\nname: review\n---\n")
	put(t, filepath.Join(repo, ".github", "mcp.json"), `{"mcpServers":{"db":{"type":"local","command":"db-mcp"}}}`)
	put(t, filepath.Join(sub, ".mcp.json"), `{"mcpServers":{"gh":{"command":"npx"}}}`)

	a, ok := Inspect(env, "copilot", sub)
	if !ok || a.Label != "Copilot CLI" {
		t.Fatalf("copilot: %+v", a)
	}
	for group, want := range map[string]string{
		GroupInstructions: "copilot-instructions.md(user) instructions/go(user) AGENTS.md(project) CLAUDE.md(project) copilot-instructions.md(project) instructions/api(project) AGENTS.md(project)",
		GroupSkills:       "tide(user) ship(project) review(project)",
		GroupMCP:          "notion(user) gh(project) db(project)",
	} {
		if got := names(a, group); got != want {
			t.Errorf("%s: %s", group, got)
		}
	}
	if len(a.Notes) != 1 || !strings.Contains(a.Notes[0], "trusted") {
		t.Errorf("notes %v", a.Notes)
	}
	// COPILOT_HOME moves your half of it.
	other := t.TempDir()
	put(t, filepath.Join(other, "copilot-instructions.md"), "")
	env.Getenv = func(k string) string { return map[string]string{"COPILOT_HOME": other}[k] }
	if a, _ := Inspect(env, "copilot", t.TempDir()); names(a, GroupInstructions) != "copilot-instructions.md(user)" {
		t.Errorf("with COPILOT_HOME: %s", names(a, GroupInstructions))
	}
}

func TestCline(t *testing.T) {
	env, repo, sub := fixture(t)
	put(t, filepath.Join(env.Home, ".agents", "AGENTS.md"), "")
	put(t, filepath.Join(env.Home, ".cline", "rules", "style.md"), "")
	put(t, filepath.Join(env.Home, ".cline", "skills", "tide", "SKILL.md"), "---\nname: tide\n---\n")
	put(t, filepath.Join(env.Home, ".cline", "data", "settings", "cline_mcp_settings.json"),
		`{"mcpServers":{"gh":{"type":"stdio","command":"npx"},"wiki":{"type":"streamableHttp","url":"https://wiki.example/mcp"}}}`)
	put(t, filepath.Join(repo, "AGENTS.md"), "")
	put(t, filepath.Join(repo, ".clinerules", "testing.md"), "")
	put(t, filepath.Join(repo, ".clinerules", "deploy.txt"), "")
	put(t, filepath.Join(repo, ".clinerules", "workflows", "release.md"), "") // a workflow, not a rule
	put(t, filepath.Join(repo, ".clinerules", "logo.png"), "")
	put(t, filepath.Join(repo, ".cline", "rules", "db.md"), "")
	put(t, filepath.Join(repo, ".clinerules", "skills", "ship", "SKILL.md"), "---\nname: ship\n---\n")

	a, ok := Inspect(env, "cline", sub)
	if !ok || a.Label != "Cline" {
		t.Fatalf("cline: %+v", a)
	}
	for group, want := range map[string]string{
		GroupInstructions: "AGENTS.md(user) rules/style(user) AGENTS.md(project) rules/deploy(project) rules/testing(project) rules/db(project)",
		GroupSkills:       "tide(user) ship(project)",
		GroupMCP:          "gh(user) wiki(user)",
	} {
		if got := names(a, group); got != want {
			t.Errorf("%s: %s", group, got)
		}
	}
	// .clinerules may be one file rather than a folder.
	_, repo2, _ := fixture(t)
	put(t, filepath.Join(repo2, ".clinerules"), "one file")
	if a, _ := Inspect(env, "cline", repo2); !strings.Contains(names(a, GroupInstructions), ".clinerules(project)") {
		t.Errorf(".clinerules as a file: %s", names(a, GroupInstructions))
	}
	// Its settings file moves with CLINE_MCP_SETTINGS_PATH, and with CLINE_DATA_DIR.
	moved := filepath.Join(t.TempDir(), "mcp.json")
	put(t, moved, `{"mcpServers":{"moved":{"command":"x"}}}`)
	if got := clineMCPSettings(a6Env(env.Home, map[string]string{"CLINE_MCP_SETTINGS_PATH": moved})); got != moved {
		t.Errorf("CLINE_MCP_SETTINGS_PATH: %s", got)
	}
	if got := clineMCPSettings(a6Env(env.Home, map[string]string{"CLINE_DATA_DIR": "/d"})); got != "/d/settings/cline_mcp_settings.json" {
		t.Errorf("CLINE_DATA_DIR: %s", got)
	}
}

func TestQwen(t *testing.T) {
	env, repo, sub := fixture(t)
	qh := filepath.Join(env.Home, ".qwen")
	put(t, filepath.Join(qh, "QWEN.md"), "mine")
	put(t, filepath.Join(qh, "skills", "tide", "SKILL.md"), "---\nname: tide\n---\n")
	put(t, filepath.Join(qh, "settings.json"), `{"mcpServers":{"wiki":{"httpUrl":"https://wiki.example/mcp"}}}`)
	put(t, filepath.Join(repo, "QWEN.md"), "")
	put(t, filepath.Join(repo, "AGENTS.md"), "")
	put(t, filepath.Join(sub, "QWEN.local.md"), "")
	put(t, filepath.Join(repo, ".qwen", "rules", "go.md"), "")
	put(t, filepath.Join(sub, ".qwen", "skills", "ship", "SKILL.md"), "---\nname: ship\n---\n")
	put(t, filepath.Join(sub, ".qwen", "settings.json"), `{"mcpServers":{"db":{"command":"db-mcp"}}}`)
	put(t, filepath.Join(sub, ".qwen", "commands", "fix.toml"), "")

	a, ok := Inspect(env, "qwen", sub)
	if !ok || a.Label != "Qwen Code" {
		t.Fatalf("qwen: %+v", a)
	}
	for group, want := range map[string]string{
		GroupInstructions: "QWEN.md(user) QWEN.md(project) AGENTS.md(project) QWEN.local.md(local) rules/go(project)",
		GroupSkills:       "tide(user) ship(project)",
		GroupMCP:          "wiki(user) db(project)",
		GroupCommands:     "/fix(project)",
	} {
		if got := names(a, group); got != want {
			t.Errorf("%s: %s", group, got)
		}
	}
	// context.fileName says which files are its instructions.
	put(t, filepath.Join(sub, ".qwen", "settings.json"), `{"context":{"fileName":["RULES.md"]}}`)
	put(t, filepath.Join(repo, "RULES.md"), "")
	if a, _ := Inspect(env, "qwen", sub); !strings.HasPrefix(names(a, GroupInstructions), "RULES.md(project)") {
		t.Errorf("context.fileName: %s", names(a, GroupInstructions))
	}
}

func TestGoose(t *testing.T) {
	env, repo, sub := fixture(t)
	gh := filepath.Join(env.Home, ".config", "goose")
	put(t, filepath.Join(gh, ".goosehints"), "mine")
	put(t, filepath.Join(env.Home, ".agents", "AGENTS.md"), "")
	put(t, filepath.Join(env.Home, ".agents", "skills", "tide", "SKILL.md"), "---\nname: tide\n---\n")
	put(t, filepath.Join(gh, "config.yaml"), `GOOSE_PROVIDER: anthropic
extensions:
  developer:
    type: builtin
    name: developer
    enabled: true
  github:
    type: stdio
    name: github
    enabled: true
    cmd: npx
    args: [-y, server-github]
    envs: {}
    env_keys: [GITHUB_TOKEN]
  wiki:
    type: streamable_http
    enabled: true
    uri: https://secret.example/mcp
  off:
    type: stdio
    enabled: false
    cmd: nope
`)
	put(t, filepath.Join(repo, ".goosehints"), "")
	put(t, filepath.Join(sub, "AGENTS.md"), "")
	put(t, filepath.Join(repo, ".goose", "skills", "ship", "SKILL.md"), "---\nname: ship\n---\n")

	a, ok := Inspect(env, "goose", sub)
	if !ok || a.Label != "Goose" {
		t.Fatalf("goose: %+v", a)
	}
	for group, want := range map[string]string{
		GroupInstructions: ".goosehints(user) .goosehints(project) AGENTS.md(project) AGENTS.md(user)",
		GroupSkills:       "tide(user) ship(project)",
		// Built-in extensions are Goose's own, and a disabled one is off.
		GroupMCP: "github(user) wiki(user)",
	} {
		if got := names(a, group); got != want {
			t.Errorf("%s: %s", group, got)
		}
	}
	for _, g := range a.Groups {
		for _, it := range g.Items {
			if strings.Contains(it.Detail, "secret") {
				t.Fatalf("a URL leaked: %+v", it)
			}
		}
	}
	// A config that is not YAML is no servers, not a failure.
	put(t, filepath.Join(gh, "config.yaml"), "extensions: [: nonsense")
	if a, _ := Inspect(env, "goose", sub); names(a, GroupMCP) != "" {
		t.Errorf("broken config: %s", names(a, GroupMCP))
	}
	// GOOSE_PATH_ROOT moves it.
	if got := gooseConfigDir(a6Env("/h", map[string]string{"GOOSE_PATH_ROOT": "/g"})); got != "/g/config" {
		t.Errorf("GOOSE_PATH_ROOT: %s", got)
	}
	if got := gooseConfigDir(a6Env("/h", map[string]string{"XDG_CONFIG_HOME": "/x"})); got != "/x/goose" {
		t.Errorf("XDG_CONFIG_HOME: %s", got)
	}
}

// A checkout's setup given to the new agents, each in its own files and
// shape: Qwen writes Gemini's (httpUrl), Copilot reads .mcp.json by itself,
// and Cline and Goose keep their servers only in a file of yours.
func TestSyncToTheLaterAgents(t *testing.T) {
	root := syncRepo(t)
	res, err := Sync(root, "claude", []string{"copilot", "cline", "qwen", "goose"}, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ agent, kind, name, action string }{
		{"copilot", SyncMCP, "gh", ActionSame},                 // it reads .mcp.json itself
		{"copilot", SyncInstructions, "CLAUDE.md", ActionSame}, // and CLAUDE.md
		{"copilot", SyncSkill, "review", ActionSame},           // and .claude/skills
		{"cline", SyncMCP, "gh", ActionSkip},
		{"goose", SyncMCP, "docs", ActionSkip},
		{"qwen", SyncMCP, "docs", ActionCreate},
		{"qwen", SyncInstructions, "QWEN.md", ActionCreate},
	} {
		if got := find(t, res, c.agent, c.kind, c.name); got.Action != c.action {
			t.Errorf("%s %s %s: %s (%s)", c.agent, c.kind, c.name, got.Action, got.Detail)
		}
	}
	var qwen struct {
		MCPServers map[string]map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(read(t, filepath.Join(root, ".qwen", "settings.json"))), &qwen); err != nil {
		t.Fatal(err)
	}
	if qwen.MCPServers["docs"]["httpUrl"] != "https://docs.example/mcp" {
		t.Errorf("qwen's docs server: %v", qwen.MCPServers["docs"])
	}
	if !strings.Contains(read(t, filepath.Join(root, "QWEN.md")), "@CLAUDE.md") {
		t.Errorf("QWEN.md should point at CLAUDE.md:\n%s", read(t, filepath.Join(root, "QWEN.md")))
	}
}

// Your own servers to Cline: its own shape, and none it cannot expand.
func TestSyncUserToCline(t *testing.T) {
	home, e := userHome(t)
	write(t, filepath.Join(home, ".claude.json"), `{"mcpServers":{
		"gh":{"command":"npx","args":["gh"],"env":{"GH_TOKEN":"${GH_TOKEN}"}},
		"wiki":{"type":"http","url":"https://wiki.example/mcp"},
		"tail":{"type":"sse","url":"https://tail.example/sse"}}}`)
	res, err := SyncUser(e, "claude", []string{"cline"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if c := find(t, res, "cline", SyncMCP, "gh"); c.Action != ActionSkip || !strings.Contains(c.Detail, "does not expand") {
		t.Errorf("gh: %+v", c)
	}
	var got struct {
		MCPServers map[string]map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(read(t, filepath.Join(home, ".cline", "data", "settings", "cline_mcp_settings.json"))), &got); err != nil {
		t.Fatal(err)
	}
	if got.MCPServers["wiki"]["type"] != "streamableHttp" || got.MCPServers["tail"]["type"] != "sse" {
		t.Errorf("cline's servers: %v", got.MCPServers)
	}
	if _, there := got.MCPServers["gh"]; there {
		t.Error("a server Cline would pass ${GH_TOKEN} to as text was written")
	}
	if !isFile(filepath.Join(home, ".cline", "rules", "AGENTS.md")) {
		t.Error("your instructions were not given to Cline")
	}
}
