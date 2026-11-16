package server_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/integration"
	"github.com/Amitgb14/conch/internal/proto"
)

// fakeGHOn puts a gh on CONCH_GH that answers pull requests with none and
// runs with list, printing log for run view.
func fakeGHOn(t *testing.T, list, log string) {
	t.Helper()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "log"), []byte(log), 0o600)
	script := `#!/bin/sh
case "$1 $2" in
"pr list") echo '[]' ;;
"run list") printf '%s' '` + list + `' ;;
"run view") cat "` + filepath.Join(dir, "log") + `" ;;
*) exit 7 ;;
esac
`
	gh := filepath.Join(dir, "gh")
	os.WriteFile(gh, []byte(script), 0o755)
	t.Setenv("CONCH_GH", gh)
}

func TestBranchCILog(t *testing.T) {
	fakeGHOn(t, `[{"databaseId": 4, "workflowName": "ci", "url": "https://x/runs/4", "headSha": "abc", "conclusion": "failure"}]`,
		strings.Repeat("noise\n", 20000)+"test\tgo test\t--- FAIL: TestX\n")
	c, _, proj := setupRepo(t)
	if miss := c.MissingCapabilities([]string{proto.CapBranchCILog}); len(miss) > 0 {
		t.Fatalf("capability missing: %v", miss)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var got proto.BranchCILog
	if err := c.Call(ctx, proto.MethodBranchCILog, proto.BranchCILogParams{ProjectID: proj.ID, Branch: "main"}, &got); err != nil {
		t.Fatal(err)
	}
	if got.Workflow != "ci" || got.URL != "https://x/runs/4" || !strings.HasSuffix(got.Log, "--- FAIL: TestX\n") {
		t.Fatalf("got %s %s, log ends %q", got.Workflow, got.URL, got.Log[max(len(got.Log)-40, 0):])
	}
	if len(got.Log) > 64<<10 {
		t.Fatalf("log of %d bytes was not capped", len(got.Log))
	}
}

func TestBranchCILogRefusals(t *testing.T) {
	fakeGHOn(t, `[{"databaseId": 4, "conclusion": "success"}]`, "")
	c, _, proj := setupRepo(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for name, tc := range map[string]struct {
		params proto.BranchCILogParams
		want   string
	}{
		"nothing failed": {proto.BranchCILogParams{ProjectID: proj.ID, Branch: "main"}, "no failed GitHub Actions run on main"},
		"no branch":      {proto.BranchCILogParams{ProjectID: proj.ID}, "no branch given"},
		"no project":     {proto.BranchCILogParams{ProjectID: "nope", Branch: "main"}, "nope"},
	} {
		err := c.Call(ctx, proto.MethodBranchCILog, tc.params, nil)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want %q", name, err, tc.want)
		}
	}
}

// ciRig is a project on github.com/acme/widgets whose pull request has a
// red CircleCI check and no failed Actions run, a fake CircleCI answering
// as its API documents, and a log host that fails the test if it sees the
// key.
type ciRig struct {
	t      *testing.T
	c      *client.Client
	proj   proto.ProjectInfo
	home   string
	api    *httptest.Server
	reruns *int32
	ghLog  string
}

const ciKey = "circle-key-5678"

const circleCheck = `{"__typename":"StatusContext","context":"ci/circleci: build-and-test","state":"FAILURE","targetUrl":"https://app.circleci.com/pipelines/github/acme/widgets/8812"}`

func newCIRig(t *testing.T, install bool, check string) *ciRig {
	t.Helper()
	home := t.TempDir()
	t.Setenv("CONCH_HOME", home)
	t.Setenv("CIRCLECI_TOKEN", "")
	integration.Forget()
	t.Cleanup(integration.Forget)
	var reruns int32
	logs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, vs := range r.Header {
			for _, v := range vs {
				if strings.Contains(v, ciKey) {
					t.Errorf("the key reached the log host")
				}
			}
		}
		io.WriteString(w, `[{"message": "make check\n"}, {"message": "--- FAIL: TestRGW\n"}]`)
	}))
	t.Cleanup(logs.Close)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Circle-Token") != ciKey {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v2/project/gh/acme/widgets/pipeline":
			io.WriteString(w, `{"items": [{"id": "p1", "number": 8812, "vcs": {"revision": "abc"}}]}`)
		case "GET /api/v2/pipeline/p1/workflow":
			io.WriteString(w, `{"items": [{"id": "w1", "name": "build-and-test", "status": "failed"}]}`)
		case "GET /api/v2/workflow/w1/job":
			io.WriteString(w, `{"items": [{"job_number": 7, "name": "unittest-rgw", "status": "failed"}]}`)
		case "GET /api/v1.1/project/gh/acme/widgets/7":
			io.WriteString(w, `{"steps": [{"actions": [{"status": "failed", "output_url": "`+logs.URL+`/o"}]}]}`)
		case "POST /api/v2/workflow/w1/rerun":
			atomic.AddInt32(&reruns, 1)
			io.WriteString(w, `{}`)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(api.Close)
	if install {
		os.MkdirAll(filepath.Join(home, "integrations"), 0o755)
		os.WriteFile(filepath.Join(home, "integrations", "circleci.toml"), []byte(`format = 3
kind = "ci"
name = "circleci"
label = "CircleCI"
api = "`+api.URL+`"
key_env = "CIRCLECI_TOKEN"
follow_hosts = ["127.0.0.1"]
[auth]
header = "Circle-Token"
value = "{key}"
[detect]
files = [".circleci/config.yml"]
checks = ["ci/circleci*"]
[project]
id = "gh/{path}"
[states]
success = "pass"
failed = "fail"
[runs]
items = "items"
id = "id"
name = "name"
state = "status"
commit = "{pipeline.vcs.revision}"
url = "https://app.circleci.com/pipelines/github/{path}/{pipeline.number}/workflows/{item.id}"
[[runs.steps]]
as = "pipeline"
call = "GET /api/v2/project/{project}/pipeline?branch={branch}"
pick = "items[vcs/revision={head}]"
[[runs.steps]]
call = "GET /api/v2/pipeline/{pipeline.id}/workflow"
[failed_log]
job = "{job.name}"
[[failed_log.steps]]
as = "job"
call = "GET /api/v2/workflow/{run}/job"
pick = "items[status=failed]"
[[failed_log.steps]]
call = "GET /api/v1.1/project/{project}/{job.job_number}"
follow = "steps[*].actions[*status=failed].output_url"
text = "[*].message"
[rerun]
call = "POST /api/v2/workflow/{run}/rerun"
body = { from_failed = true }
`), 0o644)
	}
	ghDir := t.TempDir()
	ghLog := filepath.Join(ghDir, "calls")
	gh := filepath.Join(ghDir, "gh")
	os.WriteFile(gh, []byte(`#!/bin/sh
echo "$*" >> `+ghLog+`
case "$1 $2" in
"pr list") echo '[{"number":412,"title":"Fix","state":"OPEN","url":"https://github.com/acme/widgets/pull/412","headRefName":"main","headRefOid":"abc","statusCheckRollup":[`+check+`]}]' ;;
"run list") echo '[]' ;;
*) exit 7 ;;
esac
`), 0o755)
	t.Setenv("CONCH_GH", gh)
	c, _, proj := setupRepo(t)
	repo := proj.Path
	git(t, repo, "remote", "add", "origin", "https://github.com/acme/widgets.git")
	proj = waitProject(t, c, func(p proto.ProjectInfo) bool {
		for _, b := range p.Branches {
			if b.Name == "main" && b.PR != nil && len(b.PR.Runs) > 0 {
				return true
			}
		}
		return false
	})
	return &ciRig{t: t, c: c, proj: proj, home: home, api: api, reruns: &reruns, ghLog: ghLog}
}

