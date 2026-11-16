package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

const testIntegration = `format = 3
kind = "ci"
name = "acme-ci"
label = "Acme CI"
api = "https://api.acme.dev"
key_env = "ACME_TOKEN"
follow_hosts = ["logs.acme.dev"]
key_help = "Acme → Tokens."
[detect]
files = [".acme.yml"]
[project]
id = "{owner}/{repo}"
[states]
ok = "pass"
broken = "fail"
[runs]
call = "GET /runs?branch={branch}"
items = "runs"
id = "id"
state = "status"
[failed_log]
call = "GET /runs/{run}/log"
text = "."
`

// integrationEnv is a conch home of the test's, no registry but the one
// the test serves, and a security that keeps what it is given in a file.
func integrationEnv(t *testing.T) (home, stored string) {
	t.Helper()
	home = a4Env(t)
	integration.Forget()
	t.Cleanup(integration.Forget)
	t.Setenv("ACME_TOKEN", "")
	t.Setenv("CONCH_PROVIDERS_KEY", "")
	dir := t.TempDir()
	stored = filepath.Join(dir, "stored")
	sec := filepath.Join(dir, "security")
	os.WriteFile(sec, []byte(`#!/bin/sh
case "$1" in
-i) cat >> `+stored+` ;;
find-generic-password) [ -f `+dir+`/has ] && cat `+dir+`/has || exit 44 ;;
esac
`), 0o755)
	t.Setenv("CONCH_SECURITY", sec)
	return home, stored
}

type cliRegistry struct {
	mu    sync.Mutex
	files map[string][]byte
}

func (r *cliRegistry) publish(t *testing.T, src string, version int, hosts integration.Hosts) {
	t.Helper()
	sum := sha256.Sum256([]byte(src))
	e := integration.Entry{Kind: "ci", Name: "acme-ci", Label: "Acme CI", Version: version, Format: 3, KeyEnv: "ACME_TOKEN",
		File: "integrations/acme-ci.toml", SHA256: hex.EncodeToString(sum[:]), Hosts: hosts}
	ix, _ := json.Marshal(integration.Index{Format: 2, Entries: []integration.Entry{e}})
	r.mu.Lock()
	r.files["index.json"] = ix
	r.files[e.File] = []byte(src)
	r.mu.Unlock()
}

