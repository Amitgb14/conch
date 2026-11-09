package agentsetup

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseSkillSource(t *testing.T) {
	for _, c := range []struct {
		in   string
		want SkillSource
		ok   bool
	}{
		{"anthropics/skills", SkillSource{Owner: "anthropics", Repo: "skills"}, true},
		{"anthropics/skills/skills/pdf", SkillSource{Owner: "anthropics", Repo: "skills", Path: "skills/pdf"}, true},
		{"anthropics/skills/skills/pdf@v1.2", SkillSource{Owner: "anthropics", Repo: "skills", Path: "skills/pdf", Ref: "v1.2"}, true},
		{"anthropics/skills@feature/x", SkillSource{Owner: "anthropics", Repo: "skills", Ref: "feature/x"}, true},
		{"https://github.com/anthropics/skills", SkillSource{Owner: "anthropics", Repo: "skills"}, true},
		{"https://github.com/anthropics/skills.git", SkillSource{Owner: "anthropics", Repo: "skills"}, true},
		{"https://github.com/anthropics/skills/tree/main/skills/pdf", SkillSource{Owner: "anthropics", Repo: "skills", Ref: "main", Path: "skills/pdf"}, true},
		{"https://github.com/anthropics/skills/blob/main/skills/pdf/SKILL.md", SkillSource{Owner: "anthropics", Repo: "skills", Ref: "main", Path: "skills/pdf"}, true},
		{"github.com/o/r/", SkillSource{Owner: "o", Repo: "r"}, true},
		// A path on the machine, or not GitHub at all.
		{"~/skills/review", SkillSource{}, false},
		{"/opt/skills/x", SkillSource{}, false},
		{"./x/y", SkillSource{}, false},
		{"https://gitlab.com/o/r", SkillSource{}, false},
		{"ssh://github.com/o/r", SkillSource{}, false},
		{"justone", SkillSource{}, false},
		{"", SkillSource{}, false},
		{"o/r/../../etc", SkillSource{}, false},
		{"o/r@", SkillSource{}, false},
		{"o/r@a..b", SkillSource{}, false},
		{"o/r@x y", SkillSource{}, false},
		{"o r/x", SkillSource{}, false},
	} {
		got, ok := ParseSkillSource(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("%q: %+v %v, want %+v %v", c.in, got, ok, c.want, c.ok)
		}
	}
	if s := (SkillSource{Owner: "o", Repo: "r", Path: "a/b", Ref: "v1"}).String(); s != "o/r/a/b@v1" {
		t.Errorf("String: %q", s)
	}
}

// fakeGitHub is a folder of repositories laid out as github.com is, and
// CONCH_GITHUB_URL pointing at it.
func fakeGitHub(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	root := t.TempDir()
	t.Setenv("CONCH_GITHUB_URL", "file://"+root)
	t.Setenv("CONCH_HOME", t.TempDir())
	return root
}

