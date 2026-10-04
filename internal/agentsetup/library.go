package agentsetup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
)

// The library: MCP servers and skills kept in conch rather than in one
// agent, each given to the agents you choose. A sync copies one agent's
// setup to the others once; the library is what they follow — turn an
// agent off for a server and applying takes it back out, change a server
// and every agent that has it is told.
//
// It is the user scope (your home), and it keeps every rule a sync keeps:
// a plan before anything is written, a server somebody declared by hand
// left alone, a file that is a link into a dotfiles repository skipped,
// secrets never carried, and a record that puts it back. What is new is
// that conch remembers what it wrote (written.json), because a JSON file
// has nowhere to say so, and only what it wrote is ever changed or taken
// out.

// ActionRemove takes out what conch put there before.
const ActionRemove = "remove"

// The library's own types are the protocol's: what the server keeps is
// what a client edits.
type (
	LibServer = proto.LibraryServer
	LibSkill  = proto.LibrarySkill
	Library   = proto.Library
)

// libWritten is one thing conch wrote for the library, so it knows what is
// its own to change or take out.
type libWritten struct {
	Agent string `json:"agent"`
	Kind  string `json:"kind"`
	Name  string `json:"name"`
}

// libraryDir is where the library is kept.
var libraryDir = func() string { return filepath.Join(config.Dir(), "library") }

func libraryPath() string { return filepath.Join(libraryDir(), "library.json") }
func writtenPath() string { return filepath.Join(libraryDir(), "written.json") }
func libraryUndoDir() string {
	return filepath.Join(libraryDir(), "undo")
}

// LoadLibrary reads the library; a missing one is empty.
func LoadLibrary() (Library, error) {
	var lib Library
	b, err := os.ReadFile(libraryPath())
	if os.IsNotExist(err) {
		return lib, nil
	}
	if err != nil {
		return lib, err
	}
	if err := json.Unmarshal(b, &lib); err != nil {
		return lib, fmt.Errorf("%s cannot be read: %v", libraryPath(), err)
	}
	return lib, nil
}

