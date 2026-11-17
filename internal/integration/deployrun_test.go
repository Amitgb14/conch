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

// app is the project the deploy tests ask about: branch fix-login of
// github.com/acme/app, its pull request's head abc123.
var app = Target{Remote: Remote{Host: "github.com", Path: "acme/app"}, Branch: "fix-login", Head: "abc123"}

// seen records what a fake API was asked: method, path and query, body.
type seen struct {
	mu  sync.Mutex
	got []string
}

func (s *seen) add(r *http.Request) string {
	b, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	defer s.mu.Unlock()
	line := r.Method + " " + r.URL.Path
	if r.URL.RawQuery != "" {
		line += "?" + r.URL.RawQuery
	}
	if len(b) > 0 {
		line += " " + string(b)
	}
	s.got = append(s.got, line)
	return string(b)
}

func (s *seen) all() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.got...)
}

// fakeVercel answers as Vercel's REST API documents
// (vercel.com/docs/rest-api: find projects by repoUrl, list deployments
// of a project by branch and sha, a deployment's build events, get a
// deployment, create one from another with deploymentId), with the token
// as a bearer.
func fakeVercel(t *testing.T) (*File, *seen) {
	s := &seen{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+testKey {
			w.WriteHeader(http.StatusForbidden)
			io.WriteString(w, `{"error": {"code": "forbidden", "message": "Not authorized"}}`)
			return
		}
		body := s.add(r)
		q := r.URL.Query()
		switch r.Method + " " + r.URL.Path {
		case "GET /v10/projects":
			if q.Get("repoUrl") != "https://github.com/acme/app" {
				io.WriteString(w, `{"projects": [], "pagination": {"count": 0}}`)
				return
			}
			io.WriteString(w, `{"projects": [{"id": "prj_9", "name": "app"}], "pagination": {"count": 1}}`)
		case "GET /v7/deployments":
			if q.Get("projectId") != "prj_9" || q.Get("branch") != "fix-login" {
				io.WriteString(w, `{"deployments": [], "pagination": {}}`)
				return
			}
			deps := `{"uid": "dpl_2", "name": "app", "url": "app-git-fix-login-acme.vercel.app", "readyState": "ERROR",
			  "target": null, "errorMessage": "Command \"npm run build\" exited with 1", "inspectorUrl": "https://vercel.com/acme/app/dpl_2",
			  "meta": {"githubCommitSha": "abc123"}, "created": 1731740000000, "ready": 1731740090000},
			 {"uid": "dpl_1", "name": "app", "url": "app-abc-acme.vercel.app", "readyState": "READY", "target": "production",
			  "inspectorUrl": "https://vercel.com/acme/app/dpl_1", "meta": {"githubCommitSha": "abc123"}, "created": 1731730000000, "ready": 1731730060000},
			 {"uid": "dpl_0", "name": "app", "url": "app-old-acme.vercel.app", "readyState": "SOMETHING_NEW", "meta": {"githubCommitSha": "abc123"}, "created": 1731720000000}`
			if q.Get("sha") != "" && q.Get("sha") != "abc123" {
				deps = ""
			}
			io.WriteString(w, `{"deployments": [`+deps+`], "pagination": {}}`)
		case "GET /v3/deployments/dpl_2/events":
			if q.Get("builds") != "1" || q.Get("limit") != "-1" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			io.WriteString(w, `[{"type": "command", "payload": {"text": "npm run build"}},
			  {"type": "stderr", "payload": {"text": "Error: Cannot find module './login'"}},
			  {"type": "exit", "payload": {}}]`)
		case "GET /v13/deployments/dpl_2", "GET /v13/deployments/dpl_1":
			id := strings.TrimPrefix(r.URL.Path, "/v13/deployments/")
			io.WriteString(w, `{"id": "`+id+`", "name": "app", "url": "x.vercel.app", "readyState": "READY"}`)
		case "POST /v13/deployments":
			var b map[string]any
			if json.Unmarshal([]byte(body), &b) != nil || b["deploymentId"] == nil || b["name"] != "app" {
				w.WriteHeader(http.StatusBadRequest)
				io.WriteString(w, `{"error": {"code": "bad_request"}}`)
				return
			}
			io.WriteString(w, `{"id": "dpl_new", "readyState": "QUEUED"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"error": {"code": "not_found", "message": "The deployment was not found."}}`)
		}
	}))
	t.Cleanup(srv.Close)
	f := fixture(t, "vercel")
	f.API = srv.URL
	return f, s
}