func serveRegistry(t *testing.T) *cliRegistry {
	r := &cliRegistry{files: map[string][]byte{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		defer r.mu.Unlock()
		if b, ok := r.files[strings.TrimPrefix(req.URL.Path, "/")]; ok {
			w.Write(b)
			return
		}
		http.NotFound(w, req)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("CONCH_PROVIDERS_URL", srv.URL)
	return r
}

var acme = integration.Hosts{API: "api.acme.dev", Follow: []string{"logs.acme.dev"}}

func TestIntegrationInstallUpdateRemove(t *testing.T) {
	home, stored := integrationEnv(t)
	reg := serveRegistry(t)
	reg.publish(t, testIntegration, 1, acme)

	// Nobody to say yes: nothing installed.
	if _, _, err := a4Out(t, func() error { return runIntegration([]string{"install", "acme-ci"}) }); err == nil || !strings.Contains(err.Error(), "nothing installed") {
		t.Fatalf("unanswered: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "integrations", "acme-ci.toml")); err == nil {
		t.Fatal("installed without a yes")
	}

	var err error
	out, _ := a4Capture(t, "sekrit-acme-key\n", func() { err = runIntegration([]string{"install", "-y", "-key-stdin", "acme-ci"}) })
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"sends your key ($ACME_TOKEN) to api.acme.dev only", "reads logs.acme.dev without it", "(The registry's index is unsigned.)",
		"kept in the Keychain (conch-mcp, ACME_TOKEN)", "installed acme-ci 1 (ci)", "not checked"} {
		if !strings.Contains(out, want) {
			t.Errorf("install said %q, lacking %q", out, want)
		}
	}
	if strings.Contains(out, "sekrit-acme-key") {
		t.Fatal("the key was printed")
	}
	b, _ := os.ReadFile(stored)
	if !strings.Contains(string(b), `add-generic-password -U -s conch-mcp -a ACME_TOKEN -w "sekrit-acme-key"`) {
		t.Fatalf("the Keychain was given %q", b)
	}
	if _, _, err := a4Out(t, func() error { return runIntegration([]string{"install", "-y", "acme-ci"}) }); err == nil || !strings.Contains(err.Error(), "installed already") {
		t.Fatalf("twice: %v", err)
	}

	// The same hosts: -y updates.
	reg.publish(t, testIntegration+"# v2\n", 2, acme)
	out, _, err = a4Out(t, func() error { return runIntegration([]string{"update", "-y"}) })
	if err != nil || !strings.Contains(out, "acme-ci: updated to 2") {
		t.Fatalf("update: %q %v", out, err)
	}
	// A new host: asked even with -y, and with nobody to answer, not taken.
	v3 := strings.Replace(testIntegration, `["logs.acme.dev"]`, `["logs.acme.dev", "cdn.acme.dev"]`, 1)
	reg.publish(t, v3, 3, integration.Hosts{API: "api.acme.dev", Follow: []string{"logs.acme.dev", "cdn.acme.dev"}})
	out, _, err = a4Out(t, func() error { return runIntegration([]string{"update", "-y", "acme-ci"}) })
	if err != nil || !strings.Contains(out, "cdn.acme.dev") || !strings.Contains(out, "acme-ci: not updated") {
		t.Fatalf("new hosts: %q %v", out, err)
	}
	out, _, _ = a4Out(t, func() error { return runIntegration([]string{"update", "-y"}) })
	if strings.Contains(out, "updated to 3") {
		t.Fatal("took new hosts unasked")
	}

	out, _, err = a4Out(t, func() error { return runIntegration([]string{"remove", "acme-ci"}) })
	if err != nil || out != "removed acme-ci\n" {
		t.Fatalf("remove: %q %v", out, err)
	}
	if _, _, err := a4Out(t, func() error { return runIntegration([]string{"remove", "acme-ci"}) }); err == nil {
		t.Fatal("removed twice")
	}
	if _, _, err := a4Out(t, func() error { return runIntegration([]string{"update", "acme-ci"}) }); err == nil || !strings.Contains(err.Error(), "not installed from a registry") {
		t.Fatalf("update of nothing: %v", err)
	}
}

func TestIntegrationInstallRefusals(t *testing.T) {
	integrationEnv(t)
	reg := serveRegistry(t)
	// The file reaches a host the index does not show.
	reg.publish(t, testIntegration, 1, integration.Hosts{API: "api.acme.dev"})
	_, _, err := a4Out(t, func() error { return runIntegration([]string{"install", "-y", "acme-ci"}) })
	if err == nil || !strings.Contains(err.Error(), "which the index does not show") {
		t.Fatalf("hosts: %v", err)
	}
	if _, _, err := a4Out(t, func() error { return runIntegration([]string{"install", "-y", "nothing-listed"}) }); err == nil || !strings.Contains(err.Error(), "no registry lists") {
		t.Fatalf("unknown: %v", err)
	}
	if _, _, err := a4Out(t, func() error { return runIntegration([]string{"install", "-y", "../x"}) }); err == nil {
		t.Fatal("a path for a name")
	}
	// A registry whose key conch knows must sign.
	_, pub, _ := integration.NewKey()
	t.Setenv("CONCH_PROVIDERS_KEY", pub)
	_, errOut, err := a4Out(t, func() error { return runIntegration([]string{"install", "-y", "acme-ci"}) })
	if err == nil || !strings.Contains(errOut, "not signed") {
		t.Fatalf("unsigned with a key: %q %v", errOut, err)
	}
}

func TestIntegrationList(t *testing.T) {
	home, _ := integrationEnv(t)
	t.Setenv("CONCH_PROVIDERS_URL", "http://127.0.0.1:1")
	t.Setenv("GITLAB_TOKEN", "set")
	repo := t.TempDir()
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"remote", "add", "origin", "https://gitlab.com/grp/widgets.git"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	os.MkdirAll(filepath.Join(home, "integrations"), 0o755)
	os.WriteFile(filepath.Join(home, "integrations", "acme-ci.toml"), []byte(testIntegration), 0o600)
	os.WriteFile(filepath.Join(home, "integrations", "broken.toml"), []byte("kind = "), 0o600)
	out, _, err := a4Out(t, func() error { return runIntegration([]string{"ls", "-cwd", repo}) })
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"github-actions", "linear", "jira", "a file of your own", "ships with conch", "not used: ", "broken.toml"} {
		if !strings.Contains(out, want) {
			t.Errorf("ls lacks %q:\n%s", want, out)
		}
	}
	var gitlab string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "gitlab-ci ") {
			gitlab = l
		}
	}
	if !strings.Contains(gitlab, "ok · used here") {
		t.Fatalf("gitlab-ci in a gitlab.com project with its key: %q", gitlab)
	}
	out, _, err = a4Out(t, func() error { return runIntegration([]string{"ls", "-json"}) })
	var got struct {
		Integrations []lsRow `json:"integrations"`
		Problems     []string
	}
	if err != nil || json.Unmarshal([]byte(out), &got) != nil || len(got.Integrations) < 9 || len(got.Problems) != 1 {
		t.Fatalf("json: %s %v", out, err)
	}
}

