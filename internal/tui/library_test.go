package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
)

var libAgents = []string{"claude", "codex", "gemini", "opencode", "devin"}

func libFixture() proto.AgentLibraryResult {
	return proto.AgentLibraryResult{
		Agents: libAgents,
		Library: proto.Library{
			Servers: []proto.LibraryServer{{Name: "gh", Command: "npx", Args: []string{"-y", "server github"},
				Env: map[string]string{"GITHUB_TOKEN": "${GITHUB_TOKEN}"}, Agents: []string{"codex", "gemini"}}},
			Skills: []proto.LibrarySkill{{Name: "tide", Path: "~/skills/tide", Agents: []string{"claude"}}},
		},
		Cells: []proto.LibraryCell{
			{Kind: proto.SyncMCP, Name: "gh", Agent: "codex", State: "pending"},
			{Kind: proto.SyncMCP, Name: "gh", Agent: "gemini", State: "differs", Detail: "declared there by hand, differently"},
			{Kind: proto.SyncMCP, Name: "gh", Agent: "devin", State: "remove"},
			{Kind: proto.SyncSkill, Name: "tide", Agent: "claude", State: "on"},
		},
	}
}

func libPage(t *testing.T, m *Model, s *settings) string {
	t.Helper()
	var lines []string
	for _, it := range s.libraryItems(m) {
		lines = append(lines, ansi.Strip(it.label)+"|"+ansi.Strip(it.detail))
	}
	return strings.Join(lines, "\n")
}

// The library page of Settings → Agents: a row per server and skill with a
// column per agent, what cannot be done said under it, and the ways to add,
// take in and apply.
func TestLibraryPage(t *testing.T) {
	a2Isolate(t)
	m := a2Model()
	s := &settings{tab: 2}
	m.overlay = s

	// The Agents tab has a row for it, which opens the page.
	var row settingItem
	for _, it := range s.agentItems(m) {
		if strings.HasPrefix(ansi.Strip(it.label), "Shared MCP servers & skills…") {
			row = it
		}
	}
	if row.run == nil || !row.page {
		t.Fatal("no row for the library on the Agents tab")
	}
	// Offline, and a server too old for it, say so on the page.
	row.run(m)
	if s.page != "library" || !strings.Contains(libPage(t, m, s), "local is online") {
		t.Fatalf("offline: %q\n%s", s.page, libPage(t, m, s))
	}
	m.machines[0].c = a2Client("agent.setup.v1")
	row.run(m)
	if !strings.Contains(libPage(t, m, s), "too old for the library") {
		t.Fatalf("an old server:\n%s", libPage(t, m, s))
	}
	m.machines[0].c = a2Client(proto.CapAgentLibrary)
	if cmd := row.run(m); cmd == nil || !strings.Contains(libPage(t, m, s), "loading…") {
		t.Fatalf("it should ask the server:\n%s", libPage(t, m, s))
	}

	m.receiveLibrary(libraryMsg{res: libFixture()})
	page := libPage(t, m, s)
	for _, want := range []string{
		"Shared MCP servers & skills|", "‹ Agents|esc",
		"MCP servers|Cl Cx Gm Oc Dv", "gh stdio · npx|· + ! · − ",
		"Gm declared there by hand, differently", "Add a server…",
		"Skills|Cl Cx Gm Oc Dv", "tide ~/skills/tide|✓ · · · · ", "Add a skill…",
		"Take an agent's into the library…", "Apply…|2 changes to write", "Put the last one back…|nothing to put back",
		"write ${TOKEN}, not the token",
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("the page lacks %q:\n%s", want, page)
		}
	}
	// Undo with nothing to undo says so rather than asking.
	for _, it := range s.libraryItems(m) {
		if strings.HasPrefix(ansi.Strip(it.label), "Put the last one back") {
			if cmd := it.run(m); cmd != nil || !strings.Contains(m.flash, "not been applied") {
				t.Fatalf("undo of nothing: %q", m.flash)
			}
		}
	}
	// A server that refuses what was saved says why, and the page stays.
	m.receiveLibrary(libraryMsg{err: errors.New("server x: K holds a value")})
	if !strings.Contains(m.flash, "holds a value") || m.library == nil || m.overlay != s {
		t.Fatalf("refusal: %q %v", m.flash, m.overlay)
	}

	// An empty library still has its ways in.
	empty := proto.AgentLibraryResult{Agents: libAgents, Library: proto.Library{}, Undos: []string{"x"}}
	m.receiveLibrary(libraryMsg{res: empty, flash: "took Claude Code's"})
	page = libPage(t, m, s)
	if !strings.Contains(page, "Apply…|every agent has it") || !strings.Contains(page, "undoes the last apply") ||
		!strings.Contains(m.flash, "took Claude Code's") {
		t.Fatalf("empty:\n%s", page)
	}

	// It fits every window, rendered lines and all.
	m.receiveLibrary(libraryMsg{res: libFixture()})
	for _, size := range [][2]int{{120, 40}, {80, 20}, {60, 16}, {40, 12}, {20, 5}} {
		m.width, m.height = size[0], size[1]
		b := s.render(*m)
		a2CheckBox(t, b, *m)
	}
	// esc goes back to the tab.
	if closed, _ := s.update(m, a2Key("esc")); closed || s.page != "" {
		t.Fatalf("esc: closed %v page %q", closed, s.page)
	}
}

