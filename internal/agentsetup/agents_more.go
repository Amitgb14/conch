package agentsetup

import (
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/Amitgb14/conch/internal/proto"
)

// The four agents supported after the first five. Where each looks is
// taken from its own source; the comment above each says which files.

// ---- GitHub Copilot CLI ----

func copilotHome(e Env) string {
	if d := e.get("COPILOT_HOME"); d != "" {
		return d
	}
	return filepath.Join(e.Home, ".copilot")
}

// inspectCopilot follows Copilot CLI 1.0.112's help ("Custom
// instructions", "Skills", "MCP servers"): it reads Claude Code's and
// Gemini's instruction files as well as AGENTS.md and its own, skills from
// .github, .agents and .claude, and servers from ~/.copilot/mcp-config.json
// and a project's .mcp.json or .github/mcp.json — the project's only once
// the folder is trusted.
func inspectCopilot(e Env, dir, root string) *report {
	r := newReport()
	home := copilotHome(e)
	projectRoot := root
	if projectRoot == "" {
		projectRoot = dir
	}
	r.add(GroupConch, proto.SetupItem{Name: "nothing", Scope: ScopeConch, Detail: "it reports its own state (OSC 7501)"})

	r.instruction(filepath.Join(home, "copilot-instructions.md"), ScopeUser, "")
	r.markdown(GroupInstructions, filepath.Join(home, "instructions"), ".instructions.md", "instructions/", ScopeUser, "")
	for _, d := range uniq(projectRoot, dir) {
		for _, n := range []string{"AGENTS.md", "CLAUDE.md", "GEMINI.md", filepath.Join(".claude", "CLAUDE.md"), filepath.Join(".github", "copilot-instructions.md")} {
			r.instruction(filepath.Join(d, n), ScopeProject, "")
		}
		r.markdown(GroupInstructions, filepath.Join(d, ".github", "instructions"), ".instructions.md", "instructions/", ScopeProject, "")
		r.markdown(GroupInstructions, filepath.Join(d, ".claude", "rules"), ".md", "rules/", ScopeProject, "")
	}

	r.skills(filepath.Join(home, "skills"), ScopeUser, "")
	r.skills(filepath.Join(e.Home, ".agents", "skills"), ScopeUser, "")
	for _, d := range projectDirs(dir, root) {
		for _, sub := range []string{".github/skills", ".agents/skills", ".claude/skills"} {
			r.skills(filepath.Join(d, sub), ScopeProject, "")
		}
	}

	user := filepath.Join(home, "mcp-config.json")
	r.mcp(obj(readJSON(user), "mcpServers"), ScopeUser, user, "")
	project := false
	for _, d := range upFrom(dir, projectRoot) {
		for _, n := range []string{".mcp.json", filepath.Join(".github", "mcp.json")} {
			p := filepath.Join(d, n)
			if s := obj(readJSON(p), "mcpServers"); len(s) > 0 {
				r.mcp(s, ScopeProject, p, "")
				project = true
			}
		}
	}
	if project {
		r.note("Copilot reads a project's MCP servers only once you have trusted the folder.")
	}
	return r
}

// ---- Cline ----

func clineDir(e Env) string {
	if d := e.get("CLINE_DIR"); d != "" {
		return d
	}
	return filepath.Join(e.Home, ".cline")
}

// clineMCPSettings is where Cline keeps its servers: there is no project
// file for them (sdk/packages/shared/src/storage/paths.ts).
func clineMCPSettings(e Env) string {
	if p := e.get("CLINE_MCP_SETTINGS_PATH"); p != "" {
		return p
	}
	data := filepath.Join(clineDir(e), "data")
	if d := e.get("CLINE_DATA_DIR"); d != "" {
		data = d
	}
	return filepath.Join(data, "settings", "cline_mcp_settings.json")
}

