package tui

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amitgb14/conch/internal/proto"
)

// The library: MCP servers and skills kept in conch and given to the
// agents chosen for each, on this computer. It is a page of Settings →
// Agents, since what follows you from project to project is machine-wide.
// Editing it writes to no agent; Apply shows what it would write first.

// libraryShort is each agent's column heading: five full names do not fit
// beside a server's name in a 40-column window.
var libraryShort = map[string]string{"claude": "Cl", "codex": "Cx", "gemini": "Gm", "opencode": "Oc", "devin": "Dv"}

func libraryAgent(a string) string {
	if s, ok := libraryShort[a]; ok {
		return s
	}
	if len(a) >= 2 {
		return a[:2]
	}
	return a
}

// libraryMarks are how a cell's state reads.
func libraryMark(state string) string {
	switch state {
	case "on":
		return styleOK.Render("✓")
	case "pending":
		return styleAccent.Render("+")
	case "remove":
		return styleWarn.Render("−")
	case "differs":
		return styleWarn.Render("!")
	case "cannot":
		return styleErr.Render("✗")
	case "theirs":
		return styleMuted.Render("○")
	}
	return styleMuted.Render("·")
}

type libraryMsg struct {
	res   proto.AgentLibraryResult
	err   error
	flash string // what to say once it arrives
}

type libraryApplyMsg struct {
	res proto.AgentSyncResult
	err error
}

// loadLibrary asks the local server for the library, saving or importing
// into it first when p says so.
func (m *Model) loadLibrary(p proto.AgentLibraryParams, flash string) tea.Cmd {
	c := m.clientOf(localMachine)
	if c == nil {
		m.libraryErr = m.offlineText(localMachine)
		return nil
	}
	if len(c.MissingCapabilities([]string{proto.CapAgentLibrary})) > 0 {
		m.libraryErr = "the server on this computer is too old for the library; reload it"
		return nil
	}
	return func() tea.Msg {
		var res proto.AgentLibraryResult
		err := callCtx(c, proto.MethodAgentLibrary, p, &res)
		return libraryMsg{res: res, err: err, flash: flash}
	}
}

func (m *Model) receiveLibrary(msg libraryMsg) tea.Cmd {
	if msg.err != nil {
		// Said in the status bar, so the page stays where it was.
		m.setFlash(msg.err.Error(), true)
		return nil
	}
	res := msg.res
	m.library, m.libraryErr = &res, ""
	if msg.flash != "" {
		m.setFlash(msg.flash, false)
	}
	return nil
}

// libraryApply asks what applying would write, writes it, or puts the
// last one back.
func (m *Model) libraryApply(apply, undo bool) tea.Cmd {
	c := m.clientOf(localMachine)
	if c == nil {
		m.setFlash(m.offlineText(localMachine), true)
		return nil
	}
	if len(c.MissingCapabilities([]string{proto.CapAgentLibrary})) > 0 {
		m.setFlash("the server on this computer is too old for the library; reload it", true)
		return nil
	}
	return func() tea.Msg {
		var res proto.AgentSyncResult
		err := callCtx(c, proto.MethodLibraryApply, proto.LibraryApplyParams{Apply: apply, Undo: undo}, &res)
		return libraryApplyMsg{res: res, err: err}
	}
}

func (m *Model) receiveLibraryApply(msg libraryApplyMsg) tea.Cmd {
	if msg.err != nil {
		m.setFlash(msg.err.Error(), true)
		return nil
	}
	res := msg.res
	reload := m.loadLibrary(proto.AgentLibraryParams{}, "")
	switch {
	case res.Undone:
		m.setFlash("put the library's last apply back · "+counted(doneCount(res), "change"), false)
		return reload
	case res.Applied:
		text := "gave each agent what the library says · " + counted(doneCount(res), "change")
		if failed := failedSync(res); len(failed) > 0 {
			m.setFlash(text+" · "+strings.Join(failed, "; "), true)
			return reload
		}
		m.setFlash(text+" · Put the last one back… undoes it", false)
		return reload
	}
	if syncWrites(res) == 0 {
		m.setFlash("every agent already has what the library says", false)
		return reload
	}
	back := m.overlay
	d := newConfirm("", func(m *Model) tea.Cmd {
		m.overlay = back // the page it was asked from
		return m.libraryApply(true, false)
	})
	d.title = " Apply the library "
	d.text = append([]string{"Give each agent what the library says, in your home?", ""}, syncChangeLines(res)...)
	d.text = append(d.text, "", "A server or link somebody made by hand is left alone, and only what conch wrote is ever taken out. Put the last one back… undoes it.")
	m.overlay = d
	return nil
}

