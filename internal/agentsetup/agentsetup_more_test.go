package agentsetup

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/proto"
)

func a6Env(home string, vars map[string]string) Env {
	return Env{Home: home, Getenv: func(k string) string { return vars[k] }, GOOS: "linux"}
}

func a6Item(a proto.AgentSetup, group, name string) (proto.SetupItem, bool) {
	for _, g := range a.Groups {
		if g.Title == group {
			for _, it := range g.Items {
				if it.Name == name {
					return it, true
				}
			}
		}
	}
	return proto.SetupItem{}, false
}

func TestA6NamesCurrentEnvUnknown(t *testing.T) {
	if got := strings.Join(Names(), ","); got != "claude,codex,gemini,opencode" {
		t.Fatalf("Names: %s", got)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("A6_AGENTSETUP", "x")
	e := CurrentEnv()
	if e.Home != home || e.GOOS != runtime.GOOS || e.get("A6_AGENTSETUP") != "x" {
		t.Fatalf("CurrentEnv: %+v", e)
	}
	if (Env{}).get("HOME") != "" {
		t.Fatal("nil Getenv")
	}
	if _, ok := Inspect(e, "aider", home); ok {
		t.Fatal("unknown agent inspected")
	}
}

func TestA6EmptyHomeEveryAgent(t *testing.T) {
	env, repo, _ := fixture(t)
	for _, name := range Names() {
		a, ok := Inspect(env, name, repo)
		if !ok {
			t.Fatalf("%s unknown", name)
		}
		if a.Agent != name || a.Label == "" {
			t.Fatalf("%s: %+v", name, a)
		}
		// Only conch's own entry is reported, and no notes without config.
		if len(a.Groups) != 1 || a.Groups[0].Title != GroupConch || len(a.Notes) != 0 {
			t.Fatalf("%s empty setup: %+v", name, a)
		}
	}
}

func TestA6ClaudeDetails(t *testing.T) {
	env, repo, sub := fixture(t)
	cfg := filepath.Join(filepath.Dir(env.Home), "claude-cfg")
	env = a6Env(env.Home, map[string]string{"CLAUDE_CONFIG_DIR": cfg})

	put(t, filepath.Join(cfg, "CLAUDE.md"), "")
	put(t, filepath.Join(cfg, "rules", "go", "style.md"), "")
	put(t, filepath.Join(cfg, "rules", ".hidden", "x.md"), "")
	put(t, filepath.Join(cfg, "rules", "notes.txt"), "")
	put(t, filepath.Join(cfg, "agents", "reviewer.md"), "")
	put(t, filepath.Join(cfg, "skills", "fmt", "SKILL.md"), "---\ndescription: \"Formats code\"\n---\n")
	put(t, filepath.Join(repo, "CLAUDE.md"), "")
	put(t, filepath.Join(repo, "CLAUDE.local.md"), "")
	put(t, filepath.Join(sub, ".claude", "CLAUDE.md"), "")
	put(t, filepath.Join(sub, ".claude", "rules", "svc.md"), "")
	put(t, filepath.Join(sub, ".claude", "agents", "svc-bot.md"), "")
	put(t, filepath.Join(repo, ".claude", "skills", "release", "SKILL.md"), "---\nname: release-it\n---\n")

	// Plugins: enabled by the user, disabled again locally; one enabled
	// but not installed; one with an MCP file without the wrapper key.
	plugin := filepath.Join(env.Home, "plugins", "tools")
	put(t, filepath.Join(cfg, "settings.json"), `{"enabledPlugins": {"tools@m": true, "gone@m": true, "off@m": true}}`)
	put(t, filepath.Join(repo, ".claude", "settings.json"), `{"enableAllProjectMcpServers": true}`)
	put(t, filepath.Join(repo, ".claude", "settings.local.json"), `{"enabledPlugins": {"off@m": false}}`)
	put(t, filepath.Join(cfg, "plugins", "installed_plugins.json"), `{"plugins": {
		"tools@m": [{"installPath": "`+plugin+`"}],
		"off@m": [{"installPath": "/nowhere"}],
		"weird@m": "not a list"}}`)
	put(t, filepath.Join(plugin, "skills", "lint", "SKILL.md"), "---\nname: lint\n---\n")
	put(t, filepath.Join(plugin, "agents", "helper.md"), "")
	put(t, filepath.Join(plugin, ".mcp.json"), `{"pgsql": {"command": "secret-binary", "env": {"TOKEN": "t"}}}`)

	put(t, filepath.Join(repo, ".mcp.json"), `{"mcpServers": {"a": {"type": "sse"}, "b": {"url": "u"}}}`)
	put(t, filepath.Join(cfg, ".claude.json"), `{
		"mcpServers": {"global": {"type": "stdio"}},
		"projects": {
			"`+repo+`": {"mcpServers": {"mine": {"url": "https://x"}}, "disabledMcpjsonServers": ["b"]},
			"`+filepath.Dir(repo)+`": {"hasTrustDialogAccepted": true}
		}}`)

	a, _ := Inspect(env, "claude", sub)
	if got := names(a, GroupInstructions); got != "CLAUDE.md(user) rules/go:style(user) CLAUDE.md(project) CLAUDE.local.md(local) CLAUDE.md(project) rules/svc(project)" {
		t.Fatalf("instructions: %s", got)
	}
	if got := names(a, GroupSkills); got != "fmt(user) release-it(project) lint(plugin)" {
		t.Fatalf("skills: %s", got)
	}
	if it, _ := a6Item(a, GroupSkills, "fmt"); it.Detail != "Formats code" {
		t.Fatalf("skill detail: %+v", it)
	}
	if it, _ := a6Item(a, GroupSkills, "lint"); it.Detail != "tools@m" {
		t.Fatalf("plugin skill detail: %+v", it)
	}
	if got := names(a, GroupSubagents); got != "reviewer(user) svc-bot(project) helper(plugin)" {
		t.Fatalf("subagents: %s", got)
	}
	if got := names(a, GroupPlugins); got != "gone@m(user) tools@m(user)" {
		t.Fatalf("plugins: %s", got)
	}
	if it, _ := a6Item(a, GroupPlugins, "gone@m"); !strings.Contains(it.Detail, "not installed") {
		t.Fatalf("gone plugin: %+v", it)
	}
	if it, _ := a6Item(a, GroupPlugins, "tools@m"); strings.Contains(it.Detail, "not installed") || it.Path != "~/plugins/tools" {
		t.Fatalf("installed plugin: %+v", it)
	}
	if got := names(a, GroupMCP); got != "pgsql(plugin) global(user) mine(local) a(project) b(project)" {
		t.Fatalf("mcp: %s", got)
	}
	if it, _ := a6Item(a, GroupMCP, "pgsql"); it.Detail != "stdio · tools@m" || strings.Contains(it.Detail, "secret") {
		t.Fatalf("plugin mcp: %+v", it)
	}
	if it, _ := a6Item(a, GroupMCP, "a"); it.Detail != "sse" {
		t.Fatalf("all project servers enabled: %+v", it)
	}
	if it, _ := a6Item(a, GroupMCP, "b"); it.Detail != "http · rejected here" {
		t.Fatalf("rejected server: %+v", it)
	}
	if len(a.Notes) != 0 {
		t.Fatalf("a trusted parent folder should count: %v", a.Notes)
	}
	// Group order follows groupOrder with conch last.
	var titles []string
	for _, g := range a.Groups {
		titles = append(titles, g.Title)
	}
	if strings.Join(titles, "|") != "Instructions|Skills|Subagents|MCP servers|Plugins|Added by conch" {
		t.Fatalf("group order: %v", titles)
	}
}

func TestA6ClaudeProjectMCPApproval(t *testing.T) {
	env, repo, _ := fixture(t)
	put(t, filepath.Join(repo, ".mcp.json"), `{"mcpServers": {"x": {}}}`)
	a, _ := Inspect(env, "claude", repo)
	if it, _ := a6Item(a, GroupMCP, "x"); it.Detail != "stdio · Claude asks before using it" {
		t.Fatalf("unapproved: %+v", it)
	}
	if len(a.Notes) != 0 {
		t.Fatalf("no state file, no trust note: %v", a.Notes)
	}
}

func TestA6ClaudeInHomeDir(t *testing.T) {
	env, _, _ := fixture(t)
	// Running in the home directory: ~/.claude is the user dir, not a project's.
	put(t, filepath.Join(env.Home, ".claude", "CLAUDE.md"), "")
	put(t, filepath.Join(env.Home, ".claude", "commands", "c.md"), "")
	a, _ := Inspect(env, "claude", env.Home)
	if got := names(a, GroupInstructions); got != "CLAUDE.md(user)" {
		t.Fatalf("instructions in home: %s", got)
	}
	if got := names(a, GroupCommands); got != "/c(user)" {
		t.Fatalf("commands in home: %s", got)
	}
	if it, _ := a6Item(a, GroupInstructions, "CLAUDE.md"); it.Path != "~/.claude/CLAUDE.md" {
		t.Fatalf("tilde path: %+v", it)
	}
}

func TestA6CodexDetails(t *testing.T) {
	env, repo, sub := fixture(t)
	codexHome := filepath.Join(filepath.Dir(env.Home), "codex-home")
	env = a6Env(env.Home, map[string]string{"CODEX_HOME": codexHome})
	put(t, filepath.Join(codexHome, "AGENTS.override.md"), "")
	put(t, filepath.Join(codexHome, "AGENTS.md"), "")
	put(t, filepath.Join(env.Home, ".agents", "skills", "s1", "SKILL.md"), "")
	put(t, filepath.Join(sub, ".agents", "skills", "s2", "SKILL.md"), "")
	put(t, filepath.Join(sub, ".codex", "config.toml"), "[mcp_servers.local]\ncommand = \"x\"\n")
	put(t, filepath.Join(repo, ".codex", "config.toml"), "not = [valid toml")

	a, _ := Inspect(env, "codex", sub)
	if got := names(a, GroupInstructions); got != "AGENTS.override.md(user)" {
		t.Fatalf("override wins: %s", got)
	}
	if got := names(a, GroupSkills); got != "s2(project) s1(user)" {
		t.Fatalf("skills: %s", got)
	}
	// No user config.toml: no note, and project servers are marked untrusted.
	if len(a.Notes) != 0 {
		t.Fatalf("notes: %v", a.Notes)
	}
	if it, ok := a6Item(a, GroupMCP, "local"); !ok || it.Detail != "stdio · used once the folder is trusted" || it.Scope != ScopeProject {
		t.Fatalf("project mcp: %+v", it)
	}

	put(t, filepath.Join(codexHome, "config.toml"), "[projects.\""+sub+"\"]\ntrust_level = \"trusted\"\n")
	a, _ = Inspect(env, "codex", sub)
	if it, _ := a6Item(a, GroupMCP, "local"); it.Detail != "stdio" {
		t.Fatalf("trusted project mcp: %+v", it)
	}
	// Outside a repository only dir counts.
	outside := filepath.Join(filepath.Dir(env.Home), "loose")
	put(t, filepath.Join(outside, "AGENTS.md"), "")
	a, _ = Inspect(env, "codex", outside)
	if got := names(a, GroupInstructions); got != "AGENTS.override.md(user) AGENTS.md(project)" || len(a.Notes) != 1 {
		t.Fatalf("outside repo: %s %v", got, a.Notes)
	}
}

func TestA6GeminiDetails(t *testing.T) {
	env, repo, sub := fixture(t)
	gh := filepath.Join(env.Home, ".gemini")
	put(t, filepath.Join(gh, "settings.json"), `{"contextFileName": "USER.md", "mcpServers": {"u": {"url": "x", "disabled": true}}}`)
	put(t, filepath.Join(sub, ".gemini", "settings.json"), `{"context": {"fileName": "PROJ.md"}, "mcpServers": {"p": {"type": "http"}}}`)
	put(t, filepath.Join(gh, "PROJ.md"), "")
	put(t, filepath.Join(gh, "USER.md"), "")
	put(t, filepath.Join(repo, "PROJ.md"), "")
	put(t, filepath.Join(sub, "PROJ.md"), "")
	put(t, filepath.Join(gh, "skills", "g1", "SKILL.md"), "")
	put(t, filepath.Join(sub, ".gemini", "skills", "g2", "SKILL.md"), "")
	put(t, filepath.Join(sub, ".agents", "skills", "g3", "SKILL.md"), "")
	put(t, filepath.Join(sub, ".gemini", "commands", "deploy.toml"), "")
	put(t, filepath.Join(sub, ".gemini", "commands", "readme.md"), "")
	// Extensions: no name falls back to the dir; custom context files; a
	// broken manifest is skipped.
	ext := filepath.Join(gh, "extensions", "noname")
	put(t, filepath.Join(ext, "gemini-extension.json"), `{"contextFileName": ["CTX.md", "MISSING.md"]}`)
	put(t, filepath.Join(ext, "CTX.md"), "")
	put(t, filepath.Join(ext, "commands", "x.toml"), "")
	put(t, filepath.Join(ext, "skills", "e1", "SKILL.md"), "")
	put(t, filepath.Join(gh, "extensions", "broken", "gemini-extension.json"), `{`)
	put(t, filepath.Join(gh, "extensions", "no-manifest", "GEMINI.md"), "")

	a, _ := Inspect(env, "gemini", sub)
	if got := names(a, GroupInstructions); got != "PROJ.md(user) PROJ.md(project) PROJ.md(project) CTX.md(extension)" {
		t.Fatalf("project context name wins: %s", got)
	}
	if got := names(a, GroupSkills); got != "g1(user) g2(project) g3(project) e1(extension)" {
		t.Fatalf("skills: %s", got)
	}
	if got := names(a, GroupCommands); got != "/deploy(project) /x(extension)" {
		t.Fatalf("commands: %s", got)
	}
	if got := names(a, GroupMCP); got != "u(user) p(project)" {
		t.Fatalf("mcp: %s", got)
	}
	if it, _ := a6Item(a, GroupMCP, "u"); it.Detail != "http · disabled" {
		t.Fatalf("disabled user server: %+v", it)
	}
	if got := names(a, GroupPlugins); got != "noname(extension)" {
		t.Fatalf("extensions: %s", got)
	}

	// Legacy contextFileName in user settings applies without project settings.
	a, _ = Inspect(env, "gemini", repo)
	if got := names(a, GroupInstructions); !strings.HasPrefix(got, "USER.md(user)") {
		t.Fatalf("legacy context name: %s", got)
	}
}

func TestA6OpenCodeDetails(t *testing.T) {
	env, repo, sub := fixture(t)
	xdg := filepath.Join(filepath.Dir(env.Home), "xdg")
	oc := filepath.Join(xdg, "opencode")
	vars := map[string]string{"XDG_CONFIG_HOME": xdg}
	env = a6Env(env.Home, vars)

	put(t, filepath.Join(oc, "config.json"), `{"plugin": "user-plugin", "agent": {"zeta": {}, "alpha": {}}}`)
	put(t, filepath.Join(oc, "opencode.jsonc"), `{/* broken`)
	put(t, filepath.Join(oc, "skill", "u1", "SKILL.md"), "")
	put(t, filepath.Join(oc, "command", "c1.md"), "")
	put(t, filepath.Join(oc, "agents", "a1.md"), "")
	put(t, filepath.Join(env.Home, ".claude", "CLAUDE.md"), "")
	put(t, filepath.Join(env.Home, ".claude", "skills", "cs", "SKILL.md"), "")
	put(t, filepath.Join(sub, "CLAUDE.md"), "")
	put(t, filepath.Join(repo, "AGENTS.md"), "")
	put(t, filepath.Join(sub, ".claude", "skills", "ps", "SKILL.md"), "")
	put(t, filepath.Join(sub, ".opencode", "skills", "os", "SKILL.md"), "")
	put(t, filepath.Join(repo, ".opencode", "opencode.json"), `{"plugin": ["p1", 5, "p2"], "mcp": {"remote": {"type": "remote"}}}`)
	put(t, filepath.Join(repo, ".opencode", "commands", "ship.md"), "")
	put(t, filepath.Join(repo, ".opencode", "agent", "bot.md"), "")

	a, _ := Inspect(env, "opencode", sub)
	// The nearest instruction file wins: CLAUDE.md in sub before AGENTS.md in repo.
	if got := names(a, GroupInstructions); got != "CLAUDE.md(user) CLAUDE.md(project)" {
		t.Fatalf("instructions: %s", got)
	}
	if got := names(a, GroupSkills); got != "u1(user) cs(user) os(project) ps(project)" {
		t.Fatalf("skills: %s", got)
	}
	if got := names(a, GroupCommands); got != "/c1(user) /ship(project)" {
		t.Fatalf("commands: %s", got)
	}
	if got := names(a, GroupSubagents); got != "bot(project) a1(user) alpha(user) zeta(user)" {
		t.Fatalf("subagents: %s", got)
	}
	if got := names(a, GroupPlugins); got != "user-plugin(user) p1(project) p2(project)" {
		t.Fatalf("plugins: %s", got)
	}
	if got := names(a, GroupMCP); got != "remote(project)" {
		t.Fatalf("mcp: %s", got)
	}

	// Claude compatibility off, and a user AGENTS.md.
	vars["OPENCODE_DISABLE_CLAUDE_CODE"] = "1"
	a, _ = Inspect(env, "opencode", sub)
	if got := names(a, GroupInstructions); got != "AGENTS.md(project)" {
		t.Fatalf("no claude compat: %s", got)
	}
	if got := names(a, GroupSkills); got != "u1(user) os(project)" {
		t.Fatalf("no claude skills: %s", got)
	}
	put(t, filepath.Join(oc, "AGENTS.md"), "")
	delete(vars, "OPENCODE_DISABLE_CLAUDE_CODE")
	a, _ = Inspect(env, "opencode", sub)
	if got := names(a, GroupInstructions); !strings.HasPrefix(got, "AGENTS.md(user) CLAUDE.md(project)") {
		t.Fatalf("user AGENTS.md: %s", got)
	}
}

func TestA6MarkMissingNewGroupAndOrder(t *testing.T) {
	here := proto.AgentSetup{Groups: []proto.SetupGroup{
		{Title: GroupConch, Items: []proto.SetupItem{{Name: "hooks", Scope: ScopeConch}}},
		{Title: GroupSkills, Items: []proto.SetupItem{{Name: "a", Scope: ScopeUser}}},
	}}
	main := proto.AgentSetup{Groups: []proto.SetupGroup{
		{Title: GroupSkills, Items: []proto.SetupItem{{Name: "a", Scope: ScopeUser}, {Name: "a", Scope: ScopeProject}}},
		{Title: GroupInstructions, Items: []proto.SetupItem{{Name: "AGENTS.md", Scope: ScopeProject}}},
		{Title: "Custom", Items: []proto.SetupItem{{Name: "z"}}},
	}}
	MarkMissing(&here, main)
	var titles []string
	for _, g := range here.Groups {
		titles = append(titles, g.Title)
	}
	if strings.Join(titles, "|") != "Instructions|Skills|Added by conch|Custom" {
		t.Fatalf("groups: %v", titles)
	}
	if got := names(here, GroupSkills); got != "a(user) a(project)!" {
		t.Fatalf("skills: %s", got)
	}
	if got := names(here, GroupInstructions); got != "AGENTS.md(project)!" {
		t.Fatalf("instructions: %s", got)
	}
	if main.Groups[1].Items[0].Missing {
		t.Fatal("main modified")
	}
	// Nothing missing: unchanged.
	before := len(here.Groups)
	MarkMissing(&here, proto.AgentSetup{})
	if len(here.Groups) != before {
		t.Fatal("empty main changed groups")
	}
}

func TestA6ReportAddDedup(t *testing.T) {
	r := newReport()
	dir := t.TempDir()
	r.add(GroupSkills, proto.SetupItem{Name: "x", Path: dir})
	r.add(GroupSkills, proto.SetupItem{Name: "x", Path: dir})
	r.add(GroupSkills, proto.SetupItem{Name: "y", Path: dir})   // other name: kept
	r.add(GroupCommands, proto.SetupItem{Name: "x", Path: dir}) // other group: kept
	r.add(GroupSkills, proto.SetupItem{Name: "nopath"})         // no path: never deduped
	r.add(GroupSkills, proto.SetupItem{Name: "nopath"})
	r.add(GroupSkills, proto.SetupItem{Name: "m", Path: "/missing/p"}) // unresolvable path
	r.add(GroupSkills, proto.SetupItem{Name: "m", Path: "/missing/p"})
	if len(r.groups[GroupSkills]) != 5 || len(r.groups[GroupCommands]) != 1 {
		t.Fatalf("dedup: %+v", r.groups)
	}
}

func TestA6PathHelpers(t *testing.T) {
	if got := upFrom("/a/b/c", "/a"); !reflect.DeepEqual(got, []string{"/a/b/c", "/a/b", "/a"}) {
		t.Fatalf("upFrom stop: %q", got)
	}
	if got := upFrom("/a/b", ""); !reflect.DeepEqual(got, []string{"/a/b", "/a"}) {
		t.Fatalf("upFrom root: %q", got)
	}
	// A stop that is not an ancestor goes to the filesystem root.
	if got := upFrom("/a/b", "/x"); !reflect.DeepEqual(got, []string{"/a/b", "/a", "/"}) {
		t.Fatalf("upFrom unrelated stop: %q", got)
	}
	if got := upFrom("/", ""); len(got) != 0 {
		t.Fatalf("upFrom /: %q", got)
	}
	if got := downTo("/a", "/a/b/c"); !reflect.DeepEqual(got, []string{"/a", "/a/b", "/a/b/c"}) {
		t.Fatalf("downTo: %q", got)
	}
	if got := projectDirs("/a/b", ""); !reflect.DeepEqual(got, []string{"/a/b"}) {
		t.Fatalf("projectDirs outside repo: %q", got)
	}
	base, _ := filepath.EvalSymlinks(t.TempDir())
	if RepoRoot(base) != "" {
		t.Fatalf("RepoRoot without .git: %q", RepoRoot(base))
	}
	put(t, filepath.Join(base, "r", ".git"), "gitdir: elsewhere")
	os.MkdirAll(filepath.Join(base, "r", "x", "y"), 0o755)
	if got := RepoRoot(filepath.Join(base, "r", "x", "y")); got != filepath.Join(base, "r") {
		t.Fatalf("RepoRoot: %q", got)
	}
	e := Env{Home: "/home/u"}
	for in, want := range map[string]string{"/home/u": "~", "/home/u/x": "~/x", "/home/user2": "/home/user2", "/etc": "/etc", "": ""} {
		if got := tilde(e, in); got != want {
			t.Errorf("tilde(%q) = %q", in, got)
		}
	}
	if tilde(Env{}, "/x") != "/x" {
		t.Fatal("tilde without home")
	}
	if got := uniq("a", "", "b", "a"); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("uniq: %q", got)
	}
}

