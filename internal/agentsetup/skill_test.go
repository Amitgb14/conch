package agentsetup

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/config"
)

func skillEnv(t *testing.T, vars map[string]string) Env {
	t.Helper()
	home := t.TempDir()
	return Env{Home: home, GOOS: runtime.GOOS, Getenv: func(k string) string { return vars[k] }}
}

func summary(changes []SkillChange) string {
	var b strings.Builder
	for _, c := range changes {
		b.WriteString(c.Action + " " + c.Path + " " + strings.Join(c.Agents, ",") + "\n")
	}
	return b.String()
}

// Every agent reads it with the same frontmatter: a name that is also its
// folder's (OpenCode insists), a description saying when to use it, and
// the mark conch knows its own copy by.
func TestSkillText(t *testing.T) {
	text := string(Skill)
	if !strings.HasPrefix(text, "---\nname: conch\ndescription: ") {
		t.Fatalf("frontmatter: %.80q", text)
	}
	end := strings.Index(text[4:], "\n---\n")
	if end < 0 {
		t.Fatal("frontmatter never ends")
	}
	front := text[:end+4]
	if !strings.Contains(front, "\n  installed-by: conch") || !bytes.Contains(Skill, skillMark) {
		t.Fatal("no mark")
	}
	for _, line := range strings.Split(front, "\n") {
		if strings.HasPrefix(line, "description: ") && len(line) > 1024+len("description: ") {
			t.Fatal("description over 1024 characters: agents cut it")
		}
	}
	if skillName != "conch" {
		t.Fatal("the folder must be the skill's name")
	}
	// What e2e run R36 taught, in the skill's own words: an agent doesn't
	// commit the person's work to hand it off, and a turn's end is checked,
	// not trusted — backgrounded commands and failed requests end one too.
	for _, want := range []string{"don't commit just to hand it off", "check the result", "in the foreground, not in the background",
		"ask the person to add the pattern", "absolute path to read"} {
		if !strings.Contains(text, want) {
			t.Errorf("the skill no longer says %q", want)
		}
	}
}

