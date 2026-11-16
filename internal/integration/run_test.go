package integration

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const testKey = "sekrit-key-1234"

// shipped is a copy of a file conch ships, its hosts pointed at tests.
func shipped(t *testing.T, name string) *File {
	t.Helper()
	data, err := shippedFS.ReadFile("files/" + name + ".toml")
	if err != nil {
		t.Fatal(err)
	}
	f, problems, err := Parse(data)
	if err != nil || len(problems) > 0 {
		t.Fatal(err, problems)
	}
	return f
}

// keyless is a log host: it fails the test if any header carries the key.
func keyless(t *testing.T, h http.HandlerFunc) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for name, vs := range r.Header {
			for _, v := range vs {
				if strings.Contains(v, testKey) {
					t.Errorf("the key reached a follow host in %s", name)
				}
			}
		}
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// fakeCircleCI answers as CircleCI's API documents
// (circleci.com/docs/api/v2: pipelines, workflows, jobs; v1.1's single job
// with steps[].actions[].output_url), with logs on another host.
func fakeCircleCI(t *testing.T) (*File, *httptest.Server, *[]string) {
	var mu sync.Mutex
	var reruns []string
	logs := keyless(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/out/1":
			io.WriteString(w, `[{"message": "make check\r\n", "type": "out"}, {"message": "--- FAIL: TestRGW", "type": "out"}]`)
		case "/out/2":
			io.WriteString(w, `[{"message": "lint: 2 problems\n", "type": "err"}]`)
		default:
			http.NotFound(w, r)
		}
	})
	var api *httptest.Server
	api = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Circle-Token") != testKey {
			w.WriteHeader(http.StatusUnauthorized)
			io.WriteString(w, `{"message": "You must log in"}`)
			return
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v2/project/gh/acme/widgets/pipeline":
			if r.URL.Query().Get("branch") != "rgw-fix" {
				io.WriteString(w, `{"items": [], "next_page_token": null}`)
				return
			}
			io.WriteString(w, `{"items": [
			  {"id": "p-new", "number": 8813, "state": "created", "vcs": {"revision": "fff", "branch": "rgw-fix"}},
			  {"id": "p-head", "number": 8812, "state": "created", "vcs": {"revision": "abc", "branch": "rgw-fix"}}
			], "next_page_token": null}`)
		case "GET /api/v2/pipeline/p-head/workflow", "GET /api/v2/pipeline/p-new/workflow":
			io.WriteString(w, `{"items": [
			  {"id": "w-1", "name": "build-and-test", "status": "failed", "pipeline_number": 8812, "created_at": "2026-11-15T14:00:00Z", "stopped_at": "2026-11-15T14:02:00Z"},
			  {"id": "w-2", "name": "docs", "status": "success", "created_at": "2026-11-15T14:00:00Z"},
			  {"id": "w-3", "name": "nightly", "status": "something_new"}
			]}`)
		case "GET /api/v2/workflow/w-1/job":
			io.WriteString(w, `{"items": [
			  {"job_number": 6, "name": "build", "status": "success"},
			  {"job_number": 7, "name": "unittest-rgw", "status": "failed"}
			]}`)
		case "GET /api/v1.1/project/gh/acme/widgets/7":
			io.WriteString(w, `{"build_num": 7, "steps": [
			  {"name": "checkout", "actions": [{"status": "success", "output_url": "`+logs.URL+`/out/0"}]},
			  {"name": "make check", "actions": [{"status": "failed", "output_url": "`+logs.URL+`/out/1?X-Amz-Signature=abc"}]},
			  {"name": "lint", "actions": [{"status": "failed", "output_url": "`+logs.URL+`/out/2"}]}
			]}`)
		case "POST /api/v2/workflow/w-1/rerun":
			b, _ := io.ReadAll(r.Body)
			mu.Lock()
			reruns = append(reruns, string(b))
			mu.Unlock()
			io.WriteString(w, `{"workflow_id": "w-9"}`)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(api.Close)
	f := shipped(t, "circleci")
	f.API = api.URL
	f.FollowHosts = []string{"127.0.0.1"}
	return f, api, &reruns
}

