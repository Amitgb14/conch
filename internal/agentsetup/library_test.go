package agentsetup

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// libHome is a scratch home and conch folder, with no agent installed.
func libHome(t *testing.T) (string, Env) {
	t.Helper()
	base, _ := filepath.EvalSymlinks(t.TempDir())
	home := filepath.Join(base, "home")
	conchHome := filepath.Join(base, "conch")
	os.MkdirAll(home, 0o755)
	t.Setenv("HOME", home)
	t.Setenv("CONCH_HOME", conchHome)
	for _, v := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "XDG_CONFIG_HOME"} {
		t.Setenv(v, "")
	}
	bin := filepath.Join(base, "bin")
	os.MkdirAll(bin, 0o755)
	env := map[string]string{"PATH": bin}
	return home, Env{Home: home, Getenv: func(k string) string { return env[k] }}
}

// fakeClaude puts a claude on the environment's PATH that records what it
// was asked to do.
func fakeClaude(t *testing.T, e Env) string {
	t.Helper()
	log := filepath.Join(e.Home, "claude.log")
	write(t, filepath.Join(e.get("PATH"), "claude"), "#!/bin/sh\necho \"$@\" >> '"+log+"'\n")
	os.Chmod(filepath.Join(e.get("PATH"), "claude"), 0o755)
	return log
}

func cells(cs []LibCell, kind, name string) string {
	var out []string
	for _, c := range cs {
		if c.Kind == kind && c.Name == name {
			out = append(out, c.Agent+"="+c.State)
		}
	}
	return strings.Join(out, " ")
}

func mustSave(t *testing.T, lib Library) {
	t.Helper()
	if err := SaveLibrary(lib); err != nil {
		t.Fatal(err)
	}
}

func mustApply(t *testing.T, e Env) SyncResult {
	t.Helper()
	res, err := ApplyLibrary(e, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range res.Changes {
		if c.Error != "" {
			t.Fatalf("%s %s for %s: %s", c.Kind, c.Name, c.Agent, c.Error)
		}
	}
	return res
}

func TestLibraryCheck(t *testing.T) {
	libHome(t)
	for _, c := range []struct {
		lib  Library
		want string
	}{
		{Library{Servers: []LibServer{{Name: "a b", Command: "x"}}}, "not a name"},
		{Library{Servers: []LibServer{{Name: "", Command: "x"}}}, "not a name"},
		{Library{Servers: []LibServer{{Name: "a", Command: "x"}, {Name: "a", Command: "y"}}}, "two servers"},
		{Library{Servers: []LibServer{{Name: "a"}}}, "neither"},
		{Library{Servers: []LibServer{{Name: "a", Command: "x", URL: "https://x"}}}, "both"},
		{Library{Servers: []LibServer{{Name: "a", URL: "https://x", Transport: "ws"}}}, "transport"},
		{Library{Servers: []LibServer{{Name: "a", Command: "x", Env: map[string]string{"KEY": "sk-123"}}}}, "KEY holds a value"},
		{Library{Servers: []LibServer{{Name: "a", URL: "https://x", Headers: map[string]string{"Authorization": "Bearer abc"}}}}, "header holds a value"},
		{Library{Servers: []LibServer{{Name: "a", Command: "x", Agents: []string{"cursor"}}}}, "cannot set cursor up"},
		{Library{Skills: []LibSkill{{Name: "s"}}}, "no folder"},
		{Library{Skills: []LibSkill{{Name: "s", Path: "/x"}, {Name: "s", Path: "/y"}}}, "two skills"},
		{Library{Skills: []LibSkill{{Name: "../s", Path: "/x"}}}, "not a skill name"},
	} {
		if err := SaveLibrary(c.lib); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%+v: %v, want %q", c.lib, err, c.want)
		}
	}
	if _, err := os.Stat(libraryPath()); !os.IsNotExist(err) {
		t.Fatal("a library that failed its check was saved")
	}
	// References are kept in the one form, and agents in conch's order.
	mustSave(t, Library{Servers: []LibServer{{Name: "gh", Command: " npx ", Env: map[string]string{"T": "{env:T}"},
		Agents: []string{"devin", "codex", "codex"}}}})
	lib, err := LoadLibrary()
	if err != nil || lib.Servers[0].Env["T"] != "${T}" || lib.Servers[0].Command != "npx" ||
		strings.Join(lib.Servers[0].Agents, ",") != "codex,devin" || lib.Skills == nil {
		t.Fatalf("saved %+v %v", lib, err)
	}
	if st, _ := os.Stat(libraryPath()); st.Mode().Perm() != 0o600 {
		t.Fatalf("library mode %v", st.Mode())
	}
	// A library that is not JSON says so rather than being taken as empty.
	write(t, libraryPath(), "{nope")
	if _, err := LoadLibrary(); err == nil || !strings.Contains(err.Error(), "cannot be read") {
		t.Fatalf("broken library: %v", err)
	}
	if _, _, err := LibraryStatus(Env{Home: t.TempDir()}); err == nil {
		t.Fatal("status of a broken library")
	}
}