func TestSkillTargets(t *testing.T) {
	e := skillEnv(t, nil)
	got, err := SkillTargets(e, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Claude and Devin have folders of their own; Codex, Gemini and
	// OpenCode share one.
	want := " " + filepath.Join(e.Home, ".claude", "skills", "conch", "SKILL.md") + " claude\n" +
		" " + filepath.Join(e.Home, ".agents", "skills", "conch", "SKILL.md") + " codex,gemini,opencode\n" +
		" " + filepath.Join(e.Home, ".config", "devin", "skills", "conch", "SKILL.md") + " devin\n"
	if summary(got) != want {
		t.Fatalf("targets:\n%s\nwant\n%s", summary(got), want)
	}

	// Claude's folder follows CLAUDE_CONFIG_DIR.
	e = skillEnv(t, map[string]string{"CLAUDE_CONFIG_DIR": "/elsewhere/claude"})
	got, _ = SkillTargets(e, []string{"claude"})
	if len(got) != 1 || got[0].Path != "/elsewhere/claude/skills/conch/SKILL.md" {
		t.Fatalf("CLAUDE_CONFIG_DIR: %s", summary(got))
	}
	// One agent that shares a folder gets that folder alone.
	got, _ = SkillTargets(e, []string{"opencode"})
	if len(got) != 1 || got[0].Path != filepath.Join(e.Home, ".agents", "skills", "conch", "SKILL.md") || strings.Join(got[0].Agents, ",") != "opencode" {
		t.Fatalf("opencode: %s", summary(got))
	}
	if _, err := SkillTargets(e, []string{"claude", "aider"}); err == nil || !strings.Contains(err.Error(), "doesn't know where aider keeps skills") {
		t.Fatalf("unknown agent: %v", err)
	}
}

func TestInstallSkill(t *testing.T) {
	e := skillEnv(t, nil)
	claude := filepath.Join(e.Home, ".claude", "skills", "conch", "SKILL.md")
	shared := filepath.Join(e.Home, ".agents", "skills", "conch", "SKILL.md")
	actions := func(changes []SkillChange) string {
		var out []string
		for _, c := range changes {
			out = append(out, c.Action)
			if c.Error != "" {
				t.Errorf("%s: %s", c.Path, c.Error)
			}
		}
		return strings.Join(out, ",")
	}

	// Asked what it would do, it writes nothing.
	got, err := InstallSkill(e, nil, false, false)
	if err != nil || actions(got) != "create,create,create" {
		t.Fatalf("plan: %s %v", summary(got), err)
	}
	if _, err := os.Stat(filepath.Join(e.Home, ".claude")); err == nil {
		t.Fatal("a plan wrote something")
	}

	got, _ = InstallSkill(e, nil, false, true)
	if actions(got) != "create,create,create" {
		t.Fatalf("install: %s", summary(got))
	}
	for _, p := range []string{claude, shared} {
		if b, err := os.ReadFile(p); err != nil || !bytes.Equal(b, Skill) {
			t.Fatalf("%s: %v", p, err)
		}
		if _, err := os.Stat(p + ".conch-tmp"); err == nil {
			t.Fatalf("%s: the temporary copy was left", p)
		}
	}
	if got, _ = InstallSkill(e, nil, false, true); actions(got) != "same,same,same" {
		t.Fatalf("again: %s", summary(got))
	}

	// An older conch's copy is brought up to date.
	os.WriteFile(shared, []byte("---\nname: conch\nmetadata:\n  installed-by: conch\n---\nold\n"), 0o644)
	got, _ = InstallSkill(e, nil, false, true)
	if actions(got) != "same,update,same" || got[1].Detail != "from an older conch" {
		t.Fatalf("update: %s", summary(got))
	}
	if b, _ := os.ReadFile(shared); !bytes.Equal(b, Skill) {
		t.Fatal("not updated")
	}

	// Removed, the folder goes too; asked again, there is nothing to remove.
	if got, _ = InstallSkill(e, []string{"claude"}, true, true); actions(got) != "remove" {
		t.Fatalf("remove: %s", summary(got))
	}
	if _, err := os.Stat(filepath.Dir(claude)); err == nil {
		t.Fatal("the skill's folder was left")
	}
	if _, err := os.Stat(filepath.Dir(filepath.Dir(claude))); err != nil {
		t.Fatal("the person's skills folder went with it")
	}
	if got, _ = InstallSkill(e, []string{"claude"}, true, true); actions(got) != "same" || got[0].Detail != "not installed" {
		t.Fatalf("remove again: %s", summary(got))
	}
	// A folder holding more than conch's file keeps the rest.
	extra := filepath.Join(filepath.Dir(shared), "notes.md")
	os.WriteFile(extra, []byte("mine"), 0o644)
	InstallSkill(e, []string{"codex"}, true, true)
	if _, err := os.Stat(extra); err != nil {
		t.Fatal("removed the person's file")
	}
	if _, err := os.Stat(shared); err == nil {
		t.Fatal("conch's skill still there")
	}
}

// A conch skill the person wrote is theirs: never replaced or removed.
func TestInstallSkillLeavesTheirs(t *testing.T) {
	e := skillEnv(t, nil)
	theirs := filepath.Join(e.Home, ".claude", "skills", "conch", "SKILL.md")
	os.MkdirAll(filepath.Dir(theirs), 0o755)
	mine := []byte("---\nname: conch\ndescription: my own notes on conch\n---\n")
	os.WriteFile(theirs, mine, 0o644)
	for _, remove := range []bool{false, true} {
		got, err := InstallSkill(e, []string{"claude"}, remove, true)
		if err != nil || len(got) != 1 || got[0].Action != ActionSkip || !strings.Contains(got[0].Detail, "isn't conch's own") {
			t.Fatalf("remove=%v: %s %v", remove, summary(got), err)
		}
		if b, _ := os.ReadFile(theirs); !bytes.Equal(b, mine) {
			t.Fatalf("remove=%v: changed their skill", remove)
		}
	}
}

func TestInstallSkillFailures(t *testing.T) {
	if _, err := InstallSkill(Env{}, nil, false, true); err == nil || !strings.Contains(err.Error(), "no home directory") {
		t.Fatalf("no home: %v", err)
	}
	// Something there that can't be read as a file is reported, not written over.
	e := skillEnv(t, nil)
	odd := filepath.Join(e.Home, ".claude", "skills", "conch", "SKILL.md")
	os.MkdirAll(odd, 0o755)
	got, _ := InstallSkill(e, []string{"claude"}, false, true)
	if len(got) != 1 || got[0].Action != ActionSkip || got[0].Error == "" {
		t.Fatalf("a folder where the file goes: %s", summary(got))
	}
	// A skills folder that can't be written says so for that file only.
	if os.Getuid() == 0 {
		t.Skip("root writes anywhere")
	}
	e = skillEnv(t, nil)
	locked := filepath.Join(e.Home, ".agents")
	os.MkdirAll(locked, 0o555)
	t.Cleanup(func() { os.Chmod(locked, 0o755) })
	got, _ = InstallSkill(e, nil, false, true)
	if len(got) != 3 || got[0].Error != "" || got[1].Error == "" || got[2].Error != "" {
		t.Fatalf("read-only folder: %+v", got)
	}
	if b, err := os.ReadFile(got[0].Path); err != nil || !bytes.Equal(b, Skill) {
		t.Fatal("the writable one was not written")
	}
}

// A skill file or folder linked in from elsewhere — a dotfiles repository
// — is the person's arrangement: conch neither writes through the link
// nor replaces it with a file, and doesn't remove it.
func TestInstallSkillLeavesLinks(t *testing.T) {
	for _, what := range []string{"file", "folder"} {
		t.Run(what, func(t *testing.T) {
			e := skillEnv(t, nil)
			dotfiles := t.TempDir()
			ours := append([]byte(nil), Skill...)
			ours = bytes.Replace(ours, []byte("# Working"), []byte("# Old working"), 1) // conch's, but older
			conchDir := filepath.Join(e.Home, ".claude", "skills", "conch")
			var link, target string
			if what == "file" {
				os.MkdirAll(conchDir, 0o755)
				target, link = filepath.Join(dotfiles, "SKILL.md"), filepath.Join(conchDir, "SKILL.md")
				os.WriteFile(target, ours, 0o644)
			} else {
				os.MkdirAll(filepath.Dir(conchDir), 0o755)
				target, link = dotfiles, conchDir
				os.WriteFile(filepath.Join(dotfiles, "SKILL.md"), ours, 0o644)
			}
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
			for _, remove := range []bool{false, true} {
				got, err := InstallSkill(e, []string{"claude"}, remove, true)
				if err != nil || len(got) != 1 || got[0].Action != ActionSkip || !strings.Contains(got[0].Detail, "link") {
					t.Fatalf("remove=%v: %s %v", remove, summary(got), err)
				}
				if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
					t.Fatalf("remove=%v: the link is gone or replaced", remove)
				}
				file := target
				if what == "folder" {
					file = filepath.Join(dotfiles, "SKILL.md")
				}
				if b, _ := os.ReadFile(file); !bytes.Equal(b, ours) {
					t.Fatalf("remove=%v: wrote through the link", remove)
				}
			}
		})
	}
}