func (r *ciRig) check() proto.PRCheck {
	for _, b := range r.proj.Branches {
		if b.Name == "main" {
			return b.PR.Runs[0]
		}
	}
	r.t.Fatal("no main")
	return proto.PRCheck{}
}

func (r *ciRig) call(method string, out any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return r.c.Call(ctx, method, proto.BranchCILogParams{ProjectID: r.proj.ID, Branch: "main"}, out)
}

func TestCILogFromCircleCI(t *testing.T) {
	r := newCIRig(t, true, circleCheck)
	if miss := r.c.MissingCapabilities([]string{proto.CapCISources, proto.CapPRCheckSource, proto.CapCIRerun}); len(miss) > 0 {
		t.Fatalf("capabilities missing: %v", miss)
	}
	if ch := r.check(); ch.Source != "circleci" || !ch.Readable {
		t.Fatalf("the check: %+v", ch)
	}
	// No key: the message says which variable, and nothing is asked.
	err := r.call(proto.MethodBranchCILog, nil)
	if err == nil || !strings.Contains(err.Error(), "CircleCI: set $CIRCLECI_TOKEN") || !strings.Contains(err.Error(), "no failed GitHub Actions run on main") {
		t.Fatalf("no key: %v", err)
	}
	t.Setenv("CIRCLECI_TOKEN", ciKey)
	var lg proto.BranchCILog
	if err := r.call(proto.MethodBranchCILog, &lg); err != nil {
		t.Fatal(err)
	}
	if lg.Source != "circleci" || lg.Service != "CircleCI" || lg.Job != "unittest-rgw" || lg.Workflow != "build-and-test" ||
		lg.URL != "https://app.circleci.com/pipelines/github/acme/widgets/8812/workflows/w1" || lg.Log != "make check\n--- FAIL: TestRGW\n" {
		t.Fatalf("log %+v", lg)
	}
	if p := proto.CIFailurePrompt(lg); !strings.Contains(p, "CI on CircleCI (build-and-test · unittest-rgw) failed") {
		t.Fatalf("prompt: %s", p)
	}
	var rr proto.BranchCIRerun
	if err := r.call(proto.MethodBranchCIRerun, &rr); err != nil {
		t.Fatal(err)
	}
	if rr.Source != "circleci" || rr.Run != "w1" || atomic.LoadInt32(r.reruns) != 1 {
		t.Fatalf("rerun %+v, %d calls", rr, atomic.LoadInt32(r.reruns))
	}
}

