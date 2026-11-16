package main

import (
	"crypto/sha256"
	"encoding/hex"
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
	out, _, err = a4Out(t, func() error { return runIntegration([]string{"info", "-json", "jenkins"}) })
	var info infoJSON
	if err != nil || json.Unmarshal([]byte(out), &info) != nil || info.Kind != "ci" || info.AuthType != "basic" || info.UserEnv != "JENKINS_USER" ||
		info.BaseURL != "ask" || info.Source != "ships with conch" || len(info.Does) != 5 || info.KeySet {
		t.Fatalf("info -json: %s %v", out, err)
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

// key gives a shipped integration its key, into the Keychain, never
// printing it; a key already set is said rather than asked again.
func TestIntegrationKey(t *testing.T) {
	_, stored := integrationEnv(t)
	t.Setenv("CIRCLECI_TOKEN", "")
	t.Setenv("CONCH_PROVIDERS_URL", "http://127.0.0.1:1")
	var err error
	out, _ := a4Capture(t, "circle-sekrit\n", func() { err = runIntegration([]string{"key", "-stdin", "circleci"}) })
	if err != nil || strings.Contains(out, "circle-sekrit") || !strings.Contains(out, "kept in the Keychain (conch-mcp, CIRCLECI_TOKEN)") {
		t.Fatalf("key: %q %v", out, err)
	}
	b, _ := os.ReadFile(stored)
	if !strings.Contains(string(b), `-a CIRCLECI_TOKEN -w "circle-sekrit"`) {
		t.Fatalf("stored %q", b)
	}
	t.Setenv("CIRCLECI_TOKEN", "set")
	out, _, err = a4Out(t, func() error { return runIntegration([]string{"key", "circleci"}) })
	if err != nil || !strings.Contains(out, "set already") {
		t.Fatalf("set already: %q %v", out, err)
	}
	if _, _, err := a4Out(t, func() error { return runIntegration([]string{"key", "nothing-here"}) }); err == nil {
		t.Fatal("an unknown name")
	}
	// A key the Keychain's command line could be broken by is refused,
	// not half-stored.
	t.Setenv("CIRCLECI_TOKEN", "")
	_, _ = a4Capture(t, "bad\"key\n", func() { err = runIntegration([]string{"key", "-stdin", "circleci"}) })
	if err == nil || !strings.Contains(err.Error(), "cannot take") {
		t.Fatalf("a quote in the key: %v", err)
	}
}

// -base keeps a self-hosted address in config.toml, only for a file that
// asks for one, and only an https one.
func TestIntegrationBase(t *testing.T) {
	home, _ := integrationEnv(t)
	t.Setenv("JENKINS_TOKEN", "set")
	out, _, err := a4Out(t, func() error { return runIntegration([]string{"key", "-base", "https://ci.corp/", "jenkins"}) })
	if err != nil || !strings.Contains(out, "[integrations.base] jenkins") {
		t.Fatalf("base: %q %v", out, err)
	}
	b, _ := os.ReadFile(filepath.Join(home, "config.toml"))
	if !strings.Contains(string(b), `jenkins = "https://ci.corp"`) {
		t.Fatalf("config.toml: %s", b)
	}
	if _, _, err := a4Out(t, func() error { return runIntegration([]string{"key", "-base", "http://ci.corp", "jenkins"}) }); err == nil {
		t.Fatal("a plain-http address kept")
	}
	if _, _, err := a4Out(t, func() error { return runIntegration([]string{"key", "-base", "https://x", "circleci"}) }); err == nil || !strings.Contains(err.Error(), "takes no other") {
		t.Fatalf("a base for a file that does not ask: %v", err)
	}
}

// With -cwd the key is checked against that project: one read-only call
// for its branch's runs.
func TestIntegrationKeyChecksTheProject(t *testing.T) {
	home, _ := integrationEnv(t)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer acme-key" || r.URL.Query().Get("branch") != "main" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`{"runs": [{"id": 1, "status": "ok"}, {"id": 2, "status": "broken"}]}`))
	}))
	defer api.Close()
	os.MkdirAll(filepath.Join(home, "integrations"), 0o755)
	os.WriteFile(filepath.Join(home, "integrations", "acme-ci.toml"), []byte(strings.Replace(testIntegration, "https://api.acme.dev", api.URL, 1)), 0o600)
	repo := t.TempDir()
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"remote", "add", "origin", "https://github.com/acme/widgets.git"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	os.WriteFile(filepath.Join(repo, ".acme.yml"), nil, 0o600)
	var err error
	out, _ := a4Capture(t, "acme-key\n", func() { err = runIntegration([]string{"key", "-stdin", "-cwd", repo, "acme-ci"}) })
	if err != nil || !strings.Contains(out, "checked: read 2 runs of main on Acme CI") {
		t.Fatalf("key -cwd: %q %v", out, err)
	}
}