// repo makes owner/repo.git with files (path → contents; a path ending in
// "@" is a link to the contents) on main, and tag v1 at the same commit.
func repo(t *testing.T, root, owner, name string, files map[string]string) {
	t.Helper()
	work := t.TempDir()
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git(work, "init", "-q", "-b", "main")
	for p, body := range files {
		full := filepath.Join(work, filepath.FromSlash(strings.TrimSuffix(p, "@")))
		os.MkdirAll(filepath.Dir(full), 0o755)
		if strings.HasSuffix(p, "@") {
			if err := os.Symlink(body, full); err != nil {
				t.Fatal(err)
			}
			continue
		}
		mode := os.FileMode(0o644)
		if strings.HasSuffix(p, ".sh") {
			mode = 0o755
		}
		if err := os.WriteFile(full, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	git(work, "add", "-A")
	git(work, "commit", "-q", "-m", "skills")
	git(work, "tag", "v1")
	bare := filepath.Join(root, owner, name+".git")
	os.MkdirAll(filepath.Dir(bare), 0o755)
	git(work, "clone", "-q", "--bare", work, bare)
}

func TestFetchSkill(t *testing.T) {
	root := fakeGitHub(t)
	repo(t, root, "acme", "one", map[string]string{"SKILL.md": "---\nname: one\n---\nDo one thing.", "run.sh": "#!/bin/sh\necho hi", "README.md": "x"})
	repo(t, root, "acme", "many", map[string]string{
		"skills/pdf/SKILL.md": "pdf", "skills/pdf/ref/forms.md": "forms", "skills/xlsx/SKILL.md": "xlsx", "README.md": "readme",
		"skills/pdf/escape@": "/etc/passwd",
	})
	repo(t, root, "acme", "nested", map[string]string{"tools/review/SKILL.md": "review"})
	repo(t, root, "acme", "none", map[string]string{"README.md": "nothing here"})
	ctx := context.Background()

	// A repository that is a skill.
	f, err := FetchSkill(ctx, "acme/one", "")
	if err != nil {
		t.Fatal(err)
	}
	if f.Name != "one" || f.Source != "acme/one" || f.Path != filepath.Join(SkillsDir(), "one") {
		t.Fatalf("fetched %+v", f)
	}
	if b, _ := os.ReadFile(filepath.Join(f.Path, "SKILL.md")); !strings.Contains(string(b), "Do one thing") {
		t.Fatalf("SKILL.md: %q", b)
	}
	if fi, err := os.Stat(filepath.Join(f.Path, "run.sh")); err != nil || fi.Mode()&0o111 == 0 {
		t.Errorf("a skill's script lost being runnable: %v %v", fi, err)
	}
	if _, err := os.Stat(filepath.Join(f.Path, ".git")); err == nil {
		t.Error("the repository's .git came along")
	}

	// Several skills, none named: say which.
	_, err = FetchSkill(ctx, "acme/many", "")
	var fe *FetchError
	if !errors.As(err, &fe) || strings.Join(fe.Found, ",") != "acme/many/skills/pdf,acme/many/skills/xlsx" {
		t.Fatalf("several: %v", err)
	}
	// One of them, by its folder, under a name of its own, at a tag.
	f, err = FetchSkill(ctx, "acme/many/skills/pdf@v1", "pdf-forms")
	if err != nil {
		t.Fatal(err)
	}
	if f.Name != "pdf-forms" || f.Source != "acme/many/skills/pdf@v1" {
		t.Fatalf("named: %+v", f)
	}
	if b, _ := os.ReadFile(filepath.Join(f.Path, "ref", "forms.md")); string(b) != "forms" {
		t.Errorf("a skill's own files: %q", b)
	}
	if _, err := os.Lstat(filepath.Join(f.Path, "escape")); err == nil {
		t.Error("a link in the skill was copied: it could point anywhere")
	}
	if _, err := os.Stat(filepath.Join(f.Path, "..", "pdf-forms", "README.md")); err == nil {
		t.Error("the rest of the repository came along")
	}
	// The single skill a repository holds, wherever it is.
	if f, err = FetchSkill(ctx, "https://github.com/acme/nested", ""); err != nil || f.Name != "review" || f.Source != "acme/nested/tools/review" {
		t.Fatalf("the only one: %+v %v", f, err)
	}

	// Fetching again replaces it, whole.
	os.WriteFile(filepath.Join(SkillsDir(), "one", "stale.txt"), []byte("old"), 0o644)
	if _, err := FetchSkill(ctx, "acme/one", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(SkillsDir(), "one", "stale.txt")); err == nil {
		t.Error("fetching again kept a file the skill no longer has")
	}

	// Refused, and nothing left behind.
	for _, c := range []struct{ src, name, want string }{
		{"acme/none", "", "has no SKILL.md"},
		{"acme/many/skills/nope", "", "has no SKILL.md"},
		{"acme/missing", "", "fetching acme/missing"},
		{"acme/one@nosuchref", "", "fetching acme/one@nosuchref"},
		{"~/skills/x", "", "isn't a GitHub skill"},
		{"acme/one", "bad name", "skill name"},
		{"acme/one", "../escape", "skill name"},
	} {
		if _, err := FetchSkill(ctx, c.src, c.name); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s %q: %v, want %q", c.src, c.name, err, c.want)
		}
	}
	entries, _ := os.ReadDir(SkillsDir())
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".fetching") || strings.HasSuffix(e.Name(), ".old") {
			t.Errorf("left behind: %s", e.Name())
		}
	}
}

// A skill bigger than a skill should be is refused, not half copied.
func TestFetchSkillTooBig(t *testing.T) {
	root := fakeGitHub(t)
	files := map[string]string{"SKILL.md": "big"}
	for i := 0; i <= fetchMaxFiles; i++ {
		files["f/"+strings.Repeat("x", 3)+string(rune('a'+i%26))+"/"+itoa(i)] = "."
	}
	repo(t, root, "acme", "big", files)
	if _, err := FetchSkill(context.Background(), "acme/big", ""); err == nil || !strings.Contains(err.Error(), "too many files") {
		t.Fatalf("too big: %v", err)
	}
	if _, err := os.Stat(filepath.Join(SkillsDir(), "big")); err == nil {
		t.Error("a refused skill was kept")
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	s := ""
	for ; i > 0; i /= 10 {
		s = string(rune('0'+i%10)) + s
	}
	return s
}