func TestA6Frontmatter(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		src, name, desc string
	}{
		{"---\nname: a\ndescription: plain\n---\nname: body\n", "a", "plain"},
		{"---\nname: 'quoted'\ndescription: \"dq: with colon\"\n---\n", "quoted", "dq: with colon"},
		{"---\ndescription: >-\n  folded\n  over lines\nname: after\n---\n", "after", "folded over lines"},
		{"---\ndescription: |\n\tliteral\n---\n", "", "literal"},
		{"---\ndescription:\n  on next line\n---\n", "", "on next line"},
		{"no frontmatter\nname: x\n", "", ""},
		{"", "", ""},
		{"---\nname: unterminated\n", "unterminated", ""},
		{"---\njunk line\nother: value\nname: n\n---\n", "n", ""},
		{"---\ndescription: " + strings.Repeat("d", 100) + "\n---\n", "", strings.Repeat("d", 89) + "…"},
	}
	for i, c := range cases {
		p := filepath.Join(dir, "s"+string(rune('a'+i))+".md")
		os.WriteFile(p, []byte(c.src), 0o644)
		name, desc := frontmatter(p)
		if name != c.name || desc != c.desc {
			t.Errorf("case %d: got %q %q, want %q %q", i, name, desc, c.name, c.desc)
		}
	}
	if n, d := frontmatter(filepath.Join(dir, "missing.md")); n != "" || d != "" {
		t.Fatal("missing file")
	}
	// Keys past the first 60 lines are not read.
	p := filepath.Join(dir, "long.md")
	os.WriteFile(p, []byte("---\n"+strings.Repeat("x: y\n", 70)+"name: late\n---\n"), 0o644)
	if n, _ := frontmatter(p); n != "" {
		t.Fatalf("late name read: %q", n)
	}
}