// shippedWithAPI installs a shipped file with its api moved to a test
// server, as one installed of the same name replaces it.
func shippedWithAPI(t *testing.T, home, name, api string) {
	t.Helper()
	data, _, err := integration.Source(name)
	if err != nil {
		t.Fatal(err)
	}
	src := string(data)
	i := strings.Index(src, "\napi = \"")
	j := strings.Index(src[i+1:], "\n")
	src = src[:i+1] + `api = "` + api + `"` + src[i+1+j:]
	os.MkdirAll(filepath.Join(home, "integrations"), 0o755)
	if err := os.WriteFile(filepath.Join(home, "integrations", name+".toml"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	integration.Forget()
}

// A tracker's key is checked by reading its open tickets; a webhook's by
// sending a message through it — and neither is ever printed.
func TestIntegrationKeyChecksTicketsAndNotify(t *testing.T) {
	home, _ := integrationEnv(t)
	t.Setenv("SHORTCUT_API_TOKEN", "")
	t.Setenv("SLACK_WEBHOOK_URL", "")
	sc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Shortcut-Token") != "sc-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/v3/workflows":
			w.Write([]byte(`[]`))
		default:
			w.Write([]byte(`{"data": [{"id": 1, "name": "a"}, {"id": 2, "name": "b"}], "next": null}`))
		}
	}))
	defer sc.Close()
	shippedWithAPI(t, home, "shortcut", sc.URL+"/api/v3")
	var err error
	out, _ := a4Capture(t, "sc-key\n", func() { err = runIntegration([]string{"key", "-stdin", "shortcut"}) })
	if err != nil || !strings.Contains(out, "checked: read 2 open tickets on Shortcut") || strings.Contains(out, "sc-key") {
		t.Fatalf("shortcut: %q %v", out, err)
	}

	var got []string
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = append(got, r.URL.Path+" "+string(b))
		w.Write([]byte("ok"))
	}))
	defer hook.Close()
	shippedWithAPI(t, home, "slack", hook.URL)
	out, _ = a4Capture(t, hook.URL+"/services/T/B/HOOKSECRET\n", func() { err = runIntegration([]string{"key", "-stdin", "slack"}) })
	if err != nil || !strings.Contains(out, "checked: sent a test message through Slack") || strings.Contains(out, "HOOKSECRET") {
		t.Fatalf("slack: %q %v", out, err)
	}
	if len(got) != 1 || !strings.Contains(got[0], "/services/T/B/HOOKSECRET") || !strings.Contains(got[0], "work.") {
		t.Fatalf("sent %q", got)
	}
	// A webhook on another host: refused before anything is sent, and
	// not repeated back.
	out, _ = a4Capture(t, "https://evil.example/HOOKSECRET\n", func() { err = runIntegration([]string{"key", "-stdin", "slack"}) })
	if strings.Contains(out, "HOOKSECRET") || !strings.Contains(out, "not checked") || len(got) != 1 {
		t.Fatalf("elsewhere: %q %v %d", out, err, len(got))
	}
}

