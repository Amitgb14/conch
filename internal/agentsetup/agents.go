package agentsetup

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Amitgb14/conch/internal/proto"
)

// ---- Claude Code ----

func inspectClaude(e Env, dir, root string) *report {
	r := newReport()
	cfg := filepath.Join(e.Home, ".claude")
	stateFile := filepath.Join(e.Home, ".claude.json")
	if d := e.get("CLAUDE_CONFIG_DIR"); d != "" {
		cfg, stateFile = d, filepath.Join(d, ".claude.json")
	}
	projectRoot := root
	if projectRoot == "" {
		projectRoot = dir
	}
	r.add(GroupConch, proto.SetupItem{Name: "state hooks", Scope: ScopeConch, Detail: "--settings file, merged with yours"})

	managed := "/etc/claude-code"
	if e.GOOS == "darwin" {
		managed = "/Library/Application Support/ClaudeCode"
	}
	r.memory(e.Home, filepath.Join(managed, "CLAUDE.md"), ScopeManaged, "")
	r.memory(e.Home, filepath.Join(cfg, "CLAUDE.md"), ScopeUser, "")
	r.markdown(GroupInstructions, filepath.Join(cfg, "rules"), ".md", "rules/", ScopeUser, "")
	for _, d := range downTo("", dir) {
		if filepath.Join(d, ".claude") == cfg {
			continue // the user directory, not a project's
		}
		r.memory(e.Home, filepath.Join(d, "CLAUDE.md"), ScopeProject, "")
		r.memory(e.Home, filepath.Join(d, ".claude", "CLAUDE.md"), ScopeProject, "")
		r.memory(e.Home, filepath.Join(d, "CLAUDE.local.md"), ScopeLocal, "")
		r.markdown(GroupInstructions, filepath.Join(d, ".claude", "rules"), ".md", "rules/", ScopeProject, "")
	}

	r.skills(filepath.Join(cfg, "skills"), ScopeUser, "")
	r.markdown(GroupCommands, filepath.Join(cfg, "commands"), ".md", "/", ScopeUser, "")
	r.markdown(GroupSubagents, filepath.Join(cfg, "agents"), ".md", "", ScopeUser, "")
	for _, d := range projectDirs(dir, root) {
		if filepath.Join(d, ".claude") == cfg {
			continue
		}
		r.skills(filepath.Join(d, ".claude", "skills"), ScopeProject, "")
		r.markdown(GroupCommands, filepath.Join(d, ".claude", "commands"), ".md", "/", ScopeProject, "")
		r.markdown(GroupSubagents, filepath.Join(d, ".claude", "agents"), ".md", "", ScopeProject, "")
	}

	// Plugins: enabled in any settings file, installed under ~/.claude/plugins.
	settings := []struct {
		path  string
		scope string
	}{
		{filepath.Join(cfg, "settings.json"), ScopeUser},
		{filepath.Join(projectRoot, ".claude", "settings.json"), ScopeProject},
		{filepath.Join(projectRoot, ".claude", "settings.local.json"), ScopeLocal},
	}
	enabled := map[string]string{} // plugin → scope that enabled it
	allProjectMCP := false
	for _, s := range settings {
		m := readJSON(s.path)
		for name, on := range obj(m, "enabledPlugins") {
			if b, _ := on.(bool); b {
				enabled[name] = s.scope
			} else {
				delete(enabled, name)
			}
		}
		if b, _ := m["enableAllProjectMcpServers"].(bool); b {
			allProjectMCP = true
		}
	}
	installed := obj(readJSON(filepath.Join(cfg, "plugins", "installed_plugins.json")), "plugins")
	names := make([]string, 0, len(enabled))
	for n := range enabled {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		path := ""
		if list, ok := installed[name].([]any); ok && len(list) > 0 {
			if m, ok := list[0].(map[string]any); ok {
				path = str(m, "installPath")
			}
		}
		detail := "enabled in " + enabled[name] + " settings"
		if path == "" || !isDir(path) {
			detail += " · not installed"
		}
		r.add(GroupPlugins, proto.SetupItem{Name: name, Scope: enabled[name], Path: path, Detail: detail})
		if path == "" {
			continue
		}
		r.skills(filepath.Join(path, "skills"), ScopePlugin, name)
		r.markdown(GroupCommands, filepath.Join(path, "commands"), ".md", "/", ScopePlugin, name)
		r.markdown(GroupSubagents, filepath.Join(path, "agents"), ".md", "", ScopePlugin, name)
		if m := readJSON(filepath.Join(path, ".mcp.json")); m != nil {
			servers := obj(m, "mcpServers")
			if servers == nil {
				servers = m
			}
			r.mcp(servers, ScopePlugin, "", name)
		}
	}

	// MCP servers: user and local scope live in ~/.claude.json, keyed by
	// folder; project scope in .mcp.json, which each folder must approve.
	state := readJSON(stateFile)
	r.mcp(obj(state, "mcpServers"), ScopeUser, "", "")
	projects := obj(state, "projects")
	for _, key := range uniq(dir, root) {
		r.mcp(obj(obj(projects, key), "mcpServers"), ScopeLocal, "", "")
	}
	if servers := obj(readJSON(filepath.Join(projectRoot, ".mcp.json")), "mcpServers"); len(servers) > 0 {
		var on, off []string
		for _, key := range uniq(dir, root) {
			on = append(on, strs(obj(projects, key)["enabledMcpjsonServers"])...)
			off = append(off, strs(obj(projects, key)["disabledMcpjsonServers"])...)
		}
		names := make([]string, 0, len(servers))
		for n := range servers {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			d := transport(servers[n])
			switch {
			case contains(off, n):
				d += " · rejected here"
			case !allProjectMCP && !contains(on, n):
				d += " · Claude asks before using it"
			}
			r.add(GroupMCP, proto.SetupItem{Name: n, Scope: ScopeProject, Path: filepath.Join(projectRoot, ".mcp.json"), Detail: d})
		}
	}

	if state != nil {
		trusted := false
		for _, d := range upFrom(dir, "") {
			if b, _ := obj(projects, d)["hasTrustDialogAccepted"].(bool); b {
				trusted = true
				break
			}
		}
		if !trusted {
			r.note("Claude has not trusted this folder yet and will ask when it starts.")
		}
	}
	return r
}

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func uniq(vals ...string) []string {
	var out []string
	for _, v := range vals {
		if v != "" && !contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

// ---- Codex ----

func inspectCodex(e Env, dir, root string) *report {
	r := newReport()
	home := filepath.Join(e.Home, ".codex")
	if d := e.get("CODEX_HOME"); d != "" {
		home = d
	}
	projectRoot := root
	if projectRoot == "" {
		projectRoot = dir
	}
	r.add(GroupConch, proto.SetupItem{Name: "nothing", Scope: ScopeConch, Detail: "state is read from the screen"})

	agentsMD := func(d, scope string) {
		if !r.instruction(filepath.Join(d, "AGENTS.override.md"), scope, "") {
			r.instruction(filepath.Join(d, "AGENTS.md"), scope, "")
		}
	}
	agentsMD(home, ScopeUser)
	for _, d := range downTo(projectRoot, dir) {
		agentsMD(d, ScopeProject)
	}

	for _, d := range projectDirs(dir, root) {
		r.skills(filepath.Join(d, ".agents", "skills"), ScopeProject, "")
	}
	r.skills(filepath.Join(e.Home, ".agents", "skills"), ScopeUser, "")
	r.skills(filepath.Join(home, "skills"), ScopeUser, "")
	r.skills("/etc/codex/skills", ScopeManaged, "")
	r.markdown(GroupCommands, filepath.Join(home, "prompts"), ".md", "/prompts:", ScopeUser, "")

	cfg := readTOML(filepath.Join(home, "config.toml"))
	r.mcp(obj(cfg, "mcp_servers"), ScopeUser, "", "")
	trusted := false
	for _, key := range uniq(dir, root) {
		if str(obj(obj(cfg, "projects"), key), "trust_level") == "trusted" {
			trusted = true
		}
	}
	for _, d := range projectDirs(dir, root) {
		path := filepath.Join(d, ".codex", "config.toml")
		if !isFile(path) {
			continue
		}
		detail := ""
		if !trusted {
			detail = "used once the folder is trusted"
		}
		r.mcp(obj(readTOML(path), "mcp_servers"), ScopeProject, path, detail)
	}
	if cfg != nil && !trusted {
		r.note("Codex has not trusted this folder yet and will ask when it starts.")
	}
	return r
}

// ---- Gemini CLI ----

func inspectGemini(e Env, dir, root string) *report {
	r := newReport()
	home := filepath.Join(e.Home, ".gemini")
	projectRoot := root
	if projectRoot == "" {
		projectRoot = dir
	}
	r.add(GroupConch, proto.SetupItem{Name: "state hooks", Scope: ScopeConch, Detail: "system defaults file (skipped if you set one)"})

	user := readJSON(filepath.Join(home, "settings.json"))
	projectPath := filepath.Join(dir, ".gemini", "settings.json")
	project := readJSON(projectPath)
	contextNames := func(m map[string]any) []string {
		if n := strs(obj(m, "context")["fileName"]); len(n) > 0 {
			return n
		}
		return strs(m["contextFileName"])
	}
	names := contextNames(project)
	if len(names) == 0 {
		names = contextNames(user)
	}
	if len(names) == 0 {
		names = []string{"GEMINI.md"}
	}

	for _, n := range names {
		r.memory(e.Home, filepath.Join(home, n), ScopeUser, "")
		for _, d := range downTo(projectRoot, dir) {
			r.memory(e.Home, filepath.Join(d, n), ScopeProject, "")
		}
	}

	r.skills(filepath.Join(home, "skills"), ScopeUser, "")
	r.skills(filepath.Join(e.Home, ".agents", "skills"), ScopeUser, "")
	r.skills(filepath.Join(dir, ".gemini", "skills"), ScopeProject, "")
	r.skills(filepath.Join(dir, ".agents", "skills"), ScopeProject, "")
	r.markdown(GroupCommands, filepath.Join(home, "commands"), ".toml", "/", ScopeUser, "")
	r.markdown(GroupCommands, filepath.Join(dir, ".gemini", "commands"), ".toml", "/", ScopeProject, "")

	r.mcp(obj(user, "mcpServers"), ScopeUser, filepath.Join(home, "settings.json"), "")
	r.mcp(obj(project, "mcpServers"), ScopeProject, projectPath, "")

	exts, _ := os.ReadDir(filepath.Join(home, "extensions"))
	for _, en := range exts {
		base := filepath.Join(home, "extensions", en.Name())
		m := readJSON(filepath.Join(base, "gemini-extension.json"))
		if m == nil {
			continue
		}
		name := str(m, "name")
		if name == "" {
			name = en.Name()
		}
		detail := str(m, "version")
		r.add(GroupPlugins, proto.SetupItem{Name: name, Scope: ScopeExtension, Path: base, Detail: detail})
		r.mcp(obj(m, "mcpServers"), ScopeExtension, "", name)
		ctx := strs(m["contextFileName"])
		if len(ctx) == 0 {
			ctx = []string{"GEMINI.md"}
		}
		for _, c := range ctx {
			r.instruction(filepath.Join(base, c), ScopeExtension, name)
		}
		r.markdown(GroupCommands, filepath.Join(base, "commands"), ".toml", "/", ScopeExtension, name)
		r.skills(filepath.Join(base, "skills"), ScopeExtension, name)
	}
	return r
}

// ---- OpenCode ----

func inspectOpenCode(e Env, dir, root string) *report {
	r := newReport()
	home := filepath.Join(e.Home, ".config", "opencode")
	if x := e.get("XDG_CONFIG_HOME"); x != "" {
		home = filepath.Join(x, "opencode")
	}
	projectRoot := root
	if projectRoot == "" {
		projectRoot = dir
	}
	noClaude := e.get("OPENCODE_DISABLE_CLAUDE_CODE") != ""
	r.add(GroupConch, proto.SetupItem{Name: "state plugin", Scope: ScopeConch, Detail: "OPENCODE_CONFIG_CONTENT (skipped if you set it)"})

	type config struct {
		path  string
		scope string
		m     map[string]any
	}
	var configs []config
	for _, n := range []string{"config.json", "opencode.json", "opencode.jsonc"} {
		if p := filepath.Join(home, n); isFile(p) {
			configs = append(configs, config{p, ScopeUser, readJSON(p)})
		}
	}
	for _, d := range downTo(projectRoot, dir) {
		for _, n := range []string{"opencode.json", "opencode.jsonc", ".opencode/opencode.json", ".opencode/opencode.jsonc"} {
			if p := filepath.Join(d, n); isFile(p) {
				configs = append(configs, config{p, ScopeProject, readJSON(p)})
			}
		}
	}

	if !r.instruction(filepath.Join(home, "AGENTS.md"), ScopeUser, "") && !noClaude {
		r.instruction(filepath.Join(e.Home, ".claude", "CLAUDE.md"), ScopeUser, "Claude Code compatibility")
	}
	for _, d := range projectDirs(dir, root) {
		if r.instruction(filepath.Join(d, "AGENTS.md"), ScopeProject, "") {
			break
		}
		if !noClaude && r.instruction(filepath.Join(d, "CLAUDE.md"), ScopeProject, "Claude Code compatibility") {
			break
		}
	}
	for _, c := range configs {
		for _, in := range strs(c.m["instructions"]) {
			r.add(GroupInstructions, proto.SetupItem{Name: in, Scope: c.scope, Path: c.path, Detail: "from config"})
		}
	}

	for _, sub := range []string{"skill", "skills"} {
		r.skills(filepath.Join(home, sub), ScopeUser, "")
	}
	if !noClaude {
		r.skills(filepath.Join(e.Home, ".claude", "skills"), ScopeUser, "")
	}
	r.skills(filepath.Join(e.Home, ".agents", "skills"), ScopeUser, "")
	for _, d := range projectDirs(dir, root) {
		for _, sub := range []string{".opencode/skill", ".opencode/skills", ".agents/skills"} {
			r.skills(filepath.Join(d, sub), ScopeProject, "")
		}
		if !noClaude {
			r.skills(filepath.Join(d, ".claude", "skills"), ScopeProject, "")
		}
	}
	for _, sub := range []string{"command", "commands"} {
		r.markdown(GroupCommands, filepath.Join(home, sub), ".md", "/", ScopeUser, "")
		r.markdown(GroupCommands, filepath.Join(projectRoot, ".opencode", sub), ".md", "/", ScopeProject, "")
	}
	for _, sub := range []string{"agent", "agents"} {
		r.markdown(GroupSubagents, filepath.Join(home, sub), ".md", "", ScopeUser, "")
		r.markdown(GroupSubagents, filepath.Join(projectRoot, ".opencode", sub), ".md", "", ScopeProject, "")
	}
	for _, c := range configs {
		r.mcp(obj(c.m, "mcp"), c.scope, c.path, "")
		for _, name := range sortedKeys(obj(c.m, "agent")) {
			r.add(GroupSubagents, proto.SetupItem{Name: name, Scope: c.scope, Path: c.path, Detail: "from config"})
		}
		for _, pl := range strs(c.m["plugin"]) {
			r.add(GroupPlugins, proto.SetupItem{Name: pl, Scope: c.scope, Path: c.path})
		}
	}
	return r
}

// ---- Devin for Terminal ----

// devinHome is where Devin keeps its own configuration.
func devinHome(e Env) string { return filepath.Join(e.Home, ".config", "devin") }

// devinReads says whether Devin reads another tool's configuration, which
// it does by default: read_config_from in its config.json turns each off,
// and the project's file overrides yours.
func devinReads(e Env, projectRoot, tool string) bool {
	on := true
	for _, p := range []string{
		filepath.Join(devinHome(e), "config.json"),
		filepath.Join(projectRoot, ".devin", "config.json"),
		filepath.Join(projectRoot, ".devin", "config.local.json"),
	} {
		if projectRoot == "" && !strings.HasPrefix(p, devinHome(e)) {
			continue
		}
		if b, ok := obj(readJSON(p), "read_config_from")[tool].(bool); ok {
			on = b
		}
	}
	return on
}

func inspectDevin(e Env, dir, root string) *report {
	r := newReport()
	home := devinHome(e)
	projectRoot := root
	if projectRoot == "" {
		projectRoot = dir
	}
	claude := devinReads(e, projectRoot, "claude")
	opencode := devinReads(e, projectRoot, "opencode")
	const compat = "Claude Code compatibility"
	r.add(GroupConch, proto.SetupItem{Name: "nothing", Scope: ScopeConch, Detail: "state is read from the screen"})

	r.instruction(filepath.Join(home, "AGENTS.md"), ScopeUser, "")
	r.instruction(filepath.Join(e.Home, ".devin", "global_rules.md"), ScopeUser, "")
	r.markdown(GroupInstructions, filepath.Join(e.Home, ".devin", "rules"), ".md", "rules/", ScopeUser, "")
	if claude {
		r.instruction(filepath.Join(e.Home, ".claude", "CLAUDE.md"), ScopeUser, compat)
	}
	// Files at the root load when it starts; those below it once Devin
	// works in that folder, with the ones above it up to the root.
	for _, d := range downTo(projectRoot, dir) {
		r.instruction(filepath.Join(d, "AGENTS.md"), ScopeProject, "")
		r.instruction(filepath.Join(d, "AGENT.md"), ScopeProject, "")
		r.instruction(filepath.Join(d, "AGENTS.local.md"), ScopeLocal, "")
		if claude {
			r.instruction(filepath.Join(d, "CLAUDE.md"), ScopeProject, compat)
		}
		r.instruction(filepath.Join(d, ".windsurfrules"), ScopeProject, "Windsurf compatibility")
	}
	r.instruction(filepath.Join(projectRoot, ".devin", "global_rules.md"), ScopeProject, "")
	r.markdown(GroupInstructions, filepath.Join(projectRoot, ".devin", "rules"), ".md", "rules/", ScopeProject, "")

	r.skills(filepath.Join(home, "skills"), ScopeUser, "")
	for _, d := range projectDirs(dir, root) {
		for _, sub := range []string{".devin/skills", ".agents/skills", ".windsurf/skills"} {
			r.skills(filepath.Join(d, sub), ScopeProject, "")
		}
		if claude {
			r.skills(filepath.Join(d, ".claude", "skills"), ScopeProject, compat)
			// Claude's commands are skills to Devin, and its slash commands.
			r.markdown(GroupCommands, filepath.Join(d, ".claude", "commands"), ".md", "/", ScopeProject, compat)
		}
	}

	r.devinAgents(filepath.Join(home, "agents"), ScopeUser)
	r.devinAgents(filepath.Join(projectRoot, ".devin", "agents"), ScopeProject)
	r.devinAgents(filepath.Join(projectRoot, ".agents", "agents"), ScopeProject)

	r.mcp(obj(readJSON(filepath.Join(home, "mcp_config.json")), "mcpServers"), ScopeUser, filepath.Join(home, "mcp_config.json"), "")
	for _, f := range []struct{ name, scope string }{{"mcp_config.json", ScopeProject}, {"mcp_config.local.json", ScopeLocal}} {
		p := filepath.Join(projectRoot, ".devin", f.name)
		r.mcp(obj(readJSON(p), "mcpServers"), f.scope, p, "")
	}
	if claude {
		state := readJSON(filepath.Join(e.Home, ".claude.json"))
		r.mcp(obj(state, "mcpServers"), ScopeUser, filepath.Join(e.Home, ".claude.json"), compat)
		for _, key := range uniq(dir, root) {
			r.mcp(obj(obj(obj(state, "projects"), key), "mcpServers"), ScopeLocal, filepath.Join(e.Home, ".claude.json"), compat)
		}
		p := filepath.Join(projectRoot, ".mcp.json")
		r.mcp(obj(readJSON(p), "mcpServers"), ScopeProject, p, compat)
	}
	if opencode {
		oc := filepath.Join(e.Home, ".config", "opencode", "opencode.json")
		r.mcp(obj(readJSON(oc), "mcp"), ScopeUser, oc, "OpenCode compatibility")
		p := filepath.Join(projectRoot, "opencode.json")
		r.mcp(obj(readJSON(p), "mcp"), ScopeProject, p, "OpenCode compatibility")
	}
	// Where Devin records trust is not documented, so conch cannot say
	// whether this folder is trusted — only that the question comes.
	for _, g := range []string{GroupSkills, GroupMCP} {
		for _, it := range r.groups[g] {
			if it.Scope == ScopeProject || it.Scope == ScopeLocal {
				r.note("Devin asks whether to trust a folder the first time it starts there.")
				return r
			}
		}
	}
	return r
}

// devinAgents adds Devin's subagents: <name>.md, or <name>/AGENT.md.
func (r *report) devinAgents(dir, scope string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, en := range entries {
		p := filepath.Join(dir, en.Name())
		switch {
		case en.IsDir() && isFile(filepath.Join(p, "AGENT.md")):
			r.add(GroupSubagents, proto.SetupItem{Name: en.Name(), Scope: scope, Path: filepath.Join(p, "AGENT.md")})
		case !en.IsDir() && strings.HasSuffix(en.Name(), ".md"):
			r.add(GroupSubagents, proto.SetupItem{Name: strings.TrimSuffix(en.Name(), ".md"), Scope: scope, Path: p})
		}
	}
}
