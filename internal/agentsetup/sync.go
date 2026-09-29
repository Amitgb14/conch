package agentsetup

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Amitgb14/conch/internal/config"
)

// Writing, rather than reading: one agent's setup in a checkout given to
// the others, so running several agents does not mean setting each one up
// by hand. Only a checkout is touched — never the agents' own folders in
// your home — and only three things travel: the project's instructions,
// its skills, and the MCP servers it declares.
//
// Three rules keep it safe to run:
//
//   - Nothing is written until the plan has been seen. Sync with apply
//     false says what it would do and touches nothing.
//   - A file somebody wrote by hand is left alone. conch writes its own
//     marked block, or a file that did not exist, and nothing else; a
//     second sync updates that block and leaves the rest as it is.
//   - What it did can be undone. Every write is recorded with what was
//     there before, and Undo puts it back.
//
// Secrets stay where they are: a server whose environment or headers hold
// a value rather than a ${VAR} reference is not copied, and the reason
// says so. Logins are each agent's own and are never touched.

// Kinds of setup a sync carries.
const (
	SyncInstructions = "instructions"
	SyncSkill        = "skill"
	SyncMCP          = "mcp server"
)

// What a change does. Create, Update and Link write; Same and Skip do not.
const (
	ActionCreate = "create"
	ActionUpdate = "update"
	ActionLink   = "link"
	ActionSame   = "same"
	ActionSkip   = "skip"
)

// SyncChange is one thing a sync would do, or did.
type SyncChange struct {
	Agent  string `json:"agent"`
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Path   string `json:"path"`   // relative to the checkout
	Action string `json:"action"` // create, update, link, same, skip
	Detail string `json:"detail,omitempty"`
	Done   bool   `json:"done,omitempty"`  // written, when applying
	Error  string `json:"error,omitempty"` // why it was not

	// write is what applying it does; nil for same and skip.
	write func(u *undoLog) error
}

// Writes reports whether the change would change anything.
func (c SyncChange) Writes() bool {
	switch c.Action {
	case ActionCreate, ActionUpdate, ActionLink, ActionRemove:
		return true
	}
	return false
}

// SyncResult is a plan, or what came of applying one.
type SyncResult struct {
	Dir     string       `json:"dir"`
	From    string       `json:"from"`
	To      []string     `json:"to"`
	Changes []SyncChange `json:"changes,omitempty"`
	Notes   []string     `json:"notes,omitempty"`
	// Undo names the record this apply left behind, for Undo.
	Undo string `json:"undo,omitempty"`
}

// Writes counts the changes that would write, or wrote.
func (r SyncResult) Writes() int {
	n := 0
	for _, c := range r.Changes {
		if c.Writes() {
			n++
		}
	}
	return n
}

// agentFiles is where an agent keeps its project setup, for writing.
type agentFiles struct {
	// name is the agent's, so its servers are read in its own format.
	name string
	// instructions is the file it reads at the checkout root.
	instructions string
	// imports says it expands "@path" in that file, so conch can point at
	// another agent's file instead of copying it.
	imports bool
	// skills are the folders it reads skills from, relative to the root;
	// conch puts a skill in the first, which is shared where it can be.
	skills []string
	// mcp is the file its servers are declared in, and mcpKey where in it.
	// An empty mcp means conch will not write servers for this agent here.
	mcp    string
	mcpKey string
	// mcpRead is where its servers are read from when that is not where
	// they would be written: Claude keeps its own in ~/.claude.json, which
	// conch reads and never rewrites.
	mcpRead string
	// abs says the paths are already absolute (the user scope), so nothing
	// is joined to a checkout.
	abs bool
	// readsInstructions and readsMCP are other agents' files this one
	// reads by itself — Devin reads Claude Code's CLAUDE.md and .mcp.json —
	// so what is in them needs no copy. A copy would be read twice.
	readsInstructions []string
	readsMCP          []string
}

// reads reports whether the agent already reads the file at path itself.
func (a agentFiles) reads(root, path string, list []string) bool {
	for _, rel := range list {
		if cleanPath(a.at(root, rel)) == cleanPath(path) {
			return true
		}
	}
	return false
}

// at is where a file of this agent's lives, given the root a project scope
// joins to.
func (a agentFiles) at(root, rel string) string {
	if rel == "" {
		return ""
	}
	if a.abs {
		return rel
	}
	return filepath.Join(root, rel)
}

var writable = map[string]agentFiles{
	"claude": {instructions: "CLAUDE.md", imports: true,
		skills: []string{".claude/skills"}, mcp: ".mcp.json", mcpKey: "mcpServers"},
	"codex": {instructions: "AGENTS.md",
		skills: []string{".agents/skills"}, mcp: ".codex/config.toml", mcpKey: "mcp_servers"},
	"gemini": {instructions: "GEMINI.md", imports: true,
		skills: []string{".agents/skills", ".gemini/skills"}, mcp: ".gemini/settings.json", mcpKey: "mcpServers"},
	"opencode": {instructions: "AGENTS.md",
		skills: []string{".agents/skills", ".opencode/skill"}, mcp: "opencode.json", mcpKey: "mcp"},
	// Devin also reads Claude Code's files, and OpenCode's servers; the
	// skills it reads from .claude/skills come last so nothing is linked
	// there for it.
	"devin": {instructions: "AGENTS.md",
		skills: []string{".agents/skills", ".devin/skills", ".claude/skills"}, mcp: ".devin/mcp_config.json", mcpKey: "mcpServers",
		readsInstructions: []string{"CLAUDE.md"}, readsMCP: []string{".mcp.json", "opencode.json"}},
}

