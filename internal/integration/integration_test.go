package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// isolate gives the test a CONCH_HOME of its own, so no installed file of
// the developer's is read.
func isolate(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("CONCH_HOME", home)
	t.Setenv("HOME", home)
	return home
}

const good = `
kind = "ci"
name = "acme-ci"
label = "Acme CI"
api = "https://api.acme.dev"
key_env = "ACME_TOKEN"
follow = ["logs.acme.dev"]

[detect]
files = [".acme.yml"]
remotes = ["acme.dev"]

[project]
id = "{owner}/{repo}"

[failed_log]
workflow = "{run.name}"
url = "{run.web_url}"

[[failed_log.steps]]
as = "run"
call = "GET /runs?branch={branch}&commit={head}"
pick = "runs[status=failed]"

[[failed_log.steps]]
as = "jobs"
call = "GET /runs/{run.id}/jobs"
pick = "jobs[*status=failed].log_url"

[[failed_log.steps]]
each = "jobs"
fetch = "{item}"
text = true
`

func TestParseGood(t *testing.T) {
	f, problems, err := Parse([]byte(good))
	if err != nil || len(problems) > 0 {
		t.Fatalf("err %v, problems %v", err, problems)
	}
	if f.DisplayName() != "Acme CI" || len(f.FailedLog.Steps) != 3 || f.Shipped() {
		t.Fatalf("read %+v", f)
	}
}

func TestParseProblems(t *testing.T) {
	for name, tc := range map[string]struct {
		from, to, want string
	}{
		"unknown key":      {`label = "Acme CI"`, `label = "Acme CI"` + "\nlogo = \"x\"", "unknown key logo"},
		"newer format":     {`kind = "ci"`, `kind = "ci"` + "\nformat = 9", "format 9"},
		"kind":             {`kind = "ci"`, `kind = "host"`, `kind "host"`},
		"name":             {`name = "acme-ci"`, `name = "../x"`, `name "../x"`},
		"http api":         {`https://api.acme.dev`, `http://api.acme.dev`, "api"},
		"key env":          {`"ACME_TOKEN"`, `"acme token"`, "key_env"},
		"auth without key": {`key_env = "ACME_TOKEN"`, `key_env = "ACME_TOKEN"` + "\nauth_value = \"Bearer x\"", "never sends {key}"},
		"follow url":       {`["logs.acme.dev"]`, `["https://logs.acme.dev/x"]`, "follow"},
		"no detect":        {"files = [\".acme.yml\"]\nremotes = [\"acme.dev\"]", "", "[detect] files or remotes"},
		"detect outside":   {`[".acme.yml"]`, `["../../etc/passwd"]`, "a path inside the checkout"},
		"no project":       {`id = "{owner}/{repo}"`, `id = ""`, "[project] id"},
		"bad method":       {`call = "GET /runs/{run.id}/jobs"`, `call = "DELETE /runs/{run.id}/jobs"`, "GET, POST or PUT"},
		"no slash":         {`call = "GET /runs/{run.id}/jobs"`, `call = "GET runs"`, "starts with /"},
		"call and fetch":   {`fetch = "{item}"`, `fetch = "{item}"` + "\ncall = \"GET /x\"", "call or fetch, one of them"},
		"unknown each":     {`each = "jobs"`, `each = "builds"`, `each "builds"`},
		"later name":       {`call = "GET /runs/{run.id}/jobs"`, `call = "GET /runs/{jobs.id}/jobs"`, "{jobs.id} is nothing known"},
		"item outside":     {`call = "GET /runs/{run.id}/jobs"`, `call = "GET /runs/{item.id}/jobs"`, "{item.id} is nothing known"},
		"pick unknown":     {`pick = "runs[status=failed]"`, `pick = "runs[sha={sha}]"`, "{sha} is nothing known"},
		"last not a log":   {`text = true`, `pick = "x"`, "the last step is the log"},
		"text with pick":   {`text = true`, "text = true\npick = \"x\"", "no pick or lines"},
		"as reserved":      {`as = "jobs"`, `as = "branch"`, `as "branch"`},
		"workflow unknown": {`workflow = "{run.name}"`, `workflow = "{build.name}"`, "{build.name} is no step's"},
		"fetch no follow":  {`follow = ["logs.acme.dev"]`, ``, "no follow hosts"},
	} {
		src := strings.Replace(good, tc.from, tc.to, 1)
		if src == good {
			t.Fatalf("%s: the replacement changed nothing", name)
		}
		_, problems, err := Parse([]byte(src))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if !strings.Contains(strings.Join(problems, "\n"), tc.want) {
			t.Errorf("%s: problems %q, want %q", name, problems, tc.want)
		}
	}
}

