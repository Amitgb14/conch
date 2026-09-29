package agentsetup

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
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
	for _, want := range []string{"don't commit just to hand it off", "check the result", "in the foreground, not in the background"} {
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