func TestLibraryEmpty(t *testing.T) {
	_, e := libHome(t)
	lib, cs, err := LibraryStatus(e)
	if err != nil || len(lib.Servers)+len(lib.Skills)+len(cs) != 0 {
		t.Fatalf("empty: %+v %v %v", lib, cs, err)
	}
	res, err := ApplyLibrary(e, true)
	if err != nil || res.Writes() != 0 || !strings.Contains(strings.Join(res.Notes, " "), "library is empty") {
		t.Fatalf("apply empty: %v %+v", err, res)
	}
	if _, err := ApplyLibrary(Env{}, false); err == nil {
		t.Fatal("no home")
	}
}

// A server goes to the agents chosen, each in its own format; turning one
// off takes conch's copy back out and leaves the rest of the file alone.
func TestLibraryServers(t *testing.T) {
	home, e := libHome(t)
	write(t, filepath.Join(home, ".codex", "config.toml"), "# mine\nmodel = \"o3\"\n\n[mcp_servers.own]\ncommand = \"own\"\n")
	write(t, filepath.Join(home, ".gemini", "settings.json"), `{"theme":"dark","mcpServers":{"gh":{"command":"something-else"}}}`)
	gh := LibServer{Name: "gh", Command: "npx", Args: []string{"-y", "server-github"},
		Env: map[string]string{"GITHUB_TOKEN": "${GITHUB_TOKEN}"}, Agents: []string{"codex", "gemini", "devin"}}
	mustSave(t, Library{Servers: []LibServer{gh}})

	// The plan writes nothing, and says where it stands.
	_, cs, err := LibraryStatus(e)
	if err != nil {
		t.Fatal(err)
	}
	if got := cells(cs, SyncMCP, "gh"); got != "claude=off codex=pending gemini=differs opencode=off devin=pending" {
		t.Fatalf("cells: %s", got)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "opencode")); !os.IsNotExist(err) {
		t.Fatal("status wrote something")
	}

	res := mustApply(t, e)
	if res.Undo == "" || res.Writes() != 2 {
		t.Fatalf("apply:\n%s", changeLines(res))
	}
	codex := read(t, filepath.Join(home, ".codex", "config.toml"))
	if !strings.Contains(codex, "# mine") || !strings.Contains(codex, `env_vars = ["GITHUB_TOKEN"]`) {
		t.Fatalf("codex:\n%s", codex)
	}
	if got := read(t, filepath.Join(home, ".gemini", "settings.json")); !strings.Contains(got, "something-else") {
		t.Fatalf("a hand-written server was replaced:\n%s", got)
	}
	var devin map[string]any
	json.Unmarshal([]byte(read(t, filepath.Join(home, ".config", "devin", "mcp_config.json"))), &devin)
	if got := obj(obj(obj(devin, "mcpServers"), "gh"), "env"); str(got, "GITHUB_TOKEN") != "${env:GITHUB_TOKEN}" {
		t.Fatalf("devin %v", devin)
	}
	_, cs, _ = LibraryStatus(e)
	if got := cells(cs, SyncMCP, "gh"); got != "claude=off codex=on gemini=differs opencode=off devin=on" {
		t.Fatalf("after apply: %s", got)
	}
	// A second apply has nothing to do.
	if res, _ := ApplyLibrary(e, false); res.Writes() != 0 {
		t.Fatalf("again:\n%s", changeLines(res))
	}

	// Changed in the library, it is changed where conch put it.
	gh.Args = []string{"-y", "server-github@2"}
	gh.Agents = []string{"gemini", "opencode", "devin"} // Codex off, OpenCode on
	mustSave(t, Library{Servers: []LibServer{gh}})
	res = mustApply(t, e)
	// Devin reads OpenCode's servers, so its own copy goes.
	if c := find(t, res, "devin", SyncMCP, "gh"); c.Action != ActionRemove || !strings.Contains(c.Detail, "reads it from OpenCode's") {
		t.Fatalf("devin: %+v", c)
	}
	if c := find(t, res, "codex", SyncMCP, "gh"); c.Action != ActionRemove || !c.Done {
		t.Fatalf("codex: %+v", c)
	}
	if c := find(t, res, "opencode", SyncMCP, "gh"); c.Action != ActionCreate || !c.Done {
		t.Fatalf("opencode: %+v", c)
	}
	codex = read(t, filepath.Join(home, ".codex", "config.toml"))
	if strings.Contains(codex, "mcp_servers.gh") || strings.Contains(codex, "added by conch") ||
		!strings.Contains(codex, "[mcp_servers.own]") || !strings.Contains(codex, `model = "o3"`) {
		t.Fatalf("codex after turning it off:\n%s", codex)
	}
	if got := read(t, filepath.Join(home, ".config", "opencode", "opencode.json")); !strings.Contains(got, "server-github@2") {
		t.Fatalf("opencode not updated:\n%s", got)
	}

	// Taken out of the library, it is taken out of every agent conch gave
	// it to — and only those.
	// Changed again, where conch put it: an update, not a second copy.
	gh.Args = []string{"-y", "server-github@3"}
	mustSave(t, Library{Servers: []LibServer{gh}})
	res = mustApply(t, e)
	if c := find(t, res, "opencode", SyncMCP, "gh"); c.Action != ActionUpdate || !c.Done {
		t.Fatalf("opencode update: %+v", c)
	}
	mustSave(t, Library{})
	res = mustApply(t, e)
	if res.Writes() != 1 {
		t.Fatalf("removal:\n%s", changeLines(res))
	}
	if got := read(t, filepath.Join(home, ".config", "opencode", "opencode.json")); strings.Contains(got, `"gh"`) {
		t.Fatalf("opencode still has it:\n%s", got)
	}
	if got := read(t, filepath.Join(home, ".gemini", "settings.json")); !strings.Contains(got, "something-else") {
		t.Fatalf("the hand-written one went too:\n%s", got)
	}
	if w := loadWritten(); len(w) != 0 {
		t.Fatalf("still owned: %+v", w)
	}

	// Undoing the removal puts it back, and conch owns it again.
	if _, err := UndoLibrary(e, ""); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(home, ".config", "opencode", "opencode.json")); !strings.Contains(got, `"gh"`) {
		t.Fatalf("undo did not put it back:\n%s", got)
	}
	if w := loadWritten(); !hasWritten(w, "opencode", SyncMCP, "gh") {
		t.Fatalf("ownership not restored: %+v", w)
	}
	if got := LibraryUndos(); len(got) != 3 {
		t.Fatalf("undos %v", got)
	}
}

