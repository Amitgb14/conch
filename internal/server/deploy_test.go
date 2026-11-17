package server_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/integration"
	"github.com/Amitgb14/conch/internal/proto"
)

const vercelKey = "vercel-key-1234"

// deployRig is a project on github.com/acme/widgets whose pull request's
// head is abc123 with a red Vercel check, no failed Actions run, and a
// fake Vercel answering as the registry's file reads it: a failed build
// at the head, a ready one at the head, and an older ready one at the
// checkout's own commit.
type deployRig struct {
	t        *testing.T
	c        *client.Client
	proj     proto.ProjectInfo
	head     atomic.Value // the checkout's commit, for the older deploy
	mu       sync.Mutex
	posts    []string
	gets     int32
	headFile string
}

func newDeployRig(t *testing.T, install bool) *deployRig { return newDeployRigPR(t, install, true) }

// newDeployRigPR is the rig, with or without a pull request on main.
func newDeployRigPR(t *testing.T, install, pr bool) *deployRig {
	t.Helper()
	home := t.TempDir()
	t.Setenv("CONCH_HOME", home)
	t.Setenv("VERCEL_TOKEN", "")
	integration.Forget()
	t.Cleanup(integration.Forget)
	r := &deployRig{t: t}
	r.head.Store("")
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("Authorization") != "Bearer "+vercelKey {
			w.WriteHeader(http.StatusForbidden)
			io.WriteString(w, `{"error": {"code": "forbidden"}}`)
			return
		}
		body, _ := io.ReadAll(req.Body)
		q := req.URL.Query()
		switch req.Method + " " + req.URL.Path {
		case "GET /v10/projects":
			if q.Get("repoUrl") != "https://github.com/acme/widgets" {
				io.WriteString(w, `{"projects": []}`)
				return
			}
			io.WriteString(w, `{"projects": [{"id": "prj_9", "name": "widgets"}]}`)
		case "GET /v7/deployments":
			atomic.AddInt32(&r.gets, 1)
			if q.Get("branch") != "main" {
				io.WriteString(w, `{"deployments": []}`)
				return
			}
			head := r.head.Load().(string)
			all := []struct{ commit, json string }{
				{head, `{"uid": "dpl_2", "name": "widgets", "url": "widgets-git-main-acme.vercel.app", "readyState": "ERROR", "target": null,
			  "errorMessage": "Build failed: type error in PlanCard.tsx", "inspectorUrl": "https://vercel.com/acme/widgets/dpl_2",
			  "meta": {"githubCommitSha": "` + head + `"}, "created": 1731740000000}`},
				{head, `{"uid": "dpl_1", "name": "widgets", "url": "widgets-abc-acme.vercel.app", "readyState": "READY", "target": null,
			  "inspectorUrl": "https://vercel.com/acme/widgets/dpl_1", "meta": {"githubCommitSha": "` + head + `"}, "created": 1731730000000, "ready": 1731730060000}`},
				{"0ld0ld0", `{"uid": "dpl_0", "name": "widgets", "url": "widgets-old-acme.vercel.app", "readyState": "CANCELED", "target": null,
			  "meta": {"githubCommitSha": "0ld0ld0"}, "created": 1731720000000}`},
			}
			var deps []string
			for _, d := range all {
				if sha := q.Get("sha"); sha == "" || sha == d.commit {
					deps = append(deps, d.json)
				}
			}
			io.WriteString(w, `{"deployments": [`+strings.Join(deps, ",")+`]}`)
		case "GET /v3/deployments/dpl_2/events":
			io.WriteString(w, `[{"payload": {"text": "> next build"}}, {"payload": {"text": "Type error: Property 'annual' does not exist"}}]`)
		case "GET /v13/deployments/dpl_2", "GET /v13/deployments/dpl_1":
			io.WriteString(w, `{"id": "`+strings.TrimPrefix(req.URL.Path, "/v13/deployments/")+`", "name": "widgets"}`)
		case "POST /v13/deployments":
			r.mu.Lock()
			r.posts = append(r.posts, req.URL.RawQuery+" "+string(body))
			r.mu.Unlock()
			io.WriteString(w, `{"id": "dpl_new", "readyState": "QUEUED"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"error": {"code": "not_found"}}`)
		}
	}))
	t.Cleanup(api.Close)
	if install {
		src, err := os.ReadFile("../integration/testdata/deploy/vercel.toml")
		if err != nil {
			t.Fatal(err)
		}
		os.MkdirAll(filepath.Join(home, "integrations"), 0o755)
		os.WriteFile(filepath.Join(home, "integrations", "vercel.toml"),
			[]byte(strings.Replace(string(src), `api = "https://api.vercel.com"`, `api = "`+api.URL+`"`, 1)), 0o644)
	}
	ghDir := t.TempDir()
	gh := filepath.Join(ghDir, "gh")
	// The pull request's head is the checkout's own commit, written once
	// it is made; with no file, the branch has no pull request.
	headFile := filepath.Join(ghDir, "head")
	r.headFile = headFile
	check := `{"__typename":"StatusContext","context":"Vercel","state":"FAILURE","targetUrl":"https://vercel.com/acme/widgets/dpl_2"}`
	os.WriteFile(gh, []byte(`#!/bin/sh
case "$1 $2" in
"pr list") [ -f `+headFile+` ] || { echo '[]'; exit 0; }
  echo '[{"number":42,"title":"Pricing","state":"OPEN","url":"https://github.com/acme/widgets/pull/42","headRefName":"main","headRefOid":"'$(cat `+headFile+`)'","statusCheckRollup":[`+check+`]}]' ;;
"run list") echo '[]' ;;
*) exit 7 ;;
esac
`), 0o755)
	t.Setenv("CONCH_GH", gh)
	c, _, proj := setupRepo(t)
	out, err := exec.Command("git", "-C", proj.Path, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	sha := strings.TrimSpace(string(out))
	r.head.Store(sha)
	if pr {
		os.WriteFile(headFile, []byte(sha), 0o600)
	} else {
		// No check to name Vercel by: the checkout's own file does.
		os.WriteFile(filepath.Join(proj.Path, "vercel.json"), []byte("{}"), 0o644)
	}
	git(t, proj.Path, "remote", "add", "origin", "https://github.com/acme/widgets.git")
	c.Notify(proto.MethodProjectRefresh, proto.ProjectRef{ID: proj.ID})
	r.proj = waitProject(t, c, func(p proto.ProjectInfo) bool {
		for _, b := range p.Branches {
			if b.Name == "main" && (!pr || b.PR != nil && b.PR.Head == sha && len(b.PR.Runs) > 0) {
				return true
			}
		}
		return false
	})
	r.c = c
	return r
}

