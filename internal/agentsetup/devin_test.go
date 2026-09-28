package agentsetup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDevin(t *testing.T) {
	env, repo, sub := fixture(t)
	dh := filepath.Join(env.Home, ".config", "devin")
	put(t, filepath.Join(dh, "AGENTS.md"), "mine")
	put(t, filepath.Join(env.Home, ".devin", "rules", "style.md"), "")
	put(t, filepath.Join(env.Home, ".claude", "CLAUDE.md"), "claude's")
	put(t, filepath.Join(dh, "skills", "tide", "SKILL.md"), "---\nname: tide\n---\n")
	put(t, filepath.Join(dh, "agents", "reviewer.md"), "")
	put(t, filepath.Join(dh, "mcp_config.json"), `{"mcpServers":{"notion":{"url":"https://secret.example/mcp","transport":"http"}}}`)
	put(t, filepath.Join(repo, "AGENTS.md"), "")
	put(t, filepath.Join(repo, "CLAUDE.md"), "")
	put(t, filepath.Join(sub, "AGENTS.local.md"), "")
	put(t, filepath.Join(repo, ".devin", "skills", "ship", "SKILL.md"), "---\nname: ship\n---\n")
	put(t, filepath.Join(repo, ".claude", "skills", "review", "SKILL.md"), "---\nname: review\n---\n")
	put(t, filepath.Join(repo, ".claude", "commands", "fix.md"), "")
	put(t, filepath.Join(repo, ".devin", "agents", "tester", "AGENT.md"), "")
	put(t, filepath.Join(repo, ".devin", "mcp_config.json"), `{"mcpServers":{"db":{"command":"db-mcp"}}}`)
	put(t, filepath.Join(repo, ".devin", "mcp_config.local.json"), `{"mcpServers":{"me":{"command":"me"}}}`)
	put(t, filepath.Join(repo, ".mcp.json"), `{"mcpServers":{"gh":{"command":"npx"}}}`)

	a, ok := Inspect(env, "devin", sub)
	if !ok || a.Label != "Devin" {
		t.Fatalf("devin: %+v", a)
	}
	if got := names(a, GroupInstructions); got != "AGENTS.md(user) rules/style(user) CLAUDE.md(user) AGENTS.md(project) CLAUDE.md(project) AGENTS.local.md(local)" {
		t.Fatalf("instructions: %s", got)
	}
	if got := names(a, GroupSkills); got != "tide(user) ship(project) review(project)" {
		t.Fatalf("skills: %s", got)
	}
	if got := names(a, GroupCommands); got != "/fix(project)" {
		t.Fatalf("commands: %s", got)
	}
	if got := names(a, GroupSubagents); got != "reviewer(user) tester(project)" {
		t.Fatalf("subagents: %s", got)
	}
	if got := names(a, GroupMCP); got != "notion(user) db(project) me(local) gh(project)" {
		t.Fatalf("mcp: %s", got)
	}
	for _, g := range a.Groups {
		for _, it := range g.Items {
			if strings.Contains(it.Detail, "secret") {
				t.Fatalf("a URL leaked: %+v", it)
			}
		}
	}
	if len(a.Notes) != 1 || !strings.Contains(a.Notes[0], "trust") {
		t.Fatalf("notes %v", a.Notes)
	}

	// read_config_from turns Claude's files off, and the project's word wins.
	put(t, filepath.Join(dh, "config.json"), `{"read_config_from":{"claude":false}}`)
	a, _ = Inspect(env, "devin", sub)
	if got := names(a, GroupMCP); got != "notion(user) db(project) me(local)" {
		t.Fatalf("mcp without claude: %s", got)
	}
	if strings.Contains(names(a, GroupInstructions), "CLAUDE.md") || strings.Contains(names(a, GroupSkills), "review") {
		t.Fatalf("claude's files are still read: %s / %s", names(a, GroupInstructions), names(a, GroupSkills))
	}
	put(t, filepath.Join(repo, ".devin", "config.json"), `{"read_config_from":{"claude":true}}`)
	a, _ = Inspect(env, "devin", sub)
	if !strings.Contains(names(a, GroupMCP), "gh(project)") {
		t.Fatalf("the project's setting did not win: %s", names(a, GroupMCP))
	}
}