// A built-in entry in a registry is listed as built in, and install says
// there is nothing to install rather than asking about hosts.
func TestIntegrationBuiltInEntry(t *testing.T) {
	integrationEnv(t)
	reg := serveRegistry(t)
	ix, _ := json.Marshal(integration.Index{Format: 2, Entries: []integration.Entry{
		{Kind: "tickets", Name: "jira", Label: "Jira", BuiltIn: true, KeyEnv: "JIRA_API_TOKEN"}}})
	reg.mu.Lock()
	reg.files["index.json"] = ix
	reg.mu.Unlock()
	out, _, err := a4Out(t, func() error { return runIntegration([]string{"search", "jira"}) })
	if err != nil || !strings.Contains(out, "Jira") || !strings.Contains(out, "built in") || !strings.Contains(out, "conch's own code") {
		t.Fatalf("search: %q %v", out, err)
	}
	out, _, err = a4Out(t, func() error { return runIntegration([]string{"install", "-y", "jira"}) })
	if err == nil || !strings.Contains(err.Error(), "built into conch") || strings.Contains(out, "sends your key") {
		t.Fatalf("install: %q %v", out, err)
	}
}

// Linear and Jira are built in: key keeps their key under the variable
// their tracker code reads (what the project sheet's Connect… runs), info
// says so with no registry to ask, and GitHub's have no key of conch's.
func TestIntegrationBuiltInKeyAndInfo(t *testing.T) {
	home, stored := integrationEnv(t)
	t.Setenv("CONCH_PROVIDERS_URL", "http://127.0.0.1:1")
	t.Setenv("LINEAR_API_KEY", "")
	t.Setenv("JIRA_API_TOKEN", "")
	t.Setenv("WORK_LINEAR", "")

	var err error
	out, _ := a4Capture(t, "lin-sekrit\n", func() { err = runIntegration([]string{"key", "-stdin", "linear"}) })
	if err != nil || strings.Contains(out, "lin-sekrit") || !strings.Contains(out, "kept in the Keychain (conch-mcp, LINEAR_API_KEY)") {
		t.Fatalf("key linear: %q %v", out, err)
	}
	if b, _ := os.ReadFile(stored); !strings.Contains(string(b), `-a LINEAR_API_KEY -w "lin-sekrit"`) {
		t.Fatalf("stored %q", b)
	}

	// [trackers.linear] api_key_env names another variable: that one.
	os.WriteFile(filepath.Join(home, "config.toml"), []byte("[trackers.linear]\napi_key_env = \"WORK_LINEAR\"\n"), 0o600)
	out, _ = a4Capture(t, "work-key\n", func() { err = runIntegration([]string{"key", "-stdin", "linear"}) })
	if err != nil || !strings.Contains(out, "(conch-mcp, WORK_LINEAR)") {
		t.Fatalf("key linear, its own variable: %q %v", out, err)
	}

	// Jira's token alone is not enough: its site and email are said.
	out, _ = a4Capture(t, "jira-tok\n", func() { err = runIntegration([]string{"key", "-stdin", "jira"}) })
	if err != nil || !strings.Contains(out, "JIRA_API_TOKEN") || !strings.Contains(out, "site's address and your email") {
		t.Fatalf("key jira: %q %v", out, err)
	}

	out, _, err = a4Out(t, func() error { return runIntegration([]string{"key", "github"}) })
	if err != nil || !strings.Contains(out, "gh auth login") {
		t.Fatalf("key github: %q %v", out, err)
	}

	// info, with no registry to reach: built in, set up, its variable.
	out, _, err = a4Out(t, func() error { return runIntegration([]string{"info", "-json", "jira"}) })
	var info infoJSON
	if err != nil || json.Unmarshal([]byte(out), &info) != nil || !info.BuiltIn || !info.Installed || info.Source != "built in" ||
		info.KeyEnv != "JIRA_API_TOKEN" || info.KeySet || info.Kind != "tickets" {
		t.Fatalf("info -json jira: %s %v", out, err)
	}
	t.Setenv("JIRA_API_TOKEN", "set")
	out, _, _ = a4Out(t, func() error { return runIntegration([]string{"info", "jira"}) })
	if !strings.Contains(out, "built into conch") || !strings.Contains(out, "Its key ($JIRA_API_TOKEN) is set") {
		t.Fatalf("info jira: %q", out)
	}
	if _, _, err := a4Out(t, func() error { return runIntegration([]string{"info", "github"}) }); err != nil {
		t.Fatalf("info github: %v", err)
	}
}