func (r *deployRig) call(method string, params, out any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return r.c.Call(ctx, method, params, out)
}

func (r *deployRig) previews() proto.BranchPreviews {
	r.t.Helper()
	var out proto.BranchPreviews
	if err := r.call(proto.MethodBranchPreviews, proto.BranchCILogParams{ProjectID: r.proj.ID, Branch: "main"}, &out); err != nil {
		r.t.Fatal(err)
	}
	return out
}

func TestBranchPreviews(t *testing.T) {
	r := newDeployRig(t, true)
	if miss := r.c.MissingCapabilities([]string{proto.CapBranchPreviews}); len(miss) > 0 {
		t.Fatalf("capability missing: %v", miss)
	}
	for _, b := range r.proj.Branches {
		if b.Name == "main" && (b.PR.Runs[0].Source != "vercel" || !b.PR.Runs[0].Readable) {
			t.Fatalf("the Vercel check: %+v", b.PR.Runs[0])
		}
	}
	// No key: the integration is listed, saying which variable.
	got := r.previews()
	if len(got.Integrations) != 1 || got.Integrations[0].Source != "vercel" || !strings.Contains(got.Integrations[0].Why, "$VERCEL_TOKEN") || len(got.Integrations[0].Deploys) != 0 {
		t.Fatalf("no key: %+v", got)
	}
	// What could not be asked is not kept: a key given now is used now.
	t.Setenv("VERCEL_TOKEN", vercelKey)
	got = r.previews()
	if len(got.Integrations) != 1 {
		t.Fatalf("integrations: %+v", got)
	}
	v := got.Integrations[0]
	if v.Service != "Vercel" || v.Why != "" || !v.Redeploy || !v.Promote || v.Release == nil || v.Release.Verb != "Promote" {
		t.Fatalf("vercel: %+v", v)
	}
	// With a pull request: only the deploys at its head.
	if v.Head != r.head.Load().(string) || !v.BuildLog || v.Missing != "" || len(v.Deploys) != 2 {
		t.Fatalf("at the head: %+v", v)
	}
	failed, ready := v.Deploys[0], v.Deploys[1]
	if failed.ID != "dpl_2" || failed.State != "failed" || failed.Error != "Build failed: type error in PlanCard.tsx" ||
		failed.Inspect != "https://vercel.com/acme/widgets/dpl_2" || !failed.AtHead || failed.Promotable || failed.NotPromotable != "" {
		t.Errorf("failed: %+v", failed)
	}
	if ready.State != "ready" || ready.URL != "https://widgets-abc-acme.vercel.app" || !ready.Promotable || !ready.AtHead || ready.Finished == nil {
		t.Errorf("ready: %+v", ready)
	}
	// The commit's subject, from the checkout's git.
	if ready.Subject != "init" || failed.Subject != "init" {
		t.Errorf("subjects %q %q", ready.Subject, failed.Subject)
	}
	// Kept for a moment: a second look asks Vercel nothing.
	n := atomic.LoadInt32(&r.gets)
	r.previews()
	if atomic.LoadInt32(&r.gets) != n {
		t.Error("asked again within the moment")
	}
}