// withoutDevinCompat drops what Devin reads of other agents' files when its
// read_config_from turns that off.
func withoutDevinCompat(e Env, projectRoot string, a agentFiles) agentFiles {
	var skills, instr, mcp []string
	for _, s := range a.skills {
		if !strings.Contains(s, ".claude") || devinReads(e, projectRoot, "claude") {
			skills = append(skills, s)
		}
	}
	for _, p := range a.readsInstructions {
		if devinReads(e, projectRoot, "claude") {
			instr = append(instr, p)
		}
	}
	for _, p := range a.readsMCP {
		tool := "claude"
		if strings.Contains(p, "opencode") {
			tool = "opencode"
		}
		if devinReads(e, projectRoot, tool) {
			mcp = append(mcp, p)
		}
	}
	a.skills, a.readsInstructions, a.readsMCP = skills, instr, mcp
	return a
}

// userFiles is where an agent keeps the setup that follows you rather than
// a checkout: your home. The folders each agent reads are its own, and the
// environment can move them, so they are resolved rather than written down
// as constants.
//
// Claude is not given MCP servers here: its user-scope ones live in
// ~/.claude.json, the file it keeps its own state and every project's
// history in, and conch does not rewrite that for a setting. It is read
// from there happily enough.
func userFiles(e Env, agent string) (agentFiles, bool) {
	home := e.Home
	if home == "" {
		return agentFiles{}, false
	}
	shared := filepath.Join(home, ".agents", "skills") // read by all but Claude
	switch agent {
	case "claude":
		dir := filepath.Join(home, ".claude")
		if d := e.get("CLAUDE_CONFIG_DIR"); d != "" {
			dir = d
		}
		return agentFiles{
			instructions: filepath.Join(dir, "CLAUDE.md"), imports: true,
			skills: []string{filepath.Join(dir, "skills")},
			mcp:    "", mcpKey: "mcpServers",
			mcpRead: filepath.Join(home, ".claude.json"),
		}, true
	case "codex":
		dir := filepath.Join(home, ".codex")
		if d := e.get("CODEX_HOME"); d != "" {
			dir = d
		}
		return agentFiles{
			instructions: filepath.Join(dir, "AGENTS.md"),
			skills:       []string{shared, filepath.Join(dir, "skills")},
			mcp:          filepath.Join(dir, "config.toml"), mcpKey: "mcp_servers",
		}, true
	case "gemini":
		dir := filepath.Join(home, ".gemini")
		return agentFiles{
			instructions: filepath.Join(dir, "GEMINI.md"), imports: true,
			skills: []string{shared, filepath.Join(dir, "skills")},
			mcp:    filepath.Join(dir, "settings.json"), mcpKey: "mcpServers",
		}, true
	case "opencode":
		dir := filepath.Join(home, ".config", "opencode")
		if x := e.get("XDG_CONFIG_HOME"); x != "" {
			dir = filepath.Join(x, "opencode")
		}
		return agentFiles{
			instructions: filepath.Join(dir, "AGENTS.md"),
			skills:       []string{shared, filepath.Join(dir, "skill")},
			mcp:          filepath.Join(dir, "opencode.json"), mcpKey: "mcp",
		}, true
	case "devin":
		// Devin does not read ~/.agents/skills, so a skill is linked into
		// its own folder.
		dir := devinHome(e)
		oc := filepath.Join(home, ".config", "opencode")
		if x := e.get("XDG_CONFIG_HOME"); x != "" {
			oc = filepath.Join(x, "opencode")
		}
		return withoutDevinCompat(e, "", agentFiles{
			instructions: filepath.Join(dir, "AGENTS.md"),
			skills:       []string{filepath.Join(dir, "skills")},
			mcp:          filepath.Join(dir, "mcp_config.json"), mcpKey: "mcpServers",
			readsInstructions: []string{filepath.Join(home, ".claude", "CLAUDE.md")},
			readsMCP:          []string{filepath.Join(home, ".claude.json"), filepath.Join(oc, "opencode.json")},
		}), true
	}
	return agentFiles{}, false
}

// SyncNames lists the agents a sync can read from and write for.
func SyncNames() []string {
	out := make([]string, 0, len(writable))
	for name := range writable {
		out = append(out, name)
	}
	sort.Slice(out, func(i, j int) bool { return rankAgent(out[i]) < rankAgent(out[j]) })
	return out
}

func rankAgent(name string) int {
	for i, n := range Names() {
		if n == name {
			return i
		}
	}
	return len(writable)
}

// CanSync reports whether a sync knows where this agent keeps things.
func CanSync(name string) bool { _, ok := writable[name]; return ok }

// ErrNothingToSync means the agent a sync would copy from has nothing in
// this checkout: no instructions, no skills, no servers.
var ErrNothingToSync = errors.New("nothing to sync")

// Sync gives every agent in to the setup from has in the checkout at dir.
// With apply false nothing is written and the changes are what it would
// do. to may be empty, meaning every other agent it knows.
func Sync(dir, from string, to []string, apply bool) (SyncResult, error) {
	root := RepoRoot(dir)
	if root == "" {
		root = dir
	}
	if _, ok := writable[from]; !ok {
		return SyncResult{}, fmt.Errorf("conch cannot read %s's setup (it knows %s)", from, strings.Join(SyncNames(), ", "))
	}
	if len(to) == 0 {
		for _, name := range SyncNames() {
			if name != from {
				to = append(to, name)
			}
		}
	}
	for _, name := range to {
		if !CanSync(name) {
			return SyncResult{}, fmt.Errorf("conch cannot set %s up (it knows %s)", name, strings.Join(SyncNames(), ", "))
		}
	}
	e := CurrentEnv()
	return sync(root, filepath.Join(root, undoDir), from, to, apply, func(name string) (agentFiles, bool) {
		a, ok := writable[name]
		if name == "devin" {
			a = withoutDevinCompat(e, root, a)
		}
		return a, ok
	})
}