func TestLibraryForms(t *testing.T) {
	a2Isolate(t)
	m := a2Model()
	m.machines[0].c = a2Client(proto.CapAgentLibrary)
	s := &settings{tab: 2, page: "library"}
	m.overlay = s
	if cmd := m.openLibraryServer(s, nil); cmd != nil {
		t.Fatal("a form before the library is known")
	}
	res := libFixture()
	m.library = &res

	// Editing a server fills the form with what it is, arguments quoted.
	gh := res.Library.Servers[0]
	m.openLibraryServer(s, &gh)
	d, ok := m.overlay.(*dialog)
	if !ok || d.back != s || len(d.fields) != 4+len(libAgents)+2 {
		t.Fatalf("form %#v", m.overlay)
	}
	if got := d.fields[1].in.Value(); got != `npx -y "server github"` {
		t.Fatalf("command %q", got)
	}
	if got := d.fields[2].in.Value(); got != "GITHUB_TOKEN=${GITHUB_TOKEN}" {
		t.Fatalf("env %q", got)
	}
	if !*d.fields[5].check || !*d.fields[6].check || *d.fields[4].check {
		t.Fatal("the agent boxes do not say who has it")
	}
	if cmd := d.submit(m, []string{"gh", "npx", "", "", "", "", "", "", "", "", ""}); cmd == nil {
		t.Fatal("saving asks the server")
	}

	// What the form becomes.
	v := func(vals ...string) []string { return vals }
	lib, flash, err := serverFromForm(res.Library, libAgents, "gh", true,
		v("github", `npx -y "server github"`, "A=${A}; B=${B}", "", "on", "", "", "", "on", "", ""))
	if err != nil || len(lib.Servers) != 1 || !strings.Contains(flash, "saved github") {
		t.Fatalf("renamed: %+v %q %v", lib, flash, err)
	}
	if got := lib.Servers[0]; got.Name != "github" || got.Command != "npx" || strings.Join(got.Args, "|") != "-y|server github" ||
		got.Env["B"] != "${B}" || strings.Join(got.Agents, ",") != "claude,devin" {
		t.Fatalf("server %+v", got)
	}
	if res.Library.Servers[0].Name != "gh" {
		t.Fatal("the library on screen was changed")
	}
	lib, _, _ = serverFromForm(res.Library, libAgents, "", false,
		v("docs", "https://docs.example/mcp", "", "Authorization=Bearer ${T}", "", "", "", "", "", "on"))
	if got := lib.Servers[1]; got.URL != "https://docs.example/mcp" || got.Transport != "sse" || got.Headers["Authorization"] != "Bearer ${T}" {
		t.Fatalf("url server %+v", got)
	}
	lib, flash, _ = serverFromForm(res.Library, libAgents, "gh", true, v("gh", "npx", "", "", "", "", "", "", "", "", "on"))
	if len(lib.Servers) != 0 || !strings.Contains(flash, "took gh out") {
		t.Fatalf("remove: %+v %q", lib, flash)
	}
	if _, _, err := serverFromForm(res.Library, libAgents, "", false, v("x", "x", "nope", "", "", "", "", "", "", "")); err == nil {
		t.Fatal("a pair without =")
	}
	if cmd := d.submit(m, v("x", "x", "nope", "", "", "", "", "", "", "", "")); cmd != nil || !strings.Contains(m.flash, "not NAME=value") {
		t.Fatalf("bad pair: %q", m.flash)
	}

	// Skills: a new one takes its folder's name when it has none.
	m.openLibrarySkill(s, nil)
	if d, ok := m.overlay.(*dialog); !ok || len(d.fields) != 2+len(libAgents) {
		t.Fatalf("skill form %#v", m.overlay)
	}
	lib, flash = skillFromForm(res.Library, libAgents, "", false, v("", "~/skills/review/", "", "on", "", "", ""))
	if got := lib.Skills[1]; got.Name != "review" || strings.Join(got.Agents, ",") != "codex" || !strings.Contains(flash, "saved review") {
		t.Fatalf("skill %+v %q", got, flash)
	}
	lib, flash = skillFromForm(res.Library, libAgents, "tide", true, v("tide", "~/skills/tide", "", "", "", "", "", "on"))
	if len(lib.Skills) != 0 || !strings.Contains(flash, "took tide out") {
		t.Fatalf("skill remove %+v", lib)
	}
	tide := res.Library.Skills[0]
	m.openLibrarySkill(s, &tide)
	if d, ok := m.overlay.(*dialog); !ok || d.fields[1].in.Value() != "~/skills/tide" || len(d.fields) != 2+len(libAgents)+1 {
		t.Fatalf("editing a skill %#v", m.overlay)
	}

	// Import: an empty field means Claude Code.
	m.openLibraryImport(s)
	d = m.overlay.(*dialog)
	if cmd := d.submit(m, []string{""}); cmd == nil {
		t.Fatal("import asks the server")
	}
}