func TestA6FrontmatterNestedKeys(t *testing.T) {
	p := filepath.Join(t.TempDir(), "SKILL.md")
	os.WriteFile(p, []byte("---\nname: real-name\nmetadata:\n  name: nested-name\n  description: nested\ndescription: top\n---\n"), 0o644)
	name, desc := frontmatter(p)
	// An indented key belongs to the block above it: metadata's own name
	// must not overwrite the skill's.
	if name != "real-name" {
		t.Fatalf("name: %q, want real-name", name)
	}
	if desc != "top" {
		t.Fatalf("desc: %q", desc)
	}
}

func TestA6ConfigReaders(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.json")
	if readJSON(p) != nil {
		t.Fatal("missing JSON")
	}
	os.WriteFile(p, []byte(`[1,2]`), 0o644)
	if readJSON(p) != nil {
		t.Fatal("array is not an object")
	}
	os.WriteFile(p, []byte("{\n // c\n \"a\": \"http://x\", /* multi\n line */ \"b\": [1,\n],\n}\n"), 0o644)
	if m := readJSON(p); m == nil || m["a"] != "http://x" {
		t.Fatalf("jsonc: %v", m)
	}
	os.WriteFile(p, []byte(`{"a": "unterminated`), 0o644)
	if readJSON(p) != nil {
		t.Fatal("broken json")
	}

	tp := filepath.Join(dir, "c.toml")
	if readTOML(tp) != nil {
		t.Fatal("missing toml")
	}
	os.WriteFile(tp, []byte("a = 1\n[b]\nc = \"d\"\n"), 0o644)
	if m := readTOML(tp); str(obj(m, "b"), "c") != "d" {
		t.Fatalf("toml: %v", m)
	}

	for in, want := range map[string]string{
		`{"s": "a\"//b", "t": 1}`: `{"s": "a\"//b", "t": 1}`,
		`{"x": 1 /* unterminated`: `{"x": 1 `,
		`{"x": [1, 2 , ] , }`:     `{"x": [1, 2  ]  }`,
		"{\"x\": 1 // tail":       "{\"x\": 1 \n",
		`"ends with backslash \`:  `"ends with backslash \`,
		`{"k": "v",` + "\n\t}":    `{"k": "v"` + "\n\t}",
	} {
		if got := string(stripJSONC([]byte(in))); got != want {
			t.Errorf("stripJSONC(%q) = %q, want %q", in, got, want)
		}
	}

	if got := strs("one"); !reflect.DeepEqual(got, []string{"one"}) {
		t.Fatalf("strs string: %q", got)
	}
	if got := strs([]any{"a", 1, nil, "b"}); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("strs list: %q", got)
	}
	if strs(42) != nil || strs(nil) != nil {
		t.Fatal("strs other")
	}
	if obj(nil, "x") != nil || str(map[string]any{"x": 1}, "x") != "" {
		t.Fatal("obj/str")
	}
	if sortedKeys(nil) == nil || len(sortedKeys(map[string]any{"b": 1, "a": 2})) != 2 || sortedKeys(map[string]any{"b": 1, "a": 2})[0] != "a" {
		t.Fatal("sortedKeys")
	}
}