// The mark counts only in the frontmatter: a person's own skill may well
// say "installed-by: conch" in its text.
func TestSkillMarkOnlyInFrontmatter(t *testing.T) {
	e := skillEnv(t, nil)
	theirs := filepath.Join(e.Home, ".agents", "skills", "conch", "SKILL.md")
	os.MkdirAll(filepath.Dir(theirs), 0o755)
	mine := []byte("---\nname: conch\ndescription: notes\n---\nconch's own skill has installed-by: conch in its metadata.\n")
	os.WriteFile(theirs, mine, 0o644)
	got, _ := InstallSkill(e, []string{"codex"}, true, true)
	if got[0].Action != ActionSkip {
		t.Fatalf("took the person's skill for conch's: %s", summary(got))
	}
	if b, _ := os.ReadFile(theirs); !bytes.Equal(b, mine) {
		t.Fatal("removed it")
	}
	for text, want := range map[string]bool{
		string(Skill): true,
		"---\nname: conch\nmetadata:\n  installed-by: conch\n---\n": true,
		"---\nname: conch\n---\ninstalled-by: conch\n":              false,
		"installed-by: conch\n":                                     false,
		"":                                                          false,
		"---\nname: conch\nmetadata:\n  installed-by: conch\n":       false, // never closed
		"---\nname: conch\nmetadata:\n  installed-by: conchx\n---\n": false,
	} {
		if got := isOurSkill([]byte(text)); got != want {
			t.Errorf("%q: %v", text, got)
		}
	}
}