func TestBranchDeployLogAndCILog(t *testing.T) {
	r := newDeployRig(t, true)
	t.Setenv("VERCEL_TOKEN", vercelKey)
	var lg proto.BranchCILog
	if err := r.call(proto.MethodBranchDeployLog, proto.BranchDeployParams{ProjectID: r.proj.ID, Branch: "main", Source: "vercel", Deploy: "dpl_2"}, &lg); err != nil {
		t.Fatal(err)
	}
	if lg.Source != "vercel" || lg.Service != "Vercel" || lg.URL != "https://vercel.com/acme/widgets/dpl_2" || !strings.Contains(lg.Log, "Property 'annual'") {
		t.Fatalf("log: %+v", lg)
	}
	// No CI failed: branch.ci_log is the failed build, so the autopilot,
	// fix-ci and the review queue send it as they send a failed run.
	var ci proto.BranchCILog
	if err := r.call(proto.MethodBranchCILog, proto.BranchCILogParams{ProjectID: r.proj.ID, Branch: "main"}, &ci); err != nil {
		t.Fatal(err)
	}
	if ci.Source != "vercel" || !strings.Contains(ci.Log, "Property 'annual'") {
		t.Fatalf("ci_log: %+v", ci)
	}
	if p := proto.CIFailurePrompt(ci); !strings.Contains(p, "CI on Vercel") || !strings.Contains(p, "https://vercel.com/acme/widgets/dpl_2") {
		t.Fatalf("prompt: %s", p)
	}
	// With no deploy named: the newest failed one at the head.
	var newest proto.BranchCILog
	if err := r.call(proto.MethodBranchDeployLog, proto.BranchDeployParams{ProjectID: r.proj.ID, Branch: "main"}, &newest); err != nil || newest.Job != "deploy dpl_2" {
		t.Fatalf("newest: %+v %v", newest, err)
	}
	for name, tc := range map[string]struct {
		p    proto.BranchDeployParams
		want string
	}{
		"no branch":      {proto.BranchDeployParams{ProjectID: r.proj.ID}, "no branch given"},
		"unknown source": {proto.BranchDeployParams{ProjectID: r.proj.ID, Branch: "main", Source: "netlify"}, "it uses vercel"},
		"unknown deploy": {proto.BranchDeployParams{ProjectID: r.proj.ID, Branch: "main", Source: "vercel", Deploy: "dpl_9"}, "no deploy dpl_9"},
		"deploy alone":   {proto.BranchDeployParams{ProjectID: r.proj.ID, Branch: "main", Deploy: "dpl_2"}, "named with its integration"},
	} {
		if err := r.call(proto.MethodBranchDeployLog, tc.p, nil); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want %q", name, err, tc.want)
		}
	}
}