// inspectCline follows Cline's paths.ts: rules in .clinerules (a file or
// a folder) and .cline/rules beside AGENTS.md, ~/.agents/AGENTS.md and
// ~/.cline/rules for you; skills in .clinerules/skills, .cline/skills,
// .claude/skills and .agents/skills; servers in one settings file.
func inspectCline(e Env, dir, root string) *report {
	r := newReport()
	home := clineDir(e)
	projectRoot := root
	if projectRoot == "" {
		projectRoot = dir
	}
	r.add(GroupConch, proto.SetupItem{Name: "nothing", Scope: ScopeConch, Detail: "state is read from the screen"})

	r.instruction(filepath.Join(e.Home, ".agents", "AGENTS.md"), ScopeUser, "")
	r.clineRules(filepath.Join(home, "rules"), ScopeUser)
	r.clineRules(filepath.Join(e.Home, "Cline", "Rules"), ScopeUser)
	r.clineRules(filepath.Join(e.Home, "Documents", "Cline", "Rules"), ScopeUser)
	for _, d := range uniq(projectRoot, dir) {
		r.instruction(filepath.Join(d, "AGENTS.md"), ScopeProject, "")
		r.instruction(filepath.Join(d, ".clinerules"), ScopeProject, "") // when it is a file
		r.clineRules(filepath.Join(d, ".clinerules"), ScopeProject)
		r.clineRules(filepath.Join(d, ".cline", "rules"), ScopeProject)
	}

	r.skills(filepath.Join(home, "skills"), ScopeUser, "")
	r.skills(filepath.Join(e.Home, ".agents", "skills"), ScopeUser, "")
	for _, d := range projectDirs(dir, root) {
		for _, sub := range []string{".clinerules/skills", ".cline/skills", ".claude/skills", ".agents/skills"} {
			r.skills(filepath.Join(d, sub), ScopeProject, "")
		}
	}

	p := clineMCPSettings(e)
	r.mcp(obj(readJSON(p), "mcpServers"), ScopeUser, p, "")
	return r
}

// clineRules adds the rules in one of Cline's rules folders: the files at
// its top, and only those — a folder below is skills or workflows, not
// rules (user-instruction-config-loader.ts).
func (r *report) clineRules(dir, scope string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, en := range entries {
		ext := filepath.Ext(en.Name())
		if ext != ".md" && ext != ".markdown" && ext != ".txt" {
			continue
		}
		p := filepath.Join(dir, en.Name())
		if isFile(p) {
			r.add(GroupInstructions, proto.SetupItem{Name: "rules/" + strings.TrimSuffix(en.Name(), ext), Scope: scope, Path: p})
		}
	}
}

// ---- Qwen Code ----

func qwenHome(e Env) string {
	if d := e.get("QWEN_HOME"); d != "" {
		return d
	}
	return filepath.Join(e.Home, ".qwen")
}

// inspectQwen is Gemini CLI's layout under Qwen's names: QWEN.md and
// AGENTS.md (or the names context.fileName gives), QWEN.local.md beside
// them, rules folders, skills in .qwen/skills and .agents/skills, and
// servers in settings.json (packages/cli and core, utils/memory-constants).
func inspectQwen(e Env, dir, root string) *report {
	r := newReport()
	home := qwenHome(e)
	projectRoot := root
	if projectRoot == "" {
		projectRoot = dir
	}
	r.add(GroupConch, proto.SetupItem{Name: "nothing", Scope: ScopeConch, Detail: "it reports its own state (OSC 7501)"})

	userPath := filepath.Join(home, "settings.json")
	user := readJSON(userPath)
	projectPath := filepath.Join(dir, ".qwen", "settings.json")
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
		names = []string{"QWEN.md", "AGENTS.md"}
	}
	for _, n := range names {
		r.memory(e.Home, filepath.Join(home, n), ScopeUser, "")
		for _, d := range downTo(projectRoot, dir) {
			r.memory(e.Home, filepath.Join(d, n), ScopeProject, "")
		}
	}
	for _, d := range downTo(projectRoot, dir) {
		r.instruction(filepath.Join(d, "QWEN.local.md"), ScopeLocal, "")
	}
	r.markdown(GroupInstructions, filepath.Join(home, "rules"), ".md", "rules/", ScopeUser, "")
	r.markdown(GroupInstructions, filepath.Join(projectRoot, ".qwen", "rules"), ".md", "rules/", ScopeProject, "")

	r.skills(filepath.Join(home, "skills"), ScopeUser, "")
	r.skills(filepath.Join(e.Home, ".agents", "skills"), ScopeUser, "")
	r.skills(filepath.Join(dir, ".qwen", "skills"), ScopeProject, "")
	r.skills(filepath.Join(dir, ".agents", "skills"), ScopeProject, "")
	r.markdown(GroupCommands, filepath.Join(home, "commands"), ".toml", "/", ScopeUser, "")
	r.markdown(GroupCommands, filepath.Join(dir, ".qwen", "commands"), ".toml", "/", ScopeProject, "")

	r.mcp(obj(user, "mcpServers"), ScopeUser, userPath, "")
	r.mcp(obj(project, "mcpServers"), ScopeProject, projectPath, "")
	return r
}