// An empty SKILL.md is someone's, however little it says.
func TestInstallSkillEmptyFileIsTheirs(t *testing.T) {
	e := skillEnv(t, nil)
	p := filepath.Join(e.Home, ".claude", "skills", "conch", "SKILL.md")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, nil, 0o644)
	if got, _ := InstallSkill(e, []string{"claude"}, false, true); got[0].Action != ActionSkip {
		t.Fatalf("empty: %s", summary(got))
	}
}

// An agent named twice is one reader.
func TestSkillTargetsOnceEach(t *testing.T) {
	e := skillEnv(t, nil)
	got, err := SkillTargets(e, []string{"codex", "codex", "gemini", "claude", "claude"})
	if err != nil || len(got) != 2 || strings.Join(got[0].Agents, ",") != "codex,gemini" || strings.Join(got[1].Agents, ",") != "claude" {
		t.Fatalf("%s %v", summary(got), err)
	}
}

// A copy left half-written by a crash doesn't keep conch's folder behind
// on removal, nor get in the way of the next install.
func TestInstallSkillLeftoverCopy(t *testing.T) {
	e := skillEnv(t, nil)
	InstallSkill(e, []string{"claude"}, false, true)
	p := filepath.Join(e.Home, ".claude", "skills", "conch", "SKILL.md")
	os.WriteFile(p+".conch-tmp", []byte("half"), 0o644)
	if got, _ := InstallSkill(e, []string{"claude"}, true, true); got[0].Action != ActionRemove || got[0].Error != "" {
		t.Fatalf("remove: %s", summary(got))
	}
	if _, err := os.Stat(filepath.Dir(p)); err == nil {
		t.Fatal("the folder stayed for a leftover copy")
	}
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p+".conch-tmp", []byte("half"), 0o444)
	if got, _ := InstallSkill(e, []string{"claude"}, false, true); got[0].Action != ActionCreate || got[0].Error != "" {
		t.Fatalf("install over a leftover: %s", summary(got))
	}
	if _, err := os.Stat(p + ".conch-tmp"); err == nil {
		t.Fatal("leftover still there")
	}
}

