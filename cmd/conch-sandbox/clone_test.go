package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRepoCheck(t *testing.T) {
	for _, c := range []struct {
		r    repo
		want string // "" is fine
	}{
		{repo{}, ""},
		{repo{URL: "https://github.com/o/r.git"}, ""},
		{repo{URL: "https://github.com/o/r", Branch: "feat/login-2", Ref: "abc1234"}, ""},
		{repo{Branch: "x"}, "need --repo"},
		{repo{Ref: "abcd"}, "need --repo"},
		{repo{URL: "http://github.com/o/r"}, "https://"},
		{repo{URL: "git@github.com:o/r.git"}, "https://"},
		{repo{URL: "https://"}, "https://"},
		{repo{URL: "https://github.com/o/r'; rm -rf ~"}, "https://"},
		{repo{URL: "https://github.com/o/$(id)"}, "https://"},
		{repo{URL: "https://me:pw@github.com/o/r"}, "no credentials"},
		{repo{URL: "https://github.com/o/r", Branch: "-x"}, "not a branch"},
		{repo{URL: "https://github.com/o/r", Branch: "a..b"}, "not a branch"},
		{repo{URL: "https://github.com/o/r", Branch: "a b"}, "not a branch"},
		{repo{URL: "https://github.com/o/r", Branch: "x.lock"}, "not a branch"},
		{repo{URL: "https://github.com/o/r", Branch: "x/"}, "not a branch"},
		{repo{URL: "https://github.com/o/r", Ref: "main"}, "commit's hash"},
		{repo{URL: "https://github.com/o/r", Ref: "abc"}, "commit's hash"},
		{repo{URL: "https://github.com/o/r", Ref: strings.Repeat("a", 65)}, "commit's hash"},
	} {
		err := c.r.check()
		if (err == nil) != (c.want == "") || (err != nil && !strings.Contains(err.Error(), c.want)) {
			t.Errorf("%+v: %v", c.r, err)
		}
	}
}

func TestDirName(t *testing.T) {
	r := repo{URL: "https://github.com/o/My-Repo.git"}
	for name, want := range map[string]string{"Feat Login": "feat-login", "": "my-repo", "../..": "my-repo", "ÄÖ": "my-repo",
		strings.Repeat("x", 80): strings.Repeat("x", 64)} {
		if got := r.dirName(name); got != want {
			t.Errorf("%q: %q", name, got)
		}
	}
	if got := (repo{URL: "https://github.com/"}).dirName(""); got != "project" {
		t.Errorf("no name at all: %q", got)
	}
}

func realHome(t *testing.T, f *sshStandIn, dir string) string {
	t.Helper()
	h, err := filepath.EvalSymlinks(f.home)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(h, dir)
}

func TestCreateClonesTheBranch(t *testing.T) {
	isolate(t)
	fakeProvider(t, "fakebox", tokenKind)
	f := fakeSSH(t)
	code, out, errs := run(t, "", "create", "--provider", "fakebox", "--name", "Feat Login", "--json",
		"--repo", "https://github.com/o/r.git", "--branch", "feat", "--ref", "abc1234")
	if code != 0 {
		t.Fatalf("%d %q", code, errs)
	}
	r := decode[result](t, out)
	want := realHome(t, f, "feat-login")
	if r.ProjectRoot != want {
		t.Fatalf("project_root %q, want %q", r.ProjectRoot, want)
	}
	git := f.gitCalls()
	if !strings.Contains(git, "clone --quiet --branch feat -- https://github.com/o/r.git "+f.home+"/feat-login\n") ||
		!strings.Contains(git, "checkout --quiet -B feat abc1234\n") || strings.Contains(git, "credential") {
		t.Fatalf("git:\n%s", git)
	}
	// ssh read the alias file alone, and the alias.
	if ssh := f.sshCalls(); !strings.Contains(ssh, "-F "+aliasFile()+" ") || !strings.Contains(ssh, "conch-fakebox-box1 sh -s") {
		t.Fatalf("ssh: %s", ssh)
	}
	// The root is kept in the alias, for start and get.
	if own, err := aliasOwner("conch-fakebox-box1"); err != nil || own.Root != want {
		t.Fatalf("alias: %+v %v", own, err)
	}
	code, out, _ = run(t, "", "get", "--machine", "conch-fakebox-box1", "--json")
	if code != 0 || decode[result](t, out).ProjectRoot != want {
		t.Fatalf("get: %s", out)
	}
	if !strings.Contains(errs, "cloning https://github.com/o/r.git") {
		t.Fatalf("progress: %q", errs)
	}
}