// libraryItems is the page: a row per server and skill with a column per
// agent, and the ways to add, take in and apply.
func (s *settings) libraryItems(m *Model) []settingItem {
	items := []settingItem{
		{header: true, label: "Shared MCP servers & skills", detail: "given to the agents you choose"},
		{label: styleMuted.Render("‹ Agents"), detail: styleMuted.Render("esc"),
			run: func(m *Model) tea.Cmd { s.openPage(""); return nil }},
	}
	if m.libraryErr != "" {
		return append(items, settingItem{label: styleErr.Render("  " + m.libraryErr)})
	}
	lib := m.library
	if lib == nil {
		return append(items, settingItem{label: styleMuted.Render("  loading…")})
	}
	var heads []string
	for _, a := range lib.Agents {
		heads = append(heads, libraryAgent(a))
	}
	head := strings.Join(heads, " ")
	state := map[string]proto.LibraryCell{}
	for _, c := range lib.Cells {
		state[c.Kind+"\x00"+c.Name+"\x00"+c.Agent] = c
	}
	// row is one server or skill: its cells, and a line for each agent it
	// cannot reach, since that is the thing worth reading.
	row := func(kind, name, what string, run func(m *Model) tea.Cmd) {
		var marks []string
		var why []settingItem
		for _, a := range lib.Agents {
			c, ok := state[kind+"\x00"+name+"\x00"+a]
			marks = append(marks, libraryMark(c.State)+" ")
			if ok && (c.State == "differs" || c.State == "cannot") && c.Detail != "" {
				why = append(why, settingItem{label: styleMuted.Render("     " + libraryAgent(a) + " " + c.Detail)})
			}
		}
		items = append(items, settingItem{label: name + " " + styleMuted.Render(what), detail: strings.Join(marks, ""), run: run})
		items = append(items, why...)
	}

	items = append(items, settingItem{}, settingItem{header: true, label: "MCP servers", detail: head})
	servers := append([]proto.LibraryServer(nil), lib.Library.Servers...)
	sort.Slice(servers, func(i, j int) bool { return servers[i].Name < servers[j].Name })
	for _, sv := range servers {
		sv := sv
		row(proto.SyncMCP, sv.Name, serverWhat(sv), func(m *Model) tea.Cmd { return m.openLibraryServer(s, &sv) })
	}
	items = append(items, settingItem{label: "Add a server…", run: func(m *Model) tea.Cmd { return m.openLibraryServer(s, nil) }})

	items = append(items, settingItem{}, settingItem{header: true, label: "Skills", detail: head})
	skills := append([]proto.LibrarySkill(nil), lib.Library.Skills...)
	sort.Slice(skills, func(i, j int) bool { return skills[i].Name < skills[j].Name })
	for _, sk := range skills {
		sk := sk
		row(proto.SyncSkill, sk.Name, sk.Path, func(m *Model) tea.Cmd { return m.openLibrarySkill(s, &sk) })
	}
	items = append(items, settingItem{label: "Add a skill…", run: func(m *Model) tea.Cmd { return m.openLibrarySkill(s, nil) }})

	pending := 0
	for _, c := range lib.Cells {
		if c.State == "pending" || c.State == "remove" {
			pending++
		}
	}
	apply := styleMuted.Render("every agent has it")
	if pending > 0 {
		apply = styleAccent.Render(counted(pending, "change") + " to write")
	}
	undo := styleMuted.Render("nothing to put back")
	if len(lib.Undos) > 0 {
		undo = styleMuted.Render("undoes the last apply")
	}
	items = append(items, settingItem{},
		settingItem{label: "Take an agent's into the library…", detail: styleMuted.Render("its servers and skills"),
			run: func(m *Model) tea.Cmd { return m.openLibraryImport(s) }},
		settingItem{label: "Apply…", detail: apply, run: func(m *Model) tea.Cmd { return m.libraryApply(false, false) }},
		settingItem{label: "Put the last one back…", detail: undo, run: func(m *Model) tea.Cmd {
			if len(lib.Undos) == 0 {
				m.setFlash("the library has not been applied yet", false)
				return nil
			}
			return m.libraryApply(false, true)
		}},
		settingItem{},
		settingItem{label: styleMuted.Render("  ✓ has it  + Apply writes it  − Apply takes it out  · off")},
		settingItem{label: styleMuted.Render("  ○ set by hand  ! differs, left alone  ✗ cannot")},
		settingItem{label: styleMuted.Render("  Cl Claude Code · Cx Codex · Gm Gemini · Oc OpenCode · Dv Devin")},
		settingItem{label: styleMuted.Render("  Secrets stay in your environment: write ${TOKEN}, not the token.")})
	return items
}