// Each of conch's skills is read by agents the way conch's own is: a name
// that is its folder's, a description under the cut, and conch's mark —
// without which conch could never update or remove it.
func TestBundledSkills(t *testing.T) {
	want := []string{"conch", "conch-autopilot", "conch-factory", "conch-review", "conch-ship", "conch-team"}
	var names []string
	for _, sk := range Skills {
		names = append(names, sk.Name)
		text := string(sk.Body)
		if !strings.HasPrefix(text, "---\nname: "+sk.Name+"\ndescription: ") {
			t.Errorf("%s: frontmatter %.60q", sk.Name, text)
		}
		end := strings.Index(text[4:], "\n---\n")
		if end < 0 {
			t.Errorf("%s: frontmatter never ends", sk.Name)
			continue
		}
		front := text[:end+4]
		if !isOurSkill(sk.Body) {
			t.Errorf("%s: no mark", sk.Name)
		}
		for _, line := range strings.Split(front, "\n") {
			if strings.HasPrefix(line, "description: ") && len(line) > 1024+len("description: ") {
				t.Errorf("%s: description over 1024 characters", sk.Name)
			}
		}
		// Every one is for an agent in a conch pane, and says how to tell.
		if !strings.Contains(text, "CONCH_PANE_ID") {
			t.Errorf("%s doesn't say how to tell it is in conch", sk.Name)
		}
		if sk.Name != skillName && !strings.Contains(text, "never `conch send`") {
			t.Errorf("%s doesn't warn off conch send", sk.Name)
		}
	}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("skills %v, want %v (conch's own first)", names, want)
	}
	if !bytes.Equal(Skill, Skills[0].Body) {
		t.Fatal("Skill is not conch's own")
	}
	// The autopilot's: it proposes before it changes, and leaves merging off.
	auto, _ := BundledSkillNamed("conch-autopilot")
	// The person runs what conch refuses from an agent's pane — the policy
	// and a worktree's setup are theirs — and the dry-run mode is spelled
	// as config.toml takes it. Both were wrong the first time the skill set
	// up the autopilot for real, on conch itself.
	for _, w := range []string{"Propose, then apply only", "Never turn it on unless they", "auto_merge = false",
		"Two steps are theirs, not yours", "`conch autopilot push`", "`conch project setup", "Don't run `conch autopilot push` or `conch project setup` yourself",
		`permission_mode = "` + config.PermissionsDryRun + `"`} {
		if !strings.Contains(string(auto.Body), w) {
			t.Errorf("conch-autopilot no longer says %q", w)
		}
	}
	if strings.Contains(string(auto.Body), `"dry run"`) {
		t.Errorf(`conch-autopilot spells the mode "dry run"; config.toml takes %q`, config.PermissionsDryRun)
	}
	if sk, ok := BundledSkillNamed("conch-factory"); !ok || sk.Name != "conch-factory" {
		t.Fatal("conch-factory not found by name")
	}
	if _, ok := BundledSkillNamed("nope"); ok {
		t.Fatal("found a skill conch doesn't have")
	}
	// What the person asked of the factory: the whole line, and a merge
	// only when they asked for one.
	factory, _ := BundledSkillNamed("conch-factory")
	for _, w := range []string{"gh issue view", "conch task -issue", "conch review send", "TEST-REPORT.md", "conch branch pr", "only if the\nperson asked for a merge", "auto_pr = true",
		// Agreed before it is built, built test-first, reviewed against
		// what was agreed, and a retro to improve the line.
		"Acceptance criteria", "SPEC.md", "test-first, a slice at a time", "fidelity to /abs/path/SPEC.md", "## 9. Retro", "Propose the changes; don't make them"} {
		if !strings.Contains(string(factory.Body), w) {
			t.Errorf("conch-factory no longer says %q", w)
		}
	}
}