// Bitbucket Pipelines is a service conch knows and ships no file for: its
// red check is named, and the message says what would read it.
func TestCILogNamesAServiceWithNoIntegration(t *testing.T) {
	r := newCIRig(t, false, `{"__typename":"StatusContext","context":"Pipeline #12","state":"FAILURE","targetUrl":"https://bitbucket.org/acme/widgets/pipelines/results/12"}`)
	if ch := r.check(); ch.Source != "bitbucket-pipelines" || ch.Readable {
		t.Fatalf("the check: %+v", ch)
	}
	err := r.call(proto.MethodBranchCILog, nil)
	if err == nil || !strings.Contains(err.Error(), "Bitbucket Pipelines: install its integration to read its log") {
		t.Fatalf("got %v", err)
	}
	err = r.call(proto.MethodBranchCIRerun, nil)
	if err == nil || !strings.Contains(err.Error(), "Bitbucket Pipelines: install its integration") {
		t.Fatalf("rerun: %v", err)
	}
}

// Actions answers first, and the integration is never asked.
func TestCILogActionsStillFirst(t *testing.T) {
	fakeGHOn(t, `[{"databaseId": 4, "workflowName": "ci", "url": "https://x/runs/4", "headSha": "", "conclusion": "failure"}]`, "FAIL from actions\n")
	home := t.TempDir()
	t.Setenv("CONCH_HOME", home)
	integration.Forget()
	t.Cleanup(integration.Forget)
	hit := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("an integration was asked: %s", r.URL)
	}))
	defer hit.Close()
	c, _, proj := setupRepo(t)
	os.MkdirAll(filepath.Join(proj.Path, ".circleci"), 0o755)
	os.WriteFile(filepath.Join(proj.Path, ".circleci", "config.yml"), nil, 0o644)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var lg proto.BranchCILog
	if err := c.Call(ctx, proto.MethodBranchCILog, proto.BranchCILogParams{ProjectID: proj.ID, Branch: "main"}, &lg); err != nil {
		t.Fatal(err)
	}
	if lg.Source != "github-actions" || lg.Log != "FAIL from actions\n" {
		t.Fatalf("got %+v", lg)
	}
}

// A project whose checkout uses CircleCI, with no remote to name it by,
// says so rather than asking anything.
func TestCILogNoRemote(t *testing.T) {
	fakeGHOn(t, `[]`, "")
	t.Setenv("CONCH_HOME", t.TempDir())
	t.Setenv("CIRCLECI_TOKEN", ciKey)
	integration.Forget()
	t.Cleanup(integration.Forget)
	c, _, proj := setupRepo(t)
	os.MkdirAll(filepath.Join(proj.Path, ".circleci"), 0o755)
	os.WriteFile(filepath.Join(proj.Path, ".circleci", "config.yml"), nil, 0o644)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	err := c.Call(ctx, proto.MethodBranchCILog, proto.BranchCILogParams{ProjectID: proj.ID, Branch: "main"}, nil)
	if err == nil || !strings.Contains(err.Error(), "CircleCI: the project has no remote") {
		t.Fatalf("got %v", err)
	}
}