// Devin reads Claude Code's servers itself, so a server Claude has is not
// given to Devin a second time; Claude's are added with its own command.
func TestLibraryClaudeAndDevin(t *testing.T) {
	home, e := libHome(t)
	log := fakeClaude(t, e)
	mustSave(t, Library{Servers: []LibServer{{Name: "docs", URL: "https://docs.example/mcp",
		Headers: map[string]string{"Authorization": "Bearer ${DOCS_TOKEN}"}, Agents: []string{"claude", "devin"}}}})
	_, cs, _ := LibraryStatus(e)
	if got := cells(cs, SyncMCP, "docs"); got != "claude=pending codex=off gemini=off opencode=off devin=on" {
		t.Fatalf("cells: %s", got)
	}
	res := mustApply(t, e)
	if c := find(t, res, "claude", SyncMCP, "docs"); !c.Done {
		t.Fatalf("claude: %+v", c)
	}
	got := read(t, log)
	if !strings.Contains(got, "mcp add-json docs ") || !strings.Contains(got, `"Bearer ${DOCS_TOKEN}"`) || !strings.Contains(got, "--scope user") {
		t.Fatalf("claude was asked: %s", got)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "devin", "mcp_config.json")); !os.IsNotExist(err) {
		t.Fatal("Devin was given its own copy of what it reads from Claude")
	}
	if _, err := os.Stat(filepath.Join(home, ".claude.json")); !os.IsNotExist(err) {
		t.Fatal("conch wrote Claude's state file")
	}
	// Undoing asks Claude to take it out again.
	if _, err := UndoLibrary(e, ""); err != nil {
		t.Fatal(err)
	}
	if got := read(t, log); !strings.Contains(got, "mcp remove docs --scope user") {
		t.Fatalf("undo: %s", got)
	}

	// With Claude not there to give it to, Devin gets its own.
	os.Remove(filepath.Join(e.get("PATH"), "claude"))
	_, cs, _ = LibraryStatus(e)
	if got := cells(cs, SyncMCP, "docs"); got != "claude=cannot codex=off gemini=off opencode=off devin=pending" {
		t.Fatalf("without claude: %s", got)
	}
	mustApply(t, e)
	// Once Claude is there and has it, Devin's own copy goes.
	fakeClaude(t, e)
	write(t, filepath.Join(home, ".claude.json"), `{"mcpServers":{"docs":{"type":"http","url":"https://docs.example/mcp","headers":{"Authorization":"Bearer ${DOCS_TOKEN}"}}}}`)
	res = mustApply(t, e)
	if c := find(t, res, "devin", SyncMCP, "docs"); c.Action != ActionRemove || !strings.Contains(c.Detail, "reads it from Claude Code's") {
		t.Fatalf("devin: %+v\n%s", c, changeLines(res))
	}
	if got := read(t, filepath.Join(home, ".config", "devin", "mcp_config.json")); strings.Contains(got, `"docs"`) {
		t.Fatalf("devin kept its copy:\n%s", got)
	}
}