func TestA6Transport(t *testing.T) {
	for _, c := range []struct {
		v    any
		want string
	}{
		{nil, "stdio"},
		{"not a map", "stdio"},
		{map[string]any{"type": "sse"}, "sse"},
		{map[string]any{"type": 5, "url": "u"}, "http"},
		{map[string]any{"httpUrl": "u"}, "http"},
		{map[string]any{"enabled": false}, "stdio · disabled"},
		{map[string]any{"enabled": true, "disabled": false}, "stdio"},
		{map[string]any{"command": "secret", "env": map[string]any{"K": "V"}}, "stdio"},
	} {
		if got := transport(c.v); got != c.want {
			t.Errorf("transport(%v) = %q, want %q", c.v, got, c.want)
		}
	}
}

func TestA6SkillsAndMarkdownEdges(t *testing.T) {
	base := t.TempDir()
	r := newReport()
	r.skills(filepath.Join(base, "missing"), ScopeUser, "")
	put(t, filepath.Join(base, "skills", "plain-file"), "")
	put(t, filepath.Join(base, "skills", "nodoc", "README.md"), "")
	put(t, filepath.Join(base, "skills", ".hidden-file"), "")
	put(t, filepath.Join(base, "skills", ".sys", "inner", "SKILL.md"), "---\nname: inner\n---\n")
	put(t, filepath.Join(base, "skills", "withdesc", "SKILL.md"), "---\ndescription: from file\n---\n")
	r.skills(filepath.Join(base, "skills"), ScopeUser, "")
	r.skills(filepath.Join(base, "skills"), ScopeUser, "") // twice: deduped
	a := r.result(Env{}, "x", "X")
	if got := names(a, GroupSkills); got != "inner(system) withdesc(user)" {
		t.Fatalf("skills: %s", got)
	}
	if it, _ := a6Item(a, GroupSkills, "withdesc"); it.Detail != "from file" {
		t.Fatalf("detail: %+v", it)
	}

	r = newReport()
	r.markdown(GroupCommands, filepath.Join(base, "none"), ".md", "/", ScopeUser, "")
	put(t, filepath.Join(base, "cmds", "a", "b", "c.md"), "")
	put(t, filepath.Join(base, "cmds", ".git", "x.md"), "")
	r.markdown(GroupCommands, filepath.Join(base, "cmds"), ".md", "/", ScopeUser, "d")
	a = r.result(Env{}, "x", "X")
	if got := names(a, GroupCommands); got != "/a:b:c(user)" {
		t.Fatalf("markdown: %s", got)
	}
	// A hidden root dir itself is walked.
	r = newReport()
	put(t, filepath.Join(base, ".hiddenroot", "h.md"), "")
	r.markdown(GroupCommands, filepath.Join(base, ".hiddenroot"), ".md", "", ScopeUser, "")
	if len(r.groups[GroupCommands]) != 1 {
		t.Fatalf("hidden root: %+v", r.groups)
	}
	if os.Geteuid() != 0 {
		locked := filepath.Join(base, "locked")
		put(t, filepath.Join(locked, "inside", "x.md"), "")
		os.Chmod(filepath.Join(locked, "inside"), 0)
		t.Cleanup(func() { os.Chmod(filepath.Join(locked, "inside"), 0o755) })
		r = newReport()
		r.markdown(GroupCommands, locked, ".md", "", ScopeUser, "")
		if len(r.groups[GroupCommands]) != 0 {
			t.Fatalf("unreadable dir: %+v", r.groups)
		}
	}
}

func TestA6DarwinManagedPath(t *testing.T) {
	env, repo, _ := fixture(t)
	env.GOOS = "darwin"
	// The managed file doesn't exist on the test machine in either location,
	// so darwin inspection just runs without reporting it.
	a, _ := Inspect(env, "claude", repo)
	for _, g := range a.Groups {
		for _, it := range g.Items {
			if it.Scope == ScopeManaged && !isFile("/Library/Application Support/ClaudeCode/CLAUDE.md") {
				t.Fatalf("managed item without a file: %+v", it)
			}
		}
	}
}
