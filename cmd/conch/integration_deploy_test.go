package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Amitgb14/conch/internal/integration"
)

// deployEnv installs the registry's Vercel file (the test fixture) with
// its api at a fake Vercel, and makes a checkout of github.com/acme/app on
// branch fix-login that uses it. It returns the checkout and what the
// fake was asked.
func deployEnv(t *testing.T) (repo string, calls func() []string) {
	t.Helper()
	home, _ := integrationEnv(t)
	t.Setenv("VERCEL_TOKEN", "vc-key")
	var mu sync.Mutex
	var got []string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer vc-key" {
			w.WriteHeader(http.StatusForbidden)
			io.WriteString(w, `{"error": {"code": "forbidden"}}`)
			return
		}
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		got = append(got, strings.TrimSpace(r.Method+" "+r.URL.Path+" "+string(b)))
		mu.Unlock()
		switch r.Method + " " + r.URL.Path {
		case "GET /v10/projects":
			io.WriteString(w, `{"projects": [{"id": "prj_9", "name": "app"}]}`)
		case "GET /v7/deployments":
			if r.URL.Query().Get("sha") == "fff" {
				io.WriteString(w, `{"deployments": []}`)
				return
			}
			io.WriteString(w, `{"deployments": [
			  {"uid": "dpl_2", "name": "app", "url": "app-git-fix-login.vercel.app", "readyState": "ERROR", "target": null,
			   "errorMessage": "Command \"npm run build\" exited with 1", "inspectorUrl": "https://vercel.com/acme/app/dpl_2",
			   "meta": {"githubCommitSha": "abc123456789"}, "created": 1731740000000},
			  {"uid": "dpl_1", "name": "app", "url": "app-abc.vercel.app", "readyState": "READY", "target": "production",
			   "inspectorUrl": "https://vercel.com/acme/app/dpl_1", "meta": {"githubCommitSha": "0ld0ld0ld"}, "created": 1731730000000}]}`)
		case "GET /v3/deployments/dpl_2/events":
			lines := []string{}
			for i := range 300 {
				lines = append(lines, `{"payload": {"text": "line `+strings.Repeat("x", i%3)+`"}}`)
			}
			lines = append(lines, `{"payload": {"text": "Error: Cannot find module './login'"}}`)
			io.WriteString(w, "["+strings.Join(lines, ",")+"]")
		case "GET /v13/deployments/dpl_1", "GET /v13/deployments/dpl_2":
			io.WriteString(w, `{"id": "x", "name": "app"}`)
		case "POST /v13/deployments":
			io.WriteString(w, `{"id": "dpl_new"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"error": {"code": "not_found"}}`)
		}
	}))
	t.Cleanup(api.Close)
	src, err := os.ReadFile("../../internal/integration/testdata/deploy/vercel.toml")
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(home, "integrations"), 0o755)
	data := strings.Replace(string(src), `api = "https://api.vercel.com"`, `api = "`+api.URL+`"`, 1)
	if err := os.WriteFile(filepath.Join(home, "integrations", "vercel.toml"), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	integration.Forget()
	repo = t.TempDir()
	for _, args := range [][]string{{"init", "-q", "-b", "fix-login"}, {"remote", "add", "origin", "https://github.com/acme/app.git"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	os.WriteFile(filepath.Join(repo, "vercel.json"), []byte("{}"), 0o600)
	return repo, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), got...)
	}
}