func TestVercelDeploys(t *testing.T) {
	f, s := fakeVercel(t)
	c, err := f.Connect(testKey, "", "")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	deps, err := c.Previews(ctx, app)
	if err != nil {
		t.Fatal(err)
	}
	if len(deps) != 3 {
		t.Fatalf("previews %+v", deps)
	}
	d := deps[0]
	if d.ID != "dpl_2" || d.Name != "app" || d.State != "failed" || d.Raw != "ERROR" || d.Commit != "abc123" ||
		d.URL != "https://app-git-fix-login-acme.vercel.app" || d.Inspect != "https://vercel.com/acme/app/dpl_2" ||
		d.Error != `Command "npm run build" exited with 1` || d.Environment != "" ||
		d.Finished.Sub(d.Started) != 90*time.Second {
		t.Fatalf("first %+v", d)
	}
	if deps[1].State != "ready" || deps[1].Environment != "production" {
		t.Fatalf("second %+v", deps[1])
	}
	// A state the file does not know is not a verdict.
	if deps[2].State != "building" || deps[2].Raw != "SOMETHING_NEW" || !deps[2].Finished.IsZero() {
		t.Fatalf("third %+v", deps[2])
	}
	got := strings.Join(s.all(), "\n")
	for _, want := range []string{"GET /v10/projects?repoUrl=https%3A%2F%2Fgithub.com%2Facme%2Fapp&limit=1",
		"GET /v7/deployments?projectId=prj_9&branch=fix-login&sha=abc123&limit=20"} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in\n%s", want, got)
		}
	}

	// No pull request, no head: the branch's deploys, sha not asked.
	noHead := app
	noHead.Head = ""
	if deps, err := c.Previews(ctx, noHead); err != nil || len(deps) != 3 {
		t.Fatalf("no head: %+v %v", deps, err)
	}
	if last := s.all()[len(s.all())-1]; strings.Contains(last, "sha=") {
		t.Fatalf("an empty head asked for: %s", last)
	}
	// A head with no deploy, a branch with none: nothing, and not an error.
	for _, tg := range []Target{{Remote: app.Remote, Branch: "fix-login", Head: "fff"}, {Remote: app.Remote, Branch: "other"}} {
		if deps, err := c.Previews(ctx, tg); err != nil || len(deps) != 0 {
			t.Fatalf("%+v: %+v %v", tg, deps, err)
		}
	}
	// A repository Vercel has no project for is said: nothing is linked.
	if deps, err := c.Previews(ctx, Target{Remote: Remote{Host: "github.com", Path: "acme/elsewhere"}, Branch: "fix-login"}); !errors.Is(err, ErrUnlinked) || len(deps) != 0 {
		t.Fatalf("no project: %+v %v", deps, err)
	}

	lg, err := c.FailedLog(ctx, app, "dpl_2")
	if err != nil {
		t.Fatal(err)
	}
	if lg.Text != "npm run build\nError: Cannot find module './login'\n" {
		t.Fatalf("log %q", lg.Text)
	}

	before := len(s.all())
	if err := c.Redeploy(ctx, app, "dpl_2"); err != nil {
		t.Fatal(err)
	}
	if err := c.Promote(ctx, app, "dpl_1"); err != nil {
		t.Fatal(err)
	}
	calls := s.all()[before:]
	if len(calls) != 4 || calls[0] != "GET /v13/deployments/dpl_2" || calls[2] != "GET /v13/deployments/dpl_1" {
		t.Fatalf("calls %q", calls)
	}
	var re, pro map[string]any
	if !strings.HasPrefix(calls[1], "POST /v13/deployments?forceNew=1 ") || json.Unmarshal([]byte(calls[1][strings.Index(calls[1], "{"):]), &re) != nil ||
		re["name"] != "app" || re["deploymentId"] != "dpl_2" || re["target"] != nil {
		t.Fatalf("redeploy %q", calls[1])
	}
	if !strings.HasPrefix(calls[3], "POST /v13/deployments {") || json.Unmarshal([]byte(calls[3][strings.Index(calls[3], "{"):]), &pro) != nil ||
		pro["deploymentId"] != "dpl_1" || pro["target"] != "production" {
		t.Fatalf("promote %q", calls[3])
	}

	// A deploy Vercel has not got is said as its 404, and nothing is sent.
	before = len(s.all())
	if err := c.Promote(ctx, app, "dpl_gone"); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("no such deploy: %v", err)
	}
	if calls := s.all()[before:]; len(calls) != 1 || strings.HasPrefix(calls[0], "POST") {
		t.Fatalf("a promote after a 404: %q", calls)
	}
	for _, id := range []string{"", "  "} {
		if err := c.Promote(ctx, app, id); err == nil {
			t.Fatalf("promote %q", id)
		}
		if err := c.Redeploy(ctx, app, id); err == nil {
			t.Fatalf("redeploy %q", id)
		}
		if _, err := c.FailedLog(ctx, app, id); err == nil {
			t.Fatalf("log %q", id)
		}
	}

	// The wrong key: said with the variable, not repeated back.
	bad, _ := f.Connect("wrong-key", "", "")
	if _, err := bad.Previews(ctx, app); err == nil || !strings.Contains(err.Error(), "$VERCEL_TOKEN") || strings.Contains(err.Error(), "wrong-key") {
		t.Fatalf("refused key: %v", err)
	}
}