// SyncUser gives the agents in to the setup from has in your home, rather
// than in a checkout: the instructions, skills and MCP servers that follow
// you from project to project. Nothing is written with apply false.
//
// It is the riskier half and says so: there is no git status to show what
// changed, so the plan and the record are all there is, and a file that is
// a link into a dotfiles repository is left alone rather than written
// through.
func SyncUser(e Env, from string, to []string, apply bool) (SyncResult, error) {
	if e.Home == "" {
		return SyncResult{}, errors.New("conch does not know where your home is")
	}
	if _, ok := userFiles(e, from); !ok {
		return SyncResult{}, fmt.Errorf("conch cannot read %s's setup (it knows %s)", from, strings.Join(SyncNames(), ", "))
	}
	if len(to) == 0 {
		for _, name := range SyncNames() {
			if name != from {
				to = append(to, name)
			}
		}
	}
	for _, name := range to {
		if _, ok := userFiles(e, name); !ok {
			return SyncResult{}, fmt.Errorf("conch cannot set %s up (it knows %s)", name, strings.Join(SyncNames(), ", "))
		}
	}
	return sync(e.Home, userRecordDir(), from, to, apply, func(name string) (agentFiles, bool) {
		a, ok := userFiles(e, name)
		a.abs = true
		return a, ok
	})
}

// userRecordDir is where a user-scope sync's record is kept: conch's own
// folder, since a home directory has no root to put a .conch in.
var userRecordDir = func() string { return filepath.Join(config.Dir(), "agent-sync") }

// sync is the whole of it, for either scope: root is what the paths are
// shown against, recordDir where the record goes, and files says where
// each agent keeps things.
func sync(root, recordDir, from string, to []string, apply bool, files func(string) (agentFiles, bool)) (SyncResult, error) {
	src, _ := files(from)
	src.name = from
	res := SyncResult{Dir: root, From: from, To: to}

	text, textPath := instructionsOf(root, src)
	fromShow := show(root, src.at(root, textPath), src)
	fromRef := textPath // what an import in another file points at
	if src.abs {
		fromRef = fromShow // ~/.claude/CLAUDE.md, which every agent resolves
	}
	skills := skillsOf(root, src)
	servers, secret := serversOf(root, src)
	if text == nil && len(skills) == 0 && len(servers) == 0 && len(secret) == 0 {
		return res, fmt.Errorf("%w: %s has no instructions, skills or MCP servers in %s", ErrNothingToSync, labelOf(from), root)
	}

	// Agents share files and folders — AGENTS.md is Codex's and
	// OpenCode's, .agents/skills is read by three of them — so the same
	// write is planned once and the others are told who it is shared with.
	shared := map[string]string{}
	add := func(c SyncChange, what string) {
		key := c.Kind + "\x00" + c.Path + "\x00" + c.Name
		if c.Writes() {
			if with, done := shared[key]; done {
				c.Action, c.Detail = ActionSame, "the same "+what+" as "+with
				c.write = nil
			} else {
				shared[key] = labelOf(c.Agent)
			}
		}
		res.Changes = append(res.Changes, c)
	}
	for _, name := range to {
		if name == from {
			continue
		}
		t, _ := files(name)
		t.name = name
		switch {
		case text == nil || t.instructions == src.instructions:
		case t.reads(root, src.at(root, textPath), t.readsInstructions):
			res.Changes = append(res.Changes, SyncChange{Agent: name, Kind: SyncInstructions, Name: filepath.Base(textPath),
				Path: fromShow, Action: ActionSame, Detail: labelOf(name) + " reads " + fromShow + " itself"})
		default:
			add(planInstructions(root, name, t, fromRef, fromShow, text), "file")
		}
		for _, sk := range skills {
			add(planSkill(root, name, t, sk), "folder")
		}
		mcpFrom := src.mcpRead
		if mcpFrom == "" {
			mcpFrom = src.mcp
		}
		readsThem := mcpFrom != "" && t.reads(root, src.at(root, mcpFrom), t.readsMCP)
		for _, s := range servers {
			if readsThem {
				res.Changes = append(res.Changes, SyncChange{Agent: name, Kind: SyncMCP, Name: s.Name,
					Path: show(root, src.at(root, mcpFrom), src), Action: ActionSame, Detail: labelOf(name) + " reads it there itself"})
				continue
			}
			add(planServer(root, name, t, s), "file")
		}
		for _, s := range secret {
			if readsThem { // its secret stays where it is, and is read from there
				res.Changes = append(res.Changes, SyncChange{Agent: name, Kind: SyncMCP, Name: s,
					Path: show(root, src.at(root, mcpFrom), src), Action: ActionSame, Detail: labelOf(name) + " reads it there itself"})
				continue
			}
			add(SyncChange{Agent: name, Kind: SyncMCP, Name: s, Path: t.mcp,
				Action: ActionSkip, Detail: "its environment holds a value, not a ${VAR} reference: conch does not copy secrets"}, "file")
		}
	}
	if text != nil {
		res.Notes = append(res.Notes, "instructions from "+fromShow)
	}
	// Worth saying once each: what is written is there, but an agent that
	// wants the folder trusted ignores it until you say so — silently, in
	// Gemini's case, which is how an afternoon goes missing.
	said := map[string]bool{}
	for _, c := range res.Changes {
		if !c.Writes() || said[c.Agent] {
			continue
		}
		switch {
		case c.Agent == "codex" && c.Kind == SyncMCP && !src.abs:
			said[c.Agent] = true
			res.Notes = append(res.Notes, "Codex reads a project's config.toml only once you have trusted the folder")
		case c.Agent == "gemini" && (c.Kind == SyncMCP || c.Kind == SyncSkill):
			said[c.Agent] = true
			if src.abs {
				// Trust bites here too: an untrusted folder suppresses even
				// the servers and skills that are yours rather than its.
				res.Notes = append(res.Notes, "Gemini leaves MCP servers and skills out — even your own — in a folder you have not trusted")
			} else {
				res.Notes = append(res.Notes, "Gemini leaves a project's MCP servers and skills out until you have trusted the folder, and says nothing about the skills")
			}
		}
	}
	sortChanges(res.Changes)
	if !apply {
		return res, nil
	}
	return applySync(root, recordDir, res)
}