// With nothing installed, the red Vercel check is named and what would
// read it said, as an unreadable CI's is.
func TestCILogNamesVercelWithNoIntegration(t *testing.T) {
	r := newDeployRig(t, false)
	err := r.call(proto.MethodBranchCILog, proto.BranchCILogParams{ProjectID: r.proj.ID, Branch: "main"}, nil)
	if err == nil || !strings.Contains(err.Error(), "Vercel: install its integration to read its log") {
		t.Fatalf("got %v", err)
	}
	var got proto.BranchPreviews
	if err := r.call(proto.MethodBranchPreviews, proto.BranchCILogParams{ProjectID: r.proj.ID, Branch: "main"}, &got); err != nil || len(got.Integrations) != 0 {
		t.Fatalf("previews with nothing installed: %+v %v", got, err)
	}
	if err := r.call(proto.MethodBranchDeployLog, proto.BranchDeployParams{ProjectID: r.proj.ID, Branch: "main"}, nil); err == nil || !strings.Contains(err.Error(), "no deploy integration") {
		t.Fatalf("deploy_log: %v", err)
	}
}

func TestBranchRedeployAndPromote(t *testing.T) {
	r := newDeployRig(t, true)
	t.Setenv("VERCEL_TOKEN", vercelKey)
	at := func(deploy string) proto.BranchDeployParams {
		return proto.BranchDeployParams{ProjectID: r.proj.ID, Branch: "main", Source: "vercel", Deploy: deploy}
	}
	var done proto.BranchDeployDone
	if err := r.call(proto.MethodBranchRedeploy, at("dpl_2"), &done); err != nil || done.Service != "Vercel" || done.Deploy != "dpl_2" {
		t.Fatalf("redeploy: %+v %v", done, err)
	}
	// A failed build is not released.
	if err := r.call(proto.MethodBranchPromote, at("dpl_2"), nil); err == nil || !strings.Contains(err.Error(), "dpl_2 is failed, not ready") {
		t.Fatalf("promote a failed one: %v", err)
	}
	if err := r.call(proto.MethodBranchPromote, at("dpl_1"), &done); err != nil {
		t.Fatalf("promote: %v", err)
	}
	if err := r.call(proto.MethodBranchPromote, at(""), nil); err == nil || !strings.Contains(err.Error(), "must be named") {
		t.Fatalf("promote nothing: %v", err)
	}
	r.mu.Lock()
	posts := r.posts
	r.mu.Unlock()
	if len(posts) != 2 || !strings.Contains(posts[0], "forceNew=1") || strings.Contains(posts[0], "production") {
		t.Fatalf("redeploy's post: %q", posts)
	}
	var b map[string]any
	if json.Unmarshal([]byte(strings.SplitN(posts[1], " ", 2)[1]), &b) != nil || b["target"] != "production" || b["deploymentId"] != "dpl_1" {
		t.Fatalf("promote's post: %q", posts[1])
	}
}

// Without a pull request, the branch's deploys are its history, every
// state among them; a repository Vercel has no project for is said.
func TestBranchPreviewsWithoutAPullRequest(t *testing.T) {
	r := newDeployRigPR(t, true, false)
	t.Setenv("VERCEL_TOKEN", vercelKey)
	v := r.previews().Integrations[0]
	if v.Head != "" || len(v.Deploys) != 3 || v.Deploys[2].State != "canceled" || v.Deploys[2].AtHead {
		t.Fatalf("history: %+v", v)
	}
	git(t, r.proj.Path, "remote", "set-url", "origin", "https://github.com/acme/elsewhere.git")
	var got proto.BranchPreviews
	if err := r.call(proto.MethodBranchPreviews, proto.BranchCILogParams{ProjectID: r.proj.ID, Branch: "feat"}, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Integrations) != 1 || got.Integrations[0].Missing != "No Vercel project in your account is linked to github.com/acme/elsewhere. Projects owned by a Vercel team aren't supported yet." ||
		got.Integrations[0].Why != "" || len(got.Integrations[0].Deploys) != 0 {
		t.Fatalf("nothing linked: %+v", got)
	}
}
