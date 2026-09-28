package agentsetup

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// syncRepo is a checkout with Claude Code set up in it: instructions, a
// skill, and three MCP servers — one plain, one over http, and one whose
// environment holds a secret.
func syncRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write(t, filepath.Join(root, ".git", "HEAD"), "ref: refs/heads/master\n")
	write(t, filepath.Join(root, "CLAUDE.md"), "# House rules\n\nRun the tests.\n")
	write(t, filepath.Join(root, ".claude", "skills", "review", "SKILL.md"),
		"---\nname: review\ndescription: review a change\n---\n\nRead the diff.\n")
	write(t, filepath.Join(root, ".mcp.json"), `{
	  "mcpServers": {
	    "gh": {"command": "npx", "args": ["-y", "server-github"], "env": {"GITHUB_TOKEN": "${GITHUB_TOKEN}"}},
	    "docs": {"type": "http", "url": "https://docs.example/mcp"},
	    "paid": {"command": "paid-mcp", "env": {"API_KEY": "sk-live-123"}}
	  }
	}`)
	return root
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// find returns the change for an agent, kind and name.
func find(t *testing.T, res SyncResult, agent, kind, name string) SyncChange {
	t.Helper()
	for _, c := range res.Changes {
		if c.Agent == agent && c.Kind == kind && c.Name == name {
			return c
		}
	}
	t.Fatalf("no %s %s for %s in:\n%s", kind, name, agent, changeLines(res))
	return SyncChange{}
}

func changeLines(res SyncResult) string {
	var out []string
	for _, c := range res.Changes {
		out = append(out, strings.Join([]string{c.Agent, c.Kind, c.Name, c.Action, c.Path, c.Detail}, " | "))
	}
	return strings.Join(out, "\n")
}