func TestIntegrationPreviews(t *testing.T) {
	repo, calls := deployEnv(t)
	// The project's deploy integration is found by itself.
	out, _, err := a4Out(t, func() error { return runIntegration([]string{"previews", "-cwd", repo}) })
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Vercel · fix-login", "failed", "dpl_2", "https://app-git-fix-login.vercel.app", "abc1234",
		`Command "npm run build" exited with 1`, "https://vercel.com/acme/app/dpl_2", "ready", "production"} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in\n%s", want, out)
		}
	}
	if strings.Contains(out, "abc123456789") {
		t.Errorf("a whole commit where a short one does:\n%s", out)
	}
	out, _, err = a4Out(t, func() error {
		return runIntegration([]string{"previews", "-json", "-cwd", repo, "-branch", "other", "vercel"})
	})
	var deps []integration.Deploy
	if err != nil || json.Unmarshal([]byte(out), &deps) != nil || len(deps) != 2 || deps[0].State != "failed" || deps[0].Inspect == "" {
		t.Fatalf("json: %q %v", out, err)
	}
	if last := calls()[len(calls())-1]; !strings.Contains(last, "GET /v7/deployments") {
		t.Fatalf("last call %q", last)
	}
	// Nothing at a head: said, and not an error; -json says [] rather
	// than null.
	out, _, err = a4Out(t, func() error { return runIntegration([]string{"previews", "-cwd", repo, "-head", "fff", "vercel"}) })
	if err != nil || !strings.Contains(out, "no deploys of fix-login at fff") {
		t.Fatalf("none: %q %v", out, err)
	}
	out, _, err = a4Out(t, func() error {
		return runIntegration([]string{"previews", "-json", "-cwd", repo, "-head", "fff", "vercel"})
	})
	if err != nil || strings.TrimSpace(out) != "[]" {
		t.Fatalf("none as json: %q %v", out, err)
	}
}

func TestIntegrationDeployLogAndRedeploy(t *testing.T) {
	repo, calls := deployEnv(t)
	out, _, err := a4Out(t, func() error { return runIntegration([]string{"deploy-log", "-cwd", repo, "vercel", "dpl_2"}) })
	if err != nil || !strings.HasSuffix(out, "Error: Cannot find module './login'\n") {
		t.Fatalf("log: %q %v", out, err)
	}
	// The end is what is kept, as for an agent: 200 lines unless asked.
	if n := strings.Count(out, "\n"); n != 200 {
		t.Fatalf("%d lines", n)
	}
	out, _, _ = a4Out(t, func() error { return runIntegration([]string{"deploy-log", "-lines", "0", "-cwd", repo, "dpl_2"}) })
	if n := strings.Count(out, "\n"); n != 301 {
		t.Fatalf("-lines 0: %d lines", n)
	}

	out, _, err = a4Out(t, func() error { return runIntegration([]string{"redeploy", "-cwd", repo, "vercel", "dpl_2"}) })
	if err != nil || !strings.Contains(out, "Building dpl_2 again on Vercel") {
		t.Fatalf("redeploy: %q %v", out, err)
	}
	c := calls()
	if post := c[len(c)-1]; !strings.HasPrefix(post, "POST /v13/deployments") || !strings.Contains(post, `"deploymentId":"dpl_2"`) {
		t.Fatalf("redeploy sent %q", post)
	}
}

// promote changes production, so it asks; -y is the yes given beforehand.
func TestIntegrationPromoteAsks(t *testing.T) {
	repo, calls := deployEnv(t)
	posts := func() int {
		n := 0
		for _, c := range calls() {
			if strings.HasPrefix(c, "POST") {
				n++
			}
		}
		return n
	}
	for _, answer := range []string{"n\n", "\n", ""} {
		var err error
		out, errOut := a4Capture(t, answer, func() { err = runIntegration([]string{"promote", "-cwd", repo, "vercel", "dpl_1"}) })
		if err == nil || !strings.Contains(errOut, "Release dpl_1 to production on Vercel?") || !strings.Contains(errOut, "[y/N]") || posts() != 0 {
			t.Fatalf("answer %q: %q %q %v posts %d", answer, out, errOut, err, posts())
		}
	}
	var err error
	out, _ := a4Capture(t, "y\n", func() { err = runIntegration([]string{"promote", "-cwd", repo, "vercel", "dpl_1"}) })
	if err != nil || posts() != 1 || !strings.Contains(out, "Releasing dpl_1 to production on Vercel") {
		t.Fatalf("yes: %q %v", out, err)
	}
	c := calls()
	if !strings.Contains(c[len(c)-1], `"target":"production"`) {
		t.Fatalf("promote sent %q", c[len(c)-1])
	}
	out, errOut, err := a4Out(t, func() error { return runIntegration([]string{"promote", "-y", "-cwd", repo, "vercel", "dpl_1"}) })
	if err != nil || posts() != 2 || strings.Contains(errOut, "[y/N]") {
		t.Fatalf("-y: %q %q %v", out, errOut, err)
	}
	// A deploy Vercel has not got: its answer, and nothing released.
	if _, _, err := a4Out(t, func() error { return runIntegration([]string{"promote", "-y", "-cwd", repo, "vercel", "dpl_gone"}) }); err == nil || !strings.Contains(err.Error(), "404") || posts() != 2 {
		t.Fatalf("no such deploy: %v", err)
	}
}