func serverWhat(sv proto.LibraryServer) string {
	if sv.URL != "" {
		if sv.Transport == "sse" {
			return "sse"
		}
		return "http"
	}
	return "stdio · " + sv.Command
}

// copyLibrary is a library whose slices can be changed without changing
// the one on screen.
func copyLibrary(lib proto.Library) proto.Library {
	return proto.Library{
		Servers: append([]proto.LibraryServer{}, lib.Servers...),
		Skills:  append([]proto.LibrarySkill{}, lib.Skills...),
	}
}

// agentChecks adds a checkbox per agent to d, on for those in have.
func agentChecks(d *dialog, agents, have []string) {
	for _, a := range agents {
		on := false
		for _, h := range have {
			on = on || h == a
		}
		d.addCheck(agentLabel(a), "give it to "+agentLabel(a), on)
	}
}

// checkedAgents reads those checkboxes back from the values from start.
func checkedAgents(agents, values []string, start int) []string {
	var out []string
	for i, a := range agents {
		if start+i < len(values) && values[start+i] == "on" {
			out = append(out, a)
		}
	}
	return out
}

// openLibraryServer is the form for a server, new or existing.
func (m *Model) openLibraryServer(s *settings, sv *proto.LibraryServer) tea.Cmd {
	if m.library == nil {
		return nil
	}
	agents := m.library.Agents
	title, values, have := " Add a server ", []string{"", "", "", ""}, agents
	if sv != nil {
		title = " " + sv.Name + " "
		cmd := sv.URL
		if cmd == "" {
			cmd = joinArgs(append([]string{sv.Command}, sv.Args...))
		}
		values = []string{sv.Name, cmd, joinPairs(sv.Env), joinPairs(sv.Headers)}
		have = sv.Agents
	}
	d := newDialog(*m, title, []string{"A command to run, or the URL of a server. Refer to secrets as ${VAR}: the value stays in your environment."},
		[]string{"Name", "Command or URL", "Environment", "Headers"}, values)
	d.fields[0].in.Placeholder = "github"
	d.fields[1].in.Placeholder = "npx -y @modelcontextprotocol/server-github, or https://…"
	d.fields[2].in.Placeholder = "GITHUB_TOKEN=${GITHUB_TOKEN}; …"
	d.fields[3].in.Placeholder = "Authorization=Bearer ${TOKEN}; … (a URL's)"
	agentChecks(d, agents, have)
	d.addCheck("SSE", "the URL speaks SSE rather than streamable HTTP", sv != nil && sv.Transport == "sse")
	if sv != nil {
		d.addCheck("Remove", "take it out of the library (Apply takes it out of the agents)", false)
	}
	old := ""
	if sv != nil {
		old = sv.Name
	}
	d.back = s
	d.submit = func(m *Model, v []string) tea.Cmd {
		lib, flash, err := serverFromForm(m.library.Library, agents, old, sv != nil, v)
		if err != nil {
			m.setFlash(err.Error(), true)
			return nil
		}
		return m.loadLibrary(proto.AgentLibraryParams{Set: &lib}, flash)
	}
	m.overlay = d
	return d.focusCmd()
}

// serverFromForm is the library once the server form's values are in:
// Name, Command or URL, Environment, Headers, a box per agent, SSE, and
// Remove when editing the server called old.
func serverFromForm(have proto.Library, agents []string, old string, editing bool, v []string) (proto.Library, string, error) {
	lib := copyLibrary(have)
	var out []proto.LibraryServer
	for _, x := range lib.Servers {
		if x.Name != old {
			out = append(out, x)
		}
	}
	n := len(agents)
	if editing && len(v) > 4+n+1 && v[4+n+1] == "on" {
		lib.Servers = out
		return lib, "took " + old + " out of the library · Apply takes it out of the agents", nil
	}
	ns := proto.LibraryServer{Name: strings.TrimSpace(v[0]), Agents: checkedAgents(agents, v, 4)}
	words := splitArgs(v[1])
	switch {
	case len(words) == 1 && (strings.HasPrefix(words[0], "http://") || strings.HasPrefix(words[0], "https://")):
		ns.URL = words[0]
		if len(v) > 4+n && v[4+n] == "on" {
			ns.Transport = "sse"
		}
	case len(words) > 0:
		ns.Command, ns.Args = words[0], words[1:]
	}
	var err error
	if ns.Env, err = parsePairs(v[2]); err == nil {
		ns.Headers, err = parsePairs(v[3])
	}
	if err != nil {
		return have, "", err
	}
	lib.Servers = append(out, ns)
	return lib, "saved " + ns.Name + " · Apply… gives it to the agents", nil
}