// fakeNetlify answers as Netlify's API documents (open-api.netlify.com:
// listSites with filter, listSiteDeploys by branch, getDeploy,
// createSiteBuild, restoreSiteDeploy), under /api/v1.
func fakeNetlify(t *testing.T) (*File, *seen) {
	s := &seen{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+testKey {
			w.WriteHeader(http.StatusUnauthorized)
			io.WriteString(w, `{"code": 401, "message": "Access Denied"}`)
			return
		}
		s.add(r)
		q := r.URL.Query()
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/sites":
			io.WriteString(w, `[{"id": "site_other", "name": "blog", "build_settings": {"repo_path": "acme/blog"}},
			  {"id": "site_1", "name": "acme-app", "build_settings": {"repo_path": "acme/app", "repo_branch": "main"}}]`)
		case "GET /api/v1/sites/site_1/deploys":
			if q.Get("branch") != "fix-login" {
				io.WriteString(w, `[]`)
				return
			}
			io.WriteString(w, `[{"id": "d2", "site_id": "site_1", "title": "Fix the login", "state": "error", "branch": "fix-login",
			   "deploy_ssl_url": "https://deploy-preview-7--acme-app.netlify.app", "commit_ref": "abc123", "context": "deploy-preview",
			   "error_message": "Build script returned non-zero exit code: 2", "admin_url": "https://app.netlify.com/sites/acme-app/deploys/d2",
			   "created_at": "2026-11-16T10:00:00.000Z", "published_at": null, "review_id": 7},
			  {"id": "d1", "site_id": "site_1", "title": "Earlier", "state": "ready", "branch": "fix-login",
			   "deploy_ssl_url": "https://d1--acme-app.netlify.app", "commit_ref": "0ld", "context": "deploy-preview",
			   "admin_url": "https://app.netlify.com/sites/acme-app/deploys/d1", "created_at": "2026-11-15T10:00:00.000Z",
			   "published_at": "2026-11-15T10:01:00.000Z", "review_id": 7}]`)
		case "GET /api/v1/deploys/d2":
			io.WriteString(w, `{"id": "d2", "site_id": "site_1", "branch": "fix-login", "commit_ref": "abc123", "review_id": 7,
			  "state": "error", "error_message": "Build script returned non-zero exit code: 2"}`)
		case "GET /api/v1/deploys/d1":
			io.WriteString(w, `{"id": "d1", "site_id": "site_1", "branch": "main", "commit_ref": "0ld", "review_id": null, "state": "ready"}`)
		case "POST /api/v1/sites/site_1/builds":
			io.WriteString(w, `{"id": "b9", "deploy_id": "d3", "done": false}`)
		case "POST /api/v1/sites/site_1/deploys/d1/restore":
			io.WriteString(w, `{"id": "d1", "state": "ready"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"code": 404, "message": "Not Found"}`)
		}
	}))
	t.Cleanup(srv.Close)
	f := fixture(t, "netlify")
	f.API = srv.URL + "/api/v1"
	return f, s
}