// Sandbox providers are integrations too: listed by ls with their key's
// state, by search from the registry, and installed by install through
// the sandbox installer; one installed here is set up, whatever is listed.
func TestIntegrationSandboxProviders(t *testing.T) {
	integrationEnv(t)
	for _, v := range []string{"DAYTONA_API_KEY", "BOAT_API_KEY", "DEVIN_API_KEY", "DEVIN_ORG_ID", "TENSORLAKE_API_KEY", "HCLOUD_TOKEN"} {
		t.Setenv(v, "")
	}
	reg := serveRegistry(t)
	ix, _ := json.Marshal(integration.Index{Format: 2, Entries: []integration.Entry{
		{Kind: "sandbox", Name: "hetzner", Label: "Hetzner Cloud", Version: 1, KeyEnv: "HCLOUD_TOKEN", File: "providers/hetzner.toml", Hosts: integration.Hosts{API: "api.hetzner.cloud"}},
		{Kind: "sandbox", Name: "daytona", Label: "Daytona", Version: 1, KeyEnv: "DAYTONA_API_KEY", File: "providers/daytona.toml", Hosts: integration.Hosts{API: "app.daytona.io"}}}})
	reg.mu.Lock()
	reg.files["index.json"] = ix
	reg.mu.Unlock()
	installProviderFile(t, "daytona", "https://app.daytona.io/api")
	installProviderFile(t, "boat", "https://boat.dev/api/v1")

	out, _, err := a4Out(t, func() error { return runIntegration([]string{"ls", "-json"}) })
	var got struct {
		Integrations []lsRow `json:"integrations"`
	}
	if err != nil || json.Unmarshal([]byte(out), &got) != nil {
		t.Fatalf("ls: %s %v", out, err)
	}
	var daytona, boat *lsRow
	for i, r := range got.Integrations {
		switch r.Name {
		case "daytona":
			daytona = &got.Integrations[i]
		case "boat":
			boat = &got.Integrations[i]
		}
	}
	// A sandbox is named as people write it, as conch sandbox names it.
	if boat == nil || boat.Label != "boat.dev" || daytona == nil || daytona.Label != "Daytona" {
		t.Fatalf("sandbox labels in ls: %+v %+v", boat, daytona)
	}
	if daytona == nil || daytona.Kind != "sandbox" || daytona.Source != "a file of your own" || !strings.Contains(daytona.State, "DAYTONA_API_KEY") {
		t.Fatalf("daytona in ls: %+v", daytona)
	}

	out, _, err = a4Out(t, func() error { return runIntegration([]string{"search", "sandbox"}) })
	if err != nil || !strings.Contains(out, "Hetzner Cloud") || !strings.Contains(out, "sandbox") {
		t.Fatalf("search: %q %v", out, err)
	}

	out, _, err = a4Out(t, func() error { return runIntegration([]string{"info", "-json", "daytona"}) })
	var info infoJSON
	if err != nil || json.Unmarshal([]byte(out), &info) != nil || !info.Installed || info.Source != "installed" || !strings.Contains(info.KeyHelp, "Settings → Sandboxes") {
		t.Fatalf("info daytona: %s %v", out, err)
	}

	// Installing one goes to the sandbox installer, which says one is
	// here already, as conch sandbox provider install says it.
	if _, _, err := a4Out(t, func() error { return runIntegration([]string{"install", "-y", "daytona"}) }); err == nil || !strings.Contains(err.Error(), "already a provider here") {
		t.Fatalf("install daytona: %v", err)
	}
}