// A failed command is reported, and what it did not write is not owned.
func TestLibraryClaudeFails(t *testing.T) {
	_, e := libHome(t)
	write(t, filepath.Join(e.get("PATH"), "claude"), "#!/bin/sh\necho 'no such scope' >&2\nexit 1\n")
	os.Chmod(filepath.Join(e.get("PATH"), "claude"), 0o755)
	mustSave(t, Library{Servers: []LibServer{{Name: "x", Command: "x", Agents: []string{"claude"}}}})
	res, err := ApplyLibrary(e, true)
	if err != nil {
		t.Fatal(err)
	}
	if c := find(t, res, "claude", SyncMCP, "x"); c.Done || !strings.Contains(c.Error, "no such scope") {
		t.Fatalf("claude: %+v", c)
	}
	if hasWritten(loadWritten(), "claude", SyncMCP, "x") {
		t.Fatal("a failed write is owned")
	}
}

func TestLibrarySkills(t *testing.T) {
	home, e := libHome(t)
	write(t, filepath.Join(home, "skills", "tide", "SKILL.md"), "---\nname: tide\n---\n")
	mustSave(t, Library{Skills: []LibSkill{{Name: "tide", Path: "~/skills/tide", Agents: []string{"claude", "codex", "gemini", "devin"}}}})
	res := mustApply(t, e)
	// ~/.agents/skills serves Codex and Gemini with one link.
	links := 0
	for _, c := range res.Changes {
		if c.Action == ActionLink {
			links++
		}
	}
	if links != 3 {
		t.Fatalf("links:\n%s", changeLines(res))
	}
	for _, p := range []string{".claude/skills/tide", ".agents/skills/tide", ".config/devin/skills/tide"} {
		if !isFile(filepath.Join(home, p, "SKILL.md")) {
			t.Fatalf("%s not linked", p)
		}
	}
	_, cs, _ := LibraryStatus(e)
	// OpenCode reads ~/.agents/skills too, so it has it whether asked or not.
	if got := cells(cs, SyncSkill, "tide"); got != "claude=on codex=on gemini=on opencode=on devin=on" {
		t.Fatalf("cells: %s", got)
	}

	// Codex off, Gemini on: the shared link stays, and says why.
	mustSave(t, Library{Skills: []LibSkill{{Name: "tide", Path: "~/skills/tide", Agents: []string{"claude", "gemini", "devin"}}}})
	_, cs, _ = LibraryStatus(e)
	if got := cells(cs, SyncSkill, "tide"); !strings.Contains(got, "codex=cannot") {
		t.Fatalf("shared: %s", got)
	}
	// Both off: the link goes, once.
	mustSave(t, Library{Skills: []LibSkill{{Name: "tide", Path: "~/skills/tide", Agents: []string{"claude"}}}})
	res = mustApply(t, e)
	removes := 0
	for _, c := range res.Changes {
		if c.Action == ActionRemove {
			removes++
		}
	}
	if removes != 2 || isLink(filepath.Join(home, ".agents", "skills", "tide")) || isLink(filepath.Join(home, ".config", "devin", "skills", "tide")) {
		t.Fatalf("removal:\n%s", changeLines(res))
	}
	if !isFile(filepath.Join(home, "skills", "tide", "SKILL.md")) {
		t.Fatal("the skill itself was taken")
	}
	// Undo makes the links again.
	if _, err := UndoLibrary(e, ""); err != nil {
		t.Fatal(err)
	}
	if !linksTo(filepath.Join(home, ".agents", "skills", "tide"), filepath.Join(home, "skills", "tide")) {
		t.Fatal("undo did not link it again")
	}

	// A folder without a SKILL.md cannot be given to anyone.
	mustSave(t, Library{Skills: []LibSkill{{Name: "gone", Path: "~/nowhere", Agents: []string{"codex"}}}})
	_, cs, _ = LibraryStatus(e)
	if got := cells(cs, SyncSkill, "gone"); got != "codex=cannot" {
		t.Fatalf("missing folder: %s", got)
	}
	// Something else already where the link would go is left alone.
	write(t, filepath.Join(home, ".claude", "skills", "mine", "SKILL.md"), "mine")
	write(t, filepath.Join(home, "skills", "mine", "SKILL.md"), "the library's")
	mustSave(t, Library{Skills: []LibSkill{{Name: "mine", Path: "~/skills/mine", Agents: []string{"claude"}}}})
	_, cs, _ = LibraryStatus(e)
	if got := cells(cs, SyncSkill, "mine"); got != "claude=differs codex=off gemini=off opencode=off devin=off" {
		t.Fatalf("occupied: %s", got)
	}
}