var widgets = Target{Remote: Remote{Host: "github.com", Path: "acme/widgets"}, Branch: "rgw-fix", Head: "abc"}

func TestCircleCIEndToEnd(t *testing.T) {
	f, _, reruns := fakeCircleCI(t)
	c, err := f.Connect(testKey, "", "")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	runs, err := c.Runs(ctx, widgets)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 3 {
		t.Fatalf("runs %+v", runs)
	}
	r := runs[0]
	if r.ID != "w-1" || r.Name != "build-and-test" || r.State != "fail" || r.Raw != "failed" || r.Commit != "abc" ||
		r.URL != "https://app.circleci.com/pipelines/github/acme/widgets/8812/workflows/w-1" ||
		r.Finished.Sub(r.Started) != 2*time.Minute {
		t.Fatalf("run %+v", r)
	}
	if runs[1].State != "pass" || runs[2].State != "pending" || runs[2].Raw != "something_new" {
		t.Fatalf("states %+v", runs[1:])
	}
	lg, err := c.FailedLog(ctx, widgets, "w-1")
	if err != nil {
		t.Fatal(err)
	}
	if lg.Job != "unittest-rgw" || lg.Text != "make check\r\n--- FAIL: TestRGW\nlint: 2 problems\n" {
		t.Fatalf("log %+v", lg)
	}
	if err := c.Rerun(ctx, widgets, "w-1"); err != nil {
		t.Fatal(err)
	}
	if len(*reruns) != 1 || (*reruns)[0] != `{"from_failed":true}` {
		t.Fatalf("reruns %q", *reruns)
	}
	// With no pull request there is no head: the newest pipeline is read.
	noHead := widgets
	noHead.Head = ""
	if runs, err := c.Runs(ctx, noHead); err != nil || len(runs) != 3 || runs[0].Commit != "fff" {
		t.Fatalf("no head: %+v %v", runs, err)
	}
	// A branch with no pipelines has no runs, and that is not an error.
	other := widgets
	other.Branch = "elsewhere"
	if runs, err := c.Runs(ctx, other); err != nil || len(runs) != 0 {
		t.Fatalf("no pipelines: %+v %v", runs, err)
	}
}

