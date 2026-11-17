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
format = 3
kind = "ci"
name = "acme-ci"
label = "Acme CI"
api = "https://api.acme.dev"
key_env = "ACME_TOKEN"
follow_hosts = ["logs.acme.dev"]

[detect]
files = [".acme.yml"]
remotes = ["acme.dev"]
checks = ["acme/*"]

[project]
id = "{owner}/{repo}"

[states]
ok = "pass"
broken = "fail"
busy = "pending"

[runs]
call = "GET /runs?branch={branch}&commit={head}"
items = "runs"
id = "id"
name = "{item.workflow} #{item.number}"
state = "status"
url = "web_url"

[failed_log]
job = "{jobs.0.name}"

[[failed_log.steps]]
as = "jobs"
call = "GET /runs/{run}/jobs"
pick = "jobs[*status=broken]"

[[failed_log.steps]]
each = "jobs"
call = "GET /jobs/{item.id}"
follow = "log_url"
text = "[*].line"

[rerun]
call = "POST /runs/{run}/retry"
body = { failed_only = true, branch = "{branch}" }
`

func TestParseGood(t *testing.T) {
	f, problems, err := Parse([]byte(good))
	if err != nil || len(problems) > 0 {
		t.Fatalf("err %v, problems %v", err, problems)
	}
	if f.DisplayName() != "Acme CI" || len(f.FailedLog.Steps) != 2 || f.Shipped() {
		t.Fatalf("read %+v", f)
	}
	if got := f.Operations(); strings.Join(got, " ") != "runs failed_log rerun" {
		t.Fatalf("operations %v", got)
	}
	if got := f.Hosts(); strings.Join(got, " ") != "api.acme.dev logs.acme.dev" {
		t.Fatalf("hosts %v", got)
	}
}

func TestParseProblems(t *testing.T) {
	for name, tc := range map[string]struct {
		from, to, want string
	}{
		"unknown key":        {`label = "Acme CI"`, `label = "Acme CI"` + "\nlogo = \"x\"", "unknown key logo"},
		"unknown op":         {`[rerun]`, "[cancel]\ncall = \"POST /x\"\n[rerun]", "unknown key cancel"},
		"newer format":       {`format = 3`, `format = 9`, "format 9"},
		"old format":         {`format = 3`, `format = 2`, "format = 3: an integration file says so"},
		"no format":          {`format = 3`, ``, "format = 3: an integration file says so"},
		"kind":               {`kind = "ci"`, `kind = "build"`, `kind "build"`},
		"kind not yet":       {`kind = "ci"`, `kind = "host"`, `kind "host": this conch does not read it yet`},
		"op of another kind": {`[rerun]`, "[list]\ncall = \"GET /x\"\n[rerun]", "[list]: not an operation of kind ci"},
		"kind sandbox":       {`kind = "ci"`, `kind = "sandbox"`, `read by conch sandbox`},
		"name":               {`name = "acme-ci"`, `name = "../x"`, `name "../x"`},
		"http api":           {`https://api.acme.dev`, `http://api.acme.dev`, "api"},
		"base_url":           {`key_env = "ACME_TOKEN"`, `key_env = "ACME_TOKEN"` + "\nbase_url = \"https://x\"", `base_url "https://x"`},
		"key env":            {`"ACME_TOKEN"`, `"acme token"`, "key_env"},
		"auth without key":   {`key_env = "ACME_TOKEN"`, `key_env = "ACME_TOKEN"` + "\nauth_value = \"Bearer x\"", "never sends {key}"},
		"auth twice":         {`key_env = "ACME_TOKEN"`, `key_env = "ACME_TOKEN"` + "\nauth_header = \"X\"\n[auth]\ntype = \"bearer\"", "say it once"},
		"auth type":          {`[detect]`, "[auth]\ntype = \"oauth\"\n[detect]", `type "oauth"`},
		"basic no user":      {`[detect]`, "[auth]\ntype = \"basic\"\n[detect]", "user_env"},
		"scopes not bearer":  {`[detect]`, "[auth]\nscopes = [\"read\"]\n[detect]", "scopes are for bearer"},
		"bearer header":      {`[detect]`, "[auth]\ntype = \"bearer\"\nheader = \"X\"\n[detect]", "no header or value"},
		"follow url":         {`["logs.acme.dev"]`, `["https://logs.acme.dev/x"]`, "follow_hosts"},
		"follow wildcard":    {`["logs.acme.dev"]`, `["*.acme.dev"]`, "follow_hosts"},
		"no detect":          {"files = [\".acme.yml\"]\nremotes = [\"acme.dev\"]\nchecks = [\"acme/*\"]", "", "[detect] files, remotes or checks"},
		"detect outside":     {`[".acme.yml"]`, `["../../etc/passwd"]`, "a path inside the checkout"},
		"detect remote url":  {`["acme.dev"]`, `["https://acme.dev"]`, "[detect] remotes"},
		"no project":         {`id = "{owner}/{repo}"`, `id = ""`, "[project] id"},
		"project unknown":    {`id = "{owner}/{repo}"`, `id = "{branch}"`, "{branch} is not a part of the remote"},
		"state unknown":      {`busy = "pending"`, `busy = "waiting"`, `busy = "waiting"`},
		"no fail state":      {`broken = "fail"`, ``, "maps nothing to pass or to fail"},
		"no runs":            {"[runs]\ncall = \"GET /runs?branch={branch}&commit={head}\"", "[runs]", "[runs]: required for kind ci"},
		"runs fields":        {`items = "runs"`, ``, "[runs] items, id and state"},
		"runs template":      {`"{item.workflow} #{item.number}"`, `"{build.name}"`, "[runs] name: {build.name}"},
		"call and steps":     {"[failed_log]\n", "[failed_log]\ncall = \"GET /x\"\n", "a call or steps, not both"},
		"bad method":         {`call = "POST /runs/{run}/retry"`, `call = "DELETE /runs/{run}/retry"`, "GET, POST, PUT, PATCH"},
		"no slash":           {`call = "GET /runs/{run}/jobs"`, `call = "GET runs"`, "a path under api"},
		"full url":           {`call = "GET /runs/{run}/jobs"`, `call = "GET /https://evil.dev/x"`, "a path under api"},
		"unknown each":       {`each = "jobs"`, `each = "builds"`, `each "builds"`},
		"later name":         {`call = "GET /runs/{run}/jobs"`, `call = "GET /runs/{jobs.id}/jobs"`, "{jobs.id} is nothing known"},
		"run in runs":        {`call = "GET /runs?branch={branch}&commit={head}"`, `call = "GET /runs/{run}"`, "{run} is nothing known"},
		"item outside each":  {`call = "GET /runs/{run}/jobs"`, `call = "GET /runs/{item.id}/jobs"`, "{item.id} is nothing known"},
		"reply first":        {`call = "GET /runs/{run}/jobs"`, `call = "GET /runs/{reply.id}/jobs"`, "{reply.id} is nothing known"},
		"pick unknown":       {`pick = "jobs[*status=broken]"`, `pick = "jobs[sha={sha}]"`, "{sha} is nothing known"},
		"body unknown":       {`branch = "{branch}"`, `branch = "{who}"`, "{who} is nothing known"},
		"last not a log":     {`text = "[*].line"`, ``, "the last step is the log"},
		"text with pick":     {`text = "[*].line"`, "text = \".\"\npick = \"x\"", "no pick"},
		"follow with pick":   {`follow = "log_url"`, "follow = \"log_url\"\npick = \"x\"", "no pick"},
		"follow no hosts":    {`follow_hosts = ["logs.acme.dev"]`, ``, "no follow_hosts"},
		"as reserved":        {`as = "jobs"`, `as = "branch"`, `as "branch"`},
		"job unknown":        {`job = "{jobs.0.name}"`, `job = "{build.name}"`, "[failed_log] job: {build.name}"},
		"key in path":        {`call = "POST /runs/{run}/retry"`, `call = "POST /runs/{run}/retry?token={key}"`, "{key} in"},
		"key in body":        {`failed_only = true`, `token = "{key}"`, "{key} in"},
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
	for _, want := range []string{"bitbucket-pipelines", "buildkite", "circleci", "gitlab-ci", "jenkins", "semaphore", "shortcut", "gitlab-issues", "slack", "discord", "ntfy"} {
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

// The guide is what an agent writes a file from, so every example in it
// has to be a file this conch takes — one per kind it reads.
func TestGuideExampleIsClean(t *testing.T) {
	kinds := map[string]bool{}
	rest := Guide
	for {
		var ok bool
		if _, rest, ok = strings.Cut(rest, "```toml\n"); !ok {
			break
		}
		example, after, ok := strings.Cut(rest, "```")
		if !ok {
			t.Fatal("an example is not closed")
		}
		rest = after
		f, problems, err := Parse([]byte(example))
		if err != nil || len(problems) > 0 {
			t.Fatalf("the guide's example: %v %v\n%s", err, problems, example)
		}
		kinds[f.Kind] = true
	}
	for _, k := range []string{KindCI, KindTickets, KindNotify, KindDeploy} {
		if !kinds[k] {
			t.Errorf("no example of kind %s in the guide", k)
		}
	}
}