func TestIntegrationInfoCheckGuide(t *testing.T) {
	home, _ := integrationEnv(t)
	t.Setenv("CONCH_PROVIDERS_URL", "http://127.0.0.1:1")
	out, _, err := a4Out(t, func() error { return runIntegration([]string{"info", "circleci"}) })
	if err != nil || !strings.Contains(out, "Your key ($CIRCLECI_TOKEN) goes to circleci.com only") ||
		!strings.Contains(out, "without your key") || !strings.Contains(out, "✓ A failed job's output goes to the branch's agent") {
		t.Fatalf("info: %q %v", out, err)
	}
	if _, _, err := a4Out(t, func() error { return runIntegration([]string{"info", "nothing-here"}) }); err == nil {
		t.Fatal("info of nothing")
	}
	if _, _, err := a4Out(t, func() error { return runIntegration([]string{"info", "../x"}) }); err == nil {
		t.Fatal("info of a path")
	}

	out, _, err = a4Out(t, func() error { return runIntegration([]string{"check", "jenkins"}) })
	if err != nil || out != "jenkins is fine\n" {
		t.Fatalf("check shipped: %q %v", out, err)
	}
	bad := filepath.Join(home, "bad.toml")
	os.WriteFile(bad, []byte(strings.Replace(testIntegration, "format = 3", "format = 2", 1)), 0o600)
	out, _, err = a4Out(t, func() error { return runIntegration([]string{"check", bad}) })
	if err == nil || !strings.Contains(out, "format = 3") {
		t.Fatalf("check a bad file: %q %v", out, err)
	}
	out, _, _ = a4Out(t, func() error { return runIntegration([]string{"check", "-json", bad}) })
	if !strings.Contains(out, `"problems":["format = 3`) {
		t.Fatalf("check -json: %q", out)
	}
	if _, _, err := a4Out(t, func() error { return runIntegration([]string{"check", "/no/such/file.toml"}) }); err == nil {
		t.Fatal("an unreadable file")
	}
	if _, _, err := a4Out(t, func() error { return runIntegration([]string{"check", "nothing-here"}) }); err == nil {
		t.Fatal("an unknown name")
	}

	out, _, _ = a4Out(t, func() error { return runIntegration([]string{"guide"}) })
	if !strings.HasPrefix(out, "# conch integration file") {
		t.Fatalf("guide: %q", out[:min(len(out), 40)])
	}
	out, _, _ = a4Out(t, func() error { return runIntegration(nil) })
	if !strings.HasPrefix(out, "Usage:") {
		t.Fatalf("usage: %q", out)
	}
	if _, _, err := a4Out(t, func() error { return runIntegration([]string{"lst"}) }); err == nil || !strings.Contains(err.Error(), "did you mean conch integration ls") {
		t.Fatalf("a typo: %v", err)
	}
}