// fakeGitLab answers as GitLab's REST API documents (docs.gitlab.com/api:
// pipelines, pipeline jobs with scope[], job trace, retry) for a project in
// nested groups, so its id travels escaped.
func fakeGitLab(t *testing.T) (*File, *[]string) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("PRIVATE-TOKEN") != testKey {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		seen = append(seen, r.Method+" "+r.URL.EscapedPath()+"?"+r.URL.RawQuery)
		switch r.Method + " " + r.URL.EscapedPath() {
		case "GET /api/v4/projects/grp%2Fsub%2Fwidgets/pipelines":
			io.WriteString(w, `[{"id": 41, "status": "failed", "sha": "abc", "ref": "fix", "web_url": "https://gitlab.com/grp/sub/widgets/-/pipelines/41", "created_at": "2026-11-15T14:00:00Z"},
			  {"id": 40, "status": "success", "sha": "abc", "web_url": "https://gitlab.com/grp/sub/widgets/-/pipelines/40"}]`)
		case "GET /api/v4/projects/grp%2Fsub%2Fwidgets/pipelines/41/jobs":
			io.WriteString(w, `[{"id": 901, "name": "test", "status": "failed"}, {"id": 902, "name": "lint", "status": "failed"}]`)
		case "GET /api/v4/projects/grp%2Fsub%2Fwidgets/pipelines/40/jobs":
			io.WriteString(w, `[]`)
		case "GET /api/v4/projects/grp%2Fsub%2Fwidgets/jobs/901/trace":
			io.WriteString(w, "$ go test ./...\nFAIL widgets\n")
		case "GET /api/v4/projects/grp%2Fsub%2Fwidgets/jobs/902/trace":
			io.WriteString(w, "lint failed")
		case "POST /api/v4/projects/grp%2Fsub%2Fwidgets/pipelines/41/retry":
			io.WriteString(w, `{"id": 41, "status": "pending"}`)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	f := shipped(t, "gitlab-ci")
	f.API = srv.URL + "/api/v4"
	return f, &seen
}

func TestGitLabCIEndToEnd(t *testing.T) {
	f, seen := fakeGitLab(t)
	c, err := f.Connect(testKey, "", "")
	if err != nil {
		t.Fatal(err)
	}
	target := Target{Remote: Remote{Host: "gitlab.com", Path: "grp/sub/widgets"}, Branch: "fix"}
	ctx := context.Background()
	runs, err := c.Runs(ctx, target)
	if err != nil || len(runs) != 2 || runs[0].ID != "41" || runs[0].State != "fail" || runs[0].Name != "pipeline 41" ||
		runs[0].URL != "https://gitlab.com/grp/sub/widgets/-/pipelines/41" || runs[1].State != "pass" {
		t.Fatalf("runs %+v %v", runs, err)
	}
	if q := (*seen)[0]; strings.Contains(q, "sha=") || !strings.Contains(q, "ref=fix") {
		t.Fatalf("with no head, sha is not asked for: %s", q)
	}
	lg, err := c.FailedLog(ctx, target, "41")
	if err != nil || lg.Job != "test" || lg.Text != "$ go test ./...\nFAIL widgets\nlint failed\n" {
		t.Fatalf("log %+v %v", lg, err)
	}
	if !strings.Contains(strings.Join(*seen, "\n"), "scope%5B%5D=failed") && !strings.Contains(strings.Join(*seen, "\n"), "scope[]=failed") {
		t.Fatalf("failed jobs not asked for: %v", *seen)
	}
	if err := c.Rerun(ctx, target, "41"); err != nil {
		t.Fatal(err)
	}
	// A run with no failed jobs has no log to read.
	if _, err := c.FailedLog(ctx, target, "40"); !errors.Is(err, ErrNothing) {
		t.Fatalf("no failed jobs: %v", err)
	}
	target.Head = "abc"
	*seen = nil
	if _, err := c.Runs(ctx, target); err != nil || !strings.Contains((*seen)[0], "sha=abc") {
		t.Fatalf("head asked for: %v %v", *seen, err)
	}
}

func TestBaseURLAsked(t *testing.T) {
	f, _ := fakeGitLab(t)
	api := f.API
	f.API = "https://gitlab.com/api/v4"
	// The person's own GitLab, given at install, replaces the default.
	c, err := f.Connect(testKey, "", api)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Runs(context.Background(), Target{Remote: Remote{Host: "git.corp", Path: "grp/sub/widgets"}, Branch: "fix"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Connect(testKey, "", "http://git.corp/api/v4"); err == nil {
		t.Fatal("a plain-http base taken")
	}
	cc, _, _ := fakeCircleCI(t)
	if _, err := cc.Connect(testKey, "", "https://elsewhere.dev"); err == nil {
		t.Fatal("a base for a file that does not ask for one")
	}
}

func TestConnectRefusals(t *testing.T) {
	f := shipped(t, "circleci")
	if _, err := f.Connect("  ", "", ""); !errors.Is(err, ErrNoKey) || !strings.Contains(err.Error(), "$CIRCLECI_TOKEN") {
		t.Fatalf("no key: %v", err)
	}
	f.Auth = Auth{Type: "basic", UserEnv: "JENKINS_USER"}
	if _, err := f.Connect(testKey, "", ""); !errors.Is(err, ErrNoKey) || !strings.Contains(err.Error(), "$JENKINS_USER") {
		t.Fatalf("basic with no user: %v", err)
	}
	f.Kind = KindTickets
	if _, err := f.Connect(testKey, "u", ""); err == nil {
		t.Fatal("a kind this conch does not run")
	}
}

// oneCall is a file whose runs is one call, for the error paths.
func oneCall(t *testing.T, api string, extra string) *File {
	t.Helper()
	src := `format = 3
kind = "ci"
name = "edge"
api = "` + api + `"
key_env = "EDGE_TOKEN"
follow_hosts = ["logs.example"]
[detect]
files = ["x"]
[project]
id = "{path}"
[states]
ok = "pass"
bad = "fail"
[runs]
call = "GET /runs"
items = "[*]"
id = "id"
state = "status"
[failed_log]
call = "GET /log/{run}"
follow = "url"
text = "."
` + extra
	f, problems, err := Parse([]byte(src))
	if err != nil || len(problems) > 0 {
		t.Fatal(err, problems)
	}
	return f
}

func TestAuthKinds(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		io.WriteString(w, `[]`)
	}))
	defer srv.Close()
	target := Target{Remote: Remote{Host: "h", Path: "a/b"}, Branch: "x"}
	for _, tc := range []struct {
		auth   Auth
		user   string
		header string
		want   string
	}{
		{Auth{}, "", "Authorization", "Bearer " + testKey},
		{Auth{Header: "X-Token", Value: "token {key}"}, "", "X-Token", "token " + testKey},
		{Auth{Type: "bearer", Scopes: []string{"read_builds"}}, "", "Authorization", "Bearer " + testKey},
		{Auth{Type: "basic", UserEnv: "U"}, "amit", "Authorization", "Basic YW1pdDpzZWtyaXQta2V5LTEyMzQ="},
	} {
		f := oneCall(t, srv.URL, "")
		f.Auth = tc.auth
		c, err := f.Connect(testKey, tc.user, "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.Runs(context.Background(), target); err != nil {
			t.Fatal(err)
		}
		if got.Get(tc.header) != tc.want {
			t.Errorf("%+v: %s = %q, want %q", tc.auth, tc.header, got.Get(tc.header), tc.want)
		}
		if got.Get("User-Agent") != "conch" {
			t.Errorf("user agent %q", got.Get("User-Agent"))
		}
	}
}