func TestCloneVariants(t *testing.T) {
	isolate(t)
	fakeProvider(t, "fakebox", tokenKind)
	f := fakeSSH(t)
	// A commit alone: detached there. The folder is the repository's.
	if code, _, errs := run(t, "", "create", "--provider", "fakebox", "--repo", "https://github.com/o/tool", "--ref", "abcdef12"); code != 0 {
		t.Fatalf("%q", errs)
	}
	if git := f.gitCalls(); !strings.Contains(git, "-- https://github.com/o/tool "+f.home+"/tool\n") || !strings.Contains(git, "checkout --quiet --detach abcdef12") {
		t.Fatalf("ref only:\n%s", git)
	}
	// A branch alone: the clone's own checkout of it, nothing more.
	os.Remove(f.gitLog)
	if code, _, errs := run(t, "", "create", "--provider", "fakebox", "--name", "b", "--repo", "https://github.com/o/tool", "--branch", "dev"); code != 0 {
		t.Fatalf("%q", errs)
	}
	if git := f.gitCalls(); strings.Contains(git, "checkout") || !strings.Contains(git, "--branch dev") {
		t.Fatalf("branch only:\n%s", git)
	}
	// GH_TOKEN in the sandbox reads a private repository, for the clone.
	os.Remove(f.gitLog)
	t.Setenv("GH_TOKEN", "ghp_x")
	if code, _, errs := run(t, "", "create", "--provider", "fakebox", "--name", "c", "--repo", "https://github.com/o/private"); code != 0 {
		t.Fatalf("%q", errs)
	}
	if git := f.gitCalls(); !strings.Contains(git, "-c credential.helper=!f() { echo username=x-access-token;") || strings.Contains(git, "ghp_x") {
		t.Fatalf("token:\n%s", git)
	}
}

// A clone that fails is a create that fails: the sandbox goes, and its alias.
func TestCloneFailureDeletesTheBox(t *testing.T) {
	for _, c := range []struct {
		name  string
		setup func(*testing.T, *sshStandIn)
		want  string
	}{
		{"repository missing", func(t *testing.T, f *sshStandIn) { t.Setenv("FAKE_GIT_FAIL", "1") }, "repository not found"},
		{"folder there", func(t *testing.T, f *sshStandIn) { os.MkdirAll(filepath.Join(f.home, "x"), 0o755) }, "is there already"},
		{"never answers", func(t *testing.T, f *sshStandIn) { t.Setenv("FAKE_SSH_DOWN", "1") }, "never answered"},
	} {
		t.Run(c.name, func(t *testing.T) {
			isolate(t)
			api := fakeProvider(t, "fakebox", tokenKind)
			f := fakeSSH(t)
			c.setup(t, f)
			var out, errb strings.Builder
			a := &app{stdin: strings.NewReader(""), stdout: &out, stderr: &errb, wait: 2 * time.Second, retry: 50 * time.Millisecond}
			code := a.run(t.Context(), []string{"create", "--provider", "fakebox", "--name", "x", "--repo", "https://github.com/o/r"})
			if code != 1 || out.Len() != 0 || !strings.Contains(errb.String(), c.want) {
				t.Fatalf("%d %q", code, errb.String())
			}
			if api.has("box1") || hasAlias("conch-fakebox-box1") {
				t.Fatalf("left behind:\n%s", errb.String())
			}
		})
	}
}

// A sandbox that has just started may not answer ssh yet: tried again.
func TestCloneWaitsForSSH(t *testing.T) {
	isolate(t)
	fakeProvider(t, "fakebox", tokenKind)
	f := fakeSSH(t)
	f.failFirst(3)
	t.Setenv("FAKE_SSH_SAYS", "for tok-secret-box1-1@gw.test")
	if code, _, errs := run(t, "", "create", "--provider", "fakebox", "--repo", "https://github.com/o/r"); code != 0 {
		t.Fatalf("%q", errs)
	}
	if n := strings.Count(f.sshCalls(), "\n"); n != 4 {
		t.Fatalf("%d connections", n)
	}
}

// ssh's own message names the destination, whose user is the token.
func TestCloneKeepsTheTokenOutOfErrors(t *testing.T) {
	isolate(t)
	fakeProvider(t, "fakebox", tokenKind)
	fakeSSH(t)
	// Down every time, at once: a message to strike however slow the
	// machine running the test is.
	t.Setenv("FAKE_SSH_DOWN", "1")
	t.Setenv("FAKE_SSH_SAYS", "for tok-secret-box1-1@gw.test")
	var out, errb strings.Builder
	a := &app{stdin: strings.NewReader(""), stdout: &out, stderr: &errb, wait: 1500 * time.Millisecond, retry: 20 * time.Millisecond}
	if code := a.run(t.Context(), []string{"create", "--provider", "fakebox", "--repo", "https://github.com/o/r"}); code != 1 {
		t.Fatalf("%d", code)
	}
	noToken(t, errb.String())
	if !strings.Contains(errb.String(), "…@gw.test") {
		t.Fatalf("%q", errb.String())
	}
}

func TestCloneRefusedBeforeSpending(t *testing.T) {
	isolate(t)
	api := fakeProvider(t, "fakebox", tokenKind)
	for _, args := range [][]string{
		{"--repo", "http://x/y"},
		{"--branch", "main"},
		{"--repo", "https://github.com/o/r", "--ref", "HEAD"},
	} {
		code, _, errs := run(t, "", append([]string{"create", "--provider", "fakebox"}, args...)...)
		if code != 2 || !strings.Contains(errs, "conch-sandbox create:") {
			t.Errorf("%v: %d %q", args, code, errs)
		}
	}
	if strings.Contains(api.called(), "POST") {
		t.Fatalf("made:\n%s", api.called())
	}
}

// The script itself, run by sh: a sandbox's home with a space in it.
func TestCloneScriptQuotes(t *testing.T) {
	s := repo{URL: "https://github.com/o/r", Branch: "it's", Ref: "abcd"}.cloneScript("a b")
	for _, want := range []string{`dir="$HOME"/'a b'`, `--branch 'it'\''s'`, `-B 'it'\''s' 'abcd'`} {
		if !strings.Contains(s, want) {
			t.Errorf("lacks %s:\n%s", want, s)
		}
	}
}
