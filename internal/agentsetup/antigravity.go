package agentsetup

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Amitgb14/conch/internal/proto"
)

// ---- Antigravity CLI ----

// inspectAntigravity is what agy loads, from antigravity.google/docs for
// the CLI (skills, rules, mcp, plugins), checked against agy 1.5.16's own
// folders on a Mac. Rules and skills are read in the folder it starts in
// and each one above it to the repository root; MCP servers only in that
// folder (its workspace). Everything of yours sits under ~/.gemini/config,
// shared with the Antigravity app, beside older places the CLI still
// reads. The skills agy ships (antigravity-cli/builtin) are its own and
// left out, as other agents' bundled ones are.
func inspectAntigravity(e Env, dir, root string) *report {
	r := newReport()
	g := filepath.Join(e.Home, ".gemini")
	cfg := filepath.Join(g, "config")
	projectRoot := root
	if projectRoot == "" {
		projectRoot = dir
	}

	for _, p := range []string{filepath.Join(g, "AGENTS.md"), filepath.Join(g, "GEMINI.md"),
		filepath.Join(cfg, "AGENTS.md"), filepath.Join(cfg, "GEMINI.md")} {
		r.instruction(p, ScopeUser, "")
	}
	r.rules(filepath.Join(cfg, "rules"), ScopeUser, "")
	for _, d := range downTo(projectRoot, dir) {
		for _, p := range []string{"AGENTS.md", "GEMINI.md", ".agents/AGENTS.md", ".agents/GEMINI.md"} {
			r.instruction(filepath.Join(d, p), ScopeProject, "")
		}
		r.rules(filepath.Join(d, ".agents", "rules"), ScopeProject, "")
		r.rules(filepath.Join(d, ".agent", "rules"), ScopeProject, "")
	}

	r.skills(filepath.Join(cfg, "skills"), ScopeUser, "")
	r.skills(filepath.Join(g, "antigravity-cli", "skills"), ScopeUser, "")
	r.skills(filepath.Join(g, "skills"), ScopeUser, "")
	for _, d := range downTo(projectRoot, dir) {
		r.skills(filepath.Join(d, ".agents", "skills"), ScopeProject, "")
		r.skills(filepath.Join(d, ".agent", "skills"), ScopeProject, "")
	}

	user := filepath.Join(cfg, "mcp_config.json")
	r.mcp(obj(readJSON(user), "mcpServers"), ScopeUser, user, "")
	project := filepath.Join(dir, ".agents", "mcp_config.json")
	r.mcp(obj(readJSON(project), "mcpServers"), ScopeProject, project, "")

	// A plugin is a folder of skills, servers, rules and hooks, in
	// ~/.gemini/config/plugins or a workspace's .agents/plugins.
	for _, base := range []string{filepath.Join(cfg, "plugins"), filepath.Join(dir, ".agents", "plugins")} {
		entries, _ := os.ReadDir(base)
		for _, en := range entries {
			p := filepath.Join(base, en.Name())
			if strings.HasPrefix(en.Name(), ".") || !isDir(p) {
				continue
			}
			r.add(GroupPlugins, proto.SetupItem{Name: en.Name(), Scope: ScopePlugin, Path: p})
			r.skills(filepath.Join(p, "skills"), ScopePlugin, en.Name())
			mcp := filepath.Join(p, "mcp_config.json")
			r.mcp(obj(readJSON(mcp), "mcpServers"), ScopePlugin, mcp, en.Name())
		}
	}
	return r
}

// rules adds the Markdown files directly in dir: agy reads a rules folder
// flat, and one nested deeper only when a rules.json names it.
func (r *report) rules(dir, scope, detail string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var files []string
	for _, en := range entries {
		if !en.IsDir() && strings.HasSuffix(en.Name(), ".md") {
			files = append(files, en.Name())
		}
	}
	sort.Strings(files)
	for _, f := range files {
		r.instruction(filepath.Join(dir, f), scope, detail)
	}
}