// A config kept in a dotfiles repository is a link, and neither written
// through nor taken from.
func TestLibraryDotfiles(t *testing.T) {
	home, e := libHome(t)
	dot := filepath.Join(home, "dotfiles", "opencode.json")
	write(t, dot, `{"mcp":{}}`)
	os.MkdirAll(filepath.Join(home, ".config", "opencode"), 0o755)
	os.Symlink(dot, filepath.Join(home, ".config", "opencode", "opencode.json"))
	mustSave(t, Library{Servers: []LibServer{{Name: "x", Command: "x", Agents: []string{"opencode"}}}})
	_, cs, _ := LibraryStatus(e)
	if got := cells(cs, SyncMCP, "x"); !strings.Contains(got, "opencode=cannot") {
		t.Fatalf("cells %s", got)
	}
	mustApply(t, e)
	if got := read(t, dot); got != `{"mcp":{}}` {
		t.Fatalf("written through the link: %s", got)
	}
}

func TestLibraryImport(t *testing.T) {
	home, e := libHome(t)
	write(t, filepath.Join(home, ".claude.json"), `{"mcpServers":{
	  "gh":{"command":"npx","args":["gh"],"env":{"GH_TOKEN":"${GH_TOKEN}"}},
	  "paid":{"command":"paid","env":{"KEY":"sk-live"}}
	},"projects":{}}`)
	write(t, filepath.Join(home, ".claude", "skills", "tide", "SKILL.md"), "---\nname: tide\n---\n")
	// Codex already has the same gh, in its own way of saying it.
	write(t, filepath.Join(home, ".codex", "config.toml"), "[mcp_servers.gh]\ncommand = \"npx\"\nargs = [\"gh\"]\nenv_vars = [\"GH_TOKEN\"]\n")
	ir, err := ImportLibrary(e, "claude")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(ir.Servers, ",") != "gh" || strings.Join(ir.Skills, ",") != "tide" || len(ir.Skipped) != 1 || !strings.HasPrefix(ir.Skipped[0], "paid:") {
		t.Fatalf("import %+v", ir)
	}
	lib, _ := LoadLibrary()
	if strings.Join(lib.Servers[0].Agents, ",") != "claude,codex,devin" || lib.Skills[0].Path != "~/.claude/skills/tide" {
		t.Fatalf("library %+v", lib)
	}
	if strings.Contains(read(t, libraryPath()), "sk-live") {
		t.Fatal("a secret went into the library")
	}
	// Importing is not applying: no agent was written.
	if _, err := os.Stat(filepath.Join(home, ".config", "devin")); !os.IsNotExist(err) {
		t.Fatal("import wrote to an agent")
	}
	_, cs, _ := LibraryStatus(e)
	if got := cells(cs, SyncMCP, "gh"); got != "claude=on codex=on gemini=off opencode=off devin=on" {
		t.Fatalf("cells %s", got)
	}
	// Again: nothing new, the same entries.
	if _, err := ImportLibrary(e, "claude"); err != nil {
		t.Fatal(err)
	}
	if lib, _ := LoadLibrary(); len(lib.Servers) != 1 || len(lib.Skills) != 1 {
		t.Fatalf("imported twice: %+v", lib)
	}
	// Another agent's, where the library has a different one by the name.
	write(t, filepath.Join(home, ".gemini", "settings.json"), `{"mcpServers":{"gh":{"command":"other"}}}`)
	if _, err := ImportLibrary(e, "gemini"); err == nil || !strings.Contains(err.Error(), "different server") {
		t.Fatalf("clash: %v", err)
	}
	if _, err := ImportLibrary(e, "opencode"); !errors.Is(err, ErrNothingToSync) {
		t.Fatalf("nothing: %v", err)
	}
	if _, err := ImportLibrary(e, "cursor"); err == nil {
		t.Fatal("unknown agent")
	}
}