// ---- Goose ----

// gooseConfigDir is where Goose keeps config.yaml: $GOOSE_PATH_ROOT/config,
// or else the XDG config folder (crates/goose/src/config/paths.rs).
func gooseConfigDir(e Env) string {
	if root := e.get("GOOSE_PATH_ROOT"); filepath.IsAbs(root) {
		return filepath.Join(root, "config")
	}
	base := filepath.Join(e.Home, ".config")
	if x := e.get("XDG_CONFIG_HOME"); x != "" {
		base = x
	}
	return filepath.Join(base, "goose")
}

// gooseExtensions reads the extensions in Goose's config.yaml — its MCP
// servers — in the shape r.mcp reports: cmd is Goose's command, uri its
// address. A disabled one is left out, as Goose leaves it out.
func gooseExtensions(path string) map[string]any {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var cfg struct {
		Extensions map[string]struct {
			Type    string `yaml:"type"`
			Name    string `yaml:"name"`
			Enabled *bool  `yaml:"enabled"`
			Cmd     string `yaml:"cmd"`
			URI     string `yaml:"uri"`
		} `yaml:"extensions"`
	}
	if yaml.Unmarshal(b, &cfg) != nil {
		return nil
	}
	out := map[string]any{}
	for key, x := range cfg.Extensions {
		if x.Enabled != nil && !*x.Enabled {
			continue
		}
		m := map[string]any{}
		switch {
		case x.URI != "":
			m["url"] = x.URI
			if x.Type == "sse" {
				m["type"] = "sse"
			}
		case x.Cmd != "":
			m["command"] = x.Cmd
		default:
			continue // builtin and platform extensions are Goose's own, not servers
		}
		name := x.Name
		if name == "" {
			name = key
		}
		out[name] = m
	}
	return out
}

// inspectGoose follows Goose's hints loader and docs: .goosehints and
// AGENTS.md in the project and the folders down to this one, the same in
// ~/.config/goose, ~/.agents/AGENTS.md; skills in .agents, .goose and
// .claude; and its MCP servers, which it calls extensions, in config.yaml.
func inspectGoose(e Env, dir, root string) *report {
	r := newReport()
	home := gooseConfigDir(e)
	projectRoot := root
	if projectRoot == "" {
		projectRoot = dir
	}
	r.add(GroupConch, proto.SetupItem{Name: "nothing", Scope: ScopeConch, Detail: "it reports its own state (OSC 7501)"})

	for _, n := range []string{".goosehints", "AGENTS.md"} {
		r.instruction(filepath.Join(home, n), ScopeUser, "")
		for _, d := range downTo(projectRoot, dir) {
			r.instruction(filepath.Join(d, n), ScopeProject, "")
		}
	}
	r.instruction(filepath.Join(e.Home, ".agents", "AGENTS.md"), ScopeUser, "")

	r.skills(filepath.Join(e.Home, ".agents", "skills"), ScopeUser, "")
	r.skills(filepath.Join(e.Home, ".claude", "skills"), ScopeUser, "Claude Code compatibility")
	for _, d := range projectDirs(dir, root) {
		r.skills(filepath.Join(d, ".agents", "skills"), ScopeProject, "")
		r.skills(filepath.Join(d, ".goose", "skills"), ScopeProject, "")
		r.skills(filepath.Join(d, ".claude", "skills"), ScopeProject, "Claude Code compatibility")
	}

	p := filepath.Join(home, "config.yaml")
	r.mcp(gooseExtensions(p), ScopeUser, p, "")
	return r
}