// A plan says what it would do and writes nothing.
func TestSyncPlanWritesNothing(t *testing.T) {
	root := syncRepo(t)
	res, err := Sync(root, "claude", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.From != "claude" || len(res.To) != 3 || res.Writes() == 0 {
		t.Fatalf("plan %+v\n%s", res.To, changeLines(res))
	}
	for _, p := range []string{"AGENTS.md", "GEMINI.md", ".agents/skills/review", ".codex/config.toml",
		".gemini/settings.json", "opencode.json", ".conch/agent-sync"} {
		if _, err := os.Lstat(filepath.Join(root, p)); !os.IsNotExist(err) {
			t.Fatalf("a plan wrote %s", p)
		}
	}
	if res.Undo != "" {
		t.Fatalf("a plan left a record to undo: %q", res.Undo)
	}
	// Two things worth saying about a plan: where the instructions come
	// from, and that Codex wants the folder trusted before it reads them.
	notes := strings.Join(res.Notes, " | ")
	if !strings.Contains(notes, "instructions from CLAUDE.md") || !strings.Contains(notes, "trusted the folder") {
		t.Fatalf("notes %q", notes)
	}
	// The instructions travel once, to the file Codex and OpenCode share.
	if c := find(t, res, "codex", SyncInstructions, "AGENTS.md"); c.Action != ActionCreate {
		t.Fatalf("codex instructions: %+v", c)
	}
	if c := find(t, res, "opencode", SyncInstructions, "AGENTS.md"); c.Action != ActionSame ||
		!strings.Contains(c.Detail, "the same file as Codex") {
		t.Fatalf("opencode instructions: %+v", c)
	}
	// So does the skill: three agents read .agents/skills.
	if c := find(t, res, "codex", SyncSkill, "review"); c.Action != ActionLink || c.Path != ".agents/skills/review" {
		t.Fatalf("codex skill: %+v", c)
	}
	for _, agent := range []string{"gemini", "opencode"} {
		if c := find(t, res, agent, SyncSkill, "review"); c.Action != ActionSame {
			t.Fatalf("%s skill: %+v", agent, c)
		}
	}
	// A server whose environment holds a value is never copied.
	for _, agent := range []string{"codex", "gemini", "opencode"} {
		c := find(t, res, agent, SyncMCP, "paid")
		if c.Action != ActionSkip || !strings.Contains(c.Detail, "does not copy secrets") {
			t.Fatalf("%s paid: %+v", agent, c)
		}
	}
}

// Applying writes each agent's own format, and says what it did.
func TestSyncApply(t *testing.T) {
	root := syncRepo(t)
	res, err := Sync(root, "claude", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range res.Changes {
		if c.Writes() && (!c.Done || c.Error != "") {
			t.Fatalf("not done: %+v", c)
		}
	}

	// Codex and OpenCode read AGENTS.md, which does not expand imports, so
	// it holds the text in a block conch can find again.
	agents := read(t, filepath.Join(root, "AGENTS.md"))
	if !strings.HasPrefix(agents, "<!-- conch:instructions from CLAUDE.md -->\n") ||
		!strings.Contains(agents, "Run the tests.") || !strings.HasSuffix(agents, "<!-- conch:end -->\n") {
		t.Fatalf("AGENTS.md:\n%s", agents)
	}
	// Gemini expands "@path", so it is pointed at the file instead.
	gemini := read(t, filepath.Join(root, "GEMINI.md"))
	if !strings.Contains(gemini, "@CLAUDE.md") || strings.Contains(gemini, "Run the tests.") {
		t.Fatalf("GEMINI.md:\n%s", gemini)
	}
	// The skill is linked, not copied, so there is one of it.
	link := filepath.Join(root, ".agents", "skills", "review")
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if !isFile(filepath.Join(link, "SKILL.md")) {
		t.Fatalf("the link does not reach the skill: %s → %s", link, target)
	}
	if isDir(filepath.Join(root, ".gemini", "skills")) {
		t.Fatal("a second copy of the skill folder")
	}

	// Codex: appended TOML tables, with the variable reference kept.
	codex := read(t, filepath.Join(root, ".codex", "config.toml"))
	for _, want := range []string{"[mcp_servers.gh]", `command = "npx"`, `args = ["-y", "server-github"]`,
		"[mcp_servers.gh.env]", `GITHUB_TOKEN = "${GITHUB_TOKEN}"`, "[mcp_servers.docs]", `url = "https://docs.example/mcp"`} {
		if !strings.Contains(codex, want) {
			t.Fatalf("config.toml lacks %q:\n%s", want, codex)
		}
	}
	if strings.Contains(codex, "sk-live-123") {
		t.Fatalf("a secret was copied:\n%s", codex)
	}
	if m := readTOML(filepath.Join(root, ".codex", "config.toml")); m == nil {
		t.Fatalf("the TOML conch wrote cannot be read back:\n%s", codex)
	}

	// Gemini: its settings.json, as JSON.
	var gs map[string]any
	if err := json.Unmarshal([]byte(read(t, filepath.Join(root, ".gemini", "settings.json"))), &gs); err != nil {
		t.Fatal(err)
	}
	servers := obj(gs, "mcpServers")
	if len(servers) != 2 {
		t.Fatalf("gemini servers %v", servers)
	}
	if got := obj(servers, "gh"); str(got, "command") != "npx" || len(strs(got["args"])) != 2 {
		t.Fatalf("gemini gh %v", got)
	}
	if got := obj(servers, "docs"); str(got, "url") != "https://docs.example/mcp" || str(got, "type") != "http" {
		t.Fatalf("gemini docs %v", got)
	}

	// OpenCode: its own shape — one command list, and "environment".
	var oc map[string]any
	if err := json.Unmarshal([]byte(read(t, filepath.Join(root, "opencode.json"))), &oc); err != nil {
		t.Fatal(err)
	}
	gh := obj(obj(oc, "mcp"), "gh")
	if str(gh, "type") != "local" || strings.Join(strs(gh["command"]), " ") != "npx -y server-github" {
		t.Fatalf("opencode gh %v", gh)
	}
	if env := obj(gh, "environment"); str(env, "GITHUB_TOKEN") != "${GITHUB_TOKEN}" {
		t.Fatalf("opencode env %v", gh["environment"])
	}
	if docs := obj(obj(oc, "mcp"), "docs"); str(docs, "type") != "remote" {
		t.Fatalf("opencode docs %v", docs)
	}

	// Syncing again changes nothing: everything is already there.
	again, err := Sync(root, "claude", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if again.Writes() != 0 {
		t.Fatalf("a second sync would write:\n%s", changeLines(again))
	}
	if again.Undo != "" {
		t.Fatalf("a second sync left a record: %q", again.Undo)
	}

	// And undoing puts the checkout back as it was.
	if res.Undo == "" {
		t.Fatal("nothing to undo")
	}
	undone, err := UndoSync(root, res.Undo)
	if err != nil {
		t.Fatal(err)
	}
	if len(undone.Changes) == 0 {
		t.Fatal("undo did nothing")
	}
	for _, p := range []string{"AGENTS.md", "GEMINI.md", ".agents/skills/review", ".codex/config.toml",
		".gemini/settings.json", "opencode.json"} {
		if _, err := os.Lstat(filepath.Join(root, p)); !os.IsNotExist(err) {
			t.Fatalf("%s is still there after undoing", p)
		}
	}
	if read(t, filepath.Join(root, "CLAUDE.md")) == "" {
		t.Fatal("undoing removed the file it copied from")
	}
	if got := SyncUndos(root); len(got) != 0 {
		t.Fatalf("the record is still there: %v", got)
	}
	if _, err := UndoSync(root, ""); err == nil {
		t.Fatal("undoing twice said nothing")
	}
}

// What somebody wrote by hand is left alone; conch's own block is updated.
func TestSyncLeavesHandWrittenFilesAlone(t *testing.T) {
	root := syncRepo(t)
	write(t, filepath.Join(root, "AGENTS.md"), "# Mine\n\nDon't touch this.\n")
	res, err := Sync(root, "claude", []string{"codex"}, true)
	if err != nil {
		t.Fatal(err)
	}
	c := find(t, res, "codex", SyncInstructions, "AGENTS.md")
	if c.Action != ActionSkip || !strings.Contains(c.Detail, "leaves it alone") {
		t.Fatalf("hand-written: %+v", c)
	}
	if got := read(t, filepath.Join(root, "AGENTS.md")); !strings.Contains(got, "Don't touch this.") {
		t.Fatalf("AGENTS.md was written over:\n%s", got)
	}

	// A file that already says the same thing needs no block.
	write(t, filepath.Join(root, "AGENTS.md"), read(t, filepath.Join(root, "CLAUDE.md")))
	res, _ = Sync(root, "claude", []string{"codex"}, false)
	if c := find(t, res, "codex", SyncInstructions, "AGENTS.md"); c.Action != ActionSame {
		t.Fatalf("already the same: %+v", c)
	}

	// conch's block in a file with other text of its own: only the block
	// changes, and it is updated when the source does.
	write(t, filepath.Join(root, "AGENTS.md"), "# Mine\n\nKeep me.\n\n"+
		"<!-- conch:instructions from CLAUDE.md -->\nold text\n<!-- conch:end -->\n\nAnd me.\n")
	res, err = Sync(root, "claude", []string{"codex"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if c := find(t, res, "codex", SyncInstructions, "AGENTS.md"); c.Action != ActionUpdate || !c.Done {
		t.Fatalf("update: %+v", c)
	}
	got := read(t, filepath.Join(root, "AGENTS.md"))
	for _, want := range []string{"Keep me.", "And me.", "Run the tests."} {
		if !strings.Contains(got, want) {
			t.Fatalf("after updating, %q is gone:\n%s", want, got)
		}
	}
	if strings.Contains(got, "old text") {
		t.Fatalf("the old block is still there:\n%s", got)
	}
	// Undoing puts the hand-written file back, block and all.
	if _, err := UndoSync(root, ""); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(root, "AGENTS.md")); !strings.Contains(got, "old text") {
		t.Fatalf("undo:\n%s", got)
	}
}

// A file conch cannot rewrite without losing something is left alone.
func TestSyncSkipsJSONItCannotKeep(t *testing.T) {
	root := syncRepo(t)
	write(t, filepath.Join(root, "opencode.json"), "{\n  // mine\n  \"theme\": \"dark\"\n}\n")
	res, err := Sync(root, "claude", []string{"opencode"}, true)
	if err != nil {
		t.Fatal(err)
	}
	c := find(t, res, "opencode", SyncMCP, "gh")
	if c.Action != ActionSkip || !strings.Contains(c.Detail, "not plain JSON") {
		t.Fatalf("JSONC: %+v", c)
	}
	if got := read(t, filepath.Join(root, "opencode.json")); !strings.Contains(got, "// mine") {
		t.Fatalf("the comments went:\n%s", got)
	}
	// A server already declared there is left as it is, whoever wrote it.
	write(t, filepath.Join(root, "opencode.json"), `{"mcp":{"gh":{"type":"local","command":["mine"]}}}`)
	res, _ = Sync(root, "claude", []string{"opencode"}, false)
	if c := find(t, res, "opencode", SyncMCP, "gh"); c.Action != ActionSame {
		t.Fatalf("already declared: %+v", c)
	}
	if c := find(t, res, "opencode", SyncMCP, "docs"); c.Action != ActionUpdate {
		t.Fatalf("the other one: %+v", c)
	}
}

// The other way round: from an AGENTS.md project to Claude, which expands
// imports, so it is pointed at the file rather than given a copy.
func TestSyncFromCodex(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".git", "HEAD"), "ref: refs/heads/master\n")
	write(t, filepath.Join(root, "AGENTS.md"), "# Rules\n")
	write(t, filepath.Join(root, ".agents", "skills", "plan", "SKILL.md"), "---\nname: plan\n---\n")
	write(t, filepath.Join(root, ".codex", "config.toml"),
		"# mine\nmodel = \"o3\"\n\n[mcp_servers.gh]\ncommand = \"npx\"\nargs = [\"gh\"]\n")
	res, err := Sync(root, "codex", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(root, "CLAUDE.md")); !strings.Contains(got, "@AGENTS.md") {
		t.Fatalf("CLAUDE.md:\n%s", got)
	}
	if !isFile(filepath.Join(root, ".claude", "skills", "plan", "SKILL.md")) {
		t.Fatalf("the skill did not reach Claude: %s", changeLines(res))
	}
	// Gemini and OpenCode already read .agents/skills, so nothing is done
	// for them, and Gemini keeps reading AGENTS.md's own name.
	for _, agent := range []string{"gemini", "opencode"} {
		if c := find(t, res, agent, SyncSkill, "plan"); c.Action != ActionSame {
			t.Fatalf("%s: %+v", agent, c)
		}
	}
	for _, c := range res.Changes {
		if c.Agent == "opencode" && c.Kind == SyncInstructions {
			t.Fatalf("OpenCode reads AGENTS.md already: %+v", c)
		}
	}
	var mcp map[string]any
	if err := json.Unmarshal([]byte(read(t, filepath.Join(root, ".mcp.json"))), &mcp); err != nil {
		t.Fatal(err)
	}
	if got := obj(obj(mcp, "mcpServers"), "gh"); str(got, "command") != "npx" {
		t.Fatalf(".mcp.json %v", mcp)
	}
	// The TOML it read from is untouched, comments and all.
	if got := read(t, filepath.Join(root, ".codex", "config.toml")); !strings.Contains(got, "# mine") {
		t.Fatalf("the source file changed:\n%s", got)
	}
}

func TestSyncRefusals(t *testing.T) {
	root := syncRepo(t)
	if _, err := Sync(root, "devin", nil, false); err == nil || !strings.Contains(err.Error(), "cannot read devin's setup") {
		t.Fatalf("unknown source: %v", err)
	}
	if _, err := Sync(root, "claude", []string{"devin"}, false); err == nil || !strings.Contains(err.Error(), "cannot set devin up") {
		t.Fatalf("unknown target: %v", err)
	}
	// A checkout with nothing in it says so rather than writing empty files.
	empty := t.TempDir()
	_, err := Sync(empty, "claude", nil, false)
	if !errors.Is(err, ErrNothingToSync) {
		t.Fatalf("empty checkout: %v", err)
	}
	// A folder that is not a repository is taken as the root itself.
	write(t, filepath.Join(empty, "CLAUDE.md"), "# Rules\n")
	res, err := Sync(empty, "claude", []string{"codex"}, false)
	if err != nil || res.Dir != empty {
		t.Fatalf("outside a repository: %v %q", err, res.Dir)
	}
	// An instruction file of nothing but white space is nothing to copy.
	write(t, filepath.Join(empty, "CLAUDE.md"), "   \n\n")
	if _, err := Sync(empty, "claude", []string{"codex"}, false); !errors.Is(err, ErrNothingToSync) {
		t.Fatalf("an empty file: %v", err)
	}
	// Syncing to the agent it came from does nothing at all.
	res, err = Sync(root, "claude", []string{"claude"}, false)
	if err != nil || len(res.Changes) != 0 {
		t.Fatalf("to itself: %v\n%s", err, changeLines(res))
	}
	// A record that cannot be read says which one.
	if _, err := UndoSync(root, "20260101-000000"); err == nil || !strings.Contains(err.Error(), "no record of sync") {
		t.Fatalf("a record that isn't there: %v", err)
	}
	write(t, filepath.Join(root, undoDir, "20260101-000001.json"), "not json")
	if _, err := UndoSync(root, "20260101-000001"); err == nil || !strings.Contains(err.Error(), "cannot be read") {
		t.Fatalf("a record that is nonsense: %v", err)
	}
	if got := SyncNames(); strings.Join(got, ",") != "claude,codex,gemini,opencode" {
		t.Fatalf("SyncNames %v", got)
	}
	if !CanSync("claude") || CanSync("devin") {
		t.Fatal("CanSync")
	}
}

// Undoing takes the newest record, and a file written twice goes back to
// what it was before either write.
func TestSyncUndoOrder(t *testing.T) {
	root := syncRepo(t)
	first, err := Sync(root, "claude", []string{"codex"}, true)
	if err != nil || first.Undo == "" {
		t.Fatalf("first: %v %+v", err, first)
	}
	// A second server, so .codex/config.toml is written again.
	write(t, filepath.Join(root, ".mcp.json"), `{"mcpServers":{"more":{"command":"more-mcp"}}}`)
	second, err := Sync(root, "claude", []string{"codex"}, true)
	if err != nil || second.Undo == "" || second.Undo == first.Undo {
		t.Fatalf("second: %v %+v", err, second)
	}
	if got := read(t, filepath.Join(root, ".codex", "config.toml")); !strings.Contains(got, "more-mcp") ||
		!strings.Contains(got, "[mcp_servers.gh]") {
		t.Fatalf("both servers should be there:\n%s", got)
	}
	if got := SyncUndos(root); len(got) != 2 || got[0] != second.Undo {
		t.Fatalf("records %v", got)
	}
	// Undoing without a name takes the newest: the first sync's files stay.
	if _, err := UndoSync(root, ""); err != nil {
		t.Fatal(err)
	}
	got := read(t, filepath.Join(root, ".codex", "config.toml"))
	if strings.Contains(got, "more-mcp") || !strings.Contains(got, "[mcp_servers.gh]") {
		t.Fatalf("after undoing the second sync:\n%s", got)
	}
	if _, err := UndoSync(root, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".codex", "config.toml")); !os.IsNotExist(err) {
		t.Fatal("the file conch created is still there")
	}
}

// From OpenCode, whose servers name their command as a list and their
// environment by another name, to the agents that write it differently.
func TestSyncFromOpenCode(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".git", "HEAD"), "ref: refs/heads/master\n")
	write(t, filepath.Join(root, "AGENTS.md"), "# Rules\n")
	write(t, filepath.Join(root, "opencode.json"), `{"mcp":{
	  "my server": {"type":"local","command":["uvx","mcp-server","--port","0"],"environment":{"TOKEN":"$TOKEN"}},
	  "remote": {"type":"remote","url":"https://mcp.example","headers":{"Authorization":"${AUTH}"}},
	  "leaky": {"type":"remote","url":"https://mcp.example","headers":{"Authorization":"Bearer abc"}},
	  "nothing": {"type":"local"}
	}}`)
	res, err := Sync(root, "opencode", []string{"claude", "codex"}, true)
	if err != nil {
		t.Fatal(err)
	}
	// A server that says neither how to run it nor where it is, is not one.
	for _, c := range res.Changes {
		if c.Name == "nothing" {
			t.Fatalf("a server with nothing to go on: %+v", c)
		}
	}
	claude := read(t, filepath.Join(root, ".mcp.json"))
	var doc map[string]any
	if err := json.Unmarshal([]byte(claude), &doc); err != nil {
		t.Fatal(err)
	}
	mine := obj(obj(doc, "mcpServers"), "my server")
	if str(mine, "command") != "uvx" || strings.Join(strs(mine["args"]), " ") != "mcp-server --port 0" {
		t.Fatalf("the command list became %v", mine)
	}
	if env := obj(mine, "env"); str(env, "TOKEN") != "$TOKEN" {
		t.Fatalf("environment %v", mine["env"])
	}
	if strings.Contains(claude, "Bearer abc") {
		t.Fatalf("a header with a token in it was copied:\n%s", claude)
	}
	if c := find(t, res, "claude", SyncMCP, "leaky"); c.Action != ActionSkip {
		t.Fatalf("leaky: %+v", c)
	}
	// A name TOML cannot write bare is quoted, and reads back.
	codex := read(t, filepath.Join(root, ".codex", "config.toml"))
	if !strings.Contains(codex, `[mcp_servers."my server"]`) {
		t.Fatalf("config.toml:\n%s", codex)
	}
	m := readTOML(filepath.Join(root, ".codex", "config.toml"))
	if got := obj(obj(m, "mcp_servers"), "my server"); str(got, "command") != "uvx" {
		t.Fatalf("the TOML does not read back: %v", m)
	}
	if got := obj(obj(m, "mcp_servers"), "remote"); str(got, "url") != "https://mcp.example" {
		t.Fatalf("the remote server: %v", got)
	}
}