func TestRequestsStayOnTheFilesHosts(t *testing.T) {
	elsewhere := keyless(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("a request reached a host the file does not name: %s", r.URL)
	})
	target := Target{Remote: Remote{Host: "h", Path: "a/b"}, Branch: "x"}
	for name, log := range map[string]string{
		"follow to an unlisted host":  elsewhere.URL + "/log",
		"plain http to a listed host": "http://logs.example/log",
		"not a URL":                   "::nope",
		"a listed host's look-alike":  "https://logs.example.evil.dev/log",
		"userinfo trick":              "https://logs.example@evil.dev/log",
	} {
		api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]string{"url": log})
		}))
		c, err := oneCall(t, api.URL, "").Connect(testKey, "", "")
		if err != nil {
			t.Fatal(err)
		}
		_, err = c.FailedLog(context.Background(), target, "1")
		if err == nil || !strings.Contains(err.Error(), "may not be fetched") {
			t.Errorf("%s: %v", name, err)
		}
		if err != nil && strings.Contains(err.Error(), "abc") {
			t.Errorf("%s: a presigned query in the error: %v", name, err)
		}
		api.Close()
	}

	// The API redirecting elsewhere would carry the key with it.
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/runs", http.StatusFound)
	}))
	defer api.Close()
	c, _ := oneCall(t, api.URL, "").Connect(testKey, "", "")
	if _, err := c.Runs(context.Background(), target); err == nil || !strings.Contains(err.Error(), "redirected") {
		t.Fatalf("a redirect off the API: %v", err)
	}

	// A follow host redirecting elsewhere is refused too. Both test
	// servers are on 127.0.0.1, so the redirect names the other by
	// localhost, a host the file does not list.
	logs := keyless(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, strings.Replace(elsewhere.URL, "127.0.0.1", "localhost", 1)+"/x", http.StatusFound)
	})
	api2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"url": logs.URL + "/log"})
	}))
	defer api2.Close()
	f := oneCall(t, api2.URL, "")
	f.FollowHosts = []string{"127.0.0.1"}
	c, _ = f.Connect(testKey, "", "")
	if _, err := c.FailedLog(context.Background(), target, "1"); err == nil || !strings.Contains(err.Error(), "redirected") {
		t.Fatalf("a redirect off a follow host: %v", err)
	}
}

