// Package agentsetup reports what an agent loads when it starts in a
// directory: instruction files, skills, commands, subagents, MCP servers and
// plugins, from the user's configuration and the project. It only reads;
// conch never edits agent configuration. Server commands and environment
// variables of MCP servers are not reported, since they often hold secrets.
//
// The locations follow each agent's documented behaviour and are a best
// effort: agents change where they look between releases.
package agentsetup

import (
	"bufio"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/Amitgb14/conch/internal/proto"
)

// Group titles, in display order.
const (
	GroupConch        = "Added by conch"
	GroupInstructions = "Instructions"
	GroupSkills       = "Skills"
	GroupCommands     = "Commands"
	GroupSubagents    = "Subagents"
	GroupMCP          = "MCP servers"
	GroupPlugins      = "Plugins"
)

var groupOrder = []string{GroupInstructions, GroupSkills, GroupCommands, GroupSubagents, GroupMCP, GroupPlugins, GroupConch}

// Scopes.
const (
	ScopeUser      = "user"
	ScopeProject   = "project"
	ScopeLocal     = "local" // personal to this checkout
	ScopePlugin    = "plugin"
	ScopeExtension = "extension"
	ScopeSystem    = "system"
	ScopeManaged   = "managed"
	ScopeConch     = "conch"
)

// Env is where agents find user-level configuration.
type Env struct {
	Home   string
	Getenv func(string) string
	GOOS   string
}

// CurrentEnv is this process's environment.
func CurrentEnv() Env {
	home, _ := os.UserHomeDir()
	return Env{Home: home, Getenv: os.Getenv, GOOS: runtime.GOOS}
}

func (e Env) get(k string) string {
	if e.Getenv == nil {
		return ""
	}
	return e.Getenv(k)
}

type inspector struct {
	name, label string
	inspect     func(e Env, dir, root string) *report
}

var inspectors = []inspector{
	{"claude", "Claude Code", inspectClaude},
	{"codex", "Codex", inspectCodex},
	{"gemini", "Gemini CLI", inspectGemini},
	{"opencode", "OpenCode", inspectOpenCode},
}

// Names lists the agents Inspect knows.
func Names() []string {
	out := make([]string, len(inspectors))
	for i, in := range inspectors {
		out[i] = in.name
	}
	return out
}

// Inspect reports what agent loads in dir. ok is false for an unknown agent.
func Inspect(e Env, agent, dir string) (setup proto.AgentSetup, ok bool) {
	for _, in := range inspectors {
		if in.name == agent {
			r := in.inspect(e, dir, RepoRoot(dir))
			return r.result(e, in.name, in.label), true
		}
	}
	return proto.AgentSetup{}, false
}

// MarkMissing adds to here, marked Missing, the items of main (the same
// agent inspected in the main checkout) that here lacks. Items are matched
// by group, scope and name, so a skill committed in both checkouts matches
// while a local MCP server registered only for the main checkout does not.
func MarkMissing(here *proto.AgentSetup, main proto.AgentSetup) {
	have := map[string]bool{}
	for _, g := range here.Groups {
		for _, it := range g.Items {
			have[g.Title+"\x00"+it.Scope+"\x00"+it.Name] = true
		}
	}
	for _, mg := range main.Groups {
		for _, it := range mg.Items {
			if have[mg.Title+"\x00"+it.Scope+"\x00"+it.Name] {
				continue
			}
			it.Missing = true
			idx := -1
			for i, g := range here.Groups {
				if g.Title == mg.Title {
					idx = i
				}
			}
			if idx < 0 {
				here.Groups = append(here.Groups, proto.SetupGroup{Title: mg.Title})
				idx = len(here.Groups) - 1
			}
			here.Groups[idx].Items = append(here.Groups[idx].Items, it)
		}
	}
	sortGroups(here.Groups)
}

// ---- report ----

type report struct {
	groups map[string][]proto.SetupItem
	seen   map[string]bool // group + path, to skip directories reached twice
	notes  []string
}

func newReport() *report {
	return &report{groups: map[string][]proto.SetupItem{}, seen: map[string]bool{}}
}

func (r *report) add(group string, it proto.SetupItem) {
	if it.Path != "" {
		real := it.Path
		if p, err := filepath.EvalSymlinks(it.Path); err == nil {
			real = p // the same skill linked into several agents' folders
		}
		key := group + "\x00" + real + "\x00" + it.Name
		if r.seen[key] {
			return
		}
		r.seen[key] = true
	}
	r.groups[group] = append(r.groups[group], it)
}

func (r *report) note(s string) { r.notes = append(r.notes, s) }

func (r *report) result(e Env, agent, label string) proto.AgentSetup {
	out := proto.AgentSetup{Agent: agent, Label: label, Notes: r.notes}
	for title, items := range r.groups {
		for i := range items {
			items[i].Path = tilde(e, items[i].Path)
		}
		out.Groups = append(out.Groups, proto.SetupGroup{Title: title, Items: items})
	}
	sortGroups(out.Groups)
	return out
}

func sortGroups(gs []proto.SetupGroup) {
	rank := func(t string) int {
		for i, g := range groupOrder {
			if g == t {
				return i
			}
		}
		return len(groupOrder)
	}
	sort.SliceStable(gs, func(i, j int) bool { return rank(gs[i].Title) < rank(gs[j].Title) })
}