// Undoing says what each thing was, and takes away the folders the sync
// made — a checkout should look as it did, not keep empty .codex folders.
func TestSyncUndoTidiesUp(t *testing.T) {
	root := syncRepo(t)
	res, err := Sync(root, "claude", []string{"codex", "gemini"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !isDir(filepath.Join(root, ".codex")) || !isDir(filepath.Join(root, ".agents", "skills")) {
		t.Fatal("the sync made no folders")
	}
	undone, err := UndoSync(root, res.Undo)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{}
	for _, c := range undone.Changes {
		kinds[c.Name] = c.Kind
	}
	if kinds["AGENTS.md"] != SyncInstructions || kinds["review"] != SyncSkill || kinds["config.toml"] != SyncMCP {
		t.Fatalf("undoing called them %v", kinds)
	}
	for _, dir := range []string{".codex", ".agents/skills", ".agents", ".gemini"} {
		if _, err := os.Lstat(filepath.Join(root, dir)); !os.IsNotExist(err) {
			t.Fatalf("%s was left behind", dir)
		}
	}
	// Including the record's own folder, once it holds nothing.
	if _, err := os.Lstat(filepath.Join(root, ".conch")); !os.IsNotExist(err) {
		t.Fatal(".conch was left behind")
	}
	// What was there before a sync is not tidied away with it.
	if !isDir(filepath.Join(root, ".claude", "skills", "review")) || !isFile(filepath.Join(root, "CLAUDE.md")) {
		t.Fatal("undoing took the source with it")
	}

	// A folder with something else in it stays, and so does its file.
	res, err = Sync(root, "claude", []string{"codex"}, true)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, ".codex", "notes.md"), "mine\n")
	if _, err := UndoSync(root, res.Undo); err != nil {
		t.Fatal(err)
	}
	if !isFile(filepath.Join(root, ".codex", "notes.md")) {
		t.Fatal("a folder with somebody else's file in it was removed")
	}

	// A record from a build that did not say what things were still reads.
	old := `{"stamp":"20260101-000000","from":"claude","files":[{"path":"AGENTS.md","absent":true}]}`
	write(t, filepath.Join(root, undoDir, "20260101-000000.json"), old)
	write(t, filepath.Join(root, "AGENTS.md"), "from an older sync\n")
	back, err := UndoSync(root, "20260101-000000")
	if err != nil || len(back.Changes) != 1 || back.Changes[0].Kind != "file" {
		t.Fatalf("an older record: %+v %v", back.Changes, err)
	}
	if _, err := os.Stat(filepath.Join(root, "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatal("the older record did not remove its file")
	}
}

// An agent that wants the folder trusted is said so once, before anybody
// wonders why nothing happened.
func TestSyncSaysWhoWantsTrust(t *testing.T) {
	root := syncRepo(t)
	res, err := Sync(root, "claude", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	notes := strings.Join(res.Notes, " | ")
	if !strings.Contains(notes, "Codex reads a project's config.toml only once") ||
		!strings.Contains(notes, "Gemini leaves a project's MCP servers and skills out") {
		t.Fatalf("notes %q", notes)
	}
	if n := strings.Count(notes, "Gemini"); n != 1 {
		t.Fatalf("Gemini said %d times: %q", n, notes)
	}
	// Nothing written for an agent, nothing said about it.
	res, err = Sync(root, "claude", []string{"opencode"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if notes := strings.Join(res.Notes, " | "); strings.Contains(notes, "Gemini") || strings.Contains(notes, "Codex") {
		t.Fatalf("notes about agents nobody asked for: %q", notes)
	}
}

// userHome is a home with Claude Code set up in it: instructions, a skill,
// and servers in the file Claude keeps its own state in.
func userHome(t *testing.T) (string, Env) {
	t.Helper()
	home := t.TempDir()
	conchHome := filepath.Join(home, ".config", "conch")
	t.Setenv("HOME", home)
	t.Setenv("CONCH_HOME", conchHome)
	for _, v := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "XDG_CONFIG_HOME"} {
		t.Setenv(v, "")
	}
	write(t, filepath.Join(home, ".claude", "CLAUDE.md"), "# My rules\n\nBritish English, always.\n")
	write(t, filepath.Join(home, ".claude", "skills", "tide", "SKILL.md"), "---\nname: tide\ndescription: the tide\n---\n")
	write(t, filepath.Join(home, ".claude.json"), `{"mcpServers":{"gh":{"command":"npx","args":["gh"],"env":{"TOKEN":"${GH_TOKEN}"}}},"projects":{"/src":{"history":["keep me"]}}}`)
	return home, Env{Home: home, Getenv: os.Getenv}
}

// The setup that follows you, rather than a checkout's: your home.
func TestSyncUser(t *testing.T) {
	home, e := userHome(t)

	res, err := SyncUser(e, "claude", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	// It says where things go with the ~ back on, so nothing about the
	// plan is guesswork.
	plan := changeLines(res)
	for _, want := range []string{"~/.codex/AGENTS.md", "~/.gemini/GEMINI.md", "~/.agents/skills/tide",
		"~/.codex/config.toml", "~/.gemini/settings.json", "~/.config/opencode/opencode.json"} {
		if !strings.Contains(plan, want) {
			t.Fatalf("the plan lacks %q:\n%s", want, plan)
		}
	}
	if strings.Contains(plan, home) {
		t.Fatalf("a whole path leaked into the plan:\n%s", plan)
	}
	// Nothing is written by a plan.
	if _, err := os.Stat(filepath.Join(home, ".codex", "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatal("the plan wrote something")
	}

	done, err := SyncUser(e, "claude", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	// Codex and OpenCode share ~/.agents/skills with Gemini, and the skill
	// is linked into it once.
	if !isFile(filepath.Join(home, ".agents", "skills", "tide", "SKILL.md")) {
		t.Fatalf("the skill was not linked:\n%s", changeLines(done))
	}
	// The instructions travel, as a copy or as a pointer.
	if got := read(t, filepath.Join(home, ".codex", "AGENTS.md")); !strings.Contains(got, "British English") {
		t.Fatalf("codex's instructions:\n%s", got)
	}
	if got := read(t, filepath.Join(home, ".gemini", "GEMINI.md")); !strings.Contains(got, "@") {
		t.Fatalf("gemini's instructions:\n%s", got)
	}
	// The server Claude keeps in its own state file is written where each
	// agent keeps its own.
	if got := read(t, filepath.Join(home, ".codex", "config.toml")); !strings.Contains(got, "[mcp_servers.gh]") {
		t.Fatalf("codex's servers:\n%s", got)
	}
	if got := read(t, filepath.Join(home, ".config", "opencode", "opencode.json")); !strings.Contains(got, `"gh"`) {
		t.Fatalf("opencode's servers:\n%s", got)
	}
	// And Claude's own state file is not rewritten for it.
	if got := read(t, filepath.Join(home, ".claude.json")); !strings.Contains(got, `"keep me"`) {
		t.Fatalf("Claude's state file changed:\n%s", got)
	}

	// The record lives in conch's own folder, not in your home's root.
	if _, err := os.Stat(filepath.Join(home, ".conch")); !os.IsNotExist(err) {
		t.Fatal("it put a .conch in the home directory")
	}
	if got := UserSyncUndos(); len(got) != 1 || got[0] != done.Undo {
		t.Fatalf("records %v, want %q", got, done.Undo)
	}
	// Undoing puts the home back as it was.
	if _, err := UndoUserSync(""); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{".codex/AGENTS.md", ".gemini/GEMINI.md", ".agents/skills/tide",
		".codex/config.toml", ".gemini/settings.json", ".config/opencode/opencode.json"} {
		if _, err := os.Lstat(filepath.Join(home, p)); !os.IsNotExist(err) {
			t.Fatalf("%s is still there", p)
		}
	}
	if !isFile(filepath.Join(home, ".claude", "CLAUDE.md")) {
		t.Fatal("undoing took the source with it")
	}
	if _, err := UndoUserSync(""); err == nil {
		t.Fatal("undoing twice said nothing")
	}
}

// A file kept in a dotfiles repository is a link into it, and writing
// through the link edits that repository. conch leaves it alone and says
// so — the one thing the user scope must never do quietly.
func TestSyncUserLeavesDotfileLinksAlone(t *testing.T) {
	home, e := userHome(t)
	dotfiles := filepath.Join(home, "dotfiles")
	write(t, filepath.Join(dotfiles, "codex-config.toml"), "# kept in git\nmodel = \"o3\"\n")
	write(t, filepath.Join(dotfiles, "AGENTS.md"), "# my own, in git\n")
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dotfiles, "codex-config.toml"), filepath.Join(home, ".codex", "config.toml")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dotfiles, "AGENTS.md"), filepath.Join(home, ".codex", "AGENTS.md")); err != nil {
		t.Fatal(err)
	}

	res, err := SyncUser(e, "claude", []string{"codex"}, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range res.Changes {
		if c.Agent != "codex" {
			continue
		}
		if c.Kind == SyncMCP || c.Kind == SyncInstructions {
			if c.Action != ActionSkip || !strings.Contains(c.Detail, "a dotfiles repository?") {
				t.Fatalf("%s %s: %+v", c.Kind, c.Name, c)
			}
		}
	}
	// The repository is untouched, links and all.
	if got := read(t, filepath.Join(dotfiles, "codex-config.toml")); strings.Contains(got, "mcp_servers") {
		t.Fatalf("it wrote through the link:\n%s", got)
	}
	if got := read(t, filepath.Join(dotfiles, "AGENTS.md")); strings.Contains(got, "British English") {
		t.Fatalf("it wrote through the instructions link:\n%s", got)
	}
}

// What it refuses before it starts.
func TestSyncUserRefusals(t *testing.T) {
	_, e := userHome(t)
	if _, err := SyncUser(Env{}, "claude", nil, false); err == nil || !strings.Contains(err.Error(), "where your home is") {
		t.Fatalf("no home: %v", err)
	}
	if _, err := SyncUser(e, "devin", nil, false); err == nil || !strings.Contains(err.Error(), "cannot read devin's setup") {
		t.Fatalf("unknown source: %v", err)
	}
	if _, err := SyncUser(e, "claude", []string{"devin"}, false); err == nil || !strings.Contains(err.Error(), "cannot set devin up") {
		t.Fatalf("unknown target: %v", err)
	}
	// A home with nothing in it says so rather than writing empty files.
	empty := t.TempDir()
	if _, err := SyncUser(Env{Home: empty, Getenv: os.Getenv}, "claude", nil, false); !errors.Is(err, ErrNothingToSync) {
		t.Fatalf("an empty home: %v", err)
	}
	// Claude is never given servers here: they live in the file it keeps
	// its own state in.
	res, err := SyncUser(e, "codex", []string{"claude"}, false)
	if err != nil && !errors.Is(err, ErrNothingToSync) {
		t.Fatal(err)
	}
	for _, c := range res.Changes {
		if c.Kind == SyncMCP && c.Agent == "claude" && c.Action != ActionSkip {
			t.Fatalf("it would write Claude's servers: %+v", c)
		}
	}
}