var libName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.@-]*$`)

// CheckLibrary says what is wrong with a library, before it is saved:
// names an agent could not use, a server with nothing to run, a secret
// written out, an agent conch does not know.
func CheckLibrary(lib Library) error {
	seen := map[string]bool{}
	agents := func(what string, list []string) error {
		for _, a := range list {
			if !CanSync(a) {
				return fmt.Errorf("%s: conch cannot set %s up (it knows %s)", what, a, strings.Join(SyncNames(), ", "))
			}
		}
		return nil
	}
	for _, s := range lib.Servers {
		switch {
		case !libName.MatchString(s.Name):
			return fmt.Errorf("%q is not a name every agent accepts: letters, digits, - _ . @", s.Name)
		case seen["s"+s.Name]:
			return fmt.Errorf("two servers are called %s", s.Name)
		case s.Command == "" && s.URL == "":
			return fmt.Errorf("server %s has neither a command nor a URL", s.Name)
		case s.Command != "" && s.URL != "":
			return fmt.Errorf("server %s has both a command and a URL", s.Name)
		case s.Transport != "" && s.Transport != "sse":
			return fmt.Errorf("server %s: the transport is sse or nothing, not %q", s.Name, s.Transport)
		}
		for k, v := range s.Env {
			if !safeValue(v) {
				return fmt.Errorf("server %s: %s holds a value; write ${%s} and set it in your environment — conch does not keep secrets in agents' files", s.Name, k, k)
			}
		}
		for k, v := range s.Headers {
			if !safeValue(v) {
				return fmt.Errorf("server %s: the %s header holds a value; refer to a variable, e.g. Bearer ${TOKEN}", s.Name, k)
			}
		}
		if err := agents("server "+s.Name, s.Agents); err != nil {
			return err
		}
		seen["s"+s.Name] = true
	}
	for _, sk := range lib.Skills {
		switch {
		case !libName.MatchString(sk.Name):
			return fmt.Errorf("%q is not a skill name: letters, digits, - _ . @", sk.Name)
		case seen["k"+sk.Name]:
			return fmt.Errorf("two skills are called %s", sk.Name)
		case sk.Path == "":
			return fmt.Errorf("skill %s has no folder", sk.Name)
		}
		if err := agents("skill "+sk.Name, sk.Agents); err != nil {
			return err
		}
		seen["k"+sk.Name] = true
	}
	return nil
}

// SaveLibrary checks the library and writes it. It writes nothing to any
// agent: applying does that, after the plan has been seen.
func SaveLibrary(lib Library) error {
	for i := range lib.Servers {
		s := &lib.Servers[i]
		s.Command, s.URL = neutral(strings.TrimSpace(s.Command)), neutral(strings.TrimSpace(s.URL))
		for j := range s.Args {
			s.Args[j] = neutral(s.Args[j])
		}
		s.Env, s.Headers = neutralMap(s.Env), neutralMap(s.Headers)
		s.Agents = agentOrder(s.Agents)
	}
	for i := range lib.Skills {
		lib.Skills[i].Agents = agentOrder(lib.Skills[i].Agents)
	}
	if err := CheckLibrary(lib); err != nil {
		return err
	}
	if lib.Servers == nil {
		lib.Servers = []LibServer{}
	}
	if lib.Skills == nil {
		lib.Skills = []LibSkill{}
	}
	return writeJSONFile(libraryPath(), lib)
}

func writeJSONFile(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// agentOrder puts agents in the order conch lists them, once each.
func agentOrder(list []string) []string {
	out := uniq(list...)
	sort.SliceStable(out, func(i, j int) bool { return rankAgent(out[i]) < rankAgent(out[j]) })
	return out
}

func loadWritten() []libWritten {
	var w []libWritten
	if b, err := os.ReadFile(writtenPath()); err == nil {
		_ = json.Unmarshal(b, &w)
	}
	return w
}

func hasWritten(w []libWritten, agent, kind, name string) bool {
	for _, x := range w {
		if x.Agent == agent && x.Kind == kind && x.Name == name {
			return true
		}
	}
	return false
}

// Cell states: where one server or skill stands with one agent.
const (
	CellOn      = "on"      // wanted, and there
	CellPending = "pending" // wanted, and applying writes it
	CellRemove  = "remove"  // not wanted, and applying takes conch's copy out
	CellOff     = "off"     // not wanted, and not there
	CellTheirs  = "theirs"  // not wanted here, but declared by hand: left alone
	CellDiffers = "differs" // wanted, but declared by hand differently: left alone
	CellCannot  = "cannot"  // wanted, and conch cannot write it for this agent
)

// LibCell is where a server or skill stands with one agent.
type LibCell = proto.LibraryCell

// libPlan is a plan for the library, and where each item stands.
type libPlan struct {
	res   SyncResult
	cells []LibCell
	// written is what conch will own once the plan is applied.
	written []libWritten
}

// LibraryStatus says where each server and skill stands with each agent,
// without writing anything.
func LibraryStatus(e Env) (Library, []LibCell, error) {
	lib, err := LoadLibrary()
	if err != nil {
		return lib, nil, err
	}
	p, err := planLibrary(e, lib, loadWritten())
	if err != nil {
		return lib, nil, err
	}
	return lib, p.cells, nil
}

// ApplyLibrary gives each agent what the library says it should have. With
// apply false nothing is written and the changes are what it would do.
func ApplyLibrary(e Env, apply bool) (SyncResult, error) {
	lib, err := LoadLibrary()
	if err != nil {
		return SyncResult{}, err
	}
	before := loadWritten()
	p, err := planLibrary(e, lib, before)
	if err != nil || !apply {
		return p.res, err
	}
	res := p.res
	if res.Writes() == 0 {
		return res, writeJSONFile(writtenPath(), p.written)
	}
	u := &undoLog{Root: "", dir: libraryUndoDir(), Stamp: stampFor(libraryUndoDir()), From: "library", Written: before, Library: true}
	written := p.written
	for i := range res.Changes {
		c := &res.Changes[i]
		if !c.Writes() || c.write == nil {
			continue
		}
		if err := c.write(u); err != nil {
			c.Error = err.Error()
			// Not written, so not conch's: a create that failed is not
			// owned, a remove that failed still is.
			if c.Action == ActionRemove {
				written = append(written, libWritten{c.Agent, c.Kind, c.Name})
			} else {
				written = dropWritten(written, c.Agent, c.Kind, c.Name)
			}
			continue
		}
		c.Done = true
	}
	if err := writeJSONFile(writtenPath(), written); err != nil {
		res.Notes = append(res.Notes, "what conch wrote could not be remembered: "+err.Error())
	}
	if err := u.save(); err != nil {
		res.Notes = append(res.Notes, "what was there before could not be recorded, so this cannot be undone: "+err.Error())
		return res, nil
	}
	res.Undo = u.Stamp
	return res, nil
}

func dropWritten(w []libWritten, agent, kind, name string) []libWritten {
	out := w[:0:0]
	for _, x := range w {
		if !(x.Agent == agent && x.Kind == kind && x.Name == name) {
			out = append(out, x)
		}
	}
	return out
}

// LibraryUndos lists the applies that can be put back, newest first.
func LibraryUndos() []string { return undosIn(libraryUndoDir()) }

// UndoLibrary puts back an apply of the library; stamp "" is the last.
func UndoLibrary(e Env, stamp string) (SyncResult, error) {
	return undoFrom(e, libraryUndoDir(), "", stamp, "the library")
}

func libServer(lib Library, name string) (LibServer, bool) {
	for _, s := range lib.Servers {
		if s.Name == name {
			return s, true
		}
	}
	return LibServer{}, false
}

func libMCP(s LibServer) mcpServer {
	return mcpServer{Name: s.Name, Command: s.Command, Args: s.Args, Env: s.Env, URL: s.URL, Headers: s.Headers, Transport: s.Transport}
}

func sameServer(a, b mcpServer) bool {
	eq := func(x, y map[string]string) bool {
		if len(x) != len(y) {
			return false
		}
		for k, v := range x {
			if y[k] != v {
				return false
			}
		}
		return true
	}
	return a.Command == b.Command && strings.Join(a.Args, "\x00") == strings.Join(b.Args, "\x00") &&
		a.URL == b.URL && a.Transport == b.Transport && eq(a.Env, b.Env) && eq(a.Headers, b.Headers)
}

// declared reads every server an agent declares in its own file, secrets
// and all — only to compare, never to copy.
func declared(root string, a agentFiles) map[string]mcpServer {
	where := a.mcpRead
	if where == "" {
		where = a.mcp
	}
	if where == "" {
		return nil
	}
	path := a.at(root, where)
	var raw map[string]any
	if strings.HasSuffix(where, ".toml") {
		raw = obj(readTOML(path), a.mcpKey)
	} else {
		raw = obj(readJSON(path), a.mcpKey)
	}
	out := map[string]mcpServer{}
	for name, v := range raw {
		if m, ok := v.(map[string]any); ok {
			out[name] = serverOf(a.name, name, m)
		}
	}
	return out
}

func planLibrary(e Env, lib Library, written []libWritten) (libPlan, error) {
	var p libPlan
	if e.Home == "" {
		return p, errors.New("conch does not know where your home is")
	}
	agents := SyncNames()
	p.res = SyncResult{Dir: e.Home, From: "library", To: agents}
	files := map[string]agentFiles{}
	have := map[string]map[string]mcpServer{}
	for _, name := range agents {
		a, _ := userFiles(e, name)
		a.abs, a.name = true, name
		files[name] = a
		have[name] = declared("", a)
	}
	owned := map[string]bool{} // what conch owns once this plan is applied
	for _, w := range written {
		owned[w.Agent+"\x00"+w.Kind+"\x00"+w.Name] = true
	}
	own := func(agent, kind, name string, yes bool) {
		key := agent + "\x00" + kind + "\x00" + name
		if yes {
			owned[key] = true
		} else {
			delete(owned, key)
		}
	}
	cell := func(kind, name, agent, state, detail string) {
		p.cells = append(p.cells, LibCell{Kind: kind, Name: name, Agent: agent, State: state, Detail: detail})
	}

	// Servers: what the library holds, and what conch wrote for a server
	// the library no longer holds, which is conch's to take out.
	names := map[string]bool{}
	for _, s := range lib.Servers {
		names[s.Name] = true
	}
	for _, w := range written {
		if w.Kind == SyncMCP {
			names[w.Name] = true
		}
	}
	serverNames := make([]string, 0, len(names))
	for n := range names {
		serverNames = append(serverNames, n)
	}
	sort.Strings(serverNames)

	// Who will have each server after this plan, so an agent that reads
	// another's file (Devin reads Claude Code's and OpenCode's) is not
	// given a second copy of it.
	willHave := func(agent, name string) bool {
		s, ok := libServer(lib, name)
		if !ok || !contains(s.Agents, agent) {
			return false
		}
		got, there := have[agent][name]
		if !there && agent == "claude" && agentBinary(e, "claude") == "" {
			return false // it cannot be given it
		}
		return !there || sameServer(got, libMCP(s)) || owned[agent+"\x00"+SyncMCP+"\x00"+name]
	}
	providers := func(agent string) []string { return providersOf(files, agent) }

	for _, name := range serverNames {
		s, inLib := libServer(lib, name)
		for _, agent := range agents {
			t := files[agent]
			mine := owned[agent+"\x00"+SyncMCP+"\x00"+name]
			got, there := have[agent][name]
			want := inLib && contains(s.Agents, agent)
			var from string
			for _, pr := range providers(agent) {
				if willHave(pr, name) {
					from = pr
					break
				}
			}
			switch {
			case want && from != "" && !(there && mine):
				cell(SyncMCP, name, agent, CellOn, labelOf(agent)+" reads it from "+labelOf(from)+"'s")
			case want && from != "":
				// It has its own copy and will read another: take conch's out.
				c := planServerRemove(e, t, name)
				c.Detail = labelOf(agent) + " reads it from " + labelOf(from) + "'s now"
				p.res.Changes = append(p.res.Changes, c)
				own(agent, SyncMCP, name, c.Action != ActionRemove)
				cell(SyncMCP, name, agent, CellOn, c.Detail)
			case want && there && sameServer(got, libMCP(s)):
				cell(SyncMCP, name, agent, CellOn, "")
			case want && there && !mine:
				cell(SyncMCP, name, agent, CellDiffers, "declared there by hand, differently: conch leaves it alone")
			case want:
				c := planLibServer(e, t, libMCP(s), there)
				p.res.Changes = append(p.res.Changes, c)
				switch {
				case c.Writes():
					own(agent, SyncMCP, name, true)
					cell(SyncMCP, name, agent, CellPending, c.Detail)
				case c.Action == ActionSame:
					cell(SyncMCP, name, agent, CellOn, c.Detail)
				default:
					cell(SyncMCP, name, agent, CellCannot, c.Detail)
				}
			case there && mine:
				c := planServerRemove(e, t, name)
				p.res.Changes = append(p.res.Changes, c)
				own(agent, SyncMCP, name, c.Action != ActionRemove)
				state := CellRemove
				if !c.Writes() {
					state = CellCannot // it cannot be taken out
				}
				if inLib || state == CellRemove {
					cell(SyncMCP, name, agent, state, c.Detail)
				}
			case mine:
				own(agent, SyncMCP, name, false) // already gone
				if inLib {
					cell(SyncMCP, name, agent, CellOff, "")
				}
			case there && inLib:
				cell(SyncMCP, name, agent, CellTheirs, "declared there by hand")
			case inLib && readFrom(have, providers(agent), name) != "":
				cell(SyncMCP, name, agent, CellTheirs, labelOf(agent)+" reads it from "+labelOf(readFrom(have, providers(agent), name))+"'s")
			case inLib:
				cell(SyncMCP, name, agent, CellOff, "")
			}
		}
	}

	// Skills: linked into each agent's first folder, which several agents
	// share — ~/.agents/skills serves Codex, Gemini and OpenCode — so one
	// link may serve an agent that wants it and one that does not.
	skillNames := map[string]bool{}
	for _, sk := range lib.Skills {
		skillNames[sk.Name] = true
	}
	for _, w := range written {
		if w.Kind == SyncSkill {
			skillNames[w.Name] = true
		}
	}
	sorted := make([]string, 0, len(skillNames))
	for n := range skillNames {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)
	for _, name := range sorted {
		var sk LibSkill
		inLib := false
		for _, x := range lib.Skills {
			if x.Name == name {
				sk, inLib = x, true
			}
		}
		dir := expandHome(e, sk.Path)
		if inLib && !isFile(filepath.Join(dir, "SKILL.md")) {
			for _, agent := range sk.Agents {
				cell(SyncSkill, name, agent, CellCannot, "no SKILL.md in "+tilde(e, dir))
			}
			continue
		}
		wantAt := map[string]string{} // link path → an agent that wants it
		for _, agent := range agents {
			if inLib && contains(sk.Agents, agent) {
				wantAt[filepath.Join(files[agent].skills[0], name)] = agent
			}
		}
		planned := map[string]string{} // link path → the agent it was planned for
		for _, agent := range agents {
			t := files[agent]
			link := filepath.Join(t.skills[0], name)
			mine := owned[agent+"\x00"+SyncSkill+"\x00"+name]
			want := inLib && contains(sk.Agents, agent)
			ours := linksTo(link, dir) || (!inLib && isLink(link))
			switch {
			case want && planned[link] != "":
				cell(SyncSkill, name, agent, stateOf(planned, link, p.cells), "the same folder as "+labelOf(planned[link]))
				own(agent, SyncSkill, name, true)
			case want:
				c := planSkill("", agent, t, skill{name: name, dir: dir})
				p.res.Changes = append(p.res.Changes, c)
				switch {
				case c.Writes():
					planned[link] = agent
					own(agent, SyncSkill, name, true)
					cell(SyncSkill, name, agent, CellPending, c.Detail)
				case c.Action == ActionSame:
					planned[link] = agent
					cell(SyncSkill, name, agent, CellOn, c.Detail)
				default:
					cell(SyncSkill, name, agent, CellDiffers, c.Detail)
				}
			case mine && ours && wantAt[link] != "":
				// The link serves another agent that still wants it.
				own(agent, SyncSkill, name, false)
				cell(SyncSkill, name, agent, CellCannot, labelOf(agent)+" reads "+tilde(e, filepath.Dir(link))+", which "+labelOf(wantAt[link])+" still wants it in")
			case mine && ours && planned[link] == "remove":
				own(agent, SyncSkill, name, false)
				cell(SyncSkill, name, agent, CellRemove, "")
			case mine && ours:
				c := planSkillRemove(e, agent, link)
				p.res.Changes = append(p.res.Changes, c)
				planned[link] = "remove"
				own(agent, SyncSkill, name, false)
				cell(SyncSkill, name, agent, CellRemove, c.Detail)
			case mine:
				own(agent, SyncSkill, name, false) // gone, or something else is there now
				if inLib {
					cell(SyncSkill, name, agent, CellOff, "")
				}
			case inLib && ours && wantAt[link] == "":
				cell(SyncSkill, name, agent, CellTheirs, "linked there, not by conch")
			case inLib && ours:
				cell(SyncSkill, name, agent, CellOn, "the same folder as "+labelOf(wantAt[link]))
			case inLib:
				cell(SyncSkill, name, agent, CellOff, "")
			}
		}
	}

	for key := range owned {
		parts := strings.SplitN(key, "\x00", 3)
		p.written = append(p.written, libWritten{parts[0], parts[1], parts[2]})
	}
	sort.Slice(p.written, func(i, j int) bool {
		a, b := p.written[i], p.written[j]
		return a.Agent+a.Kind+a.Name < b.Agent+b.Kind+b.Name
	})
	sortChanges(p.res.Changes)
	if len(lib.Servers)+len(lib.Skills) == 0 && len(written) == 0 {
		p.res.Notes = append(p.res.Notes, "the library is empty: add a server or a skill, or import an agent's")
	}
	return p, nil
}

// providersOf lists the agents whose servers agent reads by itself, from
// their own files: Claude Code's and OpenCode's, for Devin.
func providersOf(files map[string]agentFiles, agent string) []string {
	var out []string
	t := files[agent]
	for _, other := range SyncNames() {
		o := files[other]
		where := o.mcpRead
		if where == "" {
			where = o.mcp
		}
		if other != agent && where != "" && t.reads("", where, t.readsMCP) {
			out = append(out, other)
		}
	}
	return out
}

// readFrom is the first of providers that declares the server, or "".
func readFrom(have map[string]map[string]mcpServer, providers []string, name string) string {
	for _, p := range providers {
		if _, ok := have[p][name]; ok {
			return p
		}
	}
	return ""
}

// stateOf is the state of the cell already planned for the agent that
// owns a link.
func stateOf(planned map[string]string, link string, cells []LibCell) string {
	for i := len(cells) - 1; i >= 0; i-- {
		if cells[i].Kind == SyncSkill && cells[i].Agent == planned[link] {
			return cells[i].State
		}
	}
	return CellOn
}

func expandHome(e Env, p string) string {
	if p == "~" {
		return e.Home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(e.Home, p[2:])
	}
	return p
}

func isLink(p string) bool {
	st, err := os.Lstat(p)
	return err == nil && st.Mode()&os.ModeSymlink != 0
}

// linksTo reports whether p is a link that reaches dir.
func linksTo(p, dir string) bool {
	return isLink(p) && cleanPath(p) == cleanPath(dir)
}

// planLibServer declares a server for an agent: in its file, or through
// Claude Code's own command for Claude, whose file conch does not write.
// replace says a copy conch wrote before is there and is replaced.
func planLibServer(e Env, t agentFiles, s mcpServer, replace bool) SyncChange {
	if t.name == "claude" {
		return planClaudeServer(e, t, s, replace)
	}
	if !replace {
		return planServer("", t.name, t, s)
	}
	c := SyncChange{Agent: t.name, Kind: SyncMCP, Name: s.Name, Path: show("", t.mcp, t)}
	if why := unwritable(t.name, s); why != "" {
		c.Action, c.Detail = ActionSkip, why
		return c
	}
	if isLink(t.mcp) {
		c.Action, c.Detail = ActionSkip, "it is a link into somewhere else (a dotfiles repository?): conch leaves it alone"
		return c
	}
	if !plainJSON(t.mcp) {
		c.Action, c.Detail = ActionSkip, filepath.Base(t.mcp)+" is not plain JSON (comments, or a mistake in it): conch leaves it alone"
		return c
	}
	c.Action, c.Detail = ActionUpdate, "the library's is different · "+describe(s)
	if strings.HasSuffix(t.mcp, ".toml") {
		c.write = func(u *undoLog) error {
			have, err := os.ReadFile(t.mcp)
			if err != nil {
				return err
			}
			out := []byte(strings.TrimRight(removeTOMLTable(string(have), t.mcpKey, s.Name), "\n"))
			if len(out) > 0 {
				out = append(out, '\n', '\n')
			}
			return writeFile(t.mcp, SyncMCP, append(out, tomlServer(t.mcpKey, s)...))(u)
		}
		return c
	}
	c.write = editJSONServers(t, func(servers map[string]any) { servers[s.Name] = jsonServer(t.name, s) })
	return c
}

// planServerRemove takes out a server conch declared for an agent.
func planServerRemove(e Env, t agentFiles, name string) SyncChange {
	c := SyncChange{Agent: t.name, Kind: SyncMCP, Name: name, Action: ActionRemove, Detail: "no longer in the library for " + labelOf(t.name)}
	if t.name == "claude" {
		c.Path = "~/.claude.json"
		c.Detail += ", with claude mcp remove"
		c.write = func(u *undoLog) error {
			prev, _ := json.Marshal(claudeJSON(declared("", t)[name]))
			if err := runAgent(e, "claude", "mcp", "remove", name, "--scope", "user"); err != nil {
				return err
			}
			u.command([]string{"claude", "mcp", "add-json", name, string(prev), "--scope", "user"}, SyncMCP)
			return nil
		}
		return c
	}
	c.Path = show("", t.mcp, t)
	if isLink(t.mcp) {
		c.Action, c.Detail = ActionSkip, "it is a link into somewhere else (a dotfiles repository?): conch leaves it alone"
		return c
	}
	if !plainJSON(t.mcp) {
		c.Action, c.Detail = ActionSkip, filepath.Base(t.mcp)+" is not plain JSON (comments, or a mistake in it): conch leaves it alone"
		return c
	}
	if strings.HasSuffix(t.mcp, ".toml") {
		c.write = func(u *undoLog) error {
			have, err := os.ReadFile(t.mcp)
			if err != nil {
				return err
			}
			out := removeTOMLTable(string(have), t.mcpKey, name)
			return writeFile(t.mcp, SyncMCP, []byte(out))(u)
		}
		return c
	}
	c.write = editJSONServers(t, func(servers map[string]any) { delete(servers, name) })
	return c
}

// plainJSON reports whether conch can rewrite a file without losing
// anything: a TOML file it edits as text, a JSON one only when it has no
// comments to drop.
func plainJSON(path string) bool {
	if !strings.HasSuffix(path, ".json") {
		return true
	}
	b, err := os.ReadFile(path)
	var doc map[string]any
	return err != nil || json.Unmarshal(b, &doc) == nil
}

// editJSONServers changes the servers in an agent's JSON file, keeping
// everything else in it; the file is read when the change is applied, so
// two changes to one file do not write over each other.
func editJSONServers(t agentFiles, edit func(map[string]any)) func(*undoLog) error {
	return func(u *undoLog) error {
		doc := map[string]any{}
		b, err := os.ReadFile(t.mcp)
		switch {
		case os.IsNotExist(err):
		case err != nil:
			return err
		default:
			if json.Unmarshal(b, &doc) != nil {
				return errors.New(filepath.Base(t.mcp) + " is not plain JSON (comments, or a mistake in it): conch leaves it alone")
			}
		}
		servers := obj(doc, t.mcpKey)
		if servers == nil {
			servers = map[string]any{}
		}
		edit(servers)
		doc[t.mcpKey] = servers
		out, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			return err
		}
		return writeFile(t.mcp, SyncMCP, append(out, '\n'))(u)
	}
}

// removeTOMLTable takes [key.name] and its sub-tables out of a TOML file,
// with the "# added by conch" line conch wrote above it, and leaves every
// other line as it was.
func removeTOMLTable(text, key, name string) string {
	heads := map[string]bool{"[" + key + "." + tomlKey(name) + "]": true}
	prefix := "[" + key + "." + tomlKey(name) + "."
	lines := strings.Split(text, "\n")
	var out []string
	skipping := false
	for _, ln := range lines {
		trim := strings.TrimSpace(ln)
		if strings.HasPrefix(trim, "[") {
			was := skipping
			skipping = heads[trim] || strings.HasPrefix(trim, prefix)
			if was && !skipping && len(out) > 0 && strings.TrimSpace(out[len(out)-1]) != "" {
				out = append(out, "") // keep the next table set off
			}
			if skipping {
				// Take the marker conch wrote above it, and the blank line
				// that set it off.
				if n := len(out); n > 0 && strings.TrimSpace(out[n-1]) == "# added by conch" {
					out = out[:n-1]
				}
				for n := len(out); n > 0 && strings.TrimSpace(out[n-1]) == ""; n = len(out) {
					out = out[:n-1]
				}
				continue
			}
		}
		if !skipping {
			out = append(out, ln)
		}
	}
	s := strings.Join(out, "\n")
	if strings.TrimSpace(s) == "" {
		return ""
	}
	return strings.TrimRight(s, "\n") + "\n"
}

// planSkillRemove takes out a link conch made for a skill.
func planSkillRemove(e Env, agent, link string) SyncChange {
	c := SyncChange{Agent: agent, Kind: SyncSkill, Name: filepath.Base(link), Path: tilde(e, link),
		Action: ActionRemove, Detail: "no longer in the library for " + labelOf(agent)}
	c.write = func(u *undoLog) error {
		target, err := os.Readlink(link)
		if err != nil {
			return err
		}
		if err := os.Remove(link); err != nil {
			return err
		}
		u.unlinking(link, target)
		return nil
	}
	return c
}

// ---- Claude Code, through its own command ----

// Claude keeps the servers that are yours in ~/.claude.json, with its
// state and every project's history, and rewrites that file as it runs.
// conch does not write it; it asks Claude to, with claude mcp add-json.

// claudeTimeout bounds one claude mcp command.
const claudeTimeout = 30 * time.Second

func planClaudeServer(e Env, t agentFiles, s mcpServer, replace bool) SyncChange {
	c := SyncChange{Agent: "claude", Kind: SyncMCP, Name: s.Name, Path: "~/.claude.json"}
	if agentBinary(e, "claude") == "" {
		c.Action, c.Detail = ActionSkip, "Claude Code is not installed here, and conch adds its servers with its own command"
		return c
	}
	body, _ := json.Marshal(claudeJSON(s))
	c.Action, c.Detail = ActionCreate, "with claude mcp add-json · "+describe(s)
	if replace {
		c.Action, c.Detail = ActionUpdate, "the library's is different · with claude mcp add-json"
	}
	c.write = func(u *undoLog) error {
		var prev []byte
		if replace {
			prev, _ = json.Marshal(claudeJSON(declared("", t)[s.Name]))
			if err := runAgent(e, "claude", "mcp", "remove", s.Name, "--scope", "user"); err != nil {
				return err
			}
		}
		if err := runAgent(e, "claude", "mcp", "add-json", s.Name, string(body), "--scope", "user"); err != nil {
			if replace { // put the one it had back
				_ = runAgent(e, "claude", "mcp", "add-json", s.Name, string(prev), "--scope", "user")
			}
			return err
		}
		if replace {
			u.command([]string{"claude", "mcp", "add-json", s.Name, string(prev), "--scope", "user"}, SyncMCP)
		}
		u.command([]string{"claude", "mcp", "remove", s.Name, "--scope", "user"}, SyncMCP)
		return nil
	}
	return c
}

func claudeJSON(s mcpServer) map[string]any { return jsonServer("claude", s) }

// agentBinary finds an agent's command where its installer puts it, else
// on the PATH of the environment given — not this process's, so tests
// with a scratch environment never reach a real agent.
func agentBinary(e Env, name string) string {
	for _, p := range []string{
		filepath.Join(e.Home, ".local", "bin", name),
		filepath.Join(e.Home, ".claude", "local", name),
	} {
		if isExecutable(p) {
			return p
		}
	}
	for _, d := range filepath.SplitList(e.get("PATH")) {
		if p := filepath.Join(d, name); d != "" && isExecutable(p) {
			return p
		}
	}
	return ""
}

func isExecutable(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir() && st.Mode()&0o111 != 0
}

// runAgent runs one of an agent's own configuration commands — never one
// that talks to a model.
func runAgent(e Env, name string, args ...string) error {
	bin := agentBinary(e, name)
	if bin == "" {
		return fmt.Errorf("%s is not installed here", labelOf(name))
	}
	ctx, cancel := context.WithTimeout(context.Background(), claudeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = e.Home
	cmd.Env = append(os.Environ(), "HOME="+e.Home)
	if p := e.get("PATH"); p != "" {
		cmd.Env = append(cmd.Env, "PATH="+p)
	}
	if d := e.get("CLAUDE_CONFIG_DIR"); d != "" {
		cmd.Env = append(cmd.Env, "CLAUDE_CONFIG_DIR="+d)
	}
	cmd.WaitDelay = time.Second
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if len(msg) > 200 {
			msg = msg[:200] + "…"
		}
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("%s %s: %s", name, strings.Join(args[:min(2, len(args))], " "), msg)
	}
	return nil
}

// ---- import ----

// ImportResult says what an import took into the library, and what it
// left out.
type ImportResult struct {
	Servers []string
	Skills  []string
	Skipped []string // "name: why"
}

// ImportLibrary takes the servers and skills an agent has in your home
// into the library, turned on for that agent — which already has them —
// and for any other that already has the same. Nothing is written to any
// agent. A server already in the library gains the agent instead.
func ImportLibrary(e Env, from string) (ImportResult, error) {
	var ir ImportResult
	a, ok := userFiles(e, from)
	if !ok {
		return ir, fmt.Errorf("conch cannot read %s's setup (it knows %s)", from, strings.Join(SyncNames(), ", "))
	}
	a.abs, a.name = true, from
	lib, err := LoadLibrary()
	if err != nil {
		return ir, err
	}
	others := map[string]map[string]mcpServer{}
	files := map[string]agentFiles{}
	for _, name := range SyncNames() {
		o, _ := userFiles(e, name)
		o.abs, o.name = true, name
		files[name] = o
		others[name] = declared("", o)
	}
	list, secret := serversOf("", a)
	for _, n := range secret {
		ir.Skipped = append(ir.Skipped, n+": its environment or headers hold a value, not a ${VAR} reference")
	}
	for _, s := range list {
		if i := indexServer(lib, s.Name); i >= 0 {
			if !sameServer(libMCP(lib.Servers[i]), s) {
				ir.Skipped = append(ir.Skipped, s.Name+": the library has a different server by that name")
				continue
			}
			lib.Servers[i].Agents = agentOrder(append(lib.Servers[i].Agents, from))
			ir.Servers = append(ir.Servers, s.Name)
			continue
		}
		agents := []string{from}
		for name, have := range others {
			if got, ok := have[s.Name]; ok && sameServer(got, s) {
				agents = append(agents, name)
			}
		}
		// And an agent that reads it from one of those has it too.
		for _, name := range SyncNames() {
			for _, pr := range providersOf(files, name) {
				if contains(agents, pr) {
					agents = append(agents, name)
					break
				}
			}
		}
		lib.Servers = append(lib.Servers, LibServer{Name: s.Name, Command: s.Command, Args: s.Args, Env: s.Env,
			URL: s.URL, Headers: s.Headers, Transport: s.Transport, Agents: agentOrder(agents)})
		ir.Servers = append(ir.Servers, s.Name)
	}
	for _, sk := range skillsOf("", a) {
		if !libName.MatchString(sk.name) {
			ir.Skipped = append(ir.Skipped, sk.name+": not a name conch can link")
			continue
		}
		real := cleanPath(sk.dir)
		found := false
		for i, x := range lib.Skills {
			if x.Name != sk.name {
				continue
			}
			found = true
			if cleanPath(expandHome(e, x.Path)) != real {
				ir.Skipped = append(ir.Skipped, sk.name+": the library has a different skill by that name")
				break
			}
			lib.Skills[i].Agents = agentOrder(append(x.Agents, from))
			ir.Skills = append(ir.Skills, sk.name)
		}
		if found {
			continue
		}
		// Every agent whose folder already reaches it has it already.
		agents := []string{from}
		for _, name := range SyncNames() {
			o, _ := userFiles(e, name)
			for _, d := range o.skills {
				if cleanPath(filepath.Join(d, sk.name)) == real {
					agents = append(agents, name)
				}
			}
		}
		lib.Skills = append(lib.Skills, LibSkill{Name: sk.name, Path: tilde(e, real), Agents: agentOrder(agents)})
		ir.Skills = append(ir.Skills, sk.name)
	}
	if len(ir.Servers)+len(ir.Skills) == 0 {
		if len(ir.Skipped) > 0 {
			return ir, fmt.Errorf("nothing of %s's could be taken: %s", labelOf(from), strings.Join(ir.Skipped, "; "))
		}
		return ir, fmt.Errorf("%w: %s has no MCP servers or skills in your home", ErrNothingToSync, labelOf(from))
	}
	return ir, SaveLibrary(lib)
}

func indexServer(lib Library, name string) int {
	for i, s := range lib.Servers {
		if s.Name == name {
			return i
		}
	}
	return -1
}

// ConchName is what conch's own MCP server is called in the library and in
// every agent's configuration.
const ConchName = "conch"

// ConchServer is conch's own tools as a library entry, so installing them
// is one action rather than one per agent's format: `conch mcp` on stdio,
// which reaches the server over the socket the CLI uses and is scoped to
// the pane the agent runs in.
//
// The command is the bare name rather than this binary's path: an agent is
// started by conch, which puts ~/.local/bin first, and a path would pin a
// build that an update moves. Nothing is passed in the environment —
// `conch mcp` finds its socket the way every other command does.
func ConchServer(agents []string) LibServer {
	return LibServer{Name: ConchName, Command: "conch", Args: []string{"mcp"}, Agents: agentOrder(agents)}
}

// WithConch is lib with conch's own server in it, replacing an entry of
// that name — adding it twice is the same as adding it once.
func WithConch(lib Library, agents []string) Library {
	s := ConchServer(agents)
	if i := indexServer(lib, ConchName); i >= 0 {
		lib.Servers[i] = s
		return lib
	}
	lib.Servers = append(lib.Servers, s)
	return lib
}