// applySync writes what the plan says, recording what was there before.
func applySync(root, recordDir string, res SyncResult) (SyncResult, error) {
	if res.Writes() == 0 {
		return res, nil
	}
	u := &undoLog{Root: root, dir: recordDir, Stamp: stampFor(recordDir), From: res.From}
	for i := range res.Changes {
		c := &res.Changes[i]
		if !c.Writes() || c.write == nil {
			continue
		}
		if err := c.write(u); err != nil {
			c.Error = err.Error()
			continue
		}
		c.Done = true
	}
	if err := u.save(); err != nil {
		res.Notes = append(res.Notes, "what was there before could not be recorded, so this cannot be undone: "+err.Error())
		return res, nil
	}
	res.Undo = u.Stamp
	return res, nil
}

// stampFor names a record, without taking one that is already there: two
// syncs in the same second are not the same sync.
func stampFor(recordDir string) string {
	base := time.Now().Format("20060102-150405")
	stamp := base
	for i := 2; i < 100; i++ {
		if _, err := os.Stat(filepath.Join(recordDir, stamp+".json")); os.IsNotExist(err) {
			return stamp
		}
		stamp = fmt.Sprintf("%s-%d", base, i)
	}
	return stamp
}

func sortChanges(cs []SyncChange) {
	kind := func(k string) int {
		switch k {
		case SyncInstructions:
			return 0
		case SyncSkill:
			return 1
		}
		return 2
	}
	sort.SliceStable(cs, func(i, j int) bool {
		if a, b := rankAgent(cs[i].Agent), rankAgent(cs[j].Agent); a != b {
			return a < b
		}
		if a, b := kind(cs[i].Kind), kind(cs[j].Kind); a != b {
			return a < b
		}
		return cs[i].Name < cs[j].Name
	})
}

func labelOf(agent string) string {
	for _, in := range inspectors {
		if in.name == agent {
			return in.label
		}
	}
	return agent
}

// show is how a path reads in a plan: inside a checkout, relative to it;
// in your home, with the ~ back on. The whole path is never hidden — what
// is about to be written is the point.
func show(root, path string, a agentFiles) string {
	if !a.abs {
		if rel, err := filepath.Rel(root, path); err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
		return path
	}
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(path, home+string(filepath.Separator)) {
		return "~" + path[len(home):]
	}
	return path
}

// ---- instructions ----

// maxInstructions is the largest instruction file conch copies. Anything
// bigger is somebody's book, not a project's instructions.
const maxInstructions = 1 << 20

// conch's own block in a file it shares with hand-written text. The name
// of the file it came from is in the opening line, so it is plain where to
// make the change.
const (
	blockStart = "<!-- conch:instructions from "
	blockOpen  = " -->"
	blockEnd   = "<!-- conch:end -->"
)

var blockRe = regexp.MustCompile(`(?s)<!-- conch:instructions from [^\n]*-->\n.*?<!-- conch:end -->\n?`)

// instructionsOf reads the instruction file of the agent to copy from.
func instructionsOf(root string, a agentFiles) ([]byte, string) {
	path := a.at(root, a.instructions)
	st, err := os.Stat(path)
	if err != nil || !st.Mode().IsRegular() || st.Size() > maxInstructions {
		return nil, ""
	}
	b, err := os.ReadFile(path)
	if err != nil || len(strings.TrimSpace(string(b))) == 0 {
		return nil, ""
	}
	return b, a.instructions
}

// planInstructions is what to write for one agent's instruction file: a
// pointer at the other file for an agent that follows imports, and the
// text itself for one that does not.
// planInstructions is what to write for one agent's instruction file. ref
// is how the source file is named in what conch writes — a relative name
// inside a checkout, a ~ path in your home, since an agent resolves an
// import against the file that holds it.
func planInstructions(root, agent string, t agentFiles, ref, from string, text []byte) SyncChange {
	path := t.at(root, t.instructions)
	c := SyncChange{Agent: agent, Kind: SyncInstructions, Name: filepath.Base(t.instructions), Path: show(root, path, t)}
	want := blockFor(ref, from, text, t.imports)
	if st, lerr := os.Lstat(path); lerr == nil && st.Mode()&os.ModeSymlink != 0 {
		c.Action, c.Detail = ActionSkip, "it is a link into somewhere else (a dotfiles repository?): conch leaves it alone"
		return c
	}
	have, err := os.ReadFile(path)
	switch {
	case os.IsNotExist(err):
		c.Action, c.Detail = ActionCreate, detailFor(from, t.imports)
		c.write = writeFile(path, SyncInstructions, want)
		return c
	case err != nil:
		c.Action, c.Detail = ActionSkip, err.Error()
		return c
	}
	block := blockRe.Find(have)
	if block == nil {
		c.Action = ActionSkip
		c.Detail = filepath.Base(t.instructions) + " is somebody's own; conch leaves it alone"
		if sameText(have, text) {
			c.Action, c.Detail = ActionSame, "already the same as "+from
		}
		return c
	}
	if string(block) == string(want) {
		c.Action, c.Detail = ActionSame, "conch's block is up to date"
		return c
	}
	c.Action, c.Detail = ActionUpdate, "conch's block in it, "+detailFor(from, t.imports)
	c.write = writeFile(path, SyncInstructions, blockRe.ReplaceAll(have, want))
	return c
}

func detailFor(fromName string, imports bool) string {
	if imports {
		return "pointing at " + fromName
	}
	return "a copy of " + fromName
}