// Syncing to Devin writes only what it does not already read: Claude's
// files it reads itself, Codex's it does not.
func TestSyncToDevin(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := syncRepo(t)
	res, err := Sync(root, "claude", []string{"devin"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if res.Writes() != 0 {
		t.Fatalf("wrote for Devin what it reads itself:\n%s", changeLines(res))
	}

	// Told not to read Claude's files, it gets its own.
	write(t, filepath.Join(root, ".devin", "config.json"), `{"read_config_from":{"claude":false}}`)
	res, err = Sync(root, "claude", []string{"devin"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if c := find(t, res, "devin", SyncInstructions, "AGENTS.md"); !c.Done || !strings.Contains(read(t, filepath.Join(root, "AGENTS.md")), "Run the tests.") {
		t.Fatalf("instructions: %+v", c)
	}
	if c := find(t, res, "devin", SyncSkill, "review"); !c.Done || !isFile(filepath.Join(root, ".agents", "skills", "review", "SKILL.md")) {
		t.Fatalf("skill: %+v", c)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(read(t, filepath.Join(root, ".devin", "mcp_config.json"))), &doc); err != nil {
		t.Fatal(err)
	}
	if got := obj(obj(doc, "mcpServers"), "gh"); str(obj(got, "env"), "GITHUB_TOKEN") != "${env:GITHUB_TOKEN}" {
		t.Fatalf("devin gh %v", got)
	}
	if got := obj(obj(doc, "mcpServers"), "docs"); str(got, "transport") != "http" || str(got, "url") == "" {
		t.Fatalf("devin docs %v", got)
	}
	if c := find(t, res, "devin", SyncMCP, "paid"); c.Action != ActionSkip {
		t.Fatalf("a secret travelled: %+v", c)
	}
	// And Devin, read as a source, has what was written for it.
	back, err := Sync(root, "devin", []string{"gemini"}, false)
	if err != nil || find(t, back, "gemini", SyncMCP, "gh").Action != ActionCreate {
		t.Fatalf("from devin: %v\n%s", err, changeLines(back))
	}
}

func TestSyncUserToDevin(t *testing.T) {
	home, e := userHome(t)
	res, err := SyncUser(e, "claude", []string{"devin"}, true)
	if err != nil {
		t.Fatal(err)
	}
	// Instructions and servers it reads from Claude's own files; the skill
	// it does not, so it is linked into Devin's folder.
	if c := find(t, res, "devin", SyncInstructions, "CLAUDE.md"); c.Action != ActionSame {
		t.Fatalf("instructions: %+v", c)
	}
	if c := find(t, res, "devin", SyncMCP, "gh"); c.Action != ActionSame {
		t.Fatalf("server: %+v", c)
	}
	if !isFile(filepath.Join(home, ".config", "devin", "skills", "tide", "SKILL.md")) {
		t.Fatalf("skill not linked:\n%s", changeLines(res))
	}
	if _, err := os.Stat(filepath.Join(home, ".agents", "skills", "tide")); !os.IsNotExist(err) {
		t.Fatal("linked where Devin does not look")
	}
}

// In your home too: with Devin told not to read Claude's files, it is
// given its own instructions and servers.
func TestSyncUserToDevinWithoutClaude(t *testing.T) {
	home, e := userHome(t)
	write(t, filepath.Join(home, ".config", "devin", "config.json"), `{"read_config_from":{"claude":false}}`)
	res, err := SyncUser(e, "claude", []string{"devin"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if c := find(t, res, "devin", SyncInstructions, "AGENTS.md"); !c.Done || c.Path != "~/.config/devin/AGENTS.md" {
		t.Fatalf("instructions: %+v", c)
	}
	if got := read(t, filepath.Join(home, ".config", "devin", "AGENTS.md")); !strings.Contains(got, "British English") {
		t.Fatalf("AGENTS.md:\n%s", got)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(read(t, filepath.Join(home, ".config", "devin", "mcp_config.json"))), &doc); err != nil {
		t.Fatal(err)
	}
	if got := obj(obj(doc, "mcpServers"), "gh"); str(obj(got, "env"), "GH_TOKEN") != "${env:GH_TOKEN}" {
		t.Fatalf("devin gh %v", doc)
	}
	// The same setting keeps the library from counting on Claude's copy.
	libHome := filepath.Join(home, "lib")
	t.Setenv("CONCH_HOME", libHome)
	mustSave(t, Library{Servers: []LibServer{{Name: "docs", URL: "https://docs.example/mcp", Agents: []string{"claude", "devin"}}}})
	write(t, filepath.Join(home, ".claude.json"), `{"mcpServers":{"docs":{"type":"http","url":"https://docs.example/mcp"}}}`)
	_, cs, err := LibraryStatus(e)
	if err != nil || !strings.Contains(cells(cs, SyncMCP, "docs"), "devin=pending") {
		t.Fatalf("library: %v %s", err, cells(cs, SyncMCP, "docs"))
	}
}