// instruction adds path as an instruction file when it exists.
func (r *report) instruction(path, scope, detail string) bool {
	if !isFile(path) {
		return false
	}
	r.add(GroupInstructions, proto.SetupItem{Name: filepath.Base(path), Scope: scope, Path: path, Detail: detail})
	return true
}

// skills adds every <dir>/<name>/SKILL.md.
func (r *report) skills(dir, scope, detail string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, en := range entries {
		sub := filepath.Join(dir, en.Name())
		md := filepath.Join(sub, "SKILL.md")
		if !isFile(md) {
			if strings.HasPrefix(en.Name(), ".") && isDir(sub) {
				r.skills(sub, ScopeSystem, detail) // e.g. Codex's bundled .system skills
			}
			continue
		}
		name, desc := frontmatter(md)
		if name == "" {
			name = en.Name()
		}
		d := detail
		if d == "" {
			d = desc
		}
		r.add(GroupSkills, proto.SetupItem{Name: name, Scope: scope, Path: sub, Detail: d})
	}
}

// markdown adds the files with extension ext under dir to group, named by
// their path without the extension ("a/b.md" → prefix+"a:b").
func (r *report) markdown(group, dir, ext, prefix, scope, detail string) {
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != dir && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ext) {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		name := prefix + strings.ReplaceAll(strings.TrimSuffix(rel, ext), string(filepath.Separator), ":")
		r.add(group, proto.SetupItem{Name: name, Scope: scope, Path: path, Detail: detail})
		return nil
	})
}

// mcp adds the servers of a name → config map.
func (r *report) mcp(servers map[string]any, scope, path, detail string) {
	names := make([]string, 0, len(servers))
	for n := range servers {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		d := transport(servers[n])
		if detail != "" {
			d = strings.TrimPrefix(d+" · "+detail, " · ")
		}
		r.add(GroupMCP, proto.SetupItem{Name: n, Scope: scope, Path: path, Detail: d})
	}
}

// transport describes a server config without revealing its command, URL
// or environment.
func transport(v any) string {
	m, _ := v.(map[string]any)
	var parts []string
	switch t, _ := m["type"].(string); {
	case t != "":
		parts = append(parts, t)
	case m["url"] != nil || m["httpUrl"] != nil:
		parts = append(parts, "http")
	default:
		parts = append(parts, "stdio")
	}
	if en, ok := m["enabled"].(bool); ok && !en {
		parts = append(parts, "disabled")
	}
	if dis, ok := m["disabled"].(bool); ok && dis {
		parts = append(parts, "disabled")
	}
	return strings.Join(parts, " · ")
}

// ---- paths ----

// RepoRoot is the root of the git checkout containing dir, or "".
func RepoRoot(dir string) string {
	for d := dir; ; d = filepath.Dir(d) {
		if _, err := os.Lstat(filepath.Join(d, ".git")); err == nil {
			return d
		}
		if filepath.Dir(d) == d {
			return ""
		}
	}
}

// upFrom lists dir and its parents up to stop (inclusive). With stop "" it
// goes up to, but not including, the filesystem root.
func upFrom(dir, stop string) []string {
	var out []string
	for d := dir; ; d = filepath.Dir(d) {
		if stop == "" && filepath.Dir(d) == d {
			break
		}
		out = append(out, d)
		if d == stop || filepath.Dir(d) == d {
			break
		}
	}
	return out
}

// downTo lists the directories from stop down to dir.
func downTo(stop, dir string) []string {
	up := upFrom(dir, stop)
	for i, j := 0, len(up)-1; i < j; i, j = i+1, j-1 {
		up[i], up[j] = up[j], up[i]
	}
	return up
}

// projectDirs are the directories between dir and its repository root, or
// just dir outside a repository.
func projectDirs(dir, root string) []string {
	if root == "" {
		return []string{dir}
	}
	return upFrom(dir, root)
}

func tilde(e Env, p string) string {
	if e.Home != "" && (p == e.Home || strings.HasPrefix(p, e.Home+string(filepath.Separator))) {
		return "~" + p[len(e.Home):]
	}
	return p
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// frontmatter reads name and description from a SKILL.md header, including
// YAML block scalars ("description: >-" followed by indented lines).
func frontmatter(path string) (name, desc string) {
	f, err := os.Open(path)
	if err != nil {
		return "", ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	var block *string // value being continued by indented lines
	for i := 0; sc.Scan() && i < 60; i++ {
		line := sc.Text()
		if i == 0 {
			if strings.TrimSpace(line) != "---" {
				return "", ""
			}
			continue
		}
		if strings.TrimSpace(line) == "---" {
			break
		}
		if block != nil && (strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")) {
			*block = strings.TrimSpace(*block + " " + strings.TrimSpace(line))
			continue
		}
		block = nil
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue // a nested key, such as metadata's own name
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		var target *string
		switch strings.TrimSpace(k) {
		case "name":
			target = &name
		case "description":
			target = &desc
		default:
			continue
		}
		switch v {
		case ">", ">-", "|", "|-", ">+", "|+", "":
			*target, block = "", target
		default:
			*target = strings.Trim(v, `"'`)
		}
	}
	if len([]rune(desc)) > 90 {
		desc = string([]rune(desc)[:89]) + "…"
	}
	return name, desc
}