// blockFor is conch's block: an import of the other file where the agent
// expands one, else the text itself. The opening line names where it came
// from as a person would write it, so the block says its own provenance.
func blockFor(ref, from string, text []byte, imports bool) []byte {
	body := "@" + ref + "\n"
	if !imports {
		body = strings.TrimRight(string(text), "\n") + "\n"
	}
	return []byte(blockStart + from + blockOpen + "\n" + body + blockEnd + "\n")
}

func sameText(a, b []byte) bool {
	return strings.TrimSpace(string(a)) == strings.TrimSpace(string(b))
}

// ---- skills ----

// skill is one skill of the checkout, by name and folder.
type skill struct {
	name string
	dir  string // absolute
}

// skillsOf lists the skills the agent to copy from reads in the checkout.
func skillsOf(root string, a agentFiles) []skill {
	var out []skill
	seen := map[string]bool{}
	for _, rel := range a.skills {
		dir := a.at(root, rel)
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, en := range entries {
			sub := filepath.Join(dir, en.Name())
			if !isFile(filepath.Join(sub, "SKILL.md")) {
				continue
			}
			real := cleanPath(sub)
			if seen[real] {
				continue
			}
			seen[real] = true
			out = append(out, skill{name: en.Name(), dir: sub})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

// planSkill links a skill into the first folder the agent reads, so one
// copy of it serves every agent that reads that folder.
func planSkill(root, agent string, t agentFiles, sk skill) SyncChange {
	path := filepath.Join(t.at(root, t.skills[0]), sk.name)
	c := SyncChange{Agent: agent, Kind: SyncSkill, Name: sk.name, Path: show(root, path, t)}
	// A folder the agent already reads it from is enough, wherever it is.
	for _, dir := range t.skills {
		there := filepath.Join(t.at(root, dir), sk.name)
		if cleanPath(there) == cleanPath(sk.dir) {
			c.Action, c.Detail = ActionSame, "already read from "+show(root, t.at(root, dir), t)
			c.Path = show(root, there, t)
			return c
		}
	}
	if _, err := os.Lstat(path); err == nil {
		c.Action, c.Detail = ActionSkip, "something else is already there"
		return c
	}
	target, err := filepath.Rel(filepath.Dir(path), sk.dir)
	if err != nil {
		target = sk.dir
	}
	c.Action, c.Detail = ActionLink, "pointing at "+target
	c.write = func(u *undoLog) error {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		u.creating(path, SyncSkill)
		return os.Symlink(target, path)
	}
	return c
}

// ---- MCP servers ----

// mcpServer is a server in a shape every agent's own format can be
// written from.
// References to variables in it are in one form, ${VAR} (vars.go).
type mcpServer struct {
	Name    string
	Command string
	Args    []string
	Env     map[string]string
	URL     string
	Headers map[string]string
	// Transport is "sse" for a server that speaks it; "" is streamable
	// HTTP, or stdio without a URL.
	Transport string
}

// serversOf reads the servers the agent to copy from declares in the
// checkout. Servers whose environment holds a value rather than a ${VAR}
// reference are named in secret instead: conch does not copy those.
func serversOf(root string, a agentFiles) (list []mcpServer, secret []string) {
	where := a.mcpRead
	if where == "" {
		where = a.mcp
	}
	path := a.at(root, where)
	var raw map[string]any
	if strings.HasSuffix(a.mcp, ".toml") {
		raw = obj(readTOML(path), a.mcpKey)
	} else {
		raw = obj(readJSON(path), a.mcpKey)
	}
	for _, name := range sortedKeys(raw) {
		m, _ := raw[name].(map[string]any)
		if m == nil {
			continue
		}
		s := serverOf(a.name, name, m)
		if s.Command == "" && s.URL == "" {
			continue
		}
		if holdsValue(s.Env) || holdsValue(s.Headers) {
			secret = append(secret, name)
			continue
		}
		list = append(list, s)
	}
	return list, secret
}

// serverOf reads one server from agent's file, in its own format, with
// its references put in the one form.
func serverOf(agent, name string, m map[string]any) mcpServer {
	s := mcpServer{Name: name, Command: neutral(str(m, "command")), URL: neutral(firstStr(m, "httpUrl", "url", "serverUrl"))}
	if cmd, ok := m["command"].([]any); ok && len(cmd) > 0 { // OpenCode: command is a list
		s.Command = neutral(anyStr(cmd[0]))
		for _, v := range cmd[1:] {
			s.Args = append(s.Args, neutral(anyStr(v)))
		}
	}
	for _, v := range strs(m["args"]) {
		s.Args = append(s.Args, neutral(v))
	}
	s.Env = neutralMap(strMap(m["env"]))
	if len(s.Env) == 0 {
		s.Env = neutralMap(strMap(m["environment"])) // OpenCode's name for it
	}
	s.Headers = neutralMap(strMap(m["headers"]))
	switch t := firstStr(m, "type", "transport"); {
	case t == "sse":
		s.Transport = "sse"
	case t == "" && agent == "gemini" && str(m, "httpUrl") == "" && s.URL != "":
		s.Transport = "sse" // Gemini's url is SSE, its httpUrl streamable HTTP
	}
	if agent == "codex" {
		// Codex names the variables to pass on rather than referring to them.
		for _, v := range anyList(m["env_vars"]) {
			n := anyStr(v)
			if o, ok := v.(map[string]any); ok {
				n = str(o, "name")
			}
			if n != "" {
				s.Env = setKey(s.Env, n, "${"+n+"}")
			}
		}
		if t := str(m, "bearer_token_env_var"); t != "" {
			s.Headers = setKey(s.Headers, "Authorization", "Bearer ${"+t+"}")
		}
		for h, v := range strMap(m["env_http_headers"]) {
			s.Headers = setKey(s.Headers, h, "${"+v+"}")
		}
		for h, v := range strMap(m["http_headers"]) {
			s.Headers = setKey(s.Headers, h, v)
		}
	}
	return s
}

func neutralMap(m map[string]string) map[string]string {
	for k, v := range m {
		m[k] = neutral(v)
	}
	return m
}

func setKey(m map[string]string, k, v string) map[string]string {
	if m == nil {
		m = map[string]string{}
	}
	m[k] = v
	return m
}

func anyList(v any) []any {
	l, _ := v.([]any)
	return l
}

// holdsValue reports whether any value is something other than a
// reference to an environment variable — a key, a token, a password.
func holdsValue(m map[string]string) bool {
	for _, v := range m {
		if !safeValue(v) {
			return true
		}
	}
	return false
}

// planServer declares one server in the agent's own file and format.
func planServer(root, agent string, t agentFiles, s mcpServer) SyncChange {
	path := t.at(root, t.mcp)
	c := SyncChange{Agent: agent, Kind: SyncMCP, Name: s.Name, Path: show(root, path, t)}
	if t.mcp == "" {
		// Claude's user-scope servers live in the file it keeps its own
		// state in; conch reads that and does not rewrite it.
		c.Action, c.Detail = ActionSkip, "conch does not write "+labelOf(agent)+"'s servers here; add it with its own command"
		return c
	}
	// A file somebody keeps in a dotfiles repository is a link into it,
	// and writing through the link edits that repository.
	if st, err := os.Lstat(path); err == nil && st.Mode()&os.ModeSymlink != 0 {
		c.Action, c.Detail = ActionSkip, "it is a link into somewhere else (a dotfiles repository?): conch leaves it alone"
		return c
	}
	if why := unwritable(agent, s); why != "" {
		c.Action, c.Detail = ActionSkip, why
		return c
	}
	if strings.HasSuffix(t.mcp, ".toml") {
		return planServerTOML(path, c, t, s)
	}
	return planServerJSON(path, c, t, s)
}

// planServerJSON adds the server to a JSON file, keeping everything else
// in it. A file with comments is left alone: rewriting it would drop them.
// The file is read again when the change is applied, so two servers going
// into the same file do not write over each other.
func planServerJSON(path string, c SyncChange, t agentFiles, s mcpServer) SyncChange {
	agent := c.Agent
	edit := func() ([]byte, bool, error) {
		b, err := os.ReadFile(path)
		doc := map[string]any{}
		switch {
		case os.IsNotExist(err):
			b = nil
		case err != nil:
			return nil, false, err
		default:
			if json.Unmarshal(b, &doc) != nil {
				return nil, false, errors.New(t.mcp + " is not plain JSON (comments, or a mistake in it): conch leaves it alone")
			}
		}
		servers := obj(doc, t.mcpKey)
		if servers == nil {
			servers = map[string]any{}
		}
		if _, there := servers[s.Name]; there {
			return nil, false, nil // already declared
		}
		servers[s.Name] = jsonServer(agent, s)
		doc[t.mcpKey] = servers
		out, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			return nil, false, err
		}
		return append(out, '\n'), len(b) == 0, nil
	}
	out, fresh, err := edit()
	switch {
	case err != nil:
		c.Action, c.Detail = ActionSkip, err.Error()
		return c
	case out == nil:
		c.Action, c.Detail = ActionSame, "already declared there"
		return c
	}
	c.Action, c.Detail = ActionUpdate, describe(s)
	if fresh {
		c.Action = ActionCreate
	}
	c.write = func(u *undoLog) error {
		out, _, err := edit()
		if err != nil {
			return err
		}
		if out == nil {
			return nil // somebody declared it meanwhile
		}
		return writeFile(path, SyncMCP, out)(u)
	}
	return c
}

// jsonServer is the server in the shape the agent's file wants, its
// references written the way that agent expands them.
func jsonServer(agent string, s mcpServer) map[string]any {
	m := map[string]any{}
	url := refFor(agent, s.URL)
	headers := refsFor(agent, s.Headers)
	env := refsFor(agent, s.Env)
	args := make([]string, len(s.Args))
	for i, a := range s.Args {
		args[i] = refFor(agent, a)
	}
	command := refFor(agent, s.Command)
	switch {
	case agent == "opencode" && url != "":
		m["type"], m["url"], m["enabled"] = "remote", url, true
	case agent == "opencode":
		m["type"], m["enabled"] = "local", true
		m["command"] = toAnyList(append([]string{command}, args...))
		if len(env) > 0 {
			m["environment"] = toAny(env)
		}
	case url != "" && agent == "gemini":
		if s.Transport == "sse" {
			m["url"] = url
		} else {
			m["httpUrl"] = url
		}
	case url != "" && agent == "devin":
		m["url"], m["transport"] = url, "http"
		if s.Transport == "sse" {
			m["transport"] = "sse"
		}
	case url != "":
		m["type"], m["url"] = "http", url
		if s.Transport == "sse" {
			m["type"] = "sse"
		}
	default:
		m["command"] = command
		if len(args) > 0 {
			m["args"] = toAnyList(args)
		}
		if len(env) > 0 {
			m["env"] = toAny(env)
		}
	}
	if url != "" && len(headers) > 0 {
		m["headers"] = toAny(headers)
	}
	return m
}

// planServerTOML appends the server to a TOML file rather than rewriting
// it, so comments and everything else in it survive.
func planServerTOML(path string, c SyncChange, t agentFiles, s mcpServer) SyncChange {
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		c.Action, c.Detail = ActionSkip, err.Error()
		return c
	}
	if _, there := obj(readTOML(path), t.mcpKey)[s.Name]; there {
		c.Action, c.Detail = ActionSame, "already declared there"
		return c
	}
	c.Action, c.Detail = ActionUpdate, describe(s)
	if len(b) == 0 {
		c.Action = ActionCreate
	}
	// Read again when it is applied: two servers may be going into the
	// same file, and the second must not lose the first.
	c.write = func(u *undoLog) error {
		have, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if _, there := obj(readTOML(path), t.mcpKey)[s.Name]; there {
			return nil
		}
		out := have
		if len(out) > 0 && !strings.HasSuffix(string(out), "\n") {
			out = append(out, '\n')
		}
		if len(out) > 0 {
			out = append(out, '\n')
		}
		out = append(out, tomlServer(t.mcpKey, s)...)
		return writeFile(path, SyncMCP, out)(u)
	}
	return c
}

// tomlServer writes one [mcp_servers.name] table for Codex, which is
// told the names of the variables to pass on rather than given references
// (codexVarsOf has already said whether it can be).
func tomlServer(key string, s mcpServer) []byte {
	cv, _ := codexVarsOf(s)
	var b strings.Builder
	table := key + "." + tomlKey(s.Name)
	fmt.Fprintf(&b, "# added by conch\n[%s]\n", table)
	if s.URL != "" {
		fmt.Fprintf(&b, "url = %s\n", tomlString(s.URL))
		if cv.bearer != "" {
			fmt.Fprintf(&b, "bearer_token_env_var = %s\n", tomlString(cv.bearer))
		}
	} else {
		fmt.Fprintf(&b, "command = %s\n", tomlString(s.Command))
		if len(s.Args) > 0 {
			fmt.Fprintf(&b, "args = %s\n", tomlList(s.Args))
		}
	}
	if len(cv.envVars) > 0 {
		fmt.Fprintf(&b, "env_vars = %s\n", tomlList(cv.envVars))
	}
	for _, sub := range []struct {
		name string
		m    map[string]string
	}{{"env", cv.literalEnv}, {"http_headers", cv.literalHead}, {"env_http_headers", cv.headerVars}} {
		if len(sub.m) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n[%s.%s]\n", table, sub.name)
		for _, k := range sortedStrings(sub.m) {
			fmt.Fprintf(&b, "%s = %s\n", tomlKey(k), tomlString(sub.m[k]))
		}
	}
	return []byte(b.String())
}

func tomlList(list []string) string {
	parts := make([]string, len(list))
	for i, a := range list {
		parts[i] = tomlString(a)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

var bareKey = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func tomlKey(s string) string {
	if bareKey.MatchString(s) {
		return s
	}
	return tomlString(s)
}

func tomlString(s string) string {
	b, err := json.Marshal(s) // TOML's basic strings are JSON's
	if err != nil {
		return `""`
	}
	return string(b)
}

// describe says what a server is without showing what opens it.
func describe(s mcpServer) string {
	what := "stdio · " + s.Command
	if s.URL != "" {
		what = "http"
	}
	if len(s.Env) > 0 {
		what += fmt.Sprintf(" · %d variable%s", len(s.Env), plural(len(s.Env)))
	}
	return what
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// ---- writing and undoing ----

// writeFile records what was there and writes the new content.
func writeFile(path, kind string, content []byte) func(*undoLog) error {
	return func(u *undoLog) error {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := u.writing(path, kind); err != nil {
			return err
		}
		return os.WriteFile(path, content, 0o644)
	}
}

// undoDir is where a sync records what it changed, inside the checkout.
const undoDir = ".conch/agent-sync"

// undoLog is one sync's record: what each path held before, so it can be
// put back. A path that did not exist is recorded as one to remove.
type undoLog struct {
	dir string // where the record itself goes
	// Root is what the paths are kept relative to — the checkout, or your
	// home — so undoing puts them back where they came from, whatever the
	// working directory is by then.
	Root  string      `json:"root,omitempty"`
	Stamp string      `json:"stamp"`
	From  string      `json:"from"`
	Files []undoEntry `json:"files"`
	// Library marks an apply of the library, and Written is what conch
	// owned before it, which undoing restores.
	Library bool         `json:"library,omitempty"`
	Written []libWritten `json:"written,omitempty"`
}

type undoEntry struct {
	Path string `json:"path"` // relative to the checkout
	// Kind is what it was — instructions, a skill, a server — so undoing
	// says as much. A record from an older build has none.
	Kind   string `json:"kind,omitempty"`
	Before string `json:"before,omitempty"`
	Absent bool   `json:"absent,omitempty"` // it did not exist
	// Link is where a link conch took away pointed, which undoing makes
	// again; Command is a command that undoes what another command did
	// (claude mcp add-json, for a server Claude keeps in its own file).
	Link    string   `json:"link,omitempty"`
	Command []string `json:"command,omitempty"`
}

// writing records a file about to be written over.
func (u *undoLog) writing(path, kind string) error {
	rel := u.rel(path)
	b, err := os.ReadFile(path)
	switch {
	case os.IsNotExist(err):
		u.Files = append(u.Files, undoEntry{Path: rel, Kind: kind, Absent: true})
		return nil
	case err != nil:
		return err
	}
	u.Files = append(u.Files, undoEntry{Path: rel, Kind: kind, Before: string(b)})
	return nil
}

// creating records something new, which undoing removes.
func (u *undoLog) creating(path, kind string) {
	u.Files = append(u.Files, undoEntry{Path: u.rel(path), Kind: kind, Absent: true})
}

// unlinking records a link taken away, which undoing makes again.
func (u *undoLog) unlinking(path, target string) {
	u.Files = append(u.Files, undoEntry{Path: u.rel(path), Kind: SyncSkill, Link: target})
}

// command records the command that undoes one conch ran.
func (u *undoLog) command(args []string, kind string) {
	name := ""
	if len(args) > 3 {
		name = args[3]
	}
	u.Files = append(u.Files, undoEntry{Path: name, Kind: kind, Command: args})
}

func (u *undoLog) rel(path string) string {
	if u.Root == "" {
		return path
	}
	if r, err := filepath.Rel(u.Root, path); err == nil && !strings.HasPrefix(r, "..") {
		return r
	}
	return path
}

func (u *undoLog) save() error {
	if len(u.Files) == 0 {
		return nil
	}
	dir := u.dir
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// The record holds what the checkout's files said, so it is kept as
	// closely as the handoff documents beside it.
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(u, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, u.Stamp+".json"), append(b, '\n'), 0o600)
}

// SyncUndos lists the syncs of a checkout that can be undone, newest
// first.
func SyncUndos(dir string) []string {
	root := RepoRoot(dir)
	if root == "" {
		root = dir
	}
	return undosIn(filepath.Join(root, undoDir))
}

// undosIn lists the records in a folder, newest first.
func undosIn(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, en := range entries {
		if name := strings.TrimSuffix(en.Name(), ".json"); name != en.Name() {
			out = append(out, name)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(out)))
	return out
}

// UserSyncUndos lists the user-scope syncs that can be undone, newest
// first, and UndoUserSync puts one back. They are kept in conch's own
// folder, since your home has no root to put a .conch in.
func UserSyncUndos() []string { return undosIn(userRecordDir()) }

func UndoUserSync(stamp string) (SyncResult, error) {
	return undoFrom(CurrentEnv(), userRecordDir(), "", stamp, "your home")
}

// UndoSync puts back what a sync changed. stamp "" is the last one.
func UndoSync(dir, stamp string) (SyncResult, error) {
	root := RepoRoot(dir)
	if root == "" {
		root = dir
	}
	return undoFrom(CurrentEnv(), filepath.Join(root, undoDir), root, stamp, root)
}

// undoFrom puts back the sync named by stamp ("" is the last), whose
// record is in recordDir and whose paths are relative to root ("" when
// they are whole). where names the place in messages.
func undoFrom(e Env, recordDir, root, stamp, where string) (SyncResult, error) {
	if stamp == "" {
		list := undosIn(recordDir)
		if len(list) == 0 {
			return SyncResult{Dir: root}, errors.New("conch has no sync to undo in " + where)
		}
		stamp = list[0]
	}
	path := filepath.Join(recordDir, stamp+".json")
	b, err := os.ReadFile(path)
	if err != nil {
		return SyncResult{Dir: root}, fmt.Errorf("no record of sync %s in %s", stamp, where)
	}
	var u undoLog
	if err := json.Unmarshal(b, &u); err != nil {
		return SyncResult{Dir: root}, fmt.Errorf("the record of sync %s cannot be read: %v", stamp, err)
	}
	if u.Root != "" {
		root = u.Root // where it was written, not where we are now
	}
	res := SyncResult{Dir: root, From: u.From, Undo: stamp}
	// Newest first, so a file written twice ends as it began.
	for i := len(u.Files) - 1; i >= 0; i-- {
		f := u.Files[i]
		kind := f.Kind
		if kind == "" {
			kind = "file" // a record from a build that did not say
		}
		c := SyncChange{Kind: kind, Name: filepath.Base(f.Path), Path: f.Path, Action: ActionUpdate}
		target := f.Path // whole, in your home
		if root != "" {
			target = filepath.Join(root, f.Path)
		}
		var err error
		switch {
		case len(f.Command) > 0:
			c.Path, c.Detail = "", "with "+f.Command[0]+" "+strings.Join(f.Command[1:min(3, len(f.Command))], " ")
			err = runAgent(e, f.Command[0], f.Command[1:]...)
		case f.Link != "":
			c.Action, c.Detail = ActionLink, "linked again"
			if err = os.MkdirAll(filepath.Dir(target), 0o755); err == nil {
				err = os.Symlink(f.Link, target)
			}
		case f.Absent:
			c.Action, c.Detail = "remove", "it was not there before"
			err = os.Remove(target)
			if os.IsNotExist(err) {
				err, c.Action, c.Detail = nil, ActionSame, "already gone"
			}
			if err == nil && root != "" {
				removeEmptyDirs(root, filepath.Dir(target))
			}
		default:
			c.Detail = "put back as it was"
			err = os.WriteFile(target, []byte(f.Before), 0o644)
		}
		if err != nil {
			c.Error = err.Error()
		} else if c.Action != ActionSame {
			c.Done = true
		}
		res.Changes = append(res.Changes, c)
	}
	if u.Library {
		if err := writeJSONFile(writtenPath(), u.Written); err != nil {
			res.Notes = append(res.Notes, "what conch owns could not be put back: "+err.Error())
		}
	}
	if err := os.Remove(path); err != nil {
		res.Notes = append(res.Notes, "the record itself could not be removed: "+err.Error())
	} else {
		removeEmptyDirs(root, filepath.Dir(path)) // and .conch, if that is all it held
	}
	return res, nil
}

// removeEmptyDirs takes away the folders a sync made, as far up as they
// are empty: .codex holding nothing but what conch wrote is conch's to
// tidy. Anything with something in it stops it, and so does root.
func removeEmptyDirs(root, dir string) {
	for d := dir; d != root && strings.HasPrefix(d, root+string(filepath.Separator)); d = filepath.Dir(d) {
		if os.Remove(d) != nil {
			return
		}
	}
}

// ---- small helpers ----

func firstStr(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v := str(m, k); v != "" {
			return v
		}
	}
	return ""
}

func anyStr(v any) string {
	s, _ := v.(string)
	return s
}

func strMap(v any) map[string]string {
	m, _ := v.(map[string]any)
	if len(m) == 0 {
		return nil
	}
	out := map[string]string{}
	for k, val := range m {
		if s, ok := val.(string); ok {
			out[k] = s
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func toAny(m map[string]string) map[string]any {
	out := map[string]any{}
	for k, v := range m {
		out[k] = v
	}
	return out
}

func toAnyList(list []string) []any {
	out := make([]any, len(list))
	for i, v := range list {
		out[i] = v
	}
	return out
}

func sortedStrings(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