func TestLibraryApplyFlow(t *testing.T) {
	a2Isolate(t)
	m := a2Model()
	if cmd := m.libraryApply(false, false); cmd != nil || !strings.Contains(m.flash, "local is online") {
		t.Fatalf("offline: %q", m.flash)
	}
	m.machines[0].c = a2Client()
	if cmd := m.libraryApply(false, false); cmd != nil || !strings.Contains(m.flash, "too old for the library") {
		t.Fatalf("old: %q", m.flash)
	}
	m.machines[0].c = a2Client(proto.CapAgentLibrary)
	s := &settings{tab: 2, page: "library"}
	m.overlay = s

	plan := proto.AgentSyncResult{Dir: "~", From: "library", Changes: []proto.SyncChange{
		{Agent: "codex", Kind: proto.SyncMCP, Name: "gh", Path: "~/.codex/config.toml", Action: proto.SyncCreate, Detail: "stdio · npx"},
		{Agent: "devin", Kind: proto.SyncMCP, Name: "gh", Path: "~/.config/devin/mcp_config.json", Action: proto.SyncRemove},
		{Agent: "gemini", Kind: proto.SyncMCP, Name: "gh", Action: proto.SyncSkip, Detail: "declared there by hand"},
	}}
	m.receiveLibraryApply(libraryApplyMsg{res: plan})
	d, ok := m.overlay.(*dialog)
	if !ok || !d.confirm || !strings.Contains(d.title, "Apply the library") {
		t.Fatalf("plan %#v", m.overlay)
	}
	text := strings.Join(d.text, "\n")
	for _, want := range []string{"what the library says", "create gh → ~/.codex/config.toml", "remove gh → ~/.config/devin/mcp_config.json", "only what conch wrote"} {
		if !strings.Contains(text, want) {
			t.Fatalf("the question lacks %q:\n%s", want, text)
		}
	}
	// Yes goes back to the page and applies.
	if cmd := d.submit(m, nil); cmd == nil || m.overlay != s {
		t.Fatalf("yes: %v", m.overlay)
	}
	applied := plan
	applied.Applied, applied.Undo = true, "x"
	applied.Changes[0].Done, applied.Changes[1].Done = true, true
	if cmd := m.receiveLibraryApply(libraryApplyMsg{res: applied}); cmd == nil || !strings.Contains(m.flash, "2 changes") {
		t.Fatalf("applied: %q", m.flash)
	}
	applied.Changes[1].Error = "permission denied"
	m.receiveLibraryApply(libraryApplyMsg{res: applied})
	if !strings.Contains(m.flash, "permission denied") {
		t.Fatalf("a failure: %q", m.flash)
	}
	m.receiveLibraryApply(libraryApplyMsg{res: proto.AgentSyncResult{Undone: true, Changes: []proto.SyncChange{{Done: true}}}})
	if !strings.Contains(m.flash, "put the library's last apply back · 1 change") {
		t.Fatalf("undone: %q", m.flash)
	}
	m.overlay = s
	m.receiveLibraryApply(libraryApplyMsg{res: proto.AgentSyncResult{Changes: []proto.SyncChange{{Action: proto.SyncSame}}}})
	if m.overlay != s || !strings.Contains(m.flash, "already has") {
		t.Fatalf("nothing to do: %q", m.flash)
	}
	m.receiveLibraryApply(libraryApplyMsg{err: errors.New("the library cannot be read")})
	if !strings.Contains(m.flash, "cannot be read") {
		t.Fatalf("error: %q", m.flash)
	}
}

func TestSplitAndJoinArgs(t *testing.T) {
	for in, want := range map[string]string{
		"":                  "",
		"npx -y x":          "npx|-y|x",
		`npx  "a b"  c`:     "npx|a b|c",
		`x ""`:              "x|",
		`"unterminated arg`: "unterminated arg",
	} {
		if got := strings.Join(splitArgs(in), "|"); got != want {
			t.Errorf("splitArgs(%q) = %q, want %q", in, got, want)
		}
	}
	words := []string{"npx", "a b", ""}
	if got := splitArgs(joinArgs(words)); strings.Join(got, "|") != strings.Join(words, "|") {
		t.Fatalf("round trip %q", got)
	}
	if got, err := parsePairs(" A = ${A} ;; B=x=y "); err != nil || got["A"] != "${A}" || got["B"] != "x=y" {
		t.Fatalf("pairs %v %v", got, err)
	}
	if got, _ := parsePairs("  "); got != nil {
		t.Fatal("empty pairs")
	}
	if joinPairs(map[string]string{"B": "2", "A": "1"}) != "A=1; B=2" {
		t.Fatal("joinPairs")
	}
	if libraryAgent("claude") != "Cl" || libraryAgent("aider") != "ai" || libraryAgent("x") != "x" {
		t.Fatal("libraryAgent")
	}
}