func TestReplyErrors(t *testing.T) {
	target := Target{Remote: Remote{Host: "h", Path: "a/b"}, Branch: "x"}
	for name, tc := range map[string]struct {
		status int
		body   string
		want   string
	}{
		"unauthorized": {401, `{"message": "bad token ` + testKey + `"}`, "answered 401"},
		"server error": {500, strings.Repeat("x", 500), "answered 500"},
		"not json":     {200, "<html>", "not JSON"},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
			io.WriteString(w, tc.body)
		}))
		c, _ := oneCall(t, srv.URL, "").Connect(testKey, "", "")
		_, err := c.Runs(context.Background(), target)
		srv.Close()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if strings.Contains(err.Error(), testKey) {
			t.Errorf("%s: the key in the error: %v", name, err)
		}
		if tc.status == 401 && !strings.Contains(err.Error(), "check $EDGE_TOKEN") {
			t.Errorf("%s: no hint: %v", name, err)
		}
		if tc.status == 500 && len(err.Error()) > 400 {
			t.Errorf("%s: the whole body in the error", name)
		}
	}
}

func TestReplyCapAndCancel(t *testing.T) {
	target := Target{Remote: Remote{Host: "h", Path: "a/b"}, Branch: "x"}
	big := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chunk := []byte(strings.Repeat(" ", 1<<20))
		for i := 0; i <= maxReply>>20; i++ {
			w.Write(chunk)
		}
	}))
	defer big.Close()
	c, _ := oneCall(t, big.URL, "").Connect(testKey, "", "")
	if _, err := c.Runs(context.Background(), target); err == nil || !strings.Contains(err.Error(), "over 16 MB") {
		t.Fatalf("a reply over the cap: %v", err)
	}

	hang := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer hang.Close()
	c, _ = oneCall(t, hang.URL, "").Connect(testKey, "", "")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := c.Runs(ctx, target); err == nil {
		t.Fatal("a cancelled context answered")
	}
}

func TestEmptyPathPlaceholderRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("a call with a hole in its path was made: %s", r.URL)
	}))
	defer srv.Close()
	f := oneCall(t, srv.URL, "")
	f.Runs.Call = "GET /runs/{head}"
	c, _ := f.Connect(testKey, "", "")
	if _, err := c.Runs(context.Background(), Target{Remote: Remote{Host: "h", Path: "a/b"}, Branch: "x"}); err == nil ||
		!strings.Contains(err.Error(), "{head} is empty") {
		t.Fatalf("got %v", err)
	}
}

func TestFillers(t *testing.T) {
	vals := map[string]any{"branch": "feat/a b", "head": "", "project": "gh/o/r", "job": decode(t, `{"n": 7}`)}
	if p, err := fillPath("/p/{project}/b/{branch}/{job.n}", vals); err != nil || p != "/p/gh/o/r/b/feat%2Fa%20b/7" {
		t.Fatalf("path %q %v", p, err)
	}
	if q := fillQuery("branch={branch}&sha={head}&n=1&scope[]=failed", vals); q != "branch=feat%2Fa+b&n=1&scope[]=failed" {
		t.Fatalf("query %q", q)
	}
	b, _ := fillBody(map[string]any{"n": "{job.n}", "s": "on {branch}", "gone": "{head}", "list": []any{"{branch}"}}, vals)
	enc, _ := json.Marshal(b)
	if string(enc) != `{"list":["feat/a b"],"n":7,"s":"on feat/a b"}` {
		t.Fatalf("body %s", enc)
	}
	if parseTime("1700000000").Unix() != 1700000000 || parseTime("1700000000000").Unix() != 1700000000 ||
		!parseTime("nope").IsZero() || !parseTime("").IsZero() {
		t.Fatal("times")
	}
}