func TestParseMalformed(t *testing.T) {
	for name, src := range map[string]string{
		"not toml":  "kind = ",
		"truncated": good[:len(good)/2] + "[[",
		"huge":      strings.Repeat("#", 257<<10),
	} {
		if _, _, err := Parse([]byte(src)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	_, problems, err := Parse(nil)
	if err != nil || len(problems) == 0 {
		t.Fatalf("an empty file: %v %v", err, problems)
	}
}

func TestShippedFilesAreClean(t *testing.T) {
	isolate(t)
	all, bad := All()
	if len(bad) > 0 {
		t.Fatalf("bad: %v", bad)
	}
	names := map[string]bool{}
	for _, f := range all {
		names[f.Name] = f.Shipped()
	}
	for _, want := range []string{"circleci", "gitlab-ci"} {
		if !names[want] {
			t.Errorf("%s not shipped: %v", want, names)
		}
	}
}

func TestInstalledReplacesShipped(t *testing.T) {
	home := isolate(t)
	dir := filepath.Join(home, "integrations")
	os.MkdirAll(dir, 0o755)
	mine := strings.Replace(good, `name = "acme-ci"`, `name = "circleci"`, 1)
	os.WriteFile(filepath.Join(dir, "circleci.toml"), []byte(mine), 0o644)
	os.WriteFile(filepath.Join(dir, "acme-ci.toml"), []byte(good), 0o644)
	os.WriteFile(filepath.Join(dir, "wrong.toml"), []byte(good), 0o644) // names itself acme-ci
	os.WriteFile(filepath.Join(dir, "broken.toml"), []byte("kind = "), 0o644)
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("not one"), 0o644)
	all, bad := All()
	got := map[string]*File{}
	for _, f := range all {
		got[f.Name] = f
	}
	if f := got["circleci"]; f == nil || f.Shipped() || f.Label != "Acme CI" {
		t.Fatalf("circleci is %+v, want the installed file", f)
	}
	if got["acme-ci"] == nil || got["gitlab-ci"] == nil {
		t.Fatalf("got %v", got)
	}
	if len(bad) != 2 || !strings.Contains(strings.Join(bad, "\n"), `names itself "acme-ci"`) {
		t.Fatalf("bad %q", bad)
	}
}

func TestParseRemote(t *testing.T) {
	for in, want := range map[string]Remote{
		"https://github.com/Amitgb14/conch.git":        {"github.com", "Amitgb14/conch"},
		"https://github.com/Amitgb14/conch":            {"github.com", "Amitgb14/conch"},
		"git@github.com:Amitgb14/conch.git":            {"github.com", "Amitgb14/conch"},
		"ssh://git@gitlab.com:2222/group/sub/repo.git": {"gitlab.com", "group/sub/repo"},
		"https://GitLab.com/group/sub/repo/":           {"gitlab.com", "group/sub/repo"},
		"https://user:tok@gitlab.example.com/a/b.git":  {"gitlab.example.com", "a/b"},
		"gitlab.com:a/b":                               {"gitlab.com", "a/b"},
	} {
		got, ok := ParseRemote(in)
		if !ok || got != want {
			t.Errorf("%s: %+v %v, want %+v", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "/srv/git/repo.git", "https://github.com/onlyowner", "../x/y", "https://h/a/../b"} {
		if r, ok := ParseRemote(in); ok {
			t.Errorf("%q read as %+v", in, r)
		}
	}
}

func TestUsesAndFor(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	if got := For(KindCI, root, Remote{Host: "github.com", Path: "a/b"}); len(got) != 0 {
		t.Fatalf("a bare project uses %v", got)
	}
	os.MkdirAll(filepath.Join(root, ".circleci"), 0o755)
	os.WriteFile(filepath.Join(root, ".circleci", "config.yml"), nil, 0o644)
	got := For(KindCI, root, Remote{Host: "github.com", Path: "a/b"})
	if len(got) != 1 || got[0].Name != "circleci" {
		t.Fatalf("with .circleci: %v", got)
	}
	// A remote alone, with no checkout to look in.
	got = For(KindCI, "", Remote{Host: "gitlab.com", Path: "a/b"})
	if len(got) != 1 || got[0].Name != "gitlab-ci" {
		t.Fatalf("on gitlab.com: %v", got)
	}
	// A host that only ends in the same letters is someone else's.
	if got := For(KindCI, "", Remote{Host: "notgitlab.com", Path: "a/b"}); len(got) != 0 {
		t.Fatalf("notgitlab.com: %v", got)
	}
	if got := For("tickets", root, Remote{}); len(got) != 0 {
		t.Fatalf("another kind: %v", got)
	}
}

func TestCheckName(t *testing.T) {
	for _, ok := range []string{"circleci", "gitlab-ci", "ab"} {
		if CheckName(ok) != nil {
			t.Errorf("%s refused", ok)
		}
	}
	for _, bad := range []string{"", "a", "../x", "CircleCI", "1ci", strings.Repeat("a", 32)} {
		if CheckName(bad) == nil {
			t.Errorf("%q taken", bad)
		}
	}
}