func TestRemoveTOMLTable(t *testing.T) {
	in := "# mine\nmodel = \"o3\"\n\n# added by conch\n[mcp_servers.gh]\ncommand = \"npx\"\n\n[mcp_servers.gh.env]\nA = \"\"\n\n[mcp_servers.other]\ncommand = \"o\"\n"
	want := "# mine\nmodel = \"o3\"\n\n[mcp_servers.other]\ncommand = \"o\"\n"
	if got := removeTOMLTable(in, "mcp_servers", "gh"); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if got := removeTOMLTable("[mcp_servers.gh]\ncommand = \"x\"\n", "mcp_servers", "gh"); got != "" {
		t.Fatalf("only table: %q", got)
	}
	if got := removeTOMLTable(in, "mcp_servers", "absent"); got != in {
		t.Fatalf("absent changed it:\n%s", got)
	}
	q := "[mcp_servers.\"my server\"]\ncommand = \"x\"\n[mcp_servers.gh]\ncommand = \"y\"\n"
	if got := removeTOMLTable(q, "mcp_servers", "my server"); got != "[mcp_servers.gh]\ncommand = \"y\"\n" {
		t.Fatalf("quoted: %q", got)
	}
}

// A JSON config with comments in it cannot be rewritten without dropping
// them, so conch's own server there is neither updated nor taken out.
func TestLibraryLeavesJSONCAlone(t *testing.T) {
	home, e := libHome(t)
	gh := LibServer{Name: "gh", Command: "npx", Agents: []string{"opencode"}}
	mustSave(t, Library{Servers: []LibServer{gh}})
	mustApply(t, e)
	path := filepath.Join(home, ".config", "opencode", "opencode.json")
	jsonc := "// mine\n" + read(t, path)
	write(t, path, jsonc)
	gh.Args = []string{"v2"}
	mustSave(t, Library{Servers: []LibServer{gh}})
	res, _ := ApplyLibrary(e, false)
	if c := find(t, res, "opencode", SyncMCP, "gh"); c.Action != ActionSkip || !strings.Contains(c.Detail, "not plain JSON") {
		t.Fatalf("update: %+v", c)
	}
	// Turned off, it would come out but cannot, and the page says so.
	gh.Agents = nil
	mustSave(t, Library{Servers: []LibServer{gh}})
	if _, cs, _ := LibraryStatus(e); !strings.Contains(cells(cs, SyncMCP, "gh"), "opencode=cannot") {
		t.Fatalf("cells %s", cells(cs, SyncMCP, "gh"))
	}
	mustSave(t, Library{})
	res = mustApply(t, e)
	if c := find(t, res, "opencode", SyncMCP, "gh"); c.Action != ActionSkip {
		t.Fatalf("remove: %+v", c)
	}
	if read(t, path) != jsonc {
		t.Fatal("the file with comments was rewritten")
	}
	// Still conch's, so a later apply can take it out once it can.
	if !hasWritten(loadWritten(), "opencode", SyncMCP, "gh") {
		t.Fatal("ownership was dropped by a skip")
	}
}