func TestIntegrationDeployRefusals(t *testing.T) {
	repo, calls := deployEnv(t)
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"previews", "-cwd", repo, "circleci"}, "circleci is not a deploy integration"},
		{[]string{"previews", "-cwd", repo, "nothing-here"}, "no deploy integration nothing-here"},
		{[]string{"previews", "-cwd", t.TempDir()}, "no deploy integration is used here"},
		{[]string{"deploy-log", "-cwd", repo}, "usage: conch integration deploy-log"},
		{[]string{"redeploy", "-cwd", repo, "a", "b", "c"}, "usage: conch integration redeploy"},
		{[]string{"promote", "-y", "-cwd", repo, "vercel", " "}, "no deploy given"},
	}
	for _, tc := range cases {
		if _, _, err := a4Out(t, func() error { return runIntegration(tc.args) }); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: %v, want %q", tc.args, err, tc.want)
		}
	}
	// A repository with no remote has nothing to find the project by.
	bare := t.TempDir()
	exec.Command("git", "-C", bare, "init", "-q", "-b", "main").Run()
	if _, _, err := a4Out(t, func() error { return runIntegration([]string{"previews", "-cwd", bare, "vercel"}) }); err == nil || !strings.Contains(err.Error(), "remote") {
		t.Errorf("no remote: %v", err)
	}
	// No key: where to give it, and nothing sent.
	t.Setenv("VERCEL_TOKEN", "")
	n := len(calls())
	if _, _, err := a4Out(t, func() error { return runIntegration([]string{"previews", "-cwd", repo, "vercel"}) }); err == nil || !strings.Contains(err.Error(), "conch integration key vercel") || len(calls()) != n {
		t.Errorf("no key: %v", err)
	}
}

// A deploy integration's key is checked by reading the project's
// previews — one read — not as a CI's runs.
func TestIntegrationKeyChecksDeploy(t *testing.T) {
	repo, calls := deployEnv(t)
	t.Setenv("VERCEL_TOKEN", "")
	var err error
	out, _ := a4Capture(t, "vc-key\n", func() { err = runIntegration([]string{"key", "-stdin", "-cwd", repo, "vercel"}) })
	if err != nil || !strings.Contains(out, "checked: read 2 deploys of fix-login on Vercel") || strings.Contains(out, "vc-key") {
		t.Fatalf("key: %q %v", out, err)
	}
	for _, c := range calls() {
		if !strings.HasPrefix(c, "GET ") {
			t.Fatalf("a check that changed something: %q", c)
		}
	}
	// Outside a project that uses it there is nothing to read.
	out, _ = a4Capture(t, "vc-key\n", func() { err = runIntegration([]string{"key", "-stdin", "-cwd", t.TempDir(), "vercel"}) })
	if !strings.Contains(out, "not checked") {
		t.Fatalf("elsewhere: %q %v", out, err)
	}
}