// openLibrarySkill is the form for a skill, new or existing.
func (m *Model) openLibrarySkill(s *settings, sk *proto.LibrarySkill) tea.Cmd {
	if m.library == nil {
		return nil
	}
	agents := m.library.Agents
	title, values, have := " Add a skill ", []string{"", ""}, agents
	if sk != nil {
		title, values, have = " "+sk.Name+" ", []string{sk.Name, sk.Path}, sk.Agents
	}
	d := newDialog(*m, title, []string{"A folder holding a SKILL.md, wherever you keep it. Each agent gets a link to it, not a copy."},
		[]string{"Name", "Folder"}, values)
	d.fields[0].in.Placeholder = "review"
	d.fields[1].in.Placeholder = "~/skills/review"
	agentChecks(d, agents, have)
	if sk != nil {
		d.addCheck("Remove", "take it out of the library (Apply takes the links away)", false)
	}
	old := ""
	if sk != nil {
		old = sk.Name
	}
	d.back = s
	d.submit = func(m *Model, v []string) tea.Cmd {
		lib, flash := skillFromForm(m.library.Library, agents, old, sk != nil, v)
		return m.loadLibrary(proto.AgentLibraryParams{Set: &lib}, flash)
	}
	m.overlay = d
	return d.focusCmd()
}

// skillFromForm is the library once the skill form's values are in: Name,
// Folder, a box per agent, and Remove when editing the skill called old.
// A skill with no name is named after its folder.
func skillFromForm(have proto.Library, agents []string, old string, editing bool, v []string) (proto.Library, string) {
	lib := copyLibrary(have)
	var out []proto.LibrarySkill
	for _, x := range lib.Skills {
		if x.Name != old {
			out = append(out, x)
		}
	}
	if editing && len(v) > 2+len(agents) && v[2+len(agents)] == "on" {
		lib.Skills = out
		return lib, "took " + old + " out of the library · Apply takes the links away"
	}
	name, path := strings.TrimSpace(v[0]), strings.TrimSpace(v[1])
	if name == "" && path != "" {
		name = filepath.Base(path)
	}
	lib.Skills = append(out, proto.LibrarySkill{Name: name, Path: path, Agents: checkedAgents(agents, v, 2)})
	return lib, "saved " + name + " · Apply… links it for the agents"
}

// openLibraryImport asks which agent's servers and skills to take in.
func (m *Model) openLibraryImport(s *settings) tea.Cmd {
	if m.library == nil {
		return nil
	}
	d := newDialog(*m, " Take an agent's into the library ", []string{
		"Its MCP servers and skills in your home go into the library, turned on for it — it has them already. Nothing is written to any agent.",
		"A server whose environment holds a value rather than a ${VAR} reference is left out."},
		[]string{"Agent"}, nil)
	d.fields[0].in.Placeholder = strings.Join(m.library.Agents, ", ")
	d.back = s
	d.submit = func(m *Model, v []string) tea.Cmd {
		from := strings.TrimSpace(v[0])
		if from == "" {
			from = "claude"
		}
		return m.loadLibrary(proto.AgentLibraryParams{Import: from}, "took "+agentLabel(from)+"'s servers and skills into the library")
	}
	m.overlay = d
	return d.focusCmd()
}

// parsePairs reads "K=V; K2=V2".
func parsePairs(s string) (map[string]string, error) {
	var out map[string]string
	for _, part := range strings.Split(s, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		k, v, ok := strings.Cut(part, "=")
		if !ok || strings.TrimSpace(k) == "" {
			return nil, fmt.Errorf("%q is not NAME=value", part)
		}
		if out == nil {
			out = map[string]string{}
		}
		out[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return out, nil
}

func joinPairs(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + "=" + m[k]
	}
	return strings.Join(parts, "; ")
}

// splitArgs splits a command line on spaces, keeping "quoted words" whole.
func splitArgs(s string) []string {
	var out []string
	var cur strings.Builder
	in, quoted := false, false
	for _, r := range s {
		switch {
		case r == '"':
			in, quoted = !in, true
		case r == ' ' && !in:
			if cur.Len() > 0 || quoted {
				out = append(out, cur.String())
			}
			cur.Reset()
			quoted = false
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 || quoted {
		out = append(out, cur.String())
	}
	return out
}

// joinArgs is splitArgs the other way: a word with a space is quoted.
func joinArgs(words []string) string {
	out := make([]string, len(words))
	for i, w := range words {
		if w == "" || strings.Contains(w, " ") {
			w = `"` + w + `"`
		}
		out[i] = w
	}
	return strings.Join(out, " ")
}