func TestCIRerunActions(t *testing.T) {
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	gh := filepath.Join(dir, "gh")
	os.WriteFile(gh, []byte(`#!/bin/sh
echo "$*" >> `+calls+`
case "$1 $2" in
"pr list") echo '[]' ;;
"run list") echo '[{"databaseId": 9, "workflowName": "ci", "url": "https://x/runs/9", "conclusion": "failure"}]' ;;
"run rerun") exit 0 ;;
*) exit 7 ;;
esac
`), 0o755)
	t.Setenv("CONCH_GH", gh)
	t.Setenv("CONCH_HOME", t.TempDir())
	integration.Forget()
	t.Cleanup(integration.Forget)
	c, _, proj := setupRepo(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var rr proto.BranchCIRerun
	if err := c.Call(ctx, proto.MethodBranchCIRerun, proto.BranchCILogParams{ProjectID: proj.ID, Branch: "main"}, &rr); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(calls)
	if rr.Source != "github-actions" || rr.Run != "9" || !strings.Contains(string(b), "run rerun 9 --failed") {
		t.Fatalf("rerun %+v; gh was run as:\n%s", rr, b)
	}
}

// A self-hosted GitLab: the file asks for its address, which config.toml
// gives, and the log is read there.
func TestCILogSelfHostedBase(t *testing.T) {
	fakeGHOn(t, `[]`, "")
	home := t.TempDir()
	t.Setenv("CONCH_HOME", home)
	t.Setenv("GITLAB_TOKEN", "gl-key")
	integration.Forget()
	t.Cleanup(integration.Forget)
	gl := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("PRIVATE-TOKEN") != "gl-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.EscapedPath() {
		case "/api/v4/projects/grp%2Fwidgets/pipelines":
			io.WriteString(w, `[{"id": 41, "status": "failed", "web_url": "https://git.corp/grp/widgets/-/pipelines/41"}]`)
		case "/api/v4/projects/grp%2Fwidgets/pipelines/41/jobs":
			io.WriteString(w, `[{"id": 9, "name": "test"}]`)
		case "/api/v4/projects/grp%2Fwidgets/jobs/9/trace":
			io.WriteString(w, "FAIL on the corp gitlab\n")
		default:
			t.Errorf("unexpected %s", r.URL)
		}
	}))
	defer gl.Close()
	os.WriteFile(filepath.Join(home, "config.toml"), []byte("[integrations.base]\nmy-gitlab = \""+gl.URL+"/api/v4\"\n"), 0o600)
	os.MkdirAll(filepath.Join(home, "integrations"), 0o755)
	os.WriteFile(filepath.Join(home, "integrations", "my-gitlab.toml"), []byte(`format = 3
kind = "ci"
name = "my-gitlab"
label = "Corp GitLab"
base_url = "ask"
key_env = "GITLAB_TOKEN"
[auth]
header = "PRIVATE-TOKEN"
value = "{key}"
[detect]
remotes = ["git.corp"]
[project]
id = "{path_escaped}"
[states]
success = "pass"
failed = "fail"
[runs]
call = "GET /projects/{project}/pipelines?ref={branch}"
items = "[*]"
id = "id"
state = "status"
url = "web_url"
[failed_log]
[[failed_log.steps]]
as = "jobs"
call = "GET /projects/{project}/pipelines/{run}/jobs?scope[]=failed"
pick = "[*]"
[[failed_log.steps]]
each = "jobs"
call = "GET /projects/{project}/jobs/{item.id}/trace"
text = "."
`), 0o644)
	c, _, proj := setupRepo(t)
	git(t, proj.Path, "remote", "add", "origin", "git@git.corp:grp/widgets.git")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var lg proto.BranchCILog
	if err := c.Call(ctx, proto.MethodBranchCILog, proto.BranchCILogParams{ProjectID: proj.ID, Branch: "main"}, &lg); err != nil {
		t.Fatal(err)
	}
	if lg.Source != "my-gitlab" || lg.Service != "Corp GitLab" || lg.Log != "FAIL on the corp gitlab\n" {
		t.Fatalf("got %+v", lg)
	}
	// Without the address it says where to give it.
	os.WriteFile(filepath.Join(home, "config.toml"), nil, 0o600)
	err := c.Call(ctx, proto.MethodBranchCILog, proto.BranchCILogParams{ProjectID: proj.ID, Branch: "main"}, nil)
	if err == nil || !strings.Contains(err.Error(), "[integrations.base] my-gitlab") {
		t.Fatalf("no base: %v", err)
	}
}