// Changed in the library, Codex's table is replaced in place, and
// Claude's server is replaced and taken out through its own command.
func TestLibraryReplaceCodexAndClaude(t *testing.T) {
	home, e := libHome(t)
	log := fakeClaude(t, e)
	write(t, filepath.Join(home, ".codex", "config.toml"), "model = \"o3\"\n")
	gh := LibServer{Name: "gh", Command: "npx", Args: []string{"v1"}, Agents: []string{"claude", "codex"}}
	mustSave(t, Library{Servers: []LibServer{gh}})
	mustApply(t, e)
	// Claude's fake does not write its file; say it now has v1.
	write(t, filepath.Join(home, ".claude.json"), `{"mcpServers":{"gh":{"command":"npx","args":["v1"]}},"projects":{"/x":{"keep":true}}}`)

	gh.Args = []string{"v2"}
	mustSave(t, Library{Servers: []LibServer{gh}})
	res := mustApply(t, e)
	if c := find(t, res, "codex", SyncMCP, "gh"); c.Action != ActionUpdate || !c.Done {
		t.Fatalf("codex: %+v", c)
	}
	codex := read(t, filepath.Join(home, ".codex", "config.toml"))
	if strings.Count(codex, "[mcp_servers.gh]") != 1 || !strings.Contains(codex, `"v2"`) || strings.Contains(codex, `"v1"`) ||
		!strings.HasPrefix(codex, "model = \"o3\"\n\n") {
		t.Fatalf("codex:\n%s", codex)
	}
	if c := find(t, res, "claude", SyncMCP, "gh"); c.Action != ActionUpdate || !c.Done {
		t.Fatalf("claude: %+v", c)
	}
	got := read(t, log)
	if !strings.Contains(got, "mcp remove gh --scope user\nmcp add-json gh {\"args\":[\"v2\"]") {
		t.Fatalf("claude was asked:\n%s", got)
	}
	// Undo puts the old one back with Claude's own command, newest first.
	os.WriteFile(log, nil, 0o644)
	if _, err := UndoLibrary(e, ""); err != nil {
		t.Fatal(err)
	}
	if got := read(t, log); !strings.Contains(got, "mcp remove gh --scope user\nmcp add-json gh {\"args\":[\"v1\"]") {
		t.Fatalf("undo asked:\n%s", got)
	}

	// Turned off for Claude, it is taken out with claude mcp remove.
	os.WriteFile(log, nil, 0o644)
	gh.Agents = []string{"codex"}
	mustSave(t, Library{Servers: []LibServer{gh}})
	res = mustApply(t, e)
	if c := find(t, res, "claude", SyncMCP, "gh"); c.Action != ActionRemove || !c.Done || c.Path != "~/.claude.json" {
		t.Fatalf("claude remove: %+v", c)
	}
	if got := read(t, log); !strings.Contains(got, "mcp remove gh --scope user") {
		t.Fatalf("claude was asked:\n%s", got)
	}
	if !strings.Contains(read(t, filepath.Join(home, ".claude.json")), `"keep":true`) {
		t.Fatal("conch wrote Claude's state file")
	}
}