func TestInstallSkills(t *testing.T) {
	e := skillEnv(t, nil)
	got, err := InstallSkills(e, nil, false, false)
	if err != nil {
		t.Fatal(err)
	}
	// A change per skill per folder, a folder's together and in
	// SkillTargets' order of folders.
	if len(got) != len(Skills)*3 {
		t.Fatalf("%d changes:\n%s", len(got), summary(got))
	}
	folders := []string{filepath.Join(e.Home, ".claude", "skills"), filepath.Join(e.Home, ".agents", "skills"), filepath.Join(e.Home, ".config", "devin", "skills")}
	for i, c := range got {
		f, sk := folders[i/len(Skills)], Skills[i%len(Skills)]
		if c.Path != filepath.Join(f, sk.Name, "SKILL.md") || c.Skill != sk.Name || c.Action != ActionCreate {
			t.Fatalf("change %d: %+v", i, c)
		}
	}
	if _, err := os.Stat(folders[0]); err == nil {
		t.Fatal("a plan wrote something")
	}

	// Installed, each folder holds each skill's own text.
	if _, err := InstallSkills(e, []string{"claude"}, false, true); err != nil {
		t.Fatal(err)
	}
	for _, sk := range Skills {
		if b, err := os.ReadFile(filepath.Join(folders[0], sk.Name, "SKILL.md")); err != nil || !bytes.Equal(b, sk.Body) {
			t.Fatalf("%s: %v", sk.Name, err)
		}
	}

	// A person's own skill under one of the names is left alone, and the
	// rest still install.
	theirs := filepath.Join(folders[1], "conch-ship", "SKILL.md")
	os.MkdirAll(filepath.Dir(theirs), 0o755)
	os.WriteFile(theirs, []byte("---\nname: conch-ship\n---\nmine\n"), 0o644)
	got, _ = InstallSkills(e, []string{"codex"}, false, true)
	for _, c := range got {
		want := ActionCreate
		if c.Skill == "conch-ship" {
			want = ActionSkip
			if !strings.Contains(c.Detail, "conch-ship skill there isn't conch's own") {
				t.Errorf("detail %q", c.Detail)
			}
		}
		if c.Action != want || c.Error != "" {
			t.Errorf("%s: %s %s", c.Skill, c.Action, c.Error)
		}
	}
	if b, _ := os.ReadFile(theirs); string(b) != "---\nname: conch-ship\n---\nmine\n" {
		t.Fatal("replaced the person's skill")
	}

	// An install from before there were more: conch's own is there, the
	// others are what is left to write.
	e2 := skillEnv(t, nil)
	InstallSkill(e2, []string{"claude"}, false, true)
	got, _ = InstallSkills(e2, []string{"claude"}, false, false)
	for _, c := range got {
		want := ActionCreate
		if c.Skill == "conch" {
			want = ActionSame
		}
		if c.Action != want {
			t.Errorf("after an older install, %s: %s", c.Skill, c.Action)
		}
	}

	// Removed, every one of conch's goes, and the person's stays.
	got, _ = InstallSkills(e, nil, true, true)
	for _, c := range got {
		if c.Skill == "conch-ship" && c.Path == theirs {
			if c.Action != ActionSkip {
				t.Errorf("the person's conch-ship: %s", c.Action)
			}
		} else if c.Action != ActionRemove && c.Action != ActionSame {
			t.Errorf("remove %s: %s", c.Path, c.Action)
		}
	}
	for _, sk := range Skills {
		if _, err := os.Stat(filepath.Join(folders[0], sk.Name)); err == nil {
			t.Errorf("%s left in claude's folder", sk.Name)
		}
	}
	if _, err := os.Stat(theirs); err != nil {
		t.Fatal("removed the person's skill")
	}
	if _, err := InstallSkills(Env{}, nil, false, false); err == nil {
		t.Fatal("no home: no error")
	}
	if _, err := InstallSkills(e, []string{"aider"}, false, false); err == nil {
		t.Fatal("an unknown agent: no error")
	}
}

// The repository is also a Claude Code plugin marketplace: its plugin is
// this folder, whose skills/ are the ones conch installs, so the two ways
// in never disagree. A renamed folder or a broken manifest would leave
// `/plugin install conch@conch` installing nothing.
func TestPluginManifests(t *testing.T) {
	var market struct {
		Name    string                `json:"name"`
		Owner   struct{ Name string } `json:"owner"`
		Plugins []struct {
			Name   string `json:"name"`
			Source string `json:"source"`
		} `json:"plugins"`
	}
	b, err := os.ReadFile(filepath.Join("..", "..", ".claude-plugin", "marketplace.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &market); err != nil {
		t.Fatalf("marketplace.json: %v", err)
	}
	if market.Name != "conch" || market.Owner.Name == "" || len(market.Plugins) != 1 {
		t.Fatalf("marketplace: %+v", market)
	}
	p := market.Plugins[0]
	if p.Name != "conch" || p.Source != "./internal/agentsetup" {
		t.Fatalf("plugin entry: %+v", p)
	}
	var plugin struct {
		Name string `json:"name"`
	}
	b, err = os.ReadFile(filepath.Join(".claude-plugin", "plugin.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &plugin); err != nil || plugin.Name != p.Name {
		t.Fatalf("plugin.json: %v %+v (its name must be the entry's)", err, plugin)
	}
	// Every skill conch installs is one the plugin carries, and no other.
	dirs, _ := os.ReadDir("skills")
	var names []string
	for _, d := range dirs {
		names = append(names, d.Name())
	}
	var want []string
	for _, sk := range Skills {
		want = append(want, sk.Name)
	}
	sort.Strings(want)
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("plugin skills %v, conch's %v", names, want)
	}
}