func TestNetlifyDeploys(t *testing.T) {
	f, s := fakeNetlify(t)
	c, err := f.Connect(testKey, "", "")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// At the head, only the deploys of that commit.
	deps, err := c.Previews(ctx, app)
	if err != nil {
		t.Fatal(err)
	}
	if len(deps) != 1 {
		t.Fatalf("at the head: %+v", deps)
	}
	d := deps[0]
	if d.ID != "d2" || d.Name != "Fix the login" || d.State != "failed" || d.Commit != "abc123" || d.Environment != "deploy-preview" ||
		d.URL != "https://deploy-preview-7--acme-app.netlify.app" || d.Inspect != "https://app.netlify.com/sites/acme-app/deploys/d2" ||
		d.Error != "Build script returned non-zero exit code: 2" || d.Started.IsZero() || !d.Finished.IsZero() {
		t.Fatalf("deploy %+v", d)
	}
	// Without a head, every deploy of the branch.
	noHead := app
	noHead.Head = ""
	if deps, err := c.Previews(ctx, noHead); err != nil || len(deps) != 2 || deps[1].State != "ready" || deps[1].Finished.Sub(deps[1].Started) != time.Minute {
		t.Fatalf("no head: %+v %v", deps, err)
	}
	// A head nothing was built at is none — building soon, not missing;
	// a repository no site builds from is said.
	if deps, err := c.Previews(ctx, Target{Remote: app.Remote, Branch: "fix-login", Head: "fff"}); err != nil || len(deps) != 0 {
		t.Fatalf("head with none: %+v %v", deps, err)
	}
	if _, err := c.Previews(ctx, Target{Remote: Remote{Host: "github.com", Path: "acme/nope"}, Branch: "fix-login"}); !errors.Is(err, ErrUnlinked) {
		t.Fatalf("no site: %v", err)
	}

	lg, err := c.FailedLog(ctx, app, "d2")
	if err != nil || lg.Text != "Build script returned non-zero exit code: 2\n" {
		t.Fatalf("log %q %v", lg.Text, err)
	}
	// A deploy with no error message has no log: nothing there.
	if _, err := c.FailedLog(ctx, app, "d1"); !errors.Is(err, ErrNothing) {
		t.Fatalf("no error message: %v", err)
	}

	before := len(s.all())
	if err := c.Redeploy(ctx, app, "d2"); err != nil {
		t.Fatal(err)
	}
	if err := c.Promote(ctx, app, "d1"); err != nil {
		t.Fatal(err)
	}
	calls := s.all()[before:]
	want := []string{"GET /api/v1/deploys/d2", "POST /api/v1/sites/site_1/builds?branch=fix-login&commit_sha=abc123&review_id=7",
		"GET /api/v1/deploys/d1", "POST /api/v1/sites/site_1/deploys/d1/restore"}
	if strings.Join(calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("calls\n%s\nwant\n%s", strings.Join(calls, "\n"), strings.Join(want, "\n"))
	}
	// A deploy that was no review's: review_id null is left out.
	before = len(s.all())
	if err := c.Redeploy(ctx, app, "d1"); err != nil {
		t.Fatal(err)
	}
	if got := s.all()[before+1]; got != "POST /api/v1/sites/site_1/builds?branch=main&commit_sha=0ld" {
		t.Fatalf("redeploy %q", got)
	}
}

// A deploy file that fills only previews says what it cannot do.
func TestDeployMissingOperations(t *testing.T) {
	f := fixture(t, "netlify")
	f.FailedLog, f.Redeploy, f.Promote = LogOp{}, Operation{}, Operation{}
	c, err := f.Connect(testKey, "", "")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := c.FailedLog(ctx, app, "d2"); err == nil || !strings.Contains(err.Error(), "cannot read a log") {
		t.Fatalf("log: %v", err)
	}
	if err := c.Redeploy(ctx, app, "d2"); err == nil || !strings.Contains(err.Error(), "cannot redeploy") {
		t.Fatalf("redeploy: %v", err)
	}
	if err := c.Promote(ctx, app, "d2"); err == nil || !strings.Contains(err.Error(), "cannot promote") {
		t.Fatalf("promote: %v", err)
	}
	// Previews of a file of another kind is refused rather than read as
	// nothing.
	ci := shipped(t, "circleci")
	cc, _ := ci.Connect(testKey, "", "")
	if _, err := cc.Previews(ctx, app); err == nil {
		t.Fatal("previews of a ci file")
	}
	if err := cc.Promote(ctx, app, "x"); err == nil {
		t.Fatal("promote through a ci file")
	}
}
